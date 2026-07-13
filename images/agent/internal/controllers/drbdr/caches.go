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

// commandCache caches the last output of a per-resource drbdsetup command.
// Each resource has its own lock, held for the whole duration of its command,
// so one resource is never queried concurrently while different resources run
// in parallel. drbdsetup locks the resource in the kernel anyway, so serializing
// per resource costs nothing.
type commandCache[T any] struct {
	fetch func(ctx context.Context, resourceName string) (*T, error)

	mu      sync.Mutex
	entries map[string]*cacheEntry[T]
}

type cacheEntry[T any] struct {
	mu     sync.Mutex
	value  *T
	cached bool
}

func newCommandCache[T any](fetch func(context.Context, string) (*T, error)) *commandCache[T] {
	return &commandCache[T]{
		fetch:   fetch,
		entries: make(map[string]*cacheEntry[T]),
	}
}

func (c *commandCache[T]) entryFor(resourceName string) *cacheEntry[T] {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[resourceName]
	if e == nil {
		e = &cacheEntry[T]{}
		c.entries[resourceName] = e
	}
	return e
}

// Get returns the cached command output for resourceName, running the command
// on a cache miss. A nil result is a valid cached value meaning the resource is
// absent on the node.
func (c *commandCache[T]) Get(ctx context.Context, resourceName string) (*T, error) {
	e := c.entryFor(resourceName)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cached {
		return e.value, nil
	}
	v, err := c.fetch(ctx, resourceName)
	if err != nil {
		return nil, err
	}
	e.value = v
	e.cached = true
	return v, nil
}

// Invalidate drops the cached entry for a single resource.
func (c *commandCache[T]) Invalidate(resourceName string) {
	c.mu.Lock()
	delete(c.entries, resourceName)
	c.mu.Unlock()
}

// InvalidateAll drops every cached entry.
func (c *commandCache[T]) InvalidateAll() {
	c.mu.Lock()
	names := make([]string, 0, len(c.entries))
	for name := range c.entries {
		names = append(names, name)
	}
	c.mu.Unlock()
	for _, name := range names {
		c.Invalidate(name)
	}
}

func (c *commandCache[T]) setForTest(resourceName string, v *T) {
	e := c.entryFor(resourceName)
	e.mu.Lock()
	e.value = v
	e.cached = true
	e.mu.Unlock()
}

func (c *commandCache[T]) hasForTest(resourceName string) bool {
	c.mu.Lock()
	_, ok := c.entries[resourceName]
	c.mu.Unlock()
	return ok
}

// Caches is the node's drbdsetup status/show output cache and the single
// invalidation surface for the events2 scanner and the DRBD kernel-log
// forwarder.
type Caches struct {
	status *commandCache[drbdutils.Resource]
	show   *commandCache[drbdutils.ShowResource]
}

func NewCaches() *Caches {
	return &Caches{
		status: newCommandCache(fetchStatus),
		show:   newCommandCache(fetchShow),
	}
}

// Status returns the cached drbdsetup status output for resourceName, or nil
// when the resource is absent on the node.
func (c *Caches) Status(ctx context.Context, resourceName string) (*drbdutils.Resource, error) {
	return c.status.Get(ctx, resourceName)
}

// Show returns the cached drbdsetup show output for resourceName, or nil when
// the resource is absent on the node.
func (c *Caches) Show(ctx context.Context, resourceName string) (*drbdutils.ShowResource, error) {
	return c.show.Get(ctx, resourceName)
}

// Invalidate drops the status and show entries for a single resource.
func (c *Caches) Invalidate(resourceName string) {
	c.status.Invalidate(resourceName)
	c.show.Invalidate(resourceName)
}

// InvalidateAll drops every cached status and show result.
func (c *Caches) InvalidateAll() {
	c.status.InvalidateAll()
	c.show.InvalidateAll()
}

// PopulateStatusForTest seeds present resources into the status cache.
func (c *Caches) PopulateStatusForTest(result drbdutils.StatusResult) {
	for i := range result {
		res := result[i]
		c.status.setForTest(res.Name, &res)
	}
}

// SeedStatusAbsentForTest seeds a cached-absent status entry for each name not
// already cached, so an existence check hits the cache without a subprocess.
func (c *Caches) SeedStatusAbsentForTest(names ...string) {
	for _, name := range names {
		if !c.status.hasForTest(name) {
			c.status.setForTest(name, nil)
		}
	}
}

var (
	sharedCaches     *Caches
	sharedCachesOnce sync.Once
)

// GetCaches returns the process-wide status/show caches, created on first use.
// The scanner, reconciler and kernel-log forwarder share this instance.
func GetCaches() *Caches {
	sharedCachesOnce.Do(func() { sharedCaches = NewCaches() })
	return sharedCaches
}

// fetchStatus returns the drbdsetup status entry for resourceName, or nil when
// the resource does not exist on the node.
func fetchStatus(ctx context.Context, resourceName string) (*drbdutils.Resource, error) {
	result, err := drbdutils.ExecuteStatus(ctx, resourceName)
	if err != nil {
		return nil, fmt.Errorf("executing drbdsetup status: %w", err)
	}
	if len(result) == 0 {
		return nil, nil
	}
	return &result[0], nil
}

// fetchShow returns the drbdsetup show entry for resourceName, or nil when it is
// not present in the output.
func fetchShow(ctx context.Context, resourceName string) (*drbdutils.ShowResource, error) {
	results, err := drbdutils.ExecuteShow(ctx, resourceName, true)
	if err != nil {
		return nil, fmt.Errorf("executing drbdsetup show: %w", err)
	}
	for i := range results {
		if results[i].Resource == resourceName {
			return &results[i], nil
		}
	}
	return nil, nil
}
