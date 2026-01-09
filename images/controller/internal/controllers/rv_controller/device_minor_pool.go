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

package rvcontroller

import (
	"context"
	"fmt"
	"sort"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/idpool"
)

// DeviceMinorPoolSource provides access to an initialized in-memory [idpool.IDPool]
// used for allocating unique rv.status.deviceMinor values.
//
// DeviceMinorPool blocks until the pool is ready for use.
type DeviceMinorPoolSource interface {
	// DeviceMinorPool blocks until the pool is initialized and returns it.
	// Returns an error if initialization failed or context was cancelled.
	DeviceMinorPool(ctx context.Context) (*idpool.IDPool[v1alpha1.DeviceMinor], error)

	// DeviceMinorPoolOrNil returns the pool if it's ready, or nil if not yet initialized.
	// This is useful for non-blocking access, e.g., in predicates.
	DeviceMinorPoolOrNil() *idpool.IDPool[v1alpha1.DeviceMinor]
}

// DeviceMinorPoolInitializer is a manager.Runnable that initializes the device minor idpool
// after leader election. It implements [DeviceMinorPoolSource] to provide
// blocking access to the initialized pool.
type DeviceMinorPoolInitializer struct {
	mgr manager.Manager
	cl  client.Client
	log logr.Logger

	// readyCh is closed when initialization is complete
	readyCh chan struct{}
	// pool is set after successful initialization
	pool *idpool.IDPool[v1alpha1.DeviceMinor]
	// initErr is set if initialization failed
	initErr error
}

var _ manager.Runnable = (*DeviceMinorPoolInitializer)(nil)
var _ manager.LeaderElectionRunnable = (*DeviceMinorPoolInitializer)(nil)
var _ DeviceMinorPoolSource = (*DeviceMinorPoolInitializer)(nil)

// NewDeviceMinorPoolInitializer creates a new initializer that will populate
// the device minor idpool after leader election.
func NewDeviceMinorPoolInitializer(mgr manager.Manager) *DeviceMinorPoolInitializer {
	return &DeviceMinorPoolInitializer{
		mgr:     mgr,
		cl:      mgr.GetClient(),
		log:     mgr.GetLogger().WithName(RVControllerName),
		readyCh: make(chan struct{}),
	}
}

// NeedLeaderElection returns true to ensure this runnable only runs after
// leader election is won.
func (c *DeviceMinorPoolInitializer) NeedLeaderElection() bool {
	return true
}

// Start waits for leader election, then initializes the pool.
// It blocks until the context is cancelled after initialization completes.
func (c *DeviceMinorPoolInitializer) Start(ctx context.Context) error {
	// Wait for leader election to complete
	select {
	case <-ctx.Done():
		c.initErr = ctx.Err()
		close(c.readyCh)
		return ctx.Err()
	case <-c.mgr.Elected():
		// We are now the leader, proceed with initialization
	}

	c.log.Info("initializing device minor idpool after leader election")

	pool, err := c.doInitialize(ctx)
	if err != nil {
		c.log.Error(err, "failed to initialize device minor idpool")
		c.initErr = err
		close(c.readyCh)

		// Propagate the error to controller-runtime manager.
		// In Kubernetes this typically results in a pod restart (Deployment/DaemonSet).
		return err
	}

	c.pool = pool
	c.log.Info("initialized device minor idpool",
		"len", pool.Len(),
	)

	close(c.readyCh)

	// Block until context is done to keep the runnable alive
	<-ctx.Done()
	return nil
}

// DeviceMinorPool blocks until the pool is initialized and returns it.
// Returns an error if initialization failed or context was cancelled.
func (c *DeviceMinorPoolInitializer) DeviceMinorPool(ctx context.Context) (*idpool.IDPool[v1alpha1.DeviceMinor], error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.readyCh:
		if c.initErr != nil {
			return nil, fmt.Errorf("cache initialization failed: %w", c.initErr)
		}
		return c.pool, nil
	}
}

// DeviceMinorPoolOrNil returns the pool if it's ready, or nil if not yet initialized.
// This is useful for non-blocking access, e.g., in predicates.
func (c *DeviceMinorPoolInitializer) DeviceMinorPoolOrNil() *idpool.IDPool[v1alpha1.DeviceMinor] {
	select {
	case <-c.readyCh:
		if c.initErr != nil {
			return nil
		}
		return c.pool
	default:
		return nil
	}
}

// doInitialize reads all ReplicatedVolumes and populates an IDPool with their device minors.
//
// It bulk-registers all (rvName, deviceMinor) pairs and then sequentially patches every RV status
// via patchRVStatus, passing the corresponding pool error (nil => assigned/true).
//
// RVs are processed in the following order:
// - first: RVs with DeviceMinorAssigned condition == True
// - then: all others (no condition or condition != True)
func (c *DeviceMinorPoolInitializer) doInitialize(ctx context.Context) (*idpool.IDPool[v1alpha1.DeviceMinor], error) {
	pool := idpool.NewIDPool[v1alpha1.DeviceMinor]()

	rvList := &v1alpha1.ReplicatedVolumeList{}
	if err := c.cl.List(ctx, rvList); err != nil {
		return nil, fmt.Errorf("listing rvs: %w", err)
	}

	// Filter only RVs with deviceMinor set.
	rvs := make([]*v1alpha1.ReplicatedVolume, 0, len(rvList.Items))
	for i := range rvList.Items {
		rv := &rvList.Items[i]
		if rv.Status.DeviceMinor == nil {
			continue
		}
		rvs = append(rvs, rv)
	}

	// If there are no RVs with deviceMinor set, return the pool as is.
	if len(rvs) == 0 {
		return pool, nil
	}

	// Sort RVs so that those with DeviceMinorAssigned status condition == True go first.
	sort.SliceStable(rvs, func(i, j int) bool {
		ai := meta.IsStatusConditionTrue(rvs[i].Status.Conditions, v1alpha1.ReplicatedVolumeCondDeviceMinorAssignedType)
		aj := meta.IsStatusConditionTrue(rvs[j].Status.Conditions, v1alpha1.ReplicatedVolumeCondDeviceMinorAssignedType)
		if ai == aj {
			return false
		}
		return ai && !aj
	})

	// Bulk-register all (rvName, deviceMinor) pairs.
	pairs := make([]idpool.IDNamePair[v1alpha1.DeviceMinor], 0, len(rvs))
	for _, rv := range rvs {
		pairs = append(pairs, idpool.IDNamePair[v1alpha1.DeviceMinor]{
			Name: rv.Name,
			ID:   *rv.Status.DeviceMinor,
		})
	}
	bulkErrs := pool.Fill(pairs)

	// Report errors.
	for i, rv := range rvs {
		if bulkErrs[i] != nil {
			c.log.Error(bulkErrs[i], "deviceMinor pool reservation failed", "rv", rv.Name, "deviceMinor", *rv.Status.DeviceMinor)
		}
	}

	return pool, nil
}
