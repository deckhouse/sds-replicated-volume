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
	"log/slog"
	"math/rand"
	"slices"
	"time"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/config"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/kubeutils"
)

const (
	// attachCycleProbability is the probability of a attach cycle (vs detach)
	attachCycleProbability = 0.10
)

// VolumeAttacher periodically attaches and detaches a volume to random nodes
type VolumeAttacher struct {
	rvName           string
	cfg              config.VolumeAttacherConfig
	client           *kubeutils.Client
	log              *slog.Logger
	forceCleanupChan <-chan struct{}
}

// NewVolumeAttacher creates a new VolumeAttacher
func NewVolumeAttacher(rvName string, cfg config.VolumeAttacherConfig, client *kubeutils.Client, periodrMinMax []int, forceCleanupChan <-chan struct{}) *VolumeAttacher {
	return &VolumeAttacher{
		rvName:           rvName,
		cfg:              cfg,
		client:           client,
		log:              slog.Default().With("runner", "volume-attacher", "rv_name", rvName, "period_min_max", periodrMinMax),
		forceCleanupChan: forceCleanupChan,
	}
}

// Run starts the attach/detach cycle until context is cancelled
func (v *VolumeAttacher) Run(ctx context.Context) error {
	v.log.Info("started")
	defer v.log.Info("finished")

	for {
		if err := waitRandomWithContext(ctx, v.cfg.Period); err != nil {
			v.cleanup(ctx, err)
			return nil
		}

		rv, err := v.client.GetRV(ctx, v.rvName)
		if err != nil {
			v.log.Error("failed to get RV", "error", err)
			return err
		}

		// get a random node
		nodes, err := v.client.GetRandomNodes(ctx, 1)
		if err != nil {
			v.log.Error("failed to get random node", "error", err)
			return err
		}
		nodeName := nodes[0].Name
		log := v.log.With("node_name", nodeName)

		// TODO: maybe it's necessary to collect time statistics by cycles?
		switch len(rv.Spec.AttachTo) {
		case 0:
			if v.isAPublishCycle() {
				if err := v.attachCycle(ctx, rv, nodeName); err != nil {
					log.Error("failed to attachCycle", "error", err, "case", 0)
					return err
				}
			} else {
				if err := v.attachAndDetachCycle(ctx, rv, nodeName); err != nil {
					log.Error("failed to attachAndDetachCycle", "error", err, "case", 0)
					return err
				}
			}
		case 1:
			if slices.Contains(rv.Spec.AttachTo, nodeName) {
				if err := v.detachCycle(ctx, rv, nodeName); err != nil {
					log.Error("failed to detachCycle", "error", err, "case", 1)
					return err
				}
			} else {
				if err := v.migrationCycle(ctx, rv, nodeName); err != nil {
					log.Error("failed to migrationCycle", "error", err, "case", 1)
					return err
				}
			}
		case 2:
			if !slices.Contains(rv.Spec.AttachTo, nodeName) {
				nodeName = rv.Spec.AttachTo[0]
			}
			if err := v.detachCycle(ctx, rv, nodeName); err != nil {
				log.Error("failed to detachCycle", "error", err, "case", 2)
				return err
			}
		default:
			err := fmt.Errorf("unexpected number of nodes in AttachTo: %d", len(rv.Spec.AttachTo))
			log.Error("error", "error", err)
			return err
		}
	}
}

func (v *VolumeAttacher) cleanup(ctx context.Context, reason error) {
	log := v.log.With("reason", reason, "func", "cleanup")
	log.Info("started")
	defer log.Info("finished")

	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), CleanupTimeout)
	defer cleanupCancel()

	// If context was cancelled, listen for second signal to force cleanup cancellation.
	// First signal already cancelled the main context (stopped volume operations).
	// Second signal will close forceCleanupChan, and all cleanup handlers will receive
	// notification simultaneously (broadcast mechanism via channel closure).
	if ctx.Err() != nil && v.forceCleanupChan != nil {
		log.Info("cleanup can be interrupted by second signal")
		go func() {
			select {
			case <-v.forceCleanupChan: // All handlers receive this simultaneously when channel is closed
				log.Info("received second signal, forcing cleanup cancellation")
				cleanupCancel()
			case <-cleanupCtx.Done():
				// Cleanup already completed or was cancelled
			}
		}()
	}

	rv, err := v.client.GetRV(cleanupCtx, v.rvName)
	if err != nil {
		log.Error("failed to get RV for cleanup", "error", err)
		return
	}

	if err := v.detachCycle(cleanupCtx, rv, ""); err != nil {
		v.log.Error("failed to detachCycle", "error", err)
	}
}

func (v *VolumeAttacher) attachCycle(ctx context.Context, rv *v1alpha1.ReplicatedVolume, nodeName string) error {
	log := v.log.With("node_name", nodeName, "func", "attachCycle")
	log.Debug("started")
	defer log.Debug("finished")

	if err := v.doPublish(ctx, rv, nodeName); err != nil {
		log.Error("failed to doPublish", "error", err)
		return err
	}

	// Wait for node to be attached
	for {
		log.Debug("waiting for node to be attached")

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		rv, err := v.client.GetRV(ctx, v.rvName)
		if err != nil {
			return err
		}

		if rv.Status != nil && slices.Contains(rv.Status.AttachedTo, nodeName) {
			return nil
		}

		time.Sleep(1 * time.Second)
	}
}

func (v *VolumeAttacher) attachAndDetachCycle(ctx context.Context, rv *v1alpha1.ReplicatedVolume, nodeName string) error {
	log := v.log.With("node_name", nodeName, "func", "attachAndDetachCycle")
	log.Debug("started")
	defer log.Debug("finished")

	// Step 1: Attach the node and wait for it to be attached
	if err := v.attachCycle(ctx, rv, nodeName); err != nil {
		return err
	}

	// Step 2: Random delay between attach and detach
	randomDelay := randomDuration(v.cfg.Period)
	log.Debug("waiting random delay before detach", "duration", randomDelay.String())
	if err := waitWithContext(ctx, randomDelay); err != nil {
		return err
	}

	// Step 3: Get fresh RV and detach
	rv, err := v.client.GetRV(ctx, v.rvName)
	if err != nil {
		return err
	}

	return v.detachCycle(ctx, rv, nodeName)
}

func (v *VolumeAttacher) migrationCycle(ctx context.Context, rv *v1alpha1.ReplicatedVolume, nodeName string) error {
	log := v.log.With("node_name", nodeName, "func", "migrationCycle")
	log.Debug("started")
	defer log.Debug("finished")

	// Find the other node (not nodeName) from current AttachTo
	// In case 1, there should be exactly one node in AttachTo
	if len(rv.Spec.AttachTo) != 1 {
		return fmt.Errorf("expected exactly one node in AttachTo for migration, got %d", len(rv.Spec.AttachTo))
	}
	otherNodeName := rv.Spec.AttachTo[0]
	if otherNodeName == nodeName {
		return fmt.Errorf("other node name equals selected node name: %s", nodeName)
	}

	// Step 1: Attach the selected node and wait for it
	if err := v.attachCycle(ctx, rv, nodeName); err != nil {
		return err
	}

	// Verify both nodes are now attached
	for {
		log.Debug("waiting for both nodes to be attached", "selected_node", nodeName, "other_node", otherNodeName)

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		rv, err := v.client.GetRV(ctx, v.rvName)
		if err != nil {
			return err
		}

		if rv.Status != nil && len(rv.Status.AttachedTo) == 2 {
			break
		}

		time.Sleep(1 * time.Second)
	}

	// Step 2: Random delay
	randomDelay1 := randomDuration(v.cfg.Period)
	log.Debug("waiting random delay before detaching other node", "duration", randomDelay1.String())
	if err := waitWithContext(ctx, randomDelay1); err != nil {
		return err
	}

	// Step 3: Get fresh RV and detach the other node
	rv, err := v.client.GetRV(ctx, v.rvName)
	if err != nil {
		return err
	}

	if err := v.detachCycle(ctx, rv, otherNodeName); err != nil {
		return err
	}

	// Step 4: Random delay
	randomDelay2 := randomDuration(v.cfg.Period)
	log.Debug("waiting random delay before detaching selected node", "duration", randomDelay2.String())
	if err := waitWithContext(ctx, randomDelay2); err != nil {
		return err
	}

	// Step 5: Get fresh RV and detach the selected node
	rv, err = v.client.GetRV(ctx, v.rvName)
	if err != nil {
		return err
	}

	return v.detachCycle(ctx, rv, nodeName)
}

func (v *VolumeAttacher) doPublish(ctx context.Context, rv *v1alpha1.ReplicatedVolume, nodeName string) error {
	// Check if node is already in AttachTo
	if slices.Contains(rv.Spec.AttachTo, nodeName) {
		v.log.Debug("node already in AttachTo", "node_name", nodeName)
		return nil
	}

	originalRV := rv.DeepCopy()
	rv.Spec.AttachTo = append(rv.Spec.AttachTo, nodeName)

	err := v.client.PatchRV(ctx, originalRV, rv)
	if err != nil {
		return fmt.Errorf("failed to patch RV with new attach node: %w", err)
	}

	return nil
}

func (v *VolumeAttacher) detachCycle(ctx context.Context, rv *v1alpha1.ReplicatedVolume, nodeName string) error {
	log := v.log.With("node_name", nodeName, "func", "detachCycle")
	log.Debug("started")
	defer log.Debug("finished")

	if err := v.doUnattach(ctx, rv, nodeName); err != nil {
		log.Error("failed to doUnattach", "error", err)
		return err
	}

	// Wait for node(s) to be detached
	for {
		if nodeName == "" {
			log.Debug("waiting for all nodes to be detached")
		} else {
			log.Debug("waiting for node to be detached")
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		rv, err := v.client.GetRV(ctx, v.rvName)
		if err != nil {
			return err
		}

		if rv.Status == nil {
			// If status is nil, consider it as detached
			return nil
		}

		if nodeName == "" {
			// Check if all nodes are detached
			if len(rv.Status.AttachedTo) == 0 {
				return nil
			}
		} else {
			// Check if specific node is detached
			if !slices.Contains(rv.Status.AttachedTo, nodeName) {
				return nil
			}
		}

		time.Sleep(1 * time.Second)
	}
}

func (v *VolumeAttacher) doUnattach(ctx context.Context, rv *v1alpha1.ReplicatedVolume, nodeName string) error {
	originalRV := rv.DeepCopy()

	if nodeName == "" {
		// Detach from all nodes - make AttachTo empty
		rv.Spec.AttachTo = []string{}
	} else {
		// Check if node is in AttachTo
		if !slices.Contains(rv.Spec.AttachTo, nodeName) {
			v.log.Debug("node not in AttachTo", "node_name", nodeName)
			return nil
		}

		// Remove node from AttachTo
		newAttachTo := make([]string, 0, len(rv.Spec.AttachTo))
		for _, node := range rv.Spec.AttachTo {
			if node != nodeName {
				newAttachTo = append(newAttachTo, node)
			}
		}
		rv.Spec.AttachTo = newAttachTo
	}

	err := v.client.PatchRV(ctx, originalRV, rv)
	if err != nil {
		return fmt.Errorf("failed to patch RV to detach node: %w", err)
	}

	return nil
}

func (v *VolumeAttacher) isAPublishCycle() bool {
	//nolint:gosec // G404: math/rand is fine for non-security-critical random selection
	r := rand.Float64()
	return r < attachCycleProbability
}
