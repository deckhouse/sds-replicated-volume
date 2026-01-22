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

package rsccontroller

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"slices"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	nodeutil "k8s.io/component-helpers/node/util"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// --- Wiring / construction ---

type Reconciler struct {
	cl client.Client
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

func NewReconciler(cl client.Client) *Reconciler {
	return &Reconciler{cl: cl}
}

// --- Reconcile ---

// Reconcile pattern: Pure orchestration
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	rf := flow.BeginRootReconcile(ctx)

	// Get RSC.
	rsc, err := r.getRSC(rf.Ctx(), req.Name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return rf.Done().ToCtrl()
		}
		return rf.Fail(err).ToCtrl()
	}

	// Get RVs referencing this RSC.
	rvs, err := r.getSortedRVsByRSC(rf.Ctx(), rsc.Name)
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}

	// Reconcile main (finalizer management).
	outcome := r.reconcileMain(rf.Ctx(), rsc, rvs)
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	// Reconcile status.
	return r.reconcileStatus(rf.Ctx(), rsc, rvs).ToCtrl()
}

// reconcileMain manages the finalizer on the RSC.
//
// Reconcile pattern: Target-state driven
//
// Logic:
//   - If no finalizer → add it
//   - If deletionTimestamp set AND no RVs → remove finalizer
func (r *Reconciler) reconcileMain(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
	rvs []v1alpha1.ReplicatedVolume,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "main")
	defer rf.OnEnd(&outcome)

	actualFinalizerPresent := computeActualFinalizerPresent(rsc)
	targetFinalizerPresent := computeTargetFinalizerPresent(rsc, rvs)

	if targetFinalizerPresent == actualFinalizerPresent {
		return rf.Continue()
	}

	base := rsc.DeepCopy()
	applyFinalizer(rsc, targetFinalizerPresent)

	if err := r.patchRSC(rf.Ctx(), rsc, base, true); err != nil {
		return rf.Fail(err)
	}

	// If finalizer was removed, we're done (object will be deleted).
	if !targetFinalizerPresent {
		return rf.Done()
	}

	return rf.Continue()
}

// computeActualFinalizerPresent returns whether the controller finalizer is present on the RSC.
func computeActualFinalizerPresent(rsc *v1alpha1.ReplicatedStorageClass) bool {
	return objutilv1.HasFinalizer(rsc, v1alpha1.RSCControllerFinalizer)
}

// computeTargetFinalizerPresent returns whether the controller finalizer should be present.
// The finalizer should be present unless the RSC is being deleted AND has no RVs.
func computeTargetFinalizerPresent(rsc *v1alpha1.ReplicatedStorageClass, rvs []v1alpha1.ReplicatedVolume) bool {
	isDeleting := rsc.DeletionTimestamp != nil
	hasRVs := len(rvs) > 0

	// Keep finalizer if not deleting or if there are still RVs.
	return !isDeleting || hasRVs
}

// applyFinalizer adds or removes the controller finalizer based on target state.
func applyFinalizer(rsc *v1alpha1.ReplicatedStorageClass, targetPresent bool) {
	if targetPresent {
		objutilv1.AddFinalizer(rsc, v1alpha1.RSCControllerFinalizer)
	} else {
		objutilv1.RemoveFinalizer(rsc, v1alpha1.RSCControllerFinalizer)
	}
}

// reconcileStatus reconciles the RSC status using In-place pattern.
//
// Pattern: DeepCopy -> ensure* -> if changed -> Patch
func (r *Reconciler) reconcileStatus(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
	rvs []v1alpha1.ReplicatedVolume,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "status")
	defer rf.OnEnd(&outcome)

	// Get RSP referenced by RSC.
	rsp, err := r.getRSP(rf.Ctx(), rsc.Spec.StoragePool)
	if err != nil {
		return rf.Fail(err)
	}

	// Get LVGs referenced by RSP.
	lvgs, lvgsNotFoundErr, err := r.getSortedLVGsByRSP(rf.Ctx(), rsp)
	if err != nil {
		return rf.Fail(err)
	}

	// Get all nodes.
	nodes, err := r.getSortedNodes(rf.Ctx())
	if err != nil {
		return rf.Fail(err)
	}

	// Take patch base before mutations.
	base := rsc.DeepCopy()

	eo := flow.MergeEnsures(
		// Ensure configuration and eligible nodes.
		ensureConfigurationAndEligibleNodes(rf.Ctx(), rsc, rsp, lvgs, lvgsNotFoundErr, nodes),

		// Ensure volume counters.
		ensureVolumeSummary(rf.Ctx(), rsc, rvs),

		// Ensure rolling updates.
		ensureVolumeConditions(rf.Ctx(), rsc, rvs),
	)

	// Patch if changed.
	if eo.DidChange() {
		if err := r.patchRSCStatus(rf.Ctx(), rsc, base, eo.OptimisticLockRequired()); err != nil {
			return rf.Fail(err)
		}
	}

	return rf.Done()
}

// =============================================================================
// Ensure helpers
// =============================================================================

// ensureConfigurationAndEligibleNodes handles configuration and eligible nodes update.
//
// Algorithm:
//  1. If configuration is in sync (spec unchanged), use saved configuration; otherwise compute new one.
//  2. Validate configuration. If invalid:
//     - Set ConfigurationReady=False.
//     - If no saved configuration exists, also set EligibleNodesCalculated=False and return.
//     - Otherwise fall back to saved configuration.
//  3. Call ensureEligibleNodes to calculate/update eligible nodes.
//  4. If configuration is already in sync, return.
//  5. If EligibleNodesCalculated=False, reject configuration (ConfigurationReady=False).
//  6. Otherwise apply new configuration, set ConfigurationReady=True, require optimistic lock.
func ensureConfigurationAndEligibleNodes(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
	rsp *v1alpha1.ReplicatedStoragePool,
	lvgs []snc.LVMVolumeGroup,
	lvgsNotFoundErr error,
	nodes []corev1.Node,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "configuration-and-eligible-nodes")
	defer ef.OnEnd(&outcome)

	changed := false

	var intendedConfiguration v1alpha1.ReplicatedStorageClassConfiguration
	if isConfigurationInSync(rsc) && rsc.Status.Configuration != nil {
		intendedConfiguration = *rsc.Status.Configuration
	} else {
		intendedConfiguration = makeConfiguration(rsc)

		// Validate configuration before proceeding.
		if err := validateConfiguration(intendedConfiguration); err != nil {
			changed = applyConfigurationReadyCondFalse(rsc,
				v1alpha1.ReplicatedStorageClassCondConfigurationReadyReasonInvalidConfiguration,
				fmt.Sprintf("Configuration validation failed: %v", err),
			) || changed

			if rsc.Status.Configuration == nil {
				// First time configuration is invalid - set EligibleNodesCalculated to false.
				changed = applyEligibleNodesCalculatedCondFalse(rsc,
					v1alpha1.ReplicatedStorageClassCondEligibleNodesCalculatedReasonInvalidConfiguration,
					fmt.Sprintf("Cannot calculate eligible nodes: %v", err),
				) || changed

				return ef.Ok().ReportChangedIf(changed)
			}

			intendedConfiguration = *rsc.Status.Configuration
		}
	}

	outcome = ensureEligibleNodes(ctx, rsc, intendedConfiguration, rsp, lvgs, lvgsNotFoundErr, nodes)

	if isConfigurationInSync(rsc) {
		return outcome
	}

	if objutilv1.IsStatusConditionPresentAndFalse(rsc, v1alpha1.ReplicatedStorageClassCondEligibleNodesCalculatedType) {
		// Eligible nodes calculation failed - reject configuration.
		changed := applyConfigurationReadyCondFalse(rsc,
			v1alpha1.ReplicatedStorageClassCondConfigurationReadyReasonEligibleNodesCalculationFailed,
			"Eligible nodes calculation failed",
		)

		return outcome.ReportChangedIf(changed)
	}

	// Apply new configuration.
	rsc.Status.Configuration = &intendedConfiguration
	rsc.Status.ConfigurationGeneration = rsc.Generation

	// Set ConfigurationReady to true.
	applyConfigurationReadyCondTrue(rsc,
		v1alpha1.ReplicatedStorageClassCondConfigurationReadyReasonReady,
		"Configuration ready",
	)

	return outcome.ReportChanged().RequireOptimisticLock()
}

// ensureEligibleNodes ensures eligible nodes are calculated and up to date.
//
// Algorithm:
//  1. If RSP is nil, set EligibleNodesCalculated=False (ReplicatedStoragePoolNotFound) and return.
//  2. If any LVGs are not found, set EligibleNodesCalculated=False (LVMVolumeGroupNotFound) and return.
//  3. Validate RSP and LVGs (phase, thin pool existence). If invalid, set EligibleNodesCalculated=False.
//  4. Skip recalculation if configuration is in sync AND world state checksum matches.
//  5. Compute eligible nodes from configuration + RSP + LVGs + Nodes.
//  6. Validate eligible nodes meet replication/topology requirements. If not, set EligibleNodesCalculated=False.
//  7. Apply eligible nodes (increment revision if changed), update world state, set EligibleNodesCalculated=True.
//  8. If any changes, require optimistic lock.
func ensureEligibleNodes(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
	intendedConfiguration v1alpha1.ReplicatedStorageClassConfiguration,
	rsp *v1alpha1.ReplicatedStoragePool,
	lvgs []snc.LVMVolumeGroup,
	lvgsNotFoundErr error,
	nodes []corev1.Node,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "eligible-nodes")
	defer ef.OnEnd(&outcome)

	// Cannot calculate eligible nodes if RSP or LVGs are missing.
	// Set condition and keep old eligible nodes.
	if rsp == nil {
		changed := applyEligibleNodesCalculatedCondFalse(rsc,
			v1alpha1.ReplicatedStorageClassCondEligibleNodesCalculatedReasonReplicatedStoragePoolNotFound,
			fmt.Sprintf("ReplicatedStoragePool %q not found", rsc.Spec.StoragePool),
		)
		return ef.Ok().ReportChangedIf(changed)
	}
	if lvgsNotFoundErr != nil {
		changed := applyEligibleNodesCalculatedCondFalse(rsc,
			v1alpha1.ReplicatedStorageClassCondEligibleNodesCalculatedReasonLVMVolumeGroupNotFound,
			fmt.Sprintf("Some LVMVolumeGroups not found: %v", lvgsNotFoundErr),
		)
		return ef.Ok().ReportChangedIf(changed)
	}

	// Validate RSP and LVGs are ready and correctly configured.
	if err := validateRSPAndLVGs(rsp, lvgs); err != nil {
		changed := applyEligibleNodesCalculatedCondFalse(rsc,
			v1alpha1.ReplicatedStorageClassCondEligibleNodesCalculatedReasonInvalidStoragePoolOrLVG,
			fmt.Sprintf("RSP/LVG validation failed: %v", err),
		)
		return ef.Ok().ReportChangedIf(changed)
	}

	// Skip recalculation if external state (RSP, LVGs, Nodes) hasn't changed.
	actualEligibleNodesWorldChecksum := computeActualEligibleNodesWorldChecksum(rsp, lvgs, nodes)
	if isConfigurationInSync(rsc) && areEligibleNodesInSyncWithTheWorld(rsc, actualEligibleNodesWorldChecksum) {
		return ef.Ok()
	}

	eligibleNodes, worldStateExpiresAt := computeActualEligibleNodes(intendedConfiguration, rsp, lvgs, nodes)

	// Validate that eligible nodes meet replication and topology requirements.
	if err := validateEligibleNodes(intendedConfiguration, eligibleNodes); err != nil {
		changed := applyEligibleNodesCalculatedCondFalse(rsc,
			v1alpha1.ReplicatedStorageClassCondEligibleNodesCalculatedReasonInsufficientEligibleNodes,
			err.Error(),
		)
		return ef.Ok().ReportChangedIf(changed)
	}

	// Apply changes to status.
	changed := applyEligibleNodesAndIncrementRevisionIfChanged(rsc, eligibleNodes)

	// Update world state.
	targetWorldState := makeEligibleNodesWorldState(actualEligibleNodesWorldChecksum, worldStateExpiresAt)
	changed = applyEligibleNodesWorldState(rsc, targetWorldState) || changed

	// Set condition to success.
	changed = applyEligibleNodesCalculatedCondTrue(rsc,
		v1alpha1.ReplicatedStorageClassCondEligibleNodesCalculatedReasonCalculated,
		fmt.Sprintf("Eligible nodes calculated successfully: %d nodes", len(eligibleNodes)),
	) || changed

	if changed {
		return ef.Ok().ReportChanged().RequireOptimisticLock()
	}

	return ef.Ok()
}

// ensureVolumeSummary computes and applies volume summary.
func ensureVolumeSummary(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
	rvs []v1alpha1.ReplicatedVolume,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "volume-summary")
	defer ef.OnEnd(&outcome)

	// Compute and apply volume summary.
	summary := computeActualVolumesSummary(rsc, rvs)
	changed := applyVolumesSummary(rsc, summary)

	return ef.Ok().ReportChangedIf(changed)
}

// ensureVolumeConditions computes and applies volume-related conditions in-place.
//
// Sets ConfigurationRolledOut and VolumesSatisfyEligibleNodes conditions based on
// volume counters (StaleConfiguration, InConflictWithEligibleNodes, PendingObservation).
func ensureVolumeConditions(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
	_ []v1alpha1.ReplicatedVolume, // rvs - reserved for future rolling updates implementation
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "volume-conditions")
	defer ef.OnEnd(&outcome)

	if rsc.Status.Volumes.PendingObservation == nil {
		panic("ensureVolumeConditions: PendingObservation is nil; ensureVolumeSummary must be called first")
	}

	// If some volumes haven't observed the configuration, set alignment conditions to Unknown.
	if *rsc.Status.Volumes.PendingObservation > 0 {
		msg := fmt.Sprintf("%d volume(s) pending observation", *rsc.Status.Volumes.PendingObservation)
		changed := applyConfigurationRolledOutCondUnknown(rsc,
			v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonNewConfigurationNotYetObserved,
			msg,
		)
		changed = applyVolumesSatisfyEligibleNodesCondUnknown(rsc,
			v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesReasonUpdatedEligibleNodesNotYetObserved,
			msg,
		) || changed

		// Don't process rolling updates until all volumes acknowledge current configuration.
		return ef.Ok().ReportChangedIf(changed)
	}

	maxParallelConfigurationRollouts, maxParallelConflictResolutions := computeRollingStrategiesConfiguration(rsc)

	changed := false

	if rsc.Status.Volumes.StaleConfiguration == nil || rsc.Status.Volumes.InConflictWithEligibleNodes == nil {
		panic("ensureVolumeConditions: StaleConfiguration or InConflictWithEligibleNodes is nil; ensureVolumeSummary must be called first")
	}

	if *rsc.Status.Volumes.StaleConfiguration > 0 {
		if maxParallelConfigurationRollouts > 0 {
			changed = applyConfigurationRolledOutCondFalse(rsc,
				v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonConfigurationRolloutInProgress,
				"not implemented",
			)
		} else {
			changed = applyConfigurationRolledOutCondFalse(rsc,
				v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonConfigurationRolloutDisabled,
				"not implemented",
			)
		}
	} else {
		changed = applyConfigurationRolledOutCondTrue(rsc,
			v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonRolledOutToAllVolumes,
			"All volumes have configuration matching the storage class",
		) || changed
	}

	if *rsc.Status.Volumes.InConflictWithEligibleNodes > 0 {
		if maxParallelConflictResolutions > 0 {
			changed = applyVolumesSatisfyEligibleNodesCondFalse(rsc,
				v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesReasonConflictResolutionInProgress,
				"not implemented",
			) || changed
		} else {
			changed = applyVolumesSatisfyEligibleNodesCondFalse(rsc,
				v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesReasonManualConflictResolution,
				"not implemented",
			) || changed
		}
	} else {
		changed = applyVolumesSatisfyEligibleNodesCondTrue(rsc,
			v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesReasonAllVolumesSatisfy,
			"All volumes have replicas on eligible nodes",
		) || changed
	}

	return ef.Ok().ReportChangedIf(changed)
}

// =============================================================================
// Compute helpers
// =============================================================================

// computeRollingStrategiesConfiguration determines max parallel limits for configuration rollouts and conflict resolutions.
// Returns 0 for a strategy if it's not set to RollingUpdate/RollingRepair type (meaning disabled).
func computeRollingStrategiesConfiguration(rsc *v1alpha1.ReplicatedStorageClass) (maxParallelConfigurationRollouts, maxParallelConflictResolutions int32) {
	if rsc.Spec.ConfigurationRolloutStrategy.Type == v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategyTypeRollingUpdate {
		if rsc.Spec.ConfigurationRolloutStrategy.RollingUpdate == nil {
			panic("ConfigurationRolloutStrategy.RollingUpdate is nil but Type is RollingUpdate; API validation should prevent this")
		}
		maxParallelConfigurationRollouts = rsc.Spec.ConfigurationRolloutStrategy.RollingUpdate.MaxParallel
	}

	if rsc.Spec.EligibleNodesConflictResolutionStrategy.Type == v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategyTypeRollingRepair {
		if rsc.Spec.EligibleNodesConflictResolutionStrategy.RollingRepair == nil {
			panic("EligibleNodesConflictResolutionStrategy.RollingRepair is nil but Type is RollingRepair; API validation should prevent this")
		}
		maxParallelConflictResolutions = rsc.Spec.EligibleNodesConflictResolutionStrategy.RollingRepair.MaxParallel
	}

	return maxParallelConfigurationRollouts, maxParallelConflictResolutions
}

// makeConfiguration computes the intended configuration from RSC spec.
func makeConfiguration(rsc *v1alpha1.ReplicatedStorageClass) v1alpha1.ReplicatedStorageClassConfiguration {
	config := v1alpha1.ReplicatedStorageClassConfiguration{
		Topology:            rsc.Spec.Topology,
		Replication:         rsc.Spec.Replication,
		VolumeAccess:        rsc.Spec.VolumeAccess,
		Zones:               slices.Clone(rsc.Spec.Zones),
		SystemNetworkNames:  slices.Clone(rsc.Spec.SystemNetworkNames),
		EligibleNodesPolicy: rsc.Spec.EligibleNodesPolicy,
	}

	// Copy NodeLabelSelector if present.
	if rsc.Spec.NodeLabelSelector != nil {
		config.NodeLabelSelector = rsc.Spec.NodeLabelSelector.DeepCopy()
	}

	// Sort zones for deterministic comparison.
	sort.Strings(config.Zones)
	sort.Strings(config.SystemNetworkNames)

	return config
}

// makeEligibleNodesWorldState creates a new world state with checksum and expiration time.
func makeEligibleNodesWorldState(checksum string, expiresAt time.Time) *v1alpha1.ReplicatedStorageClassEligibleNodesWorldState {
	return &v1alpha1.ReplicatedStorageClassEligibleNodesWorldState{
		Checksum:  checksum,
		ExpiresAt: metav1.NewTime(expiresAt),
	}
}

// applyConfigurationReadyCondTrue sets the ConfigurationReady condition to True.
// Returns true if the condition was changed.
func applyConfigurationReadyCondTrue(rsc *v1alpha1.ReplicatedStorageClass, reason, message string) bool {
	return objutilv1.SetStatusCondition(rsc, metav1.Condition{
		Type:    v1alpha1.ReplicatedStorageClassCondConfigurationReadyType,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})
}

// applyConfigurationReadyCondFalse sets the ConfigurationReady condition to False.
// Returns true if the condition was changed.
func applyConfigurationReadyCondFalse(rsc *v1alpha1.ReplicatedStorageClass, reason, message string) bool {
	return objutilv1.SetStatusCondition(rsc, metav1.Condition{
		Type:    v1alpha1.ReplicatedStorageClassCondConfigurationReadyType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// applyEligibleNodesCalculatedCondTrue sets the EligibleNodesCalculated condition to True.
// Returns true if the condition was changed.
func applyEligibleNodesCalculatedCondTrue(rsc *v1alpha1.ReplicatedStorageClass, reason, message string) bool {
	return objutilv1.SetStatusCondition(rsc, metav1.Condition{
		Type:    v1alpha1.ReplicatedStorageClassCondEligibleNodesCalculatedType,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})
}

// applyEligibleNodesCalculatedCondFalse sets the EligibleNodesCalculated condition to False.
// Returns true if the condition was changed.
func applyEligibleNodesCalculatedCondFalse(rsc *v1alpha1.ReplicatedStorageClass, reason, message string) bool {
	return objutilv1.SetStatusCondition(rsc, metav1.Condition{
		Type:    v1alpha1.ReplicatedStorageClassCondEligibleNodesCalculatedType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// applyConfigurationRolledOutCondUnknown sets the ConfigurationRolledOut condition to Unknown.
// Returns true if the condition was changed.
func applyConfigurationRolledOutCondUnknown(rsc *v1alpha1.ReplicatedStorageClass, reason, message string) bool {
	return objutilv1.SetStatusCondition(rsc, metav1.Condition{
		Type:    v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType,
		Status:  metav1.ConditionUnknown,
		Reason:  reason,
		Message: message,
	})
}

// applyVolumesSatisfyEligibleNodesCondUnknown sets the VolumesSatisfyEligibleNodes condition to Unknown.
// Returns true if the condition was changed.
func applyVolumesSatisfyEligibleNodesCondUnknown(rsc *v1alpha1.ReplicatedStorageClass, reason, message string) bool {
	return objutilv1.SetStatusCondition(rsc, metav1.Condition{
		Type:    v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesType,
		Status:  metav1.ConditionUnknown,
		Reason:  reason,
		Message: message,
	})
}

// applyConfigurationRolledOutCondTrue sets the ConfigurationRolledOut condition to True.
// Returns true if the condition was changed.
func applyConfigurationRolledOutCondTrue(rsc *v1alpha1.ReplicatedStorageClass, reason, message string) bool {
	return objutilv1.SetStatusCondition(rsc, metav1.Condition{
		Type:    v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})
}

// applyConfigurationRolledOutCondFalse sets the ConfigurationRolledOut condition to False.
// Returns true if the condition was changed.
func applyConfigurationRolledOutCondFalse(rsc *v1alpha1.ReplicatedStorageClass, reason, message string) bool {
	return objutilv1.SetStatusCondition(rsc, metav1.Condition{
		Type:    v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// applyVolumesSatisfyEligibleNodesCondTrue sets the VolumesSatisfyEligibleNodes condition to True.
// Returns true if the condition was changed.
func applyVolumesSatisfyEligibleNodesCondTrue(rsc *v1alpha1.ReplicatedStorageClass, reason, message string) bool {
	return objutilv1.SetStatusCondition(rsc, metav1.Condition{
		Type:    v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesType,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})
}

// applyVolumesSatisfyEligibleNodesCondFalse sets the VolumesSatisfyEligibleNodes condition to False.
// Returns true if the condition was changed.
func applyVolumesSatisfyEligibleNodesCondFalse(rsc *v1alpha1.ReplicatedStorageClass, reason, message string) bool {
	return objutilv1.SetStatusCondition(rsc, metav1.Condition{
		Type:    v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// validateConfiguration validates that the configuration is correct and usable.
// It checks:
//   - NodeLabelSelector compiles into a valid selector
func validateConfiguration(config v1alpha1.ReplicatedStorageClassConfiguration) error {
	// Validate NodeLabelSelector.
	if config.NodeLabelSelector != nil {
		_, err := metav1.LabelSelectorAsSelector(config.NodeLabelSelector)
		if err != nil {
			return fmt.Errorf("invalid NodeLabelSelector: %w", err)
		}
	}

	return nil
}

// validateEligibleNodes validates that eligible nodes meet the requirements for the given
// replication mode and topology.
//
// Requirements by replication mode:
//   - None: at least 1 node
//   - Availability: at least 3 nodes, at least 2 with disks
//   - Consistency: 2 nodes, both with disks
//   - ConsistencyAndAvailability: at least 3 nodes with disks
//
// Additional topology requirements:
//   - TransZonal: nodes must be distributed across required number of zones
//   - Zonal: each zone must independently meet the requirements
func validateEligibleNodes(
	config v1alpha1.ReplicatedStorageClassConfiguration,
	eligibleNodes []v1alpha1.ReplicatedStorageClassEligibleNode,
) error {
	if len(eligibleNodes) == 0 {
		return fmt.Errorf("no eligible nodes")
	}

	// Count nodes and nodes with disks.
	totalNodes := len(eligibleNodes)
	nodesWithDisks := 0
	for _, n := range eligibleNodes {
		if len(n.LVMVolumeGroups) > 0 {
			nodesWithDisks++
		}
	}

	// Group nodes by zone.
	nodesByZone := make(map[string][]v1alpha1.ReplicatedStorageClassEligibleNode)
	for _, n := range eligibleNodes {
		zone := n.ZoneName
		if zone == "" {
			zone = "" // empty zone key for nodes without zone
		}
		nodesByZone[zone] = append(nodesByZone[zone], n)
	}

	// Count zones and zones with disks.
	zonesWithDisks := 0
	for _, nodes := range nodesByZone {
		for _, n := range nodes {
			if len(n.LVMVolumeGroups) > 0 {
				zonesWithDisks++
				break
			}
		}
	}

	switch config.Replication {
	case v1alpha1.ReplicationNone:
		// At least 1 node required.
		if totalNodes < 1 {
			return fmt.Errorf("replication None requires at least 1 node, have %d", totalNodes)
		}

	case v1alpha1.ReplicationAvailability:
		// At least 3 nodes, at least 2 with disks.
		if err := validateAvailabilityReplication(config.Topology, totalNodes, nodesWithDisks, nodesByZone, zonesWithDisks); err != nil {
			return err
		}

	case v1alpha1.ReplicationConsistency:
		// 2 nodes, both with disks.
		if err := validateConsistencyReplication(config.Topology, totalNodes, nodesWithDisks, nodesByZone, zonesWithDisks); err != nil {
			return err
		}

	case v1alpha1.ReplicationConsistencyAndAvailability:
		// At least 3 nodes with disks.
		if err := validateConsistencyAndAvailabilityReplication(config.Topology, nodesWithDisks, nodesByZone, zonesWithDisks); err != nil {
			return err
		}
	}

	return nil
}

// validateAvailabilityReplication validates requirements for Availability replication mode.
func validateAvailabilityReplication(
	topology v1alpha1.ReplicatedStorageClassTopology,
	totalNodes, nodesWithDisks int,
	nodesByZone map[string][]v1alpha1.ReplicatedStorageClassEligibleNode,
	zonesWithDisks int,
) error {
	switch topology {
	case v1alpha1.RSCTopologyTransZonal:
		// 3 different zones, at least 2 with disks.
		if len(nodesByZone) < 3 {
			return fmt.Errorf("replication Availability with TransZonal topology requires nodes in at least 3 zones, have %d", len(nodesByZone))
		}
		if zonesWithDisks < 2 {
			return fmt.Errorf("replication Availability with TransZonal topology requires at least 2 zones with disks, have %d", zonesWithDisks)
		}

	case v1alpha1.RSCTopologyZonal:
		// Per zone: at least 3 nodes, at least 2 with disks.
		for zone, nodes := range nodesByZone {
			zoneNodesWithDisks := 0
			for _, n := range nodes {
				if len(n.LVMVolumeGroups) > 0 {
					zoneNodesWithDisks++
				}
			}
			if len(nodes) < 3 {
				return fmt.Errorf("replication Availability with Zonal topology requires at least 3 nodes in each zone, zone %q has %d", zone, len(nodes))
			}
			if zoneNodesWithDisks < 2 {
				return fmt.Errorf("replication Availability with Zonal topology requires at least 2 nodes with disks in each zone, zone %q has %d", zone, zoneNodesWithDisks)
			}
		}

	default:
		// Ignored topology or unspecified: global check.
		if totalNodes < 3 {
			return fmt.Errorf("replication Availability requires at least 3 nodes, have %d", totalNodes)
		}
		if nodesWithDisks < 2 {
			return fmt.Errorf("replication Availability requires at least 2 nodes with disks, have %d", nodesWithDisks)
		}
	}

	return nil
}

// validateConsistencyReplication validates requirements for Consistency replication mode.
func validateConsistencyReplication(
	topology v1alpha1.ReplicatedStorageClassTopology,
	totalNodes, nodesWithDisks int,
	nodesByZone map[string][]v1alpha1.ReplicatedStorageClassEligibleNode,
	zonesWithDisks int,
) error {
	switch topology {
	case v1alpha1.RSCTopologyTransZonal:
		// 2 different zones with disks.
		if zonesWithDisks < 2 {
			return fmt.Errorf("replication Consistency with TransZonal topology requires at least 2 zones with disks, have %d", zonesWithDisks)
		}

	case v1alpha1.RSCTopologyZonal:
		// Per zone: at least 2 nodes with disks.
		for zone, nodes := range nodesByZone {
			zoneNodesWithDisks := 0
			for _, n := range nodes {
				if len(n.LVMVolumeGroups) > 0 {
					zoneNodesWithDisks++
				}
			}
			if zoneNodesWithDisks < 2 {
				return fmt.Errorf("replication Consistency with Zonal topology requires at least 2 nodes with disks in each zone, zone %q has %d", zone, zoneNodesWithDisks)
			}
		}

	default:
		// Ignored topology or unspecified: global check.
		if totalNodes < 2 {
			return fmt.Errorf("replication Consistency requires at least 2 nodes, have %d", totalNodes)
		}
		if nodesWithDisks < 2 {
			return fmt.Errorf("replication Consistency requires at least 2 nodes with disks, have %d", nodesWithDisks)
		}
	}

	return nil
}

// validateConsistencyAndAvailabilityReplication validates requirements for ConsistencyAndAvailability replication mode.
func validateConsistencyAndAvailabilityReplication(
	topology v1alpha1.ReplicatedStorageClassTopology,
	nodesWithDisks int,
	nodesByZone map[string][]v1alpha1.ReplicatedStorageClassEligibleNode,
	zonesWithDisks int,
) error {
	switch topology {
	case v1alpha1.RSCTopologyTransZonal:
		// 3 zones with disks.
		if zonesWithDisks < 3 {
			return fmt.Errorf("replication ConsistencyAndAvailability with TransZonal topology requires at least 3 zones with disks, have %d", zonesWithDisks)
		}

	case v1alpha1.RSCTopologyZonal:
		// Per zone: at least 3 nodes with disks.
		for zone, nodes := range nodesByZone {
			zoneNodesWithDisks := 0
			for _, n := range nodes {
				if len(n.LVMVolumeGroups) > 0 {
					zoneNodesWithDisks++
				}
			}
			if zoneNodesWithDisks < 3 {
				return fmt.Errorf("replication ConsistencyAndAvailability with Zonal topology requires at least 3 nodes with disks in each zone, zone %q has %d", zone, zoneNodesWithDisks)
			}
		}

	default:
		// Ignored topology or unspecified: global check.
		if nodesWithDisks < 3 {
			return fmt.Errorf("replication ConsistencyAndAvailability requires at least 3 nodes with disks, have %d", nodesWithDisks)
		}
	}

	return nil
}

// isConfigurationInSync checks if the RSC status configuration matches current generation.
func isConfigurationInSync(rsc *v1alpha1.ReplicatedStorageClass) bool {
	// Configuration must exist and generation must match.
	return rsc.Status.Configuration != nil && rsc.Status.ConfigurationGeneration == rsc.Generation
}

// areEligibleNodesInSyncWithTheWorld checks if eligible nodes are in sync with external state.
// Returns true if world state exists, checksum matches, and state has not expired.
func areEligibleNodesInSyncWithTheWorld(rsc *v1alpha1.ReplicatedStorageClass, worldChecksum string) bool {
	ws := rsc.Status.EligibleNodesWorldState
	if ws == nil {
		return false
	}
	if ws.Checksum != worldChecksum {
		return false
	}
	if time.Now().After(ws.ExpiresAt.Time) {
		return false
	}
	return true
}

// computeActualEligibleNodesWorldChecksum computes a checksum of external state that affects eligible nodes.
// It includes:
//   - RSP generation
//   - LVG generations and unschedulable annotations
//   - Node names, labels, unschedulable field, and Ready condition (status + lastTransitionTime)
//
// NOTE: lvgs and nodes MUST be pre-sorted by name for deterministic output.
func computeActualEligibleNodesWorldChecksum(
	rsp *v1alpha1.ReplicatedStoragePool,
	lvgs []snc.LVMVolumeGroup,
	nodes []corev1.Node,
) string {
	h := fnv.New128a()

	// RSP generation.
	if rsp != nil {
		_ = binary.Write(h, binary.LittleEndian, rsp.Generation)
	}

	// LVGs (pre-sorted by name).
	for i := range lvgs {
		lvg := &lvgs[i]
		_ = binary.Write(h, binary.LittleEndian, lvg.Generation)
		_, unschedulable := lvg.Annotations[v1alpha1.LVMVolumeGroupUnschedulableAnnotationKey]
		if unschedulable {
			h.Write([]byte{1})
		} else {
			h.Write([]byte{0})
		}
	}

	// Nodes (pre-sorted by name).
	for i := range nodes {
		node := &nodes[i]

		// Name.
		h.Write([]byte(node.Name))

		// Labels: sort keys for determinism.
		labelKeys := make([]string, 0, len(node.Labels))
		for k := range node.Labels {
			labelKeys = append(labelKeys, k)
		}
		sort.Strings(labelKeys)
		for _, k := range labelKeys {
			h.Write([]byte(k))
			h.Write([]byte(node.Labels[k]))
		}

		// Unschedulable.
		if node.Spec.Unschedulable {
			h.Write([]byte{1})
		} else {
			h.Write([]byte{0})
		}

		// Ready condition status and lastTransitionTime.
		_, readyCond := nodeutil.GetNodeCondition(&node.Status, corev1.NodeReady)
		if readyCond != nil {
			h.Write([]byte(string(readyCond.Status)))
			_ = binary.Write(h, binary.LittleEndian, readyCond.LastTransitionTime.Unix())
		}
	}

	return fmt.Sprintf("%032x", h.Sum(nil))
}

// computeActualEligibleNodes computes the list of eligible nodes for an RSC.
// It also returns worldStateExpiresAt - the earliest time when a node's grace period
// will expire and the eligible nodes list may change.
func computeActualEligibleNodes(
	config v1alpha1.ReplicatedStorageClassConfiguration,
	rsp *v1alpha1.ReplicatedStoragePool,
	lvgs []snc.LVMVolumeGroup,
	nodes []corev1.Node,
) (eligibleNodes []v1alpha1.ReplicatedStorageClassEligibleNode, worldStateExpiresAt time.Time) {
	if rsp == nil {
		panic("computeActualEligibleNodes: rsp is nil (invariant violation)")
	}

	// Build LVG lookup by node name.
	lvgByNode := buildLVGByNodeMap(lvgs, rsp)

	// Get grace period for not-ready nodes.
	gracePeriod := config.EligibleNodesPolicy.NotReadyGracePeriod.Duration

	// Build label selector if specified.
	var selector labels.Selector
	if config.NodeLabelSelector != nil {
		var err error
		selector, err = metav1.LabelSelectorAsSelector(config.NodeLabelSelector)
		if err != nil {
			// Configuration should have been validated before calling this function.
			panic(fmt.Sprintf("computeActualEligibleNodes: invalid NodeLabelSelector (invariant violation): %v", err))
		}
	}

	result := make([]v1alpha1.ReplicatedStorageClassEligibleNode, 0)
	var earliestExpiration time.Time

	for i := range nodes {
		node := &nodes[i]

		// Check zones filter.
		if len(config.Zones) > 0 {
			nodeZone := node.Labels[corev1.LabelTopologyZone]
			if !slices.Contains(config.Zones, nodeZone) {
				continue
			}
		}

		// Check label selector.
		if selector != nil && !selector.Matches(labels.Set(node.Labels)) {
			continue
		}

		// Check node readiness and grace period.
		nodeReady, notReadyBeyondGrace, graceExpiresAt := isNodeReadyOrWithinGrace(node, gracePeriod)
		if notReadyBeyondGrace {
			// Node has been not-ready beyond grace period - exclude from eligible nodes.
			continue
		}

		// Track earliest grace period expiration for NotReady nodes within grace.
		if !nodeReady && !graceExpiresAt.IsZero() {
			if earliestExpiration.IsZero() || graceExpiresAt.Before(earliestExpiration) {
				earliestExpiration = graceExpiresAt
			}
		}

		// Get LVGs for this node (may be empty for client-only/tiebreaker nodes).
		nodeLVGs := lvgByNode[node.Name]

		// Build eligible node entry.
		eligibleNode := v1alpha1.ReplicatedStorageClassEligibleNode{
			NodeName:        node.Name,
			ZoneName:        node.Labels[corev1.LabelTopologyZone],
			Ready:           nodeReady,
			Unschedulable:   node.Spec.Unschedulable,
			LVMVolumeGroups: nodeLVGs,
		}

		result = append(result, eligibleNode)
	}

	// Result is already sorted by node name because nodes are pre-sorted by getSortedNodes.
	return result, earliestExpiration
}

// buildLVGByNodeMap builds a map of node name to LVG entries for the RSP.
func buildLVGByNodeMap(
	lvgs []snc.LVMVolumeGroup,
	rsp *v1alpha1.ReplicatedStoragePool,
) map[string][]v1alpha1.ReplicatedStorageClassEligibleNodeLVMVolumeGroup {
	// Build RSP LVG reference lookup: lvgName -> thinPoolName (for LVMThin).
	rspLVGRef := make(map[string]string, len(rsp.Spec.LVMVolumeGroups))
	for _, ref := range rsp.Spec.LVMVolumeGroups {
		rspLVGRef[ref.Name] = ref.ThinPoolName
	}

	result := make(map[string][]v1alpha1.ReplicatedStorageClassEligibleNodeLVMVolumeGroup)

	for i := range lvgs {
		lvg := &lvgs[i]

		// Check if this LVG is referenced by the RSP.
		thinPoolName, referenced := rspLVGRef[lvg.Name]
		if !referenced {
			continue
		}

		// Get node name from LVG spec.
		nodeName := lvg.Spec.Local.NodeName
		if nodeName == "" {
			continue
		}

		// Check if LVG is unschedulable.
		_, unschedulable := lvg.Annotations[v1alpha1.LVMVolumeGroupUnschedulableAnnotationKey]

		entry := v1alpha1.ReplicatedStorageClassEligibleNodeLVMVolumeGroup{
			Name:          lvg.Name,
			ThinPoolName:  thinPoolName,
			Unschedulable: unschedulable,
		}

		result[nodeName] = append(result[nodeName], entry)
	}

	// Sort LVGs by name for deterministic output.
	for nodeName := range result {
		sort.Slice(result[nodeName], func(i, j int) bool {
			return result[nodeName][i].Name < result[nodeName][j].Name
		})
	}

	return result
}

// isNodeReadyOrWithinGrace checks node readiness and grace period status.
// Returns:
//   - nodeReady: true if node is Ready
//   - notReadyBeyondGrace: true if node is NotReady and beyond grace period (should be excluded)
//   - graceExpiresAt: when the grace period will expire (zero if node is Ready or beyond grace)
func isNodeReadyOrWithinGrace(node *corev1.Node, gracePeriod time.Duration) (nodeReady bool, notReadyBeyondGrace bool, graceExpiresAt time.Time) {
	_, readyCond := nodeutil.GetNodeCondition(&node.Status, corev1.NodeReady)

	if readyCond == nil {
		// No Ready condition - consider not ready but within grace (unknown state).
		return false, false, time.Time{}
	}

	if readyCond.Status == corev1.ConditionTrue {
		return true, false, time.Time{}
	}

	// Node is not ready - check grace period.
	graceExpiresAt = readyCond.LastTransitionTime.Time.Add(gracePeriod)
	if time.Now().After(graceExpiresAt) {
		return false, true, time.Time{} // Beyond grace period.
	}

	return false, false, graceExpiresAt // Within grace period.
}

// computeActualVolumesSummary computes volume statistics from RV conditions.
//
// If any RV hasn't acknowledged the current RSC state (name/configurationGeneration/eligibleNodesRevision mismatch),
// returns Total and PendingObservation with other counters as nil - because we don't know the real counts
// until all RVs acknowledge.
// RVs without status.storageClass are considered acknowledged (to avoid flapping on new volumes).
func computeActualVolumesSummary(rsc *v1alpha1.ReplicatedStorageClass, rvs []v1alpha1.ReplicatedVolume) v1alpha1.ReplicatedStorageClassVolumesSummary {
	total := int32(len(rvs))
	var pendingObservation, aligned, staleConfiguration, inConflictWithEligibleNodes int32

	for i := range rvs {
		rv := &rvs[i]

		// Count unobserved volumes.
		if !areRSCConfigurationAndEligibleNodesAcknowledgedByRV(rsc, rv) {
			pendingObservation++
			continue
		}

		configOK := objutilv1.IsStatusConditionPresentAndTrue(rv, v1alpha1.ReplicatedVolumeCondConfigurationReadyType)
		nodesOK := objutilv1.IsStatusConditionPresentAndTrue(rv, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType)

		if configOK && nodesOK {
			aligned++
		}

		if !configOK {
			staleConfiguration++
		}

		if !nodesOK {
			inConflictWithEligibleNodes++
		}
	}

	// If any volumes haven't observed, return only Total and PendingObservation.
	// We don't know the real counts for other counters until all RVs observe.
	if pendingObservation > 0 {
		return v1alpha1.ReplicatedStorageClassVolumesSummary{
			Total:              &total,
			PendingObservation: &pendingObservation,
		}
	}

	zero := int32(0)
	return v1alpha1.ReplicatedStorageClassVolumesSummary{
		Total:                       &total,
		PendingObservation:          &zero,
		Aligned:                     &aligned,
		StaleConfiguration:          &staleConfiguration,
		InConflictWithEligibleNodes: &inConflictWithEligibleNodes,
	}
}

// areRSCConfigurationAndEligibleNodesAcknowledgedByRV checks if the RV has acknowledged
// the current RSC configuration and eligible nodes state.
// RVs without status.storageClass are considered acknowledged (new volumes).
func areRSCConfigurationAndEligibleNodesAcknowledgedByRV(rsc *v1alpha1.ReplicatedStorageClass, rv *v1alpha1.ReplicatedVolume) bool {
	if rv.Status.StorageClass == nil {
		return true
	}
	return rv.Status.StorageClass.Name == rsc.Name &&
		rv.Status.StorageClass.ObservedConfigurationGeneration == rsc.Status.ConfigurationGeneration &&
		rv.Status.StorageClass.ObservedEligibleNodesRevision == rsc.Status.EligibleNodesRevision
}

// applyVolumesSummary applies volume summary to rsc.Status.Volumes.
// Returns true if any counter changed.
func applyVolumesSummary(rsc *v1alpha1.ReplicatedStorageClass, summary v1alpha1.ReplicatedStorageClassVolumesSummary) bool {
	changed := false
	if !ptr.Equal(rsc.Status.Volumes.Total, summary.Total) {
		rsc.Status.Volumes.Total = summary.Total
		changed = true
	}
	if !ptr.Equal(rsc.Status.Volumes.PendingObservation, summary.PendingObservation) {
		rsc.Status.Volumes.PendingObservation = summary.PendingObservation
		changed = true
	}
	if !ptr.Equal(rsc.Status.Volumes.Aligned, summary.Aligned) {
		rsc.Status.Volumes.Aligned = summary.Aligned
		changed = true
	}
	if !ptr.Equal(rsc.Status.Volumes.StaleConfiguration, summary.StaleConfiguration) {
		rsc.Status.Volumes.StaleConfiguration = summary.StaleConfiguration
		changed = true
	}
	if !ptr.Equal(rsc.Status.Volumes.InConflictWithEligibleNodes, summary.InConflictWithEligibleNodes) {
		rsc.Status.Volumes.InConflictWithEligibleNodes = summary.InConflictWithEligibleNodes
		changed = true
	}
	return changed
}

// =============================================================================
// Apply helpers
// =============================================================================

// applyEligibleNodesAndIncrementRevisionIfChanged updates eligible nodes in RSC status
// and increments revision if nodes changed. Returns true if changed.
func applyEligibleNodesAndIncrementRevisionIfChanged(
	rsc *v1alpha1.ReplicatedStorageClass,
	eligibleNodes []v1alpha1.ReplicatedStorageClassEligibleNode,
) bool {
	if areEligibleNodesEqual(rsc.Status.EligibleNodes, eligibleNodes) {
		return false
	}
	rsc.Status.EligibleNodes = eligibleNodes
	rsc.Status.EligibleNodesRevision++
	return true
}

// applyEligibleNodesWorldState updates the world state in RSC status if changed.
// Returns true if changed.
func applyEligibleNodesWorldState(
	rsc *v1alpha1.ReplicatedStorageClass,
	worldState *v1alpha1.ReplicatedStorageClassEligibleNodesWorldState,
) bool {
	if rsc.Status.EligibleNodesWorldState != nil &&
		rsc.Status.EligibleNodesWorldState.Checksum == worldState.Checksum &&
		rsc.Status.EligibleNodesWorldState.ExpiresAt.Equal(&worldState.ExpiresAt) {
		return false
	}
	rsc.Status.EligibleNodesWorldState = worldState
	return true
}

// =============================================================================
// Comparison helpers
// =============================================================================

// areEligibleNodesEqual compares two eligible nodes slices for equality.
func areEligibleNodesEqual(a, b []v1alpha1.ReplicatedStorageClassEligibleNode) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].NodeName != b[i].NodeName ||
			a[i].ZoneName != b[i].ZoneName ||
			a[i].Ready != b[i].Ready ||
			a[i].Unschedulable != b[i].Unschedulable {
			return false
		}
		if !areLVGsEqual(a[i].LVMVolumeGroups, b[i].LVMVolumeGroups) {
			return false
		}
	}
	return true
}

// areLVGsEqual compares two LVG slices for equality.
func areLVGsEqual(a, b []v1alpha1.ReplicatedStorageClassEligibleNodeLVMVolumeGroup) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name ||
			a[i].ThinPoolName != b[i].ThinPoolName ||
			a[i].Unschedulable != b[i].Unschedulable {
			return false
		}
	}
	return true
}

// validateRSPAndLVGs validates that RSP and LVGs are ready and correctly configured.
// It checks:
//   - RSP phase is Completed
//   - For LVMThin type, thinPoolName exists in each referenced LVG's Spec.ThinPools
func validateRSPAndLVGs(rsp *v1alpha1.ReplicatedStoragePool, lvgs []snc.LVMVolumeGroup) error {
	// Build LVG lookup by name.
	lvgByName := make(map[string]*snc.LVMVolumeGroup, len(lvgs))
	for i := range lvgs {
		lvgByName[lvgs[i].Name] = &lvgs[i]
	}

	// Validate ThinPool references for LVMThin type.
	if rsp.Spec.Type == v1alpha1.RSPTypeLVMThin {
		for _, rspLVG := range rsp.Spec.LVMVolumeGroups {
			if rspLVG.ThinPoolName == "" {
				return fmt.Errorf("LVMVolumeGroup %q: thinPoolName is required for LVMThin type", rspLVG.Name)
			}

			lvg, ok := lvgByName[rspLVG.Name]
			if !ok {
				// LVG not found in the provided list - this is a bug in the calling code.
				panic(fmt.Sprintf("validateRSPAndLVGs: LVG %q not found in lvgByName (invariant violation)", rspLVG.Name))
			}

			// Check if ThinPool exists in LVG.
			thinPoolFound := false
			for _, tp := range lvg.Spec.ThinPools {
				if tp.Name == rspLVG.ThinPoolName {
					thinPoolFound = true
					break
				}
			}
			if !thinPoolFound {
				return fmt.Errorf("LVMVolumeGroup %q: thinPool %q not found in Spec.ThinPools", rspLVG.Name, rspLVG.ThinPoolName)
			}
		}
	}

	return nil
}

// =============================================================================
// Single-call I/O helper categories
// =============================================================================

// getRSC fetches an RSC by name.
func (r *Reconciler) getRSC(ctx context.Context, name string) (*v1alpha1.ReplicatedStorageClass, error) {
	var rsc v1alpha1.ReplicatedStorageClass
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, &rsc); err != nil {
		return nil, err
	}
	return &rsc, nil
}

// getRSP fetches an RSP by name. Returns (nil, nil) if not found.
func (r *Reconciler) getRSP(ctx context.Context, name string) (*v1alpha1.ReplicatedStoragePool, error) {
	var rsp v1alpha1.ReplicatedStoragePool
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, &rsp); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &rsp, nil
}

// getSortedLVGsByRSP fetches LVGs referenced by the given RSP, sorted by name.
// Returns:
//   - lvgs: successfully found LVGs, sorted by name
//   - lvgsNotFoundErr: merged error for any NotFound cases (nil if all found)
//   - err: non-NotFound error (if any occurred, lvgs will be nil)
func (r *Reconciler) getSortedLVGsByRSP(ctx context.Context, rsp *v1alpha1.ReplicatedStoragePool) (
	lvgs []snc.LVMVolumeGroup,
	lvgsNotFoundErr error,
	err error,
) {
	if rsp == nil || len(rsp.Spec.LVMVolumeGroups) == 0 {
		return nil, nil, nil
	}

	lvgs = make([]snc.LVMVolumeGroup, 0, len(rsp.Spec.LVMVolumeGroups))
	var notFoundErrs []error

	for _, lvgRef := range rsp.Spec.LVMVolumeGroups {
		var lvg snc.LVMVolumeGroup
		if err := r.cl.Get(ctx, client.ObjectKey{Name: lvgRef.Name}, &lvg); err != nil {
			if apierrors.IsNotFound(err) {
				notFoundErrs = append(notFoundErrs, err)
				continue
			}
			// Non-NotFound error - fail immediately.
			return nil, nil, err
		}
		lvgs = append(lvgs, lvg)
	}

	// Sort by name for deterministic output.
	sort.Slice(lvgs, func(i, j int) bool {
		return lvgs[i].Name < lvgs[j].Name
	})

	return lvgs, errors.Join(notFoundErrs...), nil
}

// getSortedNodes fetches all nodes sorted by name.
func (r *Reconciler) getSortedNodes(ctx context.Context) ([]corev1.Node, error) {
	var list corev1.NodeList
	if err := r.cl.List(ctx, &list); err != nil {
		return nil, err
	}
	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].Name < list.Items[j].Name
	})
	return list.Items, nil
}

// getSortedRVsByRSC fetches RVs referencing a specific RSC using the index, sorted by name.
func (r *Reconciler) getSortedRVsByRSC(ctx context.Context, rscName string) ([]v1alpha1.ReplicatedVolume, error) {
	var list v1alpha1.ReplicatedVolumeList
	if err := r.cl.List(ctx, &list, client.MatchingFields{
		indexes.IndexFieldRVByReplicatedStorageClassName: rscName,
	}); err != nil {
		return nil, err
	}
	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].Name < list.Items[j].Name
	})
	return list.Items, nil
}

// patchRSC patches the RSC main resource.
func (r *Reconciler) patchRSC(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
	base *v1alpha1.ReplicatedStorageClass,
	optimisticLock bool,
) error {
	var patch client.Patch
	if optimisticLock {
		patch = client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	} else {
		patch = client.MergeFrom(base)
	}
	return r.cl.Patch(ctx, rsc, patch)
}

// patchRSCStatus patches the RSC status subresource.
func (r *Reconciler) patchRSCStatus(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
	base *v1alpha1.ReplicatedStorageClass,
	optimisticLock bool,
) error {
	var patch client.Patch
	if optimisticLock {
		patch = client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	} else {
		patch = client.MergeFrom(base)
	}
	return r.cl.Status().Patch(ctx, rsc, patch)
}
