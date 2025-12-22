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

package runners

import (
	"context"
	"log/slog"
	"math/rand"
	"time"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/config"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/kubeutils"
)

// availableReplicaTypes returns the list of replica types that can be created.
// Uncomment ReplicaTypeDiskful when diskful destroyer is implemented.
func availableReplicaTypes() []string {
	return []string{
		v1alpha3.ReplicaTypeAccess,
		v1alpha3.ReplicaTypeTieBreaker,
		// v1alpha3.ReplicaTypeDiskful, // TODO: uncomment when diskful destroyer is ready
	}
}

// VolumeReplicaCreator periodically creates random replicas for a volume.
// It does NOT wait for creation to succeed.
type VolumeReplicaCreator struct {
	rvName string
	cfg    config.VolumeReplicaCreatorConfig
	client *kubeutils.Client
	log    *slog.Logger
}

// NewVolumeReplicaCreator creates a new VolumeReplicaCreator
func NewVolumeReplicaCreator(
	rvName string,
	cfg config.VolumeReplicaCreatorConfig,
	client *kubeutils.Client,
	periodMinMax []int,
) *VolumeReplicaCreator {
	return &VolumeReplicaCreator{
		rvName: rvName,
		cfg:    cfg,
		client: client,
		log:    slog.Default().With("runner", "volume-replica-creator", "rv_name", rvName, "period_min_max", periodMinMax),
	}
}

// Run starts the create cycle until context is cancelled
func (v *VolumeReplicaCreator) Run(ctx context.Context) error {
	v.log.Info("started")
	defer v.log.Info("finished")

	for {
		// Wait random duration before create
		if err := waitRandomWithContext(ctx, v.cfg.Period); err != nil {
			return nil
		}

		// Perform create (errors are logged, not returned)
		v.doCreate(ctx)
	}
}

// selectRandomType selects a random replica type from available types
func (v *VolumeReplicaCreator) selectRandomType() string {
	types := availableReplicaTypes()
	//nolint:gosec // G404: math/rand is fine for non-security-critical random selection
	return types[rand.Intn(len(types))]
}

// generateRVRName generates a unique name for a new RVR
func (v *VolumeReplicaCreator) generateRVRName() string {
	// Use short UUID suffix for uniqueness
	shortUUID := uuid.New().String()[:8]
	return v.rvName + "-mt-" + shortUUID
}

func (v *VolumeReplicaCreator) doCreate(ctx context.Context) {
	startTime := time.Now()

	// Select random type
	replicaType := v.selectRandomType()

	// Generate unique name
	rvrName := v.generateRVRName()

	// Create RVR object
	// Note: We don't set OwnerReference here.
	// The rvr_owner_reference_controller handles this automatically
	// based on spec.replicatedVolumeName.
	rvr := &v1alpha3.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{
			Name: rvrName,
		},
		Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: v.rvName,
			Type:                 replicaType,
			// NodeName is not set - controller will schedule it
		},
	}

	// Create RVR (do NOT wait for success)
	if err := v.client.CreateRVR(ctx, rvr); err != nil {
		v.log.Error("failed to create RVR",
			"rvr_name", rvrName,
			"error", err)
		return
	}

	// Log success
	v.log.Info("RVR created",
		"rvr_name", rvrName,
		"rvr_type", replicaType,
		"duration", time.Since(startTime))
}
