/*
Copyright 2026 Flant JSC

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

package drbdr

import (
	"context"
	"fmt"
	"sync"

	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdutils"
)

// ShowCache caches per-resource results of drbdsetup show --json --show-defaults.
// Entries are fetched lazily on first access and invalidated after DRBD convergence
// actions that may change configuration.
//
// Concurrency model:
//   - Multiple reconciler goroutines may call Get concurrently (read lock).
//   - Reconciler goroutines may call Invalidate (write lock).
//   - RWMutex allows concurrent cache hits without blocking.
type ShowCache struct {
	mu      sync.RWMutex
	entries map[string]*showCacheEntry
}

type showCacheEntry struct {
	result *drbdutils.ShowResource
}

func NewShowCache() *ShowCache {
	return &ShowCache{
		entries: make(map[string]*showCacheEntry),
	}
}

// Get returns the cached show result for a resource, fetching it from drbdsetup
// if not cached. Returns nil (not error) if the resource is not found in show output.
func (c *ShowCache) Get(ctx context.Context, resourceName string) (*drbdutils.ShowResource, error) {
	c.mu.RLock()
	entry, ok := c.entries[resourceName]
	c.mu.RUnlock()

	if ok {
		return entry.result, nil
	}

	result, err := c.fetchAndStore(ctx, resourceName)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Invalidate marks a resource's cached show result as stale. The next Get call
// will re-fetch from drbdsetup.
func (c *ShowCache) Invalidate(resourceName string) {
	c.mu.Lock()
	delete(c.entries, resourceName)
	c.mu.Unlock()
}

func (c *ShowCache) fetchAndStore(ctx context.Context, resourceName string) (*drbdutils.ShowResource, error) {
	results, err := drbdutils.ExecuteShow(ctx, resourceName, true)
	if err != nil {
		return nil, fmt.Errorf("executing drbdsetup show: %w", err)
	}

	var match *drbdutils.ShowResource
	for i := range results {
		if results[i].Resource == resourceName {
			match = &results[i]
			break
		}
	}

	c.mu.Lock()
	c.entries[resourceName] = &showCacheEntry{result: match}
	c.mu.Unlock()

	return match, nil
}
