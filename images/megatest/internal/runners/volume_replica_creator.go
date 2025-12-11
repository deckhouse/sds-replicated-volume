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
	"fmt"
	"math/rand"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/config"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/k8sclient"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/logging"
	"github.com/google/uuid"
)

// ReplicaType represents the type of replica to create
type ReplicaType string

const (
	ReplicaTypeAccess     ReplicaType = "Access"
	ReplicaTypeTieBreaker ReplicaType = "TieBreaker"
	// ReplicaTypeDiskful is not used for creation currently
	// ReplicaTypeDiskful ReplicaType = "Diskful"
)

// VolumeReplicaCreator periodically creates new Access or TieBreaker replicas
// It does NOT wait for creation to succeed
type VolumeReplicaCreator struct {
	rvName string
	cfg    config.VolumeReplicaCreatorConfig
	client *k8sclient.Client
	log    *logging.Logger
	scheme *runtime.Scheme
}

// NewVolumeReplicaCreator creates a new VolumeReplicaCreator
func NewVolumeReplicaCreator(
	rvName string,
	cfg config.VolumeReplicaCreatorConfig,
	client *k8sclient.Client,
) *VolumeReplicaCreator {
	scheme := runtime.NewScheme()
	_ = v1alpha3.AddToScheme(scheme)

	return &VolumeReplicaCreator{
		rvName: rvName,
		cfg:    cfg,
		client: client,
		log:    logging.NewLogger(rvName, "volume-replica-creator"),
		scheme: scheme,
	}
}

// Name returns the runner name
func (v *VolumeReplicaCreator) Name() string {
	return "volume-replica-creator"
}

// Run starts the create cycle until context is cancelled
func (v *VolumeReplicaCreator) Run(ctx context.Context) error {
	v.log.Info("starting volume replica creator")
	defer v.log.Info("volume replica creator stopped")

	for {
		// Wait random duration before create
		if err := waitRandomWithContext(ctx, v.cfg.Period); err != nil {
			return nil
		}

		// Perform create
		if err := v.doCreate(ctx); err != nil {
			v.log.Error("create failed", err)
			// Continue even on failure
		}
	}
}

func (v *VolumeReplicaCreator) doCreate(ctx context.Context) error {
	// Select random type (Access or TieBreaker, not Diskful for now)
	replicaTypes := []ReplicaType{ReplicaTypeAccess, ReplicaTypeTieBreaker}
	//nolint:gosec // G404: math/rand is fine for non-security-critical random selection
	selectedType := replicaTypes[rand.Intn(len(replicaTypes))]

	// Select random node
	node, err := v.client.SelectRandomNode(ctx)
	if err != nil {
		return err
	}

	// Generate unique name
	rvrName := fmt.Sprintf("%s-%s-%s", v.rvName, string(selectedType), uuid.New().String()[:8])

	params := logging.ActionParams{
		"rvr_name":     rvrName,
		"replica_type": string(selectedType),
		"node_name":    node.Name,
	}

	v.log.ActionStarted("create_replica", params)
	startTime := time.Now()

	// Get RV for owner reference
	rv, err := v.client.GetRV(ctx, v.rvName)
	if err != nil {
		v.log.ActionFailed("create_replica", params, err, time.Since(startTime))
		return fmt.Errorf("getting RV: %w", err)
	}

	// Create RVR with v1alpha3 API - just need to specify type and node
	rvr := &v1alpha3.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{
			Name: rvrName,
		},
		Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: v.rvName,
			NodeName:             node.Name,
			Type:                 string(selectedType), // "Access" or "TieBreaker"
		},
	}

	// Set owner reference
	if err := controllerutil.SetControllerReference(rv, rvr, v.scheme); err != nil {
		v.log.ActionFailed("create_replica", params, err, time.Since(startTime))
		return fmt.Errorf("setting owner reference: %w", err)
	}

	// Create the RVR (don't wait for completion)
	if err := v.client.CreateRVR(ctx, rvr); err != nil {
		v.log.ActionFailed("create_replica", params, err, time.Since(startTime))
		return fmt.Errorf("creating RVR %s: %w", rvrName, err)
	}

	// Log completion immediately (fire and forget)
	v.log.ActionCompleted("create_replica", params, "create_initiated", time.Since(startTime))
	return nil
}
