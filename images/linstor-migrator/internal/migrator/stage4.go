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

package migrator

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubecl "sigs.k8s.io/controller-runtime/pkg/client"

	srvv1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/config"
)

// runStage4 polls until all ReplicatedStorageClasses referenced by switched ReplicatedVolumes
// become Ready, then switches those ReplicatedVolumes from Manual to Auto configuration.
// It also creates temporary conversion ReplicatedStoragePools for RSC that still use the
// deprecated spec.storagePool field, and cleans them up once the corresponding RSC is
// converted (spec.storagePool cleared).
func (m *Migrator) runStage4(ctx context.Context) error {
	m.log.Info("stage 4: starting switch ReplicatedVolumes to Auto configuration")

	if err := m.updateMigrationStateRetrying(ctx, config.StateStage4Started); err != nil {
		return err
	}

	ticker := time.NewTicker(m.stage4PollInterval)
	defer ticker.Stop()

	for iteration := 1; ; iteration++ {
		nonConvertibleRSCs, err := m.ensureConversionRSPs(ctx)
		if err != nil {
			return err
		}

		// Sub-stage 4c: switch ReplicatedVolumes to Auto.
		// pendingRV counts only volumes whose RSC is in a non-terminal, non-Ready phase that can
		// still reach Ready (WaitingForStoragePool with conversion RSP created, RollingOut, or
		// unknown). Volumes with a missing RSC, a terminal-phase RSC, or a non-convertible
		// WaitingForStoragePool RSC are marked auto-configuration-blocked and do not contribute
		// to pending.
		pendingRV, err := m.switchRVsToAuto(ctx, nonConvertibleRSCs)
		if err != nil {
			return err
		}

		leftoverRSPs, err := m.cleanupConversionRSPs(ctx)
		if err != nil {
			return err
		}

		if pendingRV == 0 && leftoverRSPs == 0 {
			m.log.Info("stage 4: completed, migration finished", "iterations", iteration)
			return m.updateMigrationStateRetrying(ctx, config.StateAllCompleted)
		}

		if iteration >= m.stage4MaxIterations {
			return fmt.Errorf("stage 4: exceeded %d iterations waiting for ReplicatedStorageClasses to become Ready; pending ReplicatedVolumes: %d, pending conversion RSPs: %d",
				m.stage4MaxIterations, pendingRV, leftoverRSPs)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// ensureConversionRSPs is sub-stage 4a: for every ReplicatedStorageClass that still uses the
// deprecated spec.storagePool, it ensures a temporary ReplicatedStoragePool named after the
// storagePool value exists so the RSC controller can migrate away from the deprecated field.
// The spec is copied from the corresponding linstor-auto-* RSP. Created RSPs are labeled with
// MigrationConversionReplicatedStoragePoolLabelKey so cleanup finds only owned pools.
// Returns a set of RSC names for which the conversion RSP could not be created (source
// linstor-auto-* RSP not found). These RSC are non-convertible and must be handled by
// switchRVsToAuto as terminal for migration.
func (m *Migrator) ensureConversionRSPs(ctx context.Context) (map[string]struct{}, error) {
	var rscList srvv1alpha1.ReplicatedStorageClassList
	if err := m.retryTransient(ctx, "list ReplicatedStorageClasses for conversion RSPs", func() error {
		return m.client.List(ctx, &rscList)
	}); err != nil {
		return nil, fmt.Errorf("list ReplicatedStorageClasses: %w", err)
	}

	nonConvertible := make(map[string]struct{})

	for _, rsc := range rscList.Items {
		if rsc.Spec.StoragePool == "" { //nolint:staticcheck // SA1019: migration from deprecated StoragePool
			m.log.Debug("RSC has no deprecated storagePool; nothing to convert", "rsc", rsc.Name)
			continue
		}

		targetRSPName := rsc.Spec.StoragePool //nolint:staticcheck // SA1019: migration from deprecated StoragePool
		sourceRSPName := AutoReplicatedStoragePoolName(targetRSPName)

		// Check if target RSP already exists. NotFound is swallowed inside the retry
		// closure (it is not a transient error and must not be retried).
		targetRSP := &srvv1alpha1.ReplicatedStoragePool{}
		targetOwned := false
		targetExistsForeign := false
		if err := m.retryTransient(ctx, "get target ReplicatedStoragePool", func() error {
			gerr := m.client.Get(ctx, types.NamespacedName{Name: targetRSPName}, targetRSP)
			switch {
			case gerr == nil:
				if targetRSP.Labels[srvv1alpha1.MigrationConversionReplicatedStoragePoolLabelKey] == srvv1alpha1.MigrationConversionReplicatedStoragePoolLabelValue {
					targetOwned = true
				} else {
					targetExistsForeign = true
				}
				return nil
			case apierrors.IsNotFound(gerr):
				return nil
			default:
				return gerr
			}
		}); err != nil {
			return nil, fmt.Errorf("get target ReplicatedStoragePool %q for RSC %q: %w", targetRSPName, rsc.Name, err)
		}
		if targetOwned {
			m.log.Debug("conversion ReplicatedStoragePool already exists and owned by migrator",
				"rsc", rsc.Name, "conversionRSP", targetRSPName)
			continue
		}
		if targetExistsForeign {
			m.log.Debug("foreign ReplicatedStoragePool exists; migrator will not manage it",
				"rsc", rsc.Name, "targetRSP", targetRSPName)
			continue
		}

		// Source RSP (linstor-auto-*) must exist to copy its spec. NotFound is swallowed inside
		// the retry closure so it is not retried; the missing source is reported as a warning
		// and the RSC is left unconverted.
		sourceRSP := &srvv1alpha1.ReplicatedStoragePool{}
		sourceExists := false
		if err := m.retryTransient(ctx, "get source ReplicatedStoragePool", func() error {
			gerr := m.client.Get(ctx, types.NamespacedName{Name: sourceRSPName}, sourceRSP)
			switch {
			case gerr == nil:
				sourceExists = true
				return nil
			case apierrors.IsNotFound(gerr):
				sourceExists = false
				return nil
			default:
				return gerr
			}
		}); err != nil {
			return nil, fmt.Errorf("get source ReplicatedStoragePool %q for RSC %q: %w", sourceRSPName, rsc.Name, err)
		}
		if !sourceExists {
			m.log.Warn("cannot create conversion RSP: source linstor-auto-* not found; RSC left unconverted",
				"rsc", rsc.Name, "targetRSP", targetRSPName, "sourceRSP", sourceRSPName)
			nonConvertible[rsc.Name] = struct{}{}
			continue
		}

		copyRSP := &srvv1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{
				Name: targetRSPName,
				Labels: map[string]string{
					srvv1alpha1.MigrationConversionReplicatedStoragePoolLabelKey: srvv1alpha1.MigrationConversionReplicatedStoragePoolLabelValue,
				},
			},
			Spec: sourceRSP.Spec,
		}

		log := m.log.With("rsc", rsc.Name, "conversionRSP", targetRSPName, "sourceRSP", sourceRSPName)
		if err := m.retryTransient(ctx, "create conversion RSP", func() error {
			return m.createIfNotExists(ctx, log, copyRSP, "ReplicatedStoragePool")
		}); err != nil {
			return nil, err
		}
	}

	return nonConvertible, nil
}

// switchRVsToAuto is sub-stage 4c: finds ReplicatedVolumes with the
// SwitchToAutoConfigurationLabelKey label, checks that their referenced
// ReplicatedStorageClass (via PersistentVolume) is Ready, and patches them
// from Manual to Auto configuration. Returns the count of ReplicatedVolumes
// still pending (RSC in a non-terminal, non-Ready phase that can still reach
// Ready: WaitingForStoragePool with a conversion RSP created, RollingOut, or
// unknown/empty phase).
// ReplicatedVolumes with a missing RSC, a terminal-phase RSC
// (InsufficientNodes, InvalidConfiguration, PartiallyAligned, Terminating),
// or a non-convertible WaitingForStoragePool RSC are marked
// auto-configuration-blocked instead and do not contribute to pending.
// nonConvertibleRSCs is the set of RSC names returned by ensureConversionRSPs whose conversion
// RSP could not be created; WaitingForStoragePool RSC in this set are treated as terminal
// (blocked) rather than pending.
func (m *Migrator) switchRVsToAuto(ctx context.Context, nonConvertibleRSCs map[string]struct{}) (int, error) {
	var rvList srvv1alpha1.ReplicatedVolumeList
	if err := m.retryTransient(ctx, "list ReplicatedVolumes with switch-to-auto label", func() error {
		return m.client.List(ctx, &rvList, kubecl.MatchingLabels{
			srvv1alpha1.SwitchToAutoConfigurationLabelKey: srvv1alpha1.SwitchToAutoConfigurationLabelValue,
		})
	}); err != nil {
		return 0, fmt.Errorf("list ReplicatedVolumes with switch-to-auto label: %w", err)
	}

	// No pending ReplicatedVolumes: skip the RSC list/build and return early.
	if len(rvList.Items) == 0 {
		m.log.Debug("no ReplicatedVolumes with switch-to-auto label found in the cluster")
		return 0, nil
	}

	var rscList srvv1alpha1.ReplicatedStorageClassList
	if err := m.retryTransient(ctx, "list ReplicatedStorageClasses for switch-to-auto", func() error {
		return m.client.List(ctx, &rscList)
	}); err != nil {
		return 0, fmt.Errorf("list ReplicatedStorageClasses: %w", err)
	}

	rscMap := make(map[string]*srvv1alpha1.ReplicatedStorageClass, len(rscList.Items))
	for i := range rscList.Items {
		rscMap[rscList.Items[i].Name] = &rscList.Items[i]
	}

	pending := 0
	for _, rv := range rvList.Items {
		rvLog := m.log.With("replicatedVolume", rv.Name)

		// PV name == RV name (invariant from stage 1). NotFound is swallowed inside the retry
		// closure so it is not retried; the orphaned RV is handled by marking it with the
		// no-persistent-volume label.
		pv := &corev1.PersistentVolume{}
		pvExists := true
		if err := m.retryTransient(ctx, "get PersistentVolume for switch-to-auto", func() error {
			gerr := m.client.Get(ctx, types.NamespacedName{Name: rv.Name}, pv)
			switch {
			case gerr == nil:
				return nil
			case apierrors.IsNotFound(gerr):
				pvExists = false
				return nil
			default:
				return gerr
			}
		}); err != nil {
			return 0, fmt.Errorf("get PersistentVolume %q: %w", rv.Name, err)
		}
		if !pvExists {
			rvLog.Warn("PersistentVolume not found, marking ReplicatedVolume as orphaned")
			if perr := m.patchRVMarkOrphaned(ctx, &rv); perr != nil {
				return 0, perr
			}
			continue
		}

		rscName := pv.Spec.StorageClassName
		rsc, ok := rscMap[rscName]
		if !ok {
			rvLog.Warn("PersistentVolume references StorageClass with no matching ReplicatedStorageClass; marking ReplicatedVolume as blocked from Auto configuration",
				"storageClass", rscName)
			if perr := m.patchRVBlockAutoConfiguration(ctx, &rv, fmt.Sprintf("ReplicatedStorageClass %q not found", rscName)); perr != nil {
				return 0, perr
			}
			continue
		}

		if rsc.Status.Phase == srvv1alpha1.ReplicatedStorageClassPhaseReady {
			// Ready: switch to Auto.
			if err := m.patchRVToAuto(ctx, &rv, rscName); err != nil {
				return 0, err
			}
			continue
		}

		if isTerminalRSCPhase(rsc.Status.Phase) {
			// Terminal phase: the RSC cannot reach Ready without manual intervention.
			// Mark the RV as blocked so the migration does not wait indefinitely.
			rvLog.Warn("ReplicatedStorageClass is in a terminal phase; marking ReplicatedVolume as blocked from Auto configuration",
				"rsc", rscName, "phase", rsc.Status.Phase)
			if perr := m.patchRVBlockAutoConfiguration(ctx, &rv, fmt.Sprintf("ReplicatedStorageClass %q is in terminal phase %q", rscName, rsc.Status.Phase)); perr != nil {
				return 0, perr
			}
			continue
		}

		// Non-terminal, non-Ready phase.
		if rsc.Status.Phase == srvv1alpha1.ReplicatedStorageClassPhaseWaitingForStoragePool {
			if _, blocked := nonConvertibleRSCs[rscName]; blocked {
				// Conversion RSP could not be created (no source linstor-auto-*); the RSC will
				// never reach Ready. Mark the RV as blocked instead of waiting for the iteration
				// limit.
				rvLog.Warn("ReplicatedStorageClass is in WaitingForStoragePool and conversion RSP could not be created; marking ReplicatedVolume as blocked from Auto configuration",
					"rsc", rscName)
				if perr := m.patchRVBlockAutoConfiguration(ctx, &rv, fmt.Sprintf("ReplicatedStorageClass %q is in WaitingForStoragePool and conversion RSP could not be created", rscName)); perr != nil {
					return 0, perr
				}
				continue
			}
		}

		// Other non-terminal phases (WaitingForStoragePool with conversion RSP created,
		// RollingOut, empty): wait for the RSC controller to make progress.
		rvLog.Debug("waiting for ReplicatedStorageClass to become Ready",
			"rsc", rscName, "phase", rsc.Status.Phase)
		pending++
		continue
	}

	return pending, nil
}

// cleanupConversionRSPs is sub-stage 4d: deletes temporary conversion ReplicatedStoragePools
// (labeled with MigrationConversionReplicatedStoragePoolLabelKey) when no ReplicatedStorageClass
// references them anymore (spec.storagePool has been cleared by the RSC controller). Only
// RSPs owned by the migrator (labeled) are deleted; foreign RSPs with the same name are never
// touched. Returns the count of labeled conversion RSPs still present (RSC still references them).
func (m *Migrator) cleanupConversionRSPs(ctx context.Context) (int, error) {
	// List only RSPs owned by the migrator (labeled). Crash-safe: this recovers all
	// conversion RSPs from cluster state on migrator restart, unlike an in-memory map.
	var rspList srvv1alpha1.ReplicatedStoragePoolList
	if err := m.retryTransient(ctx, "list conversion ReplicatedStoragePools", func() error {
		return m.client.List(ctx, &rspList, kubecl.MatchingLabels{
			srvv1alpha1.MigrationConversionReplicatedStoragePoolLabelKey: srvv1alpha1.MigrationConversionReplicatedStoragePoolLabelValue,
		})
	}); err != nil {
		return 0, fmt.Errorf("list conversion ReplicatedStoragePools: %w", err)
	}

	if len(rspList.Items) == 0 {
		m.log.Debug("no conversion ReplicatedStoragePools to clean up")
		return 0, nil
	}

	// Build set of RSP names still referenced by unconverted RSC (spec.storagePool != "").
	var rscList srvv1alpha1.ReplicatedStorageClassList
	if err := m.retryTransient(ctx, "list ReplicatedStorageClasses for conversion RSP cleanup", func() error {
		return m.client.List(ctx, &rscList)
	}); err != nil {
		return 0, fmt.Errorf("list ReplicatedStorageClasses: %w", err)
	}

	stillPending := make(map[string]struct{})
	for _, rsc := range rscList.Items {
		if rsc.Spec.StoragePool != "" { //nolint:staticcheck // SA1019: migration from deprecated StoragePool
			stillPending[rsc.Spec.StoragePool] = struct{}{} //nolint:staticcheck // SA1019: migration from deprecated StoragePool
		}
	}

	leftover := 0
	for i := range rspList.Items {
		rsp := &rspList.Items[i]
		if _, pending := stillPending[rsp.Name]; pending {
			m.log.Debug("keeping conversion ReplicatedStoragePool: RSC still references it",
				"conversionRSP", rsp.Name)
			leftover++
			continue
		}

		if err := m.retryTransient(ctx, "delete conversion RSP", func() error {
			toDelete := &srvv1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: rsp.Name},
			}
			err := m.client.Delete(ctx, toDelete)
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}); err != nil {
			return 0, fmt.Errorf("delete conversion RSP %q: %w", rsp.Name, err)
		}
		m.log.Info("deleted conversion ReplicatedStoragePool", "replicatedStoragePool", rsp.Name)
	}

	return leftover, nil
}

// isTerminalRSCPhase reports whether the ReplicatedStorageClass phase represents a state from
// which it cannot reach Ready without manual intervention. Such RSC block the switch of their
// ReplicatedVolumes to Auto: the migrator marks those volumes as auto-configuration-blocked
// instead of waiting indefinitely.
//
// Terminal phases:
//   - InsufficientNodes: eligible nodes do not meet FTT/GMDR/topology requirements.
//   - InvalidConfiguration: spec validation failed.
//   - PartiallyAligned: divergence exists but all auto-fixes are disabled (NewVolumesOnly
//     rollout strategy or Manual conflict resolution); requires manual strategy/topology change.
//   - Terminating: the storage class is being deleted.
//
// Non-terminal (waited for) phases: Ready (success), WaitingForStoragePool (our conversion RSP
// should help), RollingOut (active progress), empty phase (unknown, wait).
func isTerminalRSCPhase(phase srvv1alpha1.ReplicatedStorageClassPhase) bool {
	switch phase {
	case srvv1alpha1.ReplicatedStorageClassPhaseInsufficientNodes,
		srvv1alpha1.ReplicatedStorageClassPhaseInvalidConfiguration,
		srvv1alpha1.ReplicatedStorageClassPhasePartiallyAligned,
		srvv1alpha1.ReplicatedStorageClassPhaseTerminating:
		return true
	default:
		return false
	}
}

// patchRVToAuto switches a ReplicatedVolume from Manual to Auto configuration
// by setting ConfigurationMode to Auto, linking to the given ReplicatedStorageClass,
// and clearing the manual configuration. The switch-to-auto label is also removed.
func (m *Migrator) patchRVToAuto(ctx context.Context, rv *srvv1alpha1.ReplicatedVolume, rscName string) error {
	return m.retryTransient(ctx, "patch ReplicatedVolume to Auto", func() error {
		fresh := &srvv1alpha1.ReplicatedVolume{}
		if err := m.client.Get(ctx, types.NamespacedName{Name: rv.Name}, fresh); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}

		// Skip if the RV is already in Auto mode with the correct RSC and no switch-to-auto label.
		_, hasSwitch := fresh.Labels[srvv1alpha1.SwitchToAutoConfigurationLabelKey]
		if fresh.Spec.ConfigurationMode == srvv1alpha1.ReplicatedVolumeConfigurationModeAuto &&
			fresh.Spec.ReplicatedStorageClassName == rscName &&
			fresh.Spec.ManualConfiguration == nil &&
			!hasSwitch {
			return nil
		}

		old := fresh.DeepCopy()
		fresh.Spec.ConfigurationMode = srvv1alpha1.ReplicatedVolumeConfigurationModeAuto
		fresh.Spec.ReplicatedStorageClassName = rscName
		fresh.Spec.ManualConfiguration = nil

		if fresh.Labels != nil {
			delete(fresh.Labels, srvv1alpha1.SwitchToAutoConfigurationLabelKey)
			if len(fresh.Labels) == 0 {
				fresh.Labels = nil
			}
		}

		if err := m.client.Patch(ctx, fresh, kubecl.MergeFrom(old)); err != nil {
			if apierrors.IsNotFound(err) {
				// RV was deleted between Get and Patch; nothing to patch.
				return nil
			}
			return err
		}

		m.log.Info("switched ReplicatedVolume to Auto configuration",
			"replicatedVolume", rv.Name, "replicatedStorageClass", rscName)
		return nil
	})
}

// patchRVMarkOrphaned marks a ReplicatedVolume as orphaned: the PersistentVolume that was
// present at migration time no longer exists. It removes the switch-to-auto label and sets the
// no-persistent-volume label, matching the convention used in stage 1 for resources without a
// PV. The ReplicatedVolume stays in Manual mode.
func (m *Migrator) patchRVMarkOrphaned(ctx context.Context, rv *srvv1alpha1.ReplicatedVolume) error {
	return m.retryTransient(ctx, "mark ReplicatedVolume as orphaned", func() error {
		fresh := &srvv1alpha1.ReplicatedVolume{}
		if err := m.client.Get(ctx, types.NamespacedName{Name: rv.Name}, fresh); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}

		_, hasSwitch := fresh.Labels[srvv1alpha1.SwitchToAutoConfigurationLabelKey]
		hasNoPV := fresh.Labels[srvv1alpha1.NoPersistentVolumeLabelKey] == srvv1alpha1.NoPersistentVolumeLabelValue
		if !hasSwitch && hasNoPV {
			// Already marked as orphaned; nothing to do.
			return nil
		}

		old := fresh.DeepCopy()
		if fresh.Labels == nil {
			fresh.Labels = make(map[string]string)
		}
		delete(fresh.Labels, srvv1alpha1.SwitchToAutoConfigurationLabelKey)
		fresh.Labels[srvv1alpha1.NoPersistentVolumeLabelKey] = srvv1alpha1.NoPersistentVolumeLabelValue
		if len(fresh.Labels) == 0 {
			fresh.Labels = nil
		}

		if err := m.client.Patch(ctx, fresh, kubecl.MergeFrom(old)); err != nil {
			if apierrors.IsNotFound(err) {
				// RV was deleted between Get and Patch; nothing to patch.
				return nil
			}
			return err
		}

		m.log.Info("marked ReplicatedVolume as orphaned (no PersistentVolume)",
			"replicatedVolume", rv.Name)
		return nil
	})
}

// patchRVBlockAutoConfiguration marks a ReplicatedVolume as blocked from Auto configuration
// by removing the switch-to-auto label and setting the auto-configuration-blocked label.
// The ReplicatedVolume stays in Manual mode. Used when the referenced ReplicatedStorageClass
// is missing or in a terminal phase.
func (m *Migrator) patchRVBlockAutoConfiguration(ctx context.Context, rv *srvv1alpha1.ReplicatedVolume, reason string) error {
	return m.retryTransient(ctx, "mark ReplicatedVolume as auto-configuration-blocked", func() error {
		fresh := &srvv1alpha1.ReplicatedVolume{}
		if err := m.client.Get(ctx, types.NamespacedName{Name: rv.Name}, fresh); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}

		if fresh.Labels == nil {
			fresh.Labels = make(map[string]string)
		}
		_, hasSwitch := fresh.Labels[srvv1alpha1.SwitchToAutoConfigurationLabelKey]
		hasBlocked := fresh.Labels[srvv1alpha1.AutoConfigurationBlockedLabelKey] == srvv1alpha1.AutoConfigurationBlockedLabelValue
		if !hasSwitch && hasBlocked {
			// Already marked as blocked; nothing to do.
			return nil
		}

		old := fresh.DeepCopy()
		delete(fresh.Labels, srvv1alpha1.SwitchToAutoConfigurationLabelKey)
		fresh.Labels[srvv1alpha1.AutoConfigurationBlockedLabelKey] = srvv1alpha1.AutoConfigurationBlockedLabelValue
		if len(fresh.Labels) == 0 {
			fresh.Labels = nil
		}

		if err := m.client.Patch(ctx, fresh, kubecl.MergeFrom(old)); err != nil {
			if apierrors.IsNotFound(err) {
				// RV was deleted between Get and Patch; nothing to patch.
				return nil
			}
			return err
		}

		m.log.Warn("blocked ReplicatedVolume from Auto configuration; manual intervention required",
			"replicatedVolume", rv.Name, "reason", reason)
		return nil
	})
}
