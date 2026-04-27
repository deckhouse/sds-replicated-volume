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

package runners

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"slices"
	"time"

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

	// Helper function to check context and cleanup before return
	checkAndCleanup := func(err error) error {
		reason := err
		if reason == nil {
			reason = ctx.Err()
		}
		if reason != nil {
			v.cleanup(ctx, reason)
		}
		return err
	}

	for {
		if err := waitRandomWithContext(ctx, v.cfg.Period); err != nil {
			return checkAndCleanup(nil)
		}

		// Determine current desired attachments from RVA set (max 2 active attachments supported).
		rvas, err := v.client.ListRVAsByRVName(v.rvName)
		if err != nil {
			v.log.Error("failed to list RVAs", "error", err)
			return checkAndCleanup(err)
		}
		desiredNodes := make([]string, 0, len(rvas))
		for _, rva := range rvas {
			if rva.Spec.NodeName == "" {
				continue
			}
			desiredNodes = append(desiredNodes, rva.Spec.NodeName)
		}

		// get a random node
		nodes, err := v.client.GetRandomNodes(1)
		if err != nil {
			v.log.Error("failed to get random node", "error", err)
			return checkAndCleanup(err)
		}
		nodeName := nodes[0].Name
		log := v.log.With("node_name", nodeName)

		// TODO: maybe it's necessary to collect time statistics by cycles?
		switch len(desiredNodes) {
		case 0:
			if v.isAttachCycle() {
				if err := v.attachCycle(ctx, nodeName); err != nil {
					log.Error("failed to attachCycle", "error", err, "case", 0)
					return checkAndCleanup(err)
				}
			} else {
				if err := v.attachAndDetachCycle(ctx, nodeName); err != nil {
					log.Error("failed to attachAndDetachCycle", "error", err, "case", 0)
					return checkAndCleanup(err)
				}
			}
		case 1:
			otherNodeName := desiredNodes[0]
			if otherNodeName == nodeName {
				if err := v.detachCycle(ctx, nodeName); err != nil {
					log.Error("failed to detachCycle", "error", err, "case", 1)
					return checkAndCleanup(err)
				}
			} else {
				if err := v.migrationCycle(ctx, otherNodeName, nodeName); err != nil {
					log.Error("failed to migrationCycle", "error", err, "case", 1)
					return checkAndCleanup(err)
				}
			}
		case 2:
			if !slices.Contains(desiredNodes, nodeName) {
				nodeName = desiredNodes[0]
			}
			if err := v.detachCycle(ctx, nodeName); err != nil {
				log.Error("failed to detachCycle", "error", err, "case", 2)
				return checkAndCleanup(err)
			}
		default:
			err := fmt.Errorf("unexpected number of active attachments (RVA): %d", len(desiredNodes))
			log.Error("error", "error", err)
			return checkAndCleanup(err)
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

	if err := v.detachCycle(cleanupCtx, ""); err != nil {
		v.log.Error("failed to detachCycle", "error", err)
	}
}

func (v *VolumeAttacher) attachCycle(ctx context.Context, nodeName string) error {
	log := v.log.With("node_name", nodeName, "func", "attachCycle")
	log.Debug("started")
	defer log.Debug("finished")

	if err := v.doAttach(ctx, nodeName); err != nil {
		log.Error("failed to doAttach", "error", err)
		return err
	}
	return nil
}

func (v *VolumeAttacher) attachAndDetachCycle(ctx context.Context, nodeName string) error {
	log := v.log.With("node_name", nodeName, "func", "attachAndDetachCycle")
	log.Debug("started")
	defer log.Debug("finished")

	// Step 1: Attach the node and wait for it to be attached
	if err := v.attachCycle(ctx, nodeName); err != nil {
		return err
	}

	// Step 2: Random delay between attach and detach
	randomDelay := randomDuration(v.cfg.Period)
	log.Debug("waiting random delay before detach", "duration", randomDelay.String())
	if err := waitWithContext(ctx, randomDelay); err != nil {
		return err
	}

	// Step 3: Get fresh RV and detach
	return v.detachCycle(ctx, nodeName)
}

func (v *VolumeAttacher) migrationCycle(ctx context.Context, otherNodeName, nodeName string) error {
	log := v.log.With("node_name", nodeName, "func", "migrationCycle")
	log.Debug("started")
	defer log.Debug("finished")

	if otherNodeName == nodeName {
		return fmt.Errorf("other node name equals selected node name: %s", nodeName)
	}

	// Step 0: Patch MaxAttachments to 2 to allow dual-attach during migration.
	if err := v.patchMaxAttachments(ctx, 2); err != nil {
		return fmt.Errorf("failed to patch MaxAttachments to 2 before migration: %w", err)
	}

	// Ensure MaxAttachments is restored and excess RVAs are cleaned up on any exit path.
	// Uses background context so cleanup runs even when the parent context is cancelled
	// (e.g. lifetime expired mid-migration).
	defer v.migrationCleanup(log)

	// Step 1: Attach the selected node and wait for it
	if err := v.attachCycle(ctx, nodeName); err != nil {
		return err
	}

	// Verify both attachment intents exist in API.
	// Do not rely on rv.status.actuallyAttachedTo: this field is legacy.
	for {
		log.Debug("waiting for both nodes to be attached", "selected_node", nodeName, "other_node", otherNodeName)

		attachedNodes, err := v.listAttachedNodesDirect(ctx)
		if err != nil {
			return err
		}

		if slices.Contains(attachedNodes, nodeName) && slices.Contains(attachedNodes, otherNodeName) {
			break
		}

		if err := waitWithContext(ctx, 1*time.Second); err != nil {
			return err
		}
	}

	// Step 2: Random delay
	randomDelay1 := randomDuration(v.cfg.Period)
	log.Debug("waiting random delay before detaching other node", "duration", randomDelay1.String())
	if err := waitWithContext(ctx, randomDelay1); err != nil {
		return err
	}

	// Step 3: Get fresh RV and detach the other node
	if err := v.detachCycle(ctx, otherNodeName); err != nil {
		return err
	}

	// Step 4: Patch MaxAttachments back to 1 after migration is complete.
	if err := v.patchMaxAttachments(ctx, 1); err != nil {
		return fmt.Errorf("failed to patch MaxAttachments to 1 after migration: %w", err)
	}

	// Step 5: Random delay
	randomDelay2 := randomDuration(v.cfg.Period)
	log.Debug("waiting random delay before detaching selected node", "duration", randomDelay2.String())
	if err := waitWithContext(ctx, randomDelay2); err != nil {
		return err
	}

	// Step 6: Get fresh RV and detach the selected node
	return v.detachCycle(ctx, nodeName)
}

// migrationCleanup restores maxAttachments to 1 and deletes excess RVAs that were
// left behind when migrationCycle was interrupted (context cancellation, error, etc.).
func (v *VolumeAttacher) migrationCleanup(log *slog.Logger) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Restore MaxAttachments to 1 (idempotent — patchMaxAttachments checks current value).
	if err := v.patchMaxAttachments(cleanupCtx, 1); err != nil {
		log.Error("migration cleanup: failed to restore MaxAttachments to 1", "error", err)
	}

	// Delete excess RVAs: when maxAttachments=1, only one RVA should exist.
	// Use direct API call for accurate snapshot before deletion.
	rvas, err := v.client.ListRVAsByRVNameDirect(cleanupCtx, v.rvName)
	if err != nil {
		log.Error("migration cleanup: failed to list RVAs", "error", err)
		return
	}
	if len(rvas) <= 1 {
		return
	}

	// Keep the first RVA (arbitrary), delete the rest.
	for _, rva := range rvas[1:] {
		if rva.Spec.NodeName == "" {
			continue
		}
		if err := v.client.DeleteRVA(cleanupCtx, v.rvName, rva.Spec.NodeName); err != nil {
			log.Error("migration cleanup: failed to delete excess RVA", "rva", rva.Name, "error", err)
		} else {
			log.Info("migration cleanup: deleted excess RVA", "rva", rva.Name)
		}
	}
}

// patchMaxAttachments patches RV spec.maxAttachments to the given value.
// Uses fresh API read to get current resourceVersion for the patch.
func (v *VolumeAttacher) patchMaxAttachments(ctx context.Context, maxAttachments byte) error {
	rv, err := v.client.GetRV(ctx, v.rvName)
	if err != nil {
		return err
	}
	if rv.Spec.MaxAttachments != nil && *rv.Spec.MaxAttachments == maxAttachments {
		return nil
	}
	original := rv.DeepCopy()
	rv.Spec.MaxAttachments = &maxAttachments
	return v.client.PatchRV(ctx, original, rv)
}

func (v *VolumeAttacher) doAttach(ctx context.Context, nodeName string) error {
	if _, err := v.client.EnsureRVA(ctx, v.rvName, nodeName); err != nil {
		return fmt.Errorf("failed to create RVA: %w", err)
	}
	if err := v.client.WaitForRVAReady(ctx, v.rvName, nodeName); err != nil {
		return fmt.Errorf("failed to wait for RVA Ready: %w", err)
	}
	return nil
}

func (v *VolumeAttacher) detachCycle(ctx context.Context, nodeName string) error {
	log := v.log.With("node_name", nodeName, "func", "detachCycle")
	log.Debug("started")
	defer log.Debug("finished")

	if err := v.doUnattach(ctx, nodeName); err != nil {
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

		attachedNodes, err := v.listAttachedNodesDirect(ctx)
		if err != nil {
			return err
		}

		if nodeName == "" {
			if len(attachedNodes) == 0 {
				return nil
			}
		} else {
			if !slices.Contains(attachedNodes, nodeName) {
				return nil
			}
		}

		if err := waitWithContext(ctx, 1*time.Second); err != nil {
			return err
		}
	}
}

func (v *VolumeAttacher) doUnattach(ctx context.Context, nodeName string) error {
	if nodeName == "" {
		// Detach from all nodes - delete all RVAs for this RV.
		rvas, err := v.client.ListRVAsByRVName(v.rvName)
		if err != nil {
			return err
		}
		var lastErr error
		for _, rva := range rvas {
			if rva.Spec.NodeName == "" {
				continue
			}
			if err := v.client.DeleteRVA(ctx, v.rvName, rva.Spec.NodeName); err != nil {
				v.log.Error("failed to delete RVA during detach-all", "rva", rva.Name, "error", err)
				lastErr = err
			}
		}
		return lastErr
	}

	// Detach from a specific node
	if err := v.client.DeleteRVA(ctx, v.rvName, nodeName); err != nil {
		return err
	}
	return nil
}

func (v *VolumeAttacher) listAttachedNodesDirect(ctx context.Context) ([]string, error) {
	rvas, err := v.client.ListRVAsByRVNameDirect(ctx, v.rvName)
	if err != nil {
		return nil, err
	}

	nodes := make([]string, 0, len(rvas))
	for _, rva := range rvas {
		if rva.Spec.NodeName == "" {
			continue
		}
		nodes = append(nodes, rva.Spec.NodeName)
	}

	return nodes, nil
}

func (v *VolumeAttacher) isAttachCycle() bool {
	//nolint:gosec // G404: math/rand is fine for non-security-critical random selection
	r := rand.Float64()
	return r < attachCycleProbability
}
