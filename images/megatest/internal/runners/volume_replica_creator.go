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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"log/slog"
	"math/rand"
	"time"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/config"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/kubeutils"
)

// availableReplicaTypes returns the list of replica types that can be created.
// Uncomment ReplicaTypeDiskful when diskful destroyer is implemented.
func availableReplicaTypes() []string {
	return []string{
		string(v1alpha1.ReplicaTypeAccess),
		string(v1alpha1.ReplicaTypeTieBreaker),
		// string(v1alpha1.ReplicaTypeDiskful), // TODO: uncomment when diskful destroyer is ready
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

func (v *VolumeReplicaCreator) doCreate(ctx context.Context) {
	startTime := time.Now()

	// Select random type
	replicaType := v1alpha1.ReplicaType(v.selectRandomType())

	// Reserve a valid RVR ID from the current RV replica set.
	existingRVRs, err := v.client.ListRVRsByRVName(v.rvName)
	if err != nil {
		v.log.Error("failed to list RVRs", "error", err)
		return
	}

	existingPtrs := make([]*v1alpha1.ReplicatedVolumeReplica, 0, len(existingRVRs))
	for i := range existingRVRs {
		existingPtrs = append(existingPtrs, &existingRVRs[i])
	}

	// Create RVR object
	// Note: We don't set OwnerReference here.
	// The rvr_metadata_controller handles this automatically
	// based on spec.replicatedVolumeName.
	rvr := &v1alpha1.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{},
		Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: v.rvName,
			Type:                 replicaType,
		},
	}

	if !rvr.ChooseNewName(existingPtrs) {
		v.log.Debug("no free RVR IDs left for create")
		return
	}

	if replicaType == v1alpha1.ReplicaTypeAccess {
		nodes, err := v.client.GetRandomNodes(1)
		if err != nil {
			v.log.Error("failed to get random node for Access RVR", "error", err)
			return
		}
		if len(nodes) == 0 {
			v.log.Error("failed to get random node for Access RVR", "error", "no storage nodes found")
			return
		}

		rvr.Spec.NodeName = nodes[0].Name
	}

	// Create RVR (do NOT wait for success)
	if err := v.client.CreateRVR(ctx, rvr); err != nil {
		v.log.Error("failed to create RVR",
			"rvr_name", rvr.Name,
			"error", err)
		return
	}

	// Log success
	v.log.Info("RVR created",
		"rvr_name", rvr.Name,
		"rvr_type", replicaType,
		"rvr_node", rvr.Spec.NodeName,
		"duration", time.Since(startTime))
}
