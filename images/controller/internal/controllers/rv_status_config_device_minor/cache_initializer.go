/*
Copyright 2025 Flant JSC

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

package rvstatusconfigdeviceminor

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// DeviceMinorCacheSource provides access to an initialized DeviceMinorCache.
// The DeviceMinorCache method blocks until the cache is ready for use.
type DeviceMinorCacheSource interface {
	// DeviceMinorCache blocks until the cache is initialized and returns it.
	// Returns an error if initialization failed or context was cancelled.
	DeviceMinorCache(ctx context.Context) (*DeviceMinorCache, error)

	// DeviceMinorCacheOrNil returns the cache if it's ready, or nil if not yet initialized.
	// This is useful for non-blocking access, e.g., in predicates.
	DeviceMinorCacheOrNil() *DeviceMinorCache
}

// CacheInitializer is a manager.Runnable that initializes the device minor cache
// after leader election. It implements DeviceMinorCacheSource to provide
// blocking access to the initialized cache.
type CacheInitializer struct {
	mgr manager.Manager
	cl  client.Client
	log logr.Logger

	// readyCh is closed when initialization is complete
	readyCh chan struct{}
	// cache is set after successful initialization
	cache *DeviceMinorCache
	// initErr is set if initialization failed
	initErr error
}

var _ manager.Runnable = (*CacheInitializer)(nil)
var _ manager.LeaderElectionRunnable = (*CacheInitializer)(nil)
var _ DeviceMinorCacheSource = (*CacheInitializer)(nil)

// NewCacheInitializer creates a new cache initializer that will populate
// the device minor cache after leader election.
func NewCacheInitializer(mgr manager.Manager) *CacheInitializer {
	return &CacheInitializer{
		mgr:     mgr,
		cl:      mgr.GetClient(),
		log:     mgr.GetLogger().WithName(RVStatusConfigDeviceMinorControllerName),
		readyCh: make(chan struct{}),
	}
}

// NeedLeaderElection returns true to ensure this runnable only runs after
// leader election is won.
func (c *CacheInitializer) NeedLeaderElection() bool {
	return true
}

// Start waits for leader election, then initializes the cache.
// It blocks until the context is cancelled after initialization completes.
func (c *CacheInitializer) Start(ctx context.Context) error {
	// Wait for leader election to complete
	select {
	case <-ctx.Done():
		c.initErr = ctx.Err()
		close(c.readyCh)
		return ctx.Err()
	case <-c.mgr.Elected():
		// We are now the leader, proceed with initialization
	}

	c.log.Info("initializing device minor cache after leader election")

	cache, err := c.doInitialize(ctx)
	if err != nil {
		c.log.Error(err, "failed to initialize device minor cache")
		c.initErr = err
		close(c.readyCh)
		// Return nil to not crash the manager - callers will get the error via DeviceMinorCache()
		return nil
	}

	c.cache = cache
	c.log.Info("initialized device minor cache",
		"len", cache.Len(),
		"max", cache.Max(),
		"releasedLen", cache.ReleasedLen(),
	)

	close(c.readyCh)

	// Block until context is done to keep the runnable alive
	<-ctx.Done()
	return nil
}

// DeviceMinorCache blocks until the cache is initialized and returns it.
// Returns an error if initialization failed or context was cancelled.
func (c *CacheInitializer) DeviceMinorCache(ctx context.Context) (*DeviceMinorCache, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.readyCh:
		if c.initErr != nil {
			return nil, fmt.Errorf("cache initialization failed: %w", c.initErr)
		}
		return c.cache, nil
	}
}

// DeviceMinorCacheOrNil returns the cache if it's ready, or nil if not yet initialized.
// This is useful for non-blocking access, e.g., in predicates.
func (c *CacheInitializer) DeviceMinorCacheOrNil() *DeviceMinorCache {
	select {
	case <-c.readyCh:
		if c.initErr != nil {
			return nil
		}
		return c.cache
	default:
		return nil
	}
}

// doInitialize reads all ReplicatedVolumes and populates the cache.
func (c *CacheInitializer) doInitialize(ctx context.Context) (*DeviceMinorCache, error) {
	dmCache := NewDeviceMinorCache()

	rvList := &v1alpha1.ReplicatedVolumeList{}
	if err := c.cl.List(ctx, rvList); err != nil {
		return nil, fmt.Errorf("listing rvs: %w", err)
	}

	rvByName := make(map[string]*v1alpha1.ReplicatedVolume, len(rvList.Items))
	dmByRVName := make(map[string]DeviceMinor, len(rvList.Items))

	for i := range rvList.Items {
		rv := &rvList.Items[i]
		rvByName[rv.Name] = rv

		deviceMinorVal, isSet := deviceMinor(rv)
		if !isSet {
			continue
		}

		dm, valid := NewDeviceMinor(deviceMinorVal)
		if !valid {
			return nil, fmt.Errorf("invalid device minor for rv %s: %d", rv.Name, rv.Status.DRBD.Config.DeviceMinor)
		}

		dmByRVName[rv.Name] = dm
	}

	if initErr := dmCache.Initialize(dmByRVName); initErr != nil {
		if dupErr, ok := initErr.(DuplicateDeviceMinorError); ok {
			for _, rvName := range dupErr.ConflictingRVNames {
				if err := patchDupErr(ctx, c.cl, rvByName[rvName], dupErr.ConflictingRVNames); err != nil {
					initErr = errors.Join(initErr, err)
				}
			}
		}
		return nil, fmt.Errorf("initializing device minor cache: %w", initErr)
	}

	return dmCache, nil
}
