/*
Copyright 2024 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cache

import (
	"context"
	"sync"
	"time"
)

// item represents a cache item with a value and an expiration time.
type item[V any] struct {
	value  V
	expiry time.Time
}

// isExpired checks if the cache item has expired.
func (i item[V]) isExpired() bool {
	return time.Now().After(i.expiry)
}

// TTLCache is a generic cache implementation with support for time-to-live
// (TTL) expiration.
type TTLCache[K comparable, V any] struct {
	items map[K]item[V] // The map storing cache items.
	mu    sync.Mutex    // Mutex for controlling concurrent access to the cache.
}

// NewTTL creates a new TTLCache instance and starts a goroutine to periodically
// remove expired items every 5 seconds.
func NewTTL[K comparable, V any](ctx context.Context) *TTLCache[K, V] {
	c := &TTLCache[K, V]{
		items: make(map[K]item[V]),
	}

	// starting up remove expired items loop
	go c.removeExpiredLoop(ctx)

	return c
}

// Iterate over the cache items every 5 sec and delete expired ones.
func (c *TTLCache[K, V]) removeExpiredLoop(ctx context.Context) {
	// endless loop
	for {
		select {
		case <-ctx.Done():
			// do nothing in case of cancel, just return from this gorouting
			return

		default:
			c.mu.Lock()

			// Iterate over the cache items and delete expired ones.
			for key, item := range c.items {
				if item.isExpired() {
					delete(c.items, key)
				}
			}

			c.mu.Unlock()

			// sleep for 5 sec
			time.Sleep(5 * time.Second)
		}
	}
}

// Set adds a new item to the cache with the specified key, value, and
// time-to-live (TTL).
func (c *TTLCache[K, V]) Set(key K, value V, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items[key] = item[V]{
		value:  value,
		expiry: time.Now().Add(ttl),
	}
}

// Get retrieves the value associated with the given key from the cache.
func (c *TTLCache[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	item, found := c.items[key]
	if !found {
		// If the key is not found, return the nil value for V and false.
		return item.value, false
	}

	if item.isExpired() {
		// If the item has expired, remove it from the cache and return the
		// value and false.
		delete(c.items, key)
		return item.value, false
	}

	// Otherwise return the value and true.
	return item.value, true
}

// Remove removes the item with the specified key from the cache.
func (c *TTLCache[K, V]) Remove(key K) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Delete the item with the given key from the cache.
	delete(c.items, key)
}

// Pop removes and returns the item with the specified key from the cache.
func (c *TTLCache[K, V]) Pop(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	item, found := c.items[key]
	if !found {
		// If the key is not found, return the zero value for V and false.
		return item.value, false
	}

	// If the key is found, delete the item from the cache.
	delete(c.items, key)

	if item.isExpired() {
		// If the item has expired, return the value and false.
		return item.value, false
	}

	// Otherwise return the value and true.
	return item.value, true
}
