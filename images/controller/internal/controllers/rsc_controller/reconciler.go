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
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"slices"
	"sort"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// ──────────────────────────────────────────────────────────────────────────────
// Constants
//

// rvoNameSuffixUpdateConfig is the short suffix for UpdateConfiguration RVO names.
const rvoNameSuffixUpdateConfig = "updateConfig"

// rvoNameSuffixResolveNodes is the short suffix for ResolveEligibleNodesConflict RVO names.
const rvoNameSuffixResolveNodes = "resolveNodes"

// rvoNameSuffix returns the short suffix for generating RVO resource names.
func rvoNameSuffix(opType v1alpha1.ReplicatedVolumeOperationType) string {
	switch opType {
	case v1alpha1.ReplicatedVolumeOperationTypeUpdateConfiguration:
		return rvoNameSuffixUpdateConfig
	case v1alpha1.ReplicatedVolumeOperationTypeResolveEligibleNodesConflict:
		return rvoNameSuffixResolveNodes
	default:
		return string(opType)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Wiring / construction
//

type Reconciler struct {
	cl client.Client
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

func NewReconciler(cl client.Client) *Reconciler {
	return &Reconciler{cl: cl}
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile
//

// Reconcile pattern: Pure orchestration
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	rf := flow.BeginRootReconcile(ctx)

	// Get RSC. Returns nil if not found (already deleted).
	rsc, err := r.getRSC(rf.Ctx(), req.Name)
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}

	// Get RVs referencing this RSC.
	rvs, err := r.getSortedRVsByRSC(rf.Ctx(), req.Name)
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}

	// Get all RSPs that reference this RSC (via usedBy).
	usedStoragePoolNames, err := r.getUsedStoragePoolNames(rf.Ctx(), req.Name)
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}

	// If RSC is deleted (or being deleted with no RVs), clean up usedBy and remove finalizer.
	if rscShouldBeDeleted(rsc, rvs) {
		return r.reconcileDeletion(rf.Ctx(), req.Name, rsc, usedStoragePoolNames).ToCtrl()
	}

	// Reconcile migration from RSP (deprecated storagePool field).
	if rsc.Spec.StoragePool != "" {
		if outcome := r.reconcileMigrationFromRSP(rf.Ctx(), rsc); outcome.ShouldReturn() {
			return outcome.ToCtrl()
		}
	}

	// Reconcile metadata (finalizer management).
	if outcome := r.reconcileMetadata(rf.Ctx(), rsc); outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	// Compute target storage pool name (cached if already computed for this generation).
	targetStoragePoolName := computeTargetStoragePool(rsc)

	// Ensure auto-generated RSP exists and is configured.
	rsp, outcome := r.reconcileRSP(rf.Ctx(), rsc, targetStoragePoolName)
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	// Get existing RVOs for this RSC (cache refs, read-only).
	rvos, err := r.getRVOsCacheRefs(rf.Ctx(), rsc.Name)
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}

	// Take patch base before mutations.
	base := rsc.DeepCopy()

	eo := flow.MergeEnsures(
		// Ensure storagePool name and condition are up to date.
		ensureStoragePool(rf.Ctx(), rsc, targetStoragePoolName, rsp),

		// Ensure configuration is up to date based on RSP state.
		ensureConfiguration(rf.Ctx(), rsc, rsp),

		// Ensure volume summary (counters only).
		ensureVolumeSummary(rf.Ctx(), rsc, rvs),
	)

	// Ensure rolling operations (conditions + target operations).
	var targetOps []v1alpha1.ReplicatedVolumeOperation
	eoRolling, ops := ensureRollingOperations(rf.Ctx(), rsc, rvs, rvos)
	eo = eo.Merge(eoRolling)
	targetOps = ops

	// Create target operations BEFORE status patch.
	// This ensures that if RVO creation fails, the cursor doesn't advance.
	// If status patch fails after RVO creation, the RVO will be found as pending
	// on next reconcile and the cursor will catch up.
	for i := range targetOps {
		op := &targetOps[i]
		if err := r.createRVO(rf.Ctx(), op); err != nil {
			if apierrors.IsAlreadyExists(err) {
				// Operation already exists (cache lag or previous failed reconcile).
				continue
			}
			return rf.Fail(err).ToCtrl()
		}
	}

	// Patch status after successful RVO creation.
	if eo.DidChange() {
		if err := r.patchRSCStatus(rf.Ctx(), rsc, base); err != nil {
			return rf.Fail(err).ToCtrl()
		}
	}

	// Patch completed RVOs to remove finalizer when cursor has advanced or rolling is complete.
	if err := r.patchCompletedRVOsRemoveFinalizer(rf.Ctx(), rsc, rvos); err != nil {
		return rf.Fail(err).ToCtrl()
	}

	// Release storage pools that are no longer used.
	return r.reconcileUnusedRSPs(rf.Ctx(), rsc, usedStoragePoolNames).ToCtrl()
}

// rscShouldBeDeleted returns true if the RSC is marked for deletion and has no RVs blocking it.
func rscShouldBeDeleted(rsc *v1alpha1.ReplicatedStorageClass, rvs []rvView) bool {
	return rsc == nil || (rsc.DeletionTimestamp != nil && len(rvs) == 0)
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: deletion
//

// reconcileDeletion handles RSC deletion: releases all RSPs from usedBy, then removes the finalizer.
//
// Reconcile pattern: Pure orchestration
func (r *Reconciler) reconcileDeletion(
	ctx context.Context,
	rscName string,
	rsc *v1alpha1.ReplicatedStorageClass, // may be nil if already deleted
	usedStoragePoolNames []string,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "deletion")
	defer rf.OnEnd(&outcome)

	// Release all RSPs that reference this RSC.
	outcomes := make([]flow.ReconcileOutcome, 0, len(usedStoragePoolNames))
	for _, rspName := range usedStoragePoolNames {
		outcomes = append(outcomes, r.reconcileRSPRelease(rf.Ctx(), rscName, rspName))
	}
	if merged := flow.MergeReconciles(outcomes...); merged.ShouldReturn() {
		return merged
	}

	// All RSPs released. Remove finalizer (if RSC still exists).
	if rsc != nil && objutilv1.HasFinalizer(rsc, v1alpha1.RSCControllerFinalizer) {
		base := rsc.DeepCopy()
		objutilv1.RemoveFinalizer(rsc, v1alpha1.RSCControllerFinalizer)
		if err := r.patchRSC(rf.Ctx(), rsc, base); err != nil {
			return rf.Fail(err)
		}
	}

	return rf.Done()
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: migration-from-rsp
//

// reconcileMigrationFromRSP migrates StoragePool to spec.Storage.
//
// Precondition: rsc.Spec.StoragePool != "" (checked by caller)
//
// Reconcile pattern: Target-state driven
//
// Logic:
//   - If RSP not found → set conditions (Ready=False, StoragePoolReady=False), patch status, return Done
//   - If RSP found → copy type+lvmVolumeGroups to spec.storage, clear storagePool
func (r *Reconciler) reconcileMigrationFromRSP(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "migration-from-rsp", "rsp", rsc.Spec.StoragePool)
	defer rf.OnEnd(&outcome)

	rsp, err := r.getRSP(rf.Ctx(), rsc.Spec.StoragePool)
	if err != nil {
		return rf.Fail(err)
	}

	// RSP not found - set conditions and wait.
	if rsp == nil {
		base := rsc.DeepCopy()
		changed := applyReadyCondFalse(rsc,
			v1alpha1.ReplicatedStorageClassCondReadyReasonWaitingForStoragePool,
			fmt.Sprintf("Cannot migrate from storagePool field: ReplicatedStoragePool %q not found", rsc.Spec.StoragePool))
		changed = applyStoragePoolReadyCondFalse(rsc,
			v1alpha1.ReplicatedStorageClassCondStoragePoolReadyReasonStoragePoolNotFound,
			fmt.Sprintf("ReplicatedStoragePool %q not found", rsc.Spec.StoragePool)) || changed
		if changed {
			if err := r.patchRSCStatus(rf.Ctx(), rsc, base); err != nil {
				return rf.Fail(err)
			}
		}
		return rf.Done()
	}

	// RSP found, migrate storage configuration.
	base := rsc.DeepCopy()

	// Clone LVMVolumeGroups to avoid aliasing.
	lvmVolumeGroups := make([]v1alpha1.ReplicatedStoragePoolLVMVolumeGroups, len(rsp.Spec.LVMVolumeGroups))
	copy(lvmVolumeGroups, rsp.Spec.LVMVolumeGroups)

	rsc.Spec.Storage = v1alpha1.ReplicatedStorageClassStorage{
		Type:            rsp.Spec.Type,
		LVMVolumeGroups: lvmVolumeGroups,
	}
	rsc.Spec.StoragePool = ""

	if err := r.patchRSC(rf.Ctx(), rsc, base); err != nil {
		return rf.Fail(err)
	}

	return rf.Continue()
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: metadata
//

// reconcileMetadata ensures the controller finalizer is present.
//
// Precondition: RSC is not being deleted (deletion is handled by reconcileDeletion).
//
// Reconcile pattern: Conditional target evaluation
func (r *Reconciler) reconcileMetadata(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "metadata")
	defer rf.OnEnd(&outcome)

	if objutilv1.HasFinalizer(rsc, v1alpha1.RSCControllerFinalizer) {
		return rf.Continue()
	}

	base := rsc.DeepCopy()
	objutilv1.AddFinalizer(rsc, v1alpha1.RSCControllerFinalizer)

	if err := r.patchRSC(rf.Ctx(), rsc, base); err != nil {
		return rf.Fail(err)
	}

	return rf.Continue()
}

// --- Ensure helpers ---

// ensureStoragePool ensures status.storagePoolName and StoragePoolReady condition are up to date.
//
// Logic:
//   - If storagePool not in sync → update status.storagePoolName and status.storagePoolBasedOnGeneration
//   - If rsp == nil → set StoragePoolReady=False (not found)
//   - If rsp != nil → copy Ready condition from RSP to our StoragePoolReady
func ensureStoragePool(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
	targetStoragePoolName string,
	rsp *v1alpha1.ReplicatedStoragePool,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "storage-pool")
	defer ef.OnEnd(&outcome)

	// Update storagePoolName.
	changed := applyStoragePool(rsc, targetStoragePoolName)

	// Update StoragePoolReady condition based on RSP existence and state.
	if rsp == nil {
		changed = applyStoragePoolReadyCondFalse(rsc,
			v1alpha1.ReplicatedStorageClassCondStoragePoolReadyReasonStoragePoolNotFound,
			fmt.Sprintf("ReplicatedStoragePool %q not found", targetStoragePoolName)) || changed
	} else {
		changed = applyStoragePoolReadyCondFromRSP(rsc, rsp) || changed
	}

	return ef.Ok().ReportChangedIf(changed)
}

// ensureConfiguration ensures configuration is up to date based on RSP state.
//
// Algorithm:
//  1. Panic if StoragePoolBasedOnGeneration != Generation (caller bug).
//  2. If StoragePoolReady != True: set Ready=False (WaitingForStoragePool) and return.
//  3. If RSP.EligibleNodesRevision changed OR configuration is not in sync:
//     - Validate RSP.EligibleNodes against topology/replication requirements.
//     - If invalid: Ready=False (InsufficientEligibleNodes) and return.
//     - Update rsc.status.StoragePoolEligibleNodesRevision if changed.
//  4. If ConfigurationGeneration == Generation: done (configuration already in sync).
//  5. Otherwise: apply new Configuration, set ConfigurationGeneration.
func ensureConfiguration(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
	rsp *v1alpha1.ReplicatedStoragePool,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "configuration")
	defer ef.OnEnd(&outcome)

	// 1. Panic if StoragePoolBasedOnGeneration != Generation (caller bug).
	if rsc.Status.StoragePoolBasedOnGeneration != rsc.Generation {
		panic(fmt.Sprintf("ensureConfiguration: StoragePoolBasedOnGeneration (%d) != Generation (%d); ensureStoragePool must be called first",
			rsc.Status.StoragePoolBasedOnGeneration, rsc.Generation))
	}

	changed := false

	// Compute diff message for use in Ready=False messages.
	pendingDiffMsg := computePendingConfigurationDiffMessage(rsc, rsc.Status.StoragePoolName)

	// 2. If StoragePoolReady != True: set Ready=False and return.
	if !objutilv1.IsStatusConditionPresentAndTrue(rsc, v1alpha1.ReplicatedStorageClassCondStoragePoolReadyType) {
		msg := "Waiting for ReplicatedStoragePool to become ready"
		if pendingDiffMsg != "" {
			msg = pendingDiffMsg + ". " + msg
		}
		changed = applyReadyCondFalse(rsc,
			v1alpha1.ReplicatedStorageClassCondReadyReasonWaitingForStoragePool,
			msg)
		return ef.Ok().ReportChangedIf(changed)
	}

	// 3. Validate eligibleNodes if revision changed OR configuration is not in sync
	//    (spec.replication/topology may have changed without RSP revision change).
	needsValidation := rsp.Status.EligibleNodesRevision != rsc.Status.StoragePoolEligibleNodesRevision ||
		!isConfigurationInSync(rsc)
	if needsValidation {
		if err := validateEligibleNodes(rsp.Status.EligibleNodes, rsc.Spec.Topology, rsc.Spec.Replication); err != nil {
			msg := err.Error()
			if pendingDiffMsg != "" {
				msg = pendingDiffMsg + ". " + msg
			}
			changed = applyReadyCondFalse(rsc,
				v1alpha1.ReplicatedStorageClassCondReadyReasonInsufficientEligibleNodes,
				msg)
			return ef.Ok().ReportChangedIf(changed)
		}

		// Update StoragePoolEligibleNodesRevision.
		if rsc.Status.StoragePoolEligibleNodesRevision != rsp.Status.EligibleNodesRevision {
			rsc.Status.StoragePoolEligibleNodesRevision = rsp.Status.EligibleNodesRevision
			changed = true
		}
	}

	// 4. If configuration is in sync, re-assert Ready=True (may have been cleared
	//    by a transient StoragePool-not-ready condition) and we're done.
	if isConfigurationInSync(rsc) {
		changed = applyReadyCondTrue(rsc,
			v1alpha1.ReplicatedStorageClassCondReadyReasonReady,
			"Storage class is ready",
		) || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// 5. Apply new configuration.
	config := makeConfiguration(rsc, rsc.Status.StoragePoolName)
	rsc.Status.Configuration = &config
	rsc.Status.ConfigurationGeneration = rsc.Generation

	// Set Ready condition.
	applyReadyCondTrue(rsc,
		v1alpha1.ReplicatedStorageClassCondReadyReasonReady,
		"Storage class is ready",
	)

	return ef.Ok().ReportChanged()
}

// ensureVolumeSummary computes and applies volume summary in-place.
//
// Computes volume counters only. Conditions are handled by ensureRollingOperations.
func ensureVolumeSummary(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
	rvs []rvView,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "volume-summary")
	defer ef.OnEnd(&outcome)

	// Compute and apply volume summary.
	summary := computeActualVolumesSummary(rsc, rvs)
	changed := applyVolumesSummary(rsc, summary)

	return ef.Ok().ReportChangedIf(changed)
}

// ensureRollingOperations computes volume conditions and target operations for rolling updates.
//
// Sets ConfigurationRolledOut and VolumesSatisfyEligibleNodes conditions based on
// volume counters and existing operations. Returns target operations to be created.
//
// Algorithm:
//  1. If volumes pending observation - set conditions to Unknown, skip rolling.
//  2. If all volumes aligned - clear sortSalt/cursors, set conditions to True.
//  3. Otherwise - generate sortSalt if needed, compute target operations using round-robin.
func ensureRollingOperations(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
	rvs []rvView,
	rvos *rvoCacheRefs,
) (outcome flow.EnsureOutcome, targetOps []v1alpha1.ReplicatedVolumeOperation) {
	ef := flow.BeginEnsure(ctx, "rolling-operations")
	defer ef.OnEnd(&outcome)

	if rsc.Status.Volumes.PendingObservation == nil {
		panic("ensureRollingOperations: PendingObservation is nil; ensureVolumeSummary must be called first")
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
		return ef.Ok().ReportChangedIf(changed), nil
	}

	if rsc.Status.Volumes.StaleConfiguration == nil || rsc.Status.Volumes.InConflictWithEligibleNodes == nil {
		panic("ensureRollingOperations: StaleConfiguration or InConflictWithEligibleNodes is nil; ensureVolumeSummary must be called first")
	}

	staleCount := *rsc.Status.Volumes.StaleConfiguration
	conflictCount := *rsc.Status.Volumes.InConflictWithEligibleNodes

	// If all volumes aligned - clear sortSalt/cursors and set conditions to True.
	if staleCount == 0 && conflictCount == 0 {
		changed := applyClearRollingState(rsc)
		changed = applyConfigurationRolledOutCondTrue(rsc,
			v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonRolledOutToAllVolumes,
			"All volumes have configuration matching the storage class",
		) || changed
		changed = applyVolumesSatisfyEligibleNodesCondTrue(rsc,
			v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesReasonAllVolumesSatisfy,
			"All volumes have replicas on eligible nodes",
		) || changed
		return ef.Ok().ReportChangedIf(changed), nil
	}

	maxParallelConfigurationRollouts, maxParallelConflictResolutions := computeRollingStrategiesConfiguration(rsc)

	changed := false

	// Set conditions based on strategy and counters.
	if staleCount > 0 {
		if maxParallelConfigurationRollouts > 0 {
			changed = applyConfigurationRolledOutCondFalse(rsc,
				v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonConfigurationRolloutInProgress,
				fmt.Sprintf("%d volume(s) with stale configuration, rolling update in progress", staleCount),
			)
		} else {
			changed = applyConfigurationRolledOutCondFalse(rsc,
				v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonConfigurationRolloutDisabled,
				fmt.Sprintf("%d volume(s) with stale configuration, automatic rollout disabled", staleCount),
			)
		}
	} else {
		changed = applyConfigurationRolledOutCondTrue(rsc,
			v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonRolledOutToAllVolumes,
			"All volumes have configuration matching the storage class",
		) || changed
	}

	if conflictCount > 0 {
		if maxParallelConflictResolutions > 0 {
			changed = applyVolumesSatisfyEligibleNodesCondFalse(rsc,
				v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesReasonConflictResolutionInProgress,
				fmt.Sprintf("%d volume(s) in conflict with eligible nodes, resolution in progress", conflictCount),
			) || changed
		} else {
			changed = applyVolumesSatisfyEligibleNodesCondFalse(rsc,
				v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesReasonManualConflictResolution,
				fmt.Sprintf("%d volume(s) in conflict with eligible nodes, manual resolution required", conflictCount),
			) || changed
		}
	} else {
		changed = applyVolumesSatisfyEligibleNodesCondTrue(rsc,
			v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesReasonAllVolumesSatisfy,
			"All volumes have replicas on eligible nodes",
		) || changed
	}

	// If no rolling is enabled, return early.
	if maxParallelConfigurationRollouts == 0 && maxParallelConflictResolutions == 0 {
		return ef.Ok().ReportChangedIf(changed), nil
	}

	// Ensure salt exists (mutable + rand — ensure helper owns this).
	if rsc.Status.RollingOperationsSortSalt == "" {
		rsc.Status.RollingOperationsSortSalt = generateSortSalt()
		changed = true
	}

	// Pure computation: select target operations and compute new cursor.
	ops, newCursor := computeTargetRollingOperations(
		rsc, rvs, rvos,
		maxParallelConfigurationRollouts,
		maxParallelConflictResolutions,
	)

	// Apply cursor change (mutable — ensure helper owns this).
	changed = applyRollingOperationsCursor(rsc, newCursor) || changed
	targetOps = ops

	return ef.Ok().ReportChangedIf(changed), targetOps
}

// ──────────────────────────────────────────────────────────────────────────────
// View types
//

// rvView is a lightweight projection of ReplicatedVolume fields used by this controller.
type rvView struct {
	name                            string
	uid                             types.UID
	configurationStoragePoolName    string
	configurationObservedGeneration int64
	conditions                      rvViewConditions
}

type rvViewConditions struct {
	satisfyEligibleNodesKnown bool // true when SatisfyEligibleNodes condition is present
	satisfyEligibleNodes      bool // true when SatisfyEligibleNodes condition is present and True
	configurationReadyKnown   bool // true when ConfigurationReady condition is present
	configurationReady        bool // true when ConfigurationReady condition is present and True
}

// newRVView creates an rvView from a ReplicatedVolume.
// The unsafeRV may come from cache without DeepCopy; rvView copies only the needed scalar fields.
func newRVView(unsafeRV *v1alpha1.ReplicatedVolume) rvView {
	view := rvView{
		name:                            unsafeRV.Name,
		uid:                             unsafeRV.UID,
		configurationObservedGeneration: unsafeRV.Status.ConfigurationObservedGeneration,
		conditions: rvViewConditions{
			satisfyEligibleNodesKnown: objutilv1.HasStatusCondition(unsafeRV, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType),
			satisfyEligibleNodes:      objutilv1.IsStatusConditionPresentAndTrue(unsafeRV, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType),
			configurationReadyKnown:   objutilv1.HasStatusCondition(unsafeRV, v1alpha1.ReplicatedVolumeCondConfigurationReadyType),
			configurationReady:        objutilv1.IsStatusConditionPresentAndTrue(unsafeRV, v1alpha1.ReplicatedVolumeCondConfigurationReadyType),
		},
	}

	if unsafeRV.Status.Configuration != nil {
		view.configurationStoragePoolName = unsafeRV.Status.Configuration.StoragePoolName
	}

	return view
}

// computeRollingStrategiesConfiguration determines max parallel limits for configuration rollouts and conflict resolutions.
// Returns 0 for a strategy if it's not set to RollingUpdate/RollingRepair type (meaning disabled).
func computeRollingStrategiesConfiguration(rsc *v1alpha1.ReplicatedStorageClass) (maxParallelConfigurationRollouts, maxParallelConflictResolutions int32) {
	if rsc.Spec.ConfigurationRolloutStrategy.Type == v1alpha1.ConfigurationRolloutRollingUpdate {
		if rsc.Spec.ConfigurationRolloutStrategy.RollingUpdate == nil {
			panic("ConfigurationRolloutStrategy.RollingUpdate is nil but Type is RollingUpdate; API validation should prevent this")
		}
		maxParallelConfigurationRollouts = rsc.Spec.ConfigurationRolloutStrategy.RollingUpdate.MaxParallel
	}

	if rsc.Spec.EligibleNodesConflictResolutionStrategy.Type == v1alpha1.EligibleNodesConflictResolutionRollingRepair {
		if rsc.Spec.EligibleNodesConflictResolutionStrategy.RollingRepair == nil {
			panic("EligibleNodesConflictResolutionStrategy.RollingRepair is nil but Type is RollingRepair; API validation should prevent this")
		}
		maxParallelConflictResolutions = rsc.Spec.EligibleNodesConflictResolutionStrategy.RollingRepair.MaxParallel
	}

	return maxParallelConfigurationRollouts, maxParallelConflictResolutions
}

// makeConfiguration computes the intended configuration from RSC spec.
func makeConfiguration(rsc *v1alpha1.ReplicatedStorageClass, storagePoolName string) v1alpha1.ReplicatedStorageClassConfiguration {
	return v1alpha1.ReplicatedStorageClassConfiguration{
		Topology:        rsc.Spec.Topology,
		Replication:     rsc.Spec.Replication,
		VolumeAccess:    rsc.Spec.VolumeAccess,
		StoragePoolName: storagePoolName,
	}
}

// computePendingConfigurationDiffMessage returns a human-readable description of the differences between
// the current spec-derived configuration and the accepted configuration in status.
// When status.configuration is nil (first configuration), returns "Pending: initial configuration".
// When all fields match (no diff), returns an empty string.
func computePendingConfigurationDiffMessage(rsc *v1alpha1.ReplicatedStorageClass, targetStoragePoolName string) string {
	if rsc.Status.Configuration == nil {
		return "Pending: initial configuration"
	}

	cur := rsc.Status.Configuration
	var diffs []string

	if cur.Replication != rsc.Spec.Replication {
		diffs = append(diffs, fmt.Sprintf("replication %s -> %s", cur.Replication, rsc.Spec.Replication))
	}
	if cur.Topology != rsc.Spec.Topology {
		diffs = append(diffs, fmt.Sprintf("topology %s -> %s", cur.Topology, rsc.Spec.Topology))
	}
	if cur.VolumeAccess != rsc.Spec.VolumeAccess {
		diffs = append(diffs, fmt.Sprintf("volumeAccess %s -> %s", cur.VolumeAccess, rsc.Spec.VolumeAccess))
	}
	if cur.StoragePoolName != targetStoragePoolName {
		diffs = append(diffs, fmt.Sprintf("storage: not yet accepted (current pool: %s, pending pool: %s)", cur.StoragePoolName, targetStoragePoolName))
	}

	if len(diffs) == 0 {
		return ""
	}

	return "Pending: " + strings.Join(diffs, ", ")
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

// applyReadyCondTrue sets the Ready condition to True.
// Returns true if the condition was changed.
func applyReadyCondTrue(rsc *v1alpha1.ReplicatedStorageClass, reason, message string) bool {
	return objutilv1.SetStatusCondition(rsc, metav1.Condition{
		Type:    v1alpha1.ReplicatedStorageClassCondReadyType,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})
}

// applyReadyCondFalse sets the Ready condition to False.
// Returns true if the condition was changed.
func applyReadyCondFalse(rsc *v1alpha1.ReplicatedStorageClass, reason, message string) bool {
	return objutilv1.SetStatusCondition(rsc, metav1.Condition{
		Type:    v1alpha1.ReplicatedStorageClassCondReadyType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// applyStoragePoolReadyCondFalse sets the StoragePoolReady condition to False.
// Returns true if the condition was changed.
func applyStoragePoolReadyCondFalse(rsc *v1alpha1.ReplicatedStorageClass, reason, message string) bool {
	return objutilv1.SetStatusCondition(rsc, metav1.Condition{
		Type:    v1alpha1.ReplicatedStorageClassCondStoragePoolReadyType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// applyStoragePoolReadyCondFromRSP copies the Ready condition from RSP to RSC's StoragePoolReady condition.
// Returns true if the condition was changed.
func applyStoragePoolReadyCondFromRSP(rsc *v1alpha1.ReplicatedStorageClass, rsp *v1alpha1.ReplicatedStoragePool) bool {
	readyCond := objutilv1.GetStatusCondition(rsp, v1alpha1.ReplicatedStoragePoolCondReadyType)
	if readyCond == nil {
		// RSP has no Ready condition yet - set StoragePoolReady to Unknown.
		return objutilv1.SetStatusCondition(rsc, metav1.Condition{
			Type:    v1alpha1.ReplicatedStorageClassCondStoragePoolReadyType,
			Status:  metav1.ConditionUnknown,
			Reason:  v1alpha1.ReplicatedStorageClassCondStoragePoolReadyReasonPending,
			Message: "ReplicatedStoragePool has no Ready condition yet",
		})
	}

	// Copy Ready condition from RSP to RSC's StoragePoolReady.
	return objutilv1.SetStatusCondition(rsc, metav1.Condition{
		Type:    v1alpha1.ReplicatedStorageClassCondStoragePoolReadyType,
		Status:  readyCond.Status,
		Reason:  readyCond.Reason,
		Message: readyCond.Message,
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

// applyRollingOperationsCursor updates the rolling operations cursor.
// Returns true if the cursor was changed.
func applyRollingOperationsCursor(rsc *v1alpha1.ReplicatedStorageClass, cursor string) bool {
	if cursor == "" || cursor == rsc.Status.RollingOperationsCursor {
		return false
	}
	rsc.Status.RollingOperationsCursor = cursor
	return true
}

// applyClearRollingState clears sortSalt and cursor when all volumes are aligned.
// Returns true if any field was cleared.
func applyClearRollingState(rsc *v1alpha1.ReplicatedStorageClass) bool {
	changed := false
	if rsc.Status.RollingOperationsSortSalt != "" {
		rsc.Status.RollingOperationsSortSalt = ""
		changed = true
	}
	if rsc.Status.RollingOperationsCursor != "" {
		rsc.Status.RollingOperationsCursor = ""
		changed = true
	}
	return changed
}

// computeTargetRollingOperations computes target operations for rolling updates and the new cursor position.
// It treats rsc as read-only and does not mutate any inputs.
//
// Precondition: rsc.Status.RollingOperationsSortSalt MUST be non-empty (caller ensures this).
//
// Algorithm:
//  1. Build lookup for pending operations.
//  2. Filter RVs that need operations (not aligned, no pending operation) — O(N) cheap checks.
//  3. Compute hashes and sort candidates — O(C) hash computations, O(C log C) sort.
//  4. Calculate available budget based on existing uncompleted operations.
//  5. Select RVs from cursor position, wrapping around for full circle.
//  6. Build target operations.
//
// Optimization: filter-before-compute reduces hash computations from O(N+C) to O(C).
// See .cursor/rules/go-performance-patterns.mdc "Filter before compute".
//
// Behavior when ConfigurationRolloutStrategy=NewVolumesOnly (disabled for existing)
// but EligibleNodesConflictResolutionStrategy=RollingRepair (enabled):
// If a volume needs both config update and conflict resolution, we still perform
// conflict resolution alone. Eligible nodes violations are usually more urgent than
// config drift. Config update will happen later when strategy changes or volume is
// recreated.
func computeTargetRollingOperations(
	rsc *v1alpha1.ReplicatedStorageClass,
	rvs []rvView,
	rvos *rvoCacheRefs,
	maxParallelConfigurationRollouts int32,
	maxParallelConflictResolutions int32,
) (targetOps []v1alpha1.ReplicatedVolumeOperation, newCursor string) {
	// Build lookup for existing uncompleted operations by RV name.
	pendingOpsByRV := make(map[string]*v1alpha1.ReplicatedVolumeOperation)
	var inFlightConfigOps, inFlightConflictOps int32
	for i := 0; i < rvos.Len(); i++ {
		op := rvos.At(i) // Read-only cache ref.
		// Skip completed operations.
		if objutilv1.IsStatusConditionPresentAndTrue(op, v1alpha1.ReplicatedVolumeOperationCondCompletedType) {
			continue
		}
		pendingOpsByRV[op.Spec.ReplicatedVolumeName] = op
		switch op.Spec.Type {
		case v1alpha1.ReplicatedVolumeOperationTypeUpdateConfiguration:
			inFlightConfigOps++
			if op.Spec.UpdateConfigOptions != nil && op.Spec.UpdateConfigOptions.ResolveEligibleNodes {
				inFlightConflictOps++
			}
		case v1alpha1.ReplicatedVolumeOperationTypeResolveEligibleNodesConflict:
			inFlightConflictOps++
		}
	}

	// Calculate available budget.
	configBudget := maxParallelConfigurationRollouts - inFlightConfigOps
	if configBudget < 0 {
		configBudget = 0
	}
	conflictBudget := maxParallelConflictResolutions - inFlightConflictOps
	if conflictBudget < 0 {
		conflictBudget = 0
	}

	// Step 1: Filter candidates first (cheap O(N) condition checks, no hashing).
	type rvNeed struct {
		rv                rvView
		needsConfigUpdate bool
		needsConflictFix  bool
		hash              string // computed later, only for candidates
	}
	var rawCandidates []rvNeed

	for i := range rvs {
		rv := rvs[i]

		// Skip RVs with pending operations.
		if _, hasPending := pendingOpsByRV[rv.name]; hasPending {
			continue
		}

		configOK := rv.conditions.configurationReady
		nodesOK := rv.conditions.satisfyEligibleNodes

		if configOK && nodesOK {
			continue // Already aligned.
		}

		rawCandidates = append(rawCandidates, rvNeed{
			rv:                rv,
			needsConfigUpdate: !configOK,
			needsConflictFix:  !nodesOK,
			// hash computed in step 2
		})
	}

	if len(rawCandidates) == 0 {
		return nil, ""
	}

	// Step 2: Compute hashes and sort candidates (O(C) hash calls, O(C log C) sort).
	// This is cheaper than sorting all N RVs when C << N.
	// Uses Schwartzian transform: hash is computed once per candidate.
	for i := range rawCandidates {
		rawCandidates[i].hash = computeRVHash(rsc.Status.RollingOperationsSortSalt, string(rawCandidates[i].rv.uid))
	}
	sort.SliceStable(rawCandidates, func(i, j int) bool {
		return rawCandidates[i].hash < rawCandidates[j].hash
	})
	candidates := rawCandidates

	// Find cursor position for round-robin (shared by all operation types).
	cursorIdx := findCursorIndex(candidates, rsc.Status.RollingOperationsCursor,
		func(n rvNeed) string { return n.hash },
		func(_ rvNeed) bool { return true }) // Match all candidates.

	var lastSelectedHash string
	selectedRVs := make(map[string]bool)

	// Select RVs for configuration update (higher priority).
	for i := 0; i < len(candidates) && configBudget > 0; i++ {
		idx := (cursorIdx + i) % len(candidates)
		need := candidates[idx]
		if !need.needsConfigUpdate {
			continue
		}

		// Check if we also need conflict resolution budget.
		if need.needsConflictFix {
			if conflictBudget <= 0 {
				continue // No conflict budget available.
			}
			conflictBudget--
		}

		selectedRVs[need.rv.name] = true
		configBudget--
		lastSelectedHash = need.hash

		op := buildRVOperation(rsc, need.rv, v1alpha1.ReplicatedVolumeOperationTypeUpdateConfiguration, need.hash, need.needsConflictFix)
		targetOps = append(targetOps, op)
	}

	// Select RVs for conflict resolution (not needing config update,
	// OR needing config update but config rollout is disabled for existing volumes).
	// If a volume needs both but wasn't selected above, it means config rollout is
	// disabled (NewVolumesOnly). We still perform conflict resolution — see doc comment.
	for i := 0; i < len(candidates) && conflictBudget > 0; i++ {
		idx := (cursorIdx + i) % len(candidates)
		need := candidates[idx]

		// Skip if already selected (config update loop picked it).
		if selectedRVs[need.rv.name] {
			continue
		}
		if !need.needsConflictFix {
			continue
		}

		selectedRVs[need.rv.name] = true
		conflictBudget--
		lastSelectedHash = need.hash

		op := buildRVOperation(rsc, need.rv, v1alpha1.ReplicatedVolumeOperationTypeResolveEligibleNodesConflict, need.hash, false)
		targetOps = append(targetOps, op)
	}

	// Return the new cursor position (caller applies it).
	if lastSelectedHash != "" {
		newCursor = lastSelectedHash
	}

	return targetOps, newCursor
}

// generateSortSalt generates a random 8-character hex string for sorting.
func generateSortSalt() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// computeRVHash computes a deterministic hash for sorting RVs.
//
// TODO(perf): fmt.Sprintf is ~50ns overhead per call. For 500+ candidates,
// consider replacing with strconv.FormatUint or manual hex encoding:
//
//	const hexDigits = "0123456789abcdef"
//	v := h.Sum64() & 0xFFFFFFFF
//	buf := [8]byte{...}  // manual hex conversion
//
// Benchmark before optimizing — current overhead is acceptable for typical workloads.
func computeRVHash(salt, uid string) string {
	h := fnv.New64a()
	h.Write([]byte(salt))
	h.Write([]byte(uid))
	return fmt.Sprintf("%016x", h.Sum64()) // 16 hex chars, full 64-bit.
}

// findCursorIndex finds the starting index for round-robin based on cursor.
// Returns the index of the first matching item AFTER the cursor item.
// If cursor is empty or not found, returns 0.
func findCursorIndex[T any](items []T, cursor string, getHash func(T) string, match func(T) bool) int {
	if cursor == "" {
		return 0
	}

	foundCursor := false
	for i, item := range items {
		if !match(item) {
			continue
		}

		if getHash(item) == cursor {
			foundCursor = true
			continue // Skip cursor item, start from next
		}

		if foundCursor {
			return i
		}
	}

	// Cursor not found or no items after cursor - wrap around to beginning.
	return 0
}

// buildRVOperation builds a ReplicatedVolumeOperation for the given RV.
func buildRVOperation(
	rsc *v1alpha1.ReplicatedStorageClass,
	rv rvView,
	opType v1alpha1.ReplicatedVolumeOperationType,
	cursor string,
	resolveConflict bool,
) v1alpha1.ReplicatedVolumeOperation {
	opName := fmt.Sprintf("%s-%s-%s", rv.name, rvoNameSuffix(opType), cursor)

	op := v1alpha1.ReplicatedVolumeOperation{
		ObjectMeta: metav1.ObjectMeta{
			Name: opName,
			OwnerReferences: []metav1.OwnerReference{
				// RSC as controller owner (for deletion when RSC is deleted).
				{
					APIVersion:         v1alpha1.SchemeGroupVersion.String(),
					Kind:               "ReplicatedStorageClass",
					Name:               rsc.Name,
					UID:                rsc.UID,
					Controller:         ptr.To(true),
					BlockOwnerDeletion: ptr.To(true),
				},
				// RV as non-controller owner (for deletion when RV is deleted).
				{
					APIVersion:         v1alpha1.SchemeGroupVersion.String(),
					Kind:               "ReplicatedVolume",
					Name:               rv.name,
					UID:                rv.uid,
					Controller:         ptr.To(false),
					BlockOwnerDeletion: ptr.To(false),
				},
			},
		},
		Spec: v1alpha1.ReplicatedVolumeOperationSpec{
			ReplicatedVolumeName: rv.name,
			Type:                 opType,
		},
	}

	// Set UpdateConfigOptions only for UpdateConfiguration operations.
	if opType == v1alpha1.ReplicatedVolumeOperationTypeUpdateConfiguration && resolveConflict {
		op.Spec.UpdateConfigOptions = &v1alpha1.UpdateConfigOptions{
			ResolveEligibleNodes: true,
		}
	}

	// Add finalizer to prevent GC until cursor moves forward.
	controllerutil.AddFinalizer(&op, v1alpha1.RSCControllerFinalizer)

	return op
}

// parseRVOCursor extracts the cursor from an RVO name.
// RVO name format: <rv-name>-<suffix>-<cursor>
// Cursor is always the last segment after the final "-".
// Returns empty string if no "-" found.
func parseRVOCursor(rvoName string) string {
	if idx := strings.LastIndex(rvoName, "-"); idx != -1 {
		return rvoName[idx+1:]
	}
	return ""
}

// validateEligibleNodes validates that eligible nodes from RSP meet the requirements
// for the RSC's replication mode and topology.
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
	eligibleNodes []v1alpha1.ReplicatedStoragePoolEligibleNode,
	topology v1alpha1.ReplicatedStorageClassTopology,
	replication v1alpha1.ReplicatedStorageClassReplication,
) error {
	if len(eligibleNodes) == 0 {
		return fmt.Errorf("No nodes available in the storage pool")
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
	nodesByZone := make(map[string][]v1alpha1.ReplicatedStoragePoolEligibleNode)
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

	switch replication {
	case v1alpha1.ReplicationNone:
		// At least 1 node required.
		if totalNodes < 1 {
			return fmt.Errorf("Replication None requires at least 1 node, have %d", totalNodes)
		}

	case v1alpha1.ReplicationAvailability:
		// At least 3 nodes, at least 2 with disks.
		if err := validateAvailabilityReplication(topology, totalNodes, nodesWithDisks, nodesByZone, zonesWithDisks); err != nil {
			return err
		}

	case v1alpha1.ReplicationConsistency:
		// 2 nodes, both with disks.
		if err := validateConsistencyReplication(topology, totalNodes, nodesWithDisks, nodesByZone, zonesWithDisks); err != nil {
			return err
		}

	case v1alpha1.ReplicationConsistencyAndAvailability:
		// At least 3 nodes with disks.
		if err := validateConsistencyAndAvailabilityReplication(topology, nodesWithDisks, nodesByZone, zonesWithDisks); err != nil {
			return err
		}
	}

	return nil
}

// validateAvailabilityReplication validates requirements for Availability replication mode.
func validateAvailabilityReplication(
	topology v1alpha1.ReplicatedStorageClassTopology,
	totalNodes, nodesWithDisks int,
	nodesByZone map[string][]v1alpha1.ReplicatedStoragePoolEligibleNode,
	zonesWithDisks int,
) error {
	switch topology {
	case v1alpha1.TopologyTransZonal:
		// 3 different zones, at least 2 with disks.
		if len(nodesByZone) < 3 {
			return fmt.Errorf("Replication Availability with TransZonal topology requires nodes in at least 3 zones, have %d", len(nodesByZone))
		}
		if zonesWithDisks < 2 {
			return fmt.Errorf("Replication Availability with TransZonal topology requires at least 2 zones with disks, have %d", zonesWithDisks)
		}

	case v1alpha1.TopologyZonal:
		// Per zone: at least 3 nodes, at least 2 with disks.
		for zone, nodes := range nodesByZone {
			zoneNodesWithDisks := 0
			for _, n := range nodes {
				if len(n.LVMVolumeGroups) > 0 {
					zoneNodesWithDisks++
				}
			}
			if len(nodes) < 3 {
				return fmt.Errorf("Replication Availability with Zonal topology requires at least 3 nodes in each zone, zone %q has %d", zone, len(nodes))
			}
			if zoneNodesWithDisks < 2 {
				return fmt.Errorf("Replication Availability with Zonal topology requires at least 2 nodes with disks in each zone, zone %q has %d", zone, zoneNodesWithDisks)
			}
		}

	default:
		// Ignored topology or unspecified: global check.
		if totalNodes < 3 {
			return fmt.Errorf("Replication Availability requires at least 3 nodes, have %d", totalNodes)
		}
		if nodesWithDisks < 2 {
			return fmt.Errorf("Replication Availability requires at least 2 nodes with disks, have %d", nodesWithDisks)
		}
	}

	return nil
}

// validateConsistencyReplication validates requirements for Consistency replication mode.
func validateConsistencyReplication(
	topology v1alpha1.ReplicatedStorageClassTopology,
	totalNodes, nodesWithDisks int,
	nodesByZone map[string][]v1alpha1.ReplicatedStoragePoolEligibleNode,
	zonesWithDisks int,
) error {
	switch topology {
	case v1alpha1.TopologyTransZonal:
		// 2 different zones with disks.
		if zonesWithDisks < 2 {
			return fmt.Errorf("Replication Consistency with TransZonal topology requires at least 2 zones with disks, have %d", zonesWithDisks)
		}

	case v1alpha1.TopologyZonal:
		// Per zone: at least 2 nodes with disks.
		for zone, nodes := range nodesByZone {
			zoneNodesWithDisks := 0
			for _, n := range nodes {
				if len(n.LVMVolumeGroups) > 0 {
					zoneNodesWithDisks++
				}
			}
			if zoneNodesWithDisks < 2 {
				return fmt.Errorf("Replication Consistency with Zonal topology requires at least 2 nodes with disks in each zone, zone %q has %d", zone, zoneNodesWithDisks)
			}
		}

	default:
		// Ignored topology or unspecified: global check.
		if totalNodes < 2 {
			return fmt.Errorf("Replication Consistency requires at least 2 nodes, have %d", totalNodes)
		}
		if nodesWithDisks < 2 {
			return fmt.Errorf("Replication Consistency requires at least 2 nodes with disks, have %d", nodesWithDisks)
		}
	}

	return nil
}

// validateConsistencyAndAvailabilityReplication validates requirements for ConsistencyAndAvailability replication mode.
func validateConsistencyAndAvailabilityReplication(
	topology v1alpha1.ReplicatedStorageClassTopology,
	nodesWithDisks int,
	nodesByZone map[string][]v1alpha1.ReplicatedStoragePoolEligibleNode,
	zonesWithDisks int,
) error {
	switch topology {
	case v1alpha1.TopologyTransZonal:
		// 3 zones with disks.
		if zonesWithDisks < 3 {
			return fmt.Errorf("Replication ConsistencyAndAvailability with TransZonal topology requires at least 3 zones with disks, have %d", zonesWithDisks)
		}

	case v1alpha1.TopologyZonal:
		// Per zone: at least 3 nodes with disks.
		for zone, nodes := range nodesByZone {
			zoneNodesWithDisks := 0
			for _, n := range nodes {
				if len(n.LVMVolumeGroups) > 0 {
					zoneNodesWithDisks++
				}
			}
			if zoneNodesWithDisks < 3 {
				return fmt.Errorf("Replication ConsistencyAndAvailability with Zonal topology requires at least 3 nodes with disks in each zone, zone %q has %d", zone, zoneNodesWithDisks)
			}
		}

	default:
		// Ignored topology or unspecified: global check.
		if nodesWithDisks < 3 {
			return fmt.Errorf("Replication ConsistencyAndAvailability requires at least 3 nodes with disks, have %d", nodesWithDisks)
		}
	}

	return nil
}

// isConfigurationInSync checks if the RSC status configuration matches current generation.
func isConfigurationInSync(rsc *v1alpha1.ReplicatedStorageClass) bool {
	// Configuration must exist and generation must match.
	return rsc.Status.Configuration != nil && rsc.Status.ConfigurationGeneration == rsc.Generation
}

// computeActualVolumesSummary computes volume statistics from RV conditions.
//
// InConflictWithEligibleNodes is always calculated (regardless of acknowledgment).
// If any RV hasn't acknowledged the current RSC state (name/configurationGeneration mismatch),
// returns Total, PendingObservation, and InConflictWithEligibleNodes with Aligned/StaleConfiguration as nil -
// because we don't know the real counts for those until all RVs acknowledge.
// RVs without status.storageClass are considered acknowledged (to avoid flapping on new volumes).
func computeActualVolumesSummary(rsc *v1alpha1.ReplicatedStorageClass, rvs []rvView) v1alpha1.ReplicatedStorageClassVolumesSummary {
	total := int32(len(rvs))
	var pendingObservation, aligned, staleConfiguration, inConflictWithEligibleNodes int32
	usedStoragePoolNames := make(map[string]struct{})

	for i := range rvs {
		rv := &rvs[i]

		// Collect used storage pool names.
		if rv.configurationStoragePoolName != "" {
			usedStoragePoolNames[rv.configurationStoragePoolName] = struct{}{}
		}

		// Check nodes condition regardless of acknowledgment.
		// Only count as "in conflict" if the condition is present and not True.
		// Missing condition means the RV hasn't been evaluated yet.
		if rv.conditions.satisfyEligibleNodesKnown && !rv.conditions.satisfyEligibleNodes {
			inConflictWithEligibleNodes++
		}

		// Count unobserved volumes (aligned/staleConfiguration require acknowledgment).
		if !isRSCConfigurationAcknowledgedByRV(rsc, rv) {
			pendingObservation++
			continue
		}

		if rv.conditions.configurationReady && rv.conditions.satisfyEligibleNodes {
			aligned++
		}

		// Only count as "stale" if the condition is present and not True.
		// Missing condition means the RV hasn't been evaluated yet.
		if rv.conditions.configurationReadyKnown && !rv.conditions.configurationReady {
			staleConfiguration++
		}
	}

	// Build sorted list of used storage pool names.
	usedPoolNames := make([]string, 0, len(usedStoragePoolNames))
	for name := range usedStoragePoolNames {
		usedPoolNames = append(usedPoolNames, name)
	}
	slices.Sort(usedPoolNames)

	// If any volumes haven't observed, return Total, PendingObservation, and InConflictWithEligibleNodes.
	// We don't know the real counts for aligned/staleConfiguration until all RVs observe.
	if pendingObservation > 0 {
		return v1alpha1.ReplicatedStorageClassVolumesSummary{
			Total:                       &total,
			PendingObservation:          &pendingObservation,
			InConflictWithEligibleNodes: &inConflictWithEligibleNodes,
			UsedStoragePoolNames:        usedPoolNames,
		}
	}

	zero := int32(0)
	return v1alpha1.ReplicatedStorageClassVolumesSummary{
		Total:                       &total,
		PendingObservation:          &zero,
		Aligned:                     &aligned,
		StaleConfiguration:          &staleConfiguration,
		InConflictWithEligibleNodes: &inConflictWithEligibleNodes,
		UsedStoragePoolNames:        usedPoolNames,
	}
}

// isRSCConfigurationAcknowledgedByRV checks if the RV has acknowledged
// the current RSC configuration.
func isRSCConfigurationAcknowledgedByRV(rsc *v1alpha1.ReplicatedStorageClass, rv *rvView) bool {
	if rv.configurationObservedGeneration == 0 {
		return true
	}
	return rv.configurationObservedGeneration == rsc.Status.ConfigurationGeneration
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
	if !slices.Equal(rsc.Status.Volumes.UsedStoragePoolNames, summary.UsedStoragePoolNames) {
		rsc.Status.Volumes.UsedStoragePoolNames = summary.UsedStoragePoolNames
		changed = true
	}
	return changed
}

// --- Compute/Apply helpers: storagePool ---

// computeTargetStoragePool computes the target storagePool name.
// If status already has a value for the current generation, returns it without recomputing.
func computeTargetStoragePool(rsc *v1alpha1.ReplicatedStorageClass) string {
	// Return cached value if already computed for this generation.
	if rsc.Status.StoragePoolBasedOnGeneration == rsc.Generation && rsc.Status.StoragePoolName != "" {
		return rsc.Status.StoragePoolName
	}

	checksum := computeStoragePoolChecksum(rsc)
	return "auto-rsp-" + checksum
}

// computeStoragePoolChecksum computes FNV-128a checksum of RSC spec fields that go into RSP.
// Fields: storage.type, storage.lvmVolumeGroups, zones, nodeLabelSelector, systemNetworkNames.
func computeStoragePoolChecksum(rsc *v1alpha1.ReplicatedStorageClass) string {
	h := fnv.New128a()

	// storage.type
	h.Write([]byte(rsc.Spec.Storage.Type))
	h.Write([]byte{0}) // separator

	// storage.lvmVolumeGroups (sorted for determinism)
	lvgs := make([]string, 0, len(rsc.Spec.Storage.LVMVolumeGroups))
	for _, lvg := range rsc.Spec.Storage.LVMVolumeGroups {
		// Include both name and thinPoolName
		lvgs = append(lvgs, lvg.Name+":"+lvg.ThinPoolName)
	}
	slices.Sort(lvgs)
	for _, lvg := range lvgs {
		h.Write([]byte(lvg))
		h.Write([]byte{0})
	}

	// zones (sorted for determinism)
	zones := slices.Clone(rsc.Spec.Zones)
	slices.Sort(zones)
	for _, z := range zones {
		h.Write([]byte(z))
		h.Write([]byte{0})
	}

	// nodeLabelSelector (JSON for deterministic serialization)
	if rsc.Spec.NodeLabelSelector != nil {
		selectorBytes, _ := json.Marshal(rsc.Spec.NodeLabelSelector)
		h.Write(selectorBytes)
	}
	h.Write([]byte{0})

	// systemNetworkNames (sorted for determinism)
	networkNames := slices.Clone(rsc.Spec.SystemNetworkNames)
	slices.Sort(networkNames)
	for _, n := range networkNames {
		h.Write([]byte(n))
		h.Write([]byte{0})
	}

	return hex.EncodeToString(h.Sum(nil))
}

// applyStoragePool applies target storagePool fields to status. Returns true if changed.
func applyStoragePool(rsc *v1alpha1.ReplicatedStorageClass, targetName string) bool {
	changed := false
	if rsc.Status.StoragePoolBasedOnGeneration != rsc.Generation {
		rsc.Status.StoragePoolBasedOnGeneration = rsc.Generation
		changed = true
	}
	if rsc.Status.StoragePoolName != targetName {
		rsc.Status.StoragePoolName = targetName
		changed = true
	}
	return changed
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: ReplicatedStoragePool (RSP)
//

// reconcileRSP ensures the auto-generated RSP exists and is properly configured.
// Creates RSP if not found, updates finalizer and usedBy if needed.
//
// Reconcile pattern: Conditional target evaluation
func (r *Reconciler) reconcileRSP(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
	targetStoragePoolName string,
) (rsp *v1alpha1.ReplicatedStoragePool, outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "rsp", "rsp", targetStoragePoolName)
	defer rf.OnEnd(&outcome)

	// Get existing RSP.
	var err error
	rsp, err = r.getRSP(rf.Ctx(), targetStoragePoolName)
	if err != nil {
		return nil, rf.Fail(err)
	}

	// If RSP doesn't exist, create it.
	if rsp == nil {
		rsp = newRSP(targetStoragePoolName, rsc)
		if err := r.createRSP(rf.Ctx(), rsp); err != nil {
			if apierrors.IsAlreadyExists(err) {
				// Another RSC created this RSP concurrently. Requeue to pick it up from cache.
				rf.Log().Info("RSP already exists, requeueing", "rsp", targetStoragePoolName)
				return nil, rf.DoneAndRequeue()
			}
			return nil, rf.Fail(err)
		}
		// Continue to ensure usedBy is set below.
	}

	// Ensure finalizer is set.
	if !objutilv1.HasFinalizer(rsp, v1alpha1.RSCControllerFinalizer) {
		base := rsp.DeepCopy()
		applyRSPFinalizer(rsp, true)
		if err := r.patchRSP(rf.Ctx(), rsp, base); err != nil {
			return nil, rf.Fail(err)
		}
	}

	// Ensure usedBy is set.
	if !slices.Contains(rsp.Status.UsedBy.ReplicatedStorageClassNames, rsc.Name) {
		base := rsp.DeepCopy()
		applyRSPUsedBy(rsp, rsc.Name)
		if err := r.patchRSPStatus(rf.Ctx(), rsp, base); err != nil {
			return nil, rf.Fail(err)
		}
	}

	return rsp, rf.Continue()
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: unused-rsps
//

// reconcileUnusedRSPs releases storage pools that are no longer used by this RSC.
//
// Reconcile pattern: Pure orchestration
func (r *Reconciler) reconcileUnusedRSPs(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
	usedStoragePoolNames []string,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "unused-rsps")
	defer rf.OnEnd(&outcome)

	// Filter out RSPs that are still in use.
	unusedStoragePoolNames := slices.DeleteFunc(slices.Clone(usedStoragePoolNames), func(name string) bool {
		if name == rsc.Status.StoragePoolName {
			return true
		}
		_, found := slices.BinarySearch(rsc.Status.Volumes.UsedStoragePoolNames, name)
		return found
	})

	// Release each unused RSP.
	outcomes := make([]flow.ReconcileOutcome, 0, len(unusedStoragePoolNames))
	for _, rspName := range unusedStoragePoolNames {
		outcomes = append(outcomes, r.reconcileRSPRelease(rf.Ctx(), rsc.Name, rspName))
	}

	return flow.MergeReconciles(outcomes...)
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: rsp-release
//

// reconcileRSPRelease releases the RSP from this RSC.
// Removes RSC from usedBy, and if no more users - deletes the RSP.
//
// Reconcile pattern: Conditional target evaluation
func (r *Reconciler) reconcileRSPRelease(
	ctx context.Context,
	rscName string,
	rspName string,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "rsp-release", "rsp", rspName)
	defer rf.OnEnd(&outcome)

	// Get RSP. If not found - nothing to release.
	rsp, err := r.getRSP(rf.Ctx(), rspName)
	if err != nil {
		return rf.Fail(err)
	}
	if rsp == nil {
		return rf.Continue()
	}

	// Check if this RSC is in usedBy (sorted list).
	if _, found := slices.BinarySearch(rsp.Status.UsedBy.ReplicatedStorageClassNames, rscName); !found {
		return rf.Continue()
	}

	// Remove RSC from usedBy with optimistic lock.
	base := rsp.DeepCopy()
	applyRSPRemoveUsedBy(rsp, rscName)
	if err := r.patchRSPStatus(rf.Ctx(), rsp, base); err != nil {
		return rf.Fail(err)
	}

	// If no more users - delete RSP.
	if len(rsp.Status.UsedBy.ReplicatedStorageClassNames) == 0 {
		// Remove finalizer first (if present).
		if objutilv1.HasFinalizer(rsp, v1alpha1.RSCControllerFinalizer) {
			base := rsp.DeepCopy()
			applyRSPFinalizer(rsp, false)
			if err := r.patchRSP(rf.Ctx(), rsp, base); err != nil {
				return rf.Fail(err)
			}
		}

		// Delete RSP.
		if err := r.deleteRSP(rf.Ctx(), rsp); err != nil {
			return rf.Fail(err)
		}
	}

	return rf.Continue()
}

// --- Helpers: Reconcile (non-I/O) ---

// --- Helpers: ReplicatedStoragePool (RSP) ---

// newRSP constructs a new RSP from RSC spec.
func newRSP(name string, rsc *v1alpha1.ReplicatedStorageClass) *v1alpha1.ReplicatedStoragePool {
	rsp := &v1alpha1.ReplicatedStoragePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Finalizers: []string{v1alpha1.RSCControllerFinalizer},
		},
		Spec: v1alpha1.ReplicatedStoragePoolSpec{
			Type:               rsc.Spec.Storage.Type,
			LVMVolumeGroups:    slices.Clone(rsc.Spec.Storage.LVMVolumeGroups),
			Zones:              slices.Clone(rsc.Spec.Zones),
			SystemNetworkNames: slices.Clone(rsc.Spec.SystemNetworkNames),
			EligibleNodesPolicy: v1alpha1.ReplicatedStoragePoolEligibleNodesPolicy{
				NotReadyGracePeriod: rsc.Spec.EligibleNodesPolicy.NotReadyGracePeriod,
			},
		},
	}

	// Copy NodeLabelSelector if present.
	if rsc.Spec.NodeLabelSelector != nil {
		rsp.Spec.NodeLabelSelector = rsc.Spec.NodeLabelSelector.DeepCopy()
	}

	return rsp
}

// applyRSPFinalizer adds or removes the RSC controller finalizer on RSP.
// Returns true if the finalizer list was changed.
//
//nolint:unparam // Return value might be unused because callers pre-check with HasFinalizer.
func applyRSPFinalizer(rsp *v1alpha1.ReplicatedStoragePool, present bool) bool {
	if present {
		return objutilv1.AddFinalizer(rsp, v1alpha1.RSCControllerFinalizer)
	}
	return objutilv1.RemoveFinalizer(rsp, v1alpha1.RSCControllerFinalizer)
}

// applyRSPUsedBy adds the RSC name to RSP status.usedBy if not already present.
func applyRSPUsedBy(rsp *v1alpha1.ReplicatedStoragePool, rscName string) bool {
	if slices.Contains(rsp.Status.UsedBy.ReplicatedStorageClassNames, rscName) {
		return false
	}
	rsp.Status.UsedBy.ReplicatedStorageClassNames = append(
		rsp.Status.UsedBy.ReplicatedStorageClassNames,
		rscName,
	)
	// Sort for deterministic ordering.
	sort.Strings(rsp.Status.UsedBy.ReplicatedStorageClassNames)
	return true
}

// applyRSPRemoveUsedBy removes the RSC name from RSP status.usedBy.
func applyRSPRemoveUsedBy(rsp *v1alpha1.ReplicatedStoragePool, rscName string) bool {
	idx := slices.Index(rsp.Status.UsedBy.ReplicatedStorageClassNames, rscName)
	if idx < 0 {
		return false
	}
	rsp.Status.UsedBy.ReplicatedStorageClassNames = slices.Delete(
		rsp.Status.UsedBy.ReplicatedStorageClassNames,
		idx, idx+1,
	)
	return true
}

// ──────────────────────────────────────────────────────────────────────────────
// Single-call I/O helper categories
//

// getRSC fetches an RSC by name. Returns (nil, nil) if not found.
func (r *Reconciler) getRSC(ctx context.Context, name string) (*v1alpha1.ReplicatedStorageClass, error) {
	var rsc v1alpha1.ReplicatedStorageClass
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, &rsc); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
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

// getUsedStoragePoolNames returns names of RSPs used by this RSC.
// Uses the index for efficient lookup and UnsafeDisableDeepCopy for performance.
func (r *Reconciler) getUsedStoragePoolNames(ctx context.Context, rscName string) ([]string, error) {
	var unsafeList v1alpha1.ReplicatedStoragePoolList
	if err := r.cl.List(ctx, &unsafeList,
		client.MatchingFields{indexes.IndexFieldRSPByUsedByRSCName: rscName},
		client.UnsafeDisableDeepCopy,
	); err != nil {
		return nil, err
	}

	names := make([]string, len(unsafeList.Items))
	for i := range unsafeList.Items {
		names[i] = unsafeList.Items[i].Name
	}
	return names, nil
}

// getSortedRVsByRSC fetches RVs referencing a specific RSC using the index, sorted by name.
func (r *Reconciler) getSortedRVsByRSC(ctx context.Context, rscName string) ([]rvView, error) {
	var unsafeList v1alpha1.ReplicatedVolumeList
	if err := r.cl.List(ctx, &unsafeList,
		client.MatchingFields{indexes.IndexFieldRVByReplicatedStorageClassName: rscName},
		client.UnsafeDisableDeepCopy,
	); err != nil {
		return nil, err
	}

	rvs := make([]rvView, len(unsafeList.Items))
	for i := range unsafeList.Items {
		rvs[i] = newRVView(&unsafeList.Items[i])
	}

	sort.Slice(rvs, func(i, j int) bool {
		return rvs[i].name < rvs[j].name
	})

	return rvs, nil
}

// patchRSC patches the RSC main resource.
func (r *Reconciler) patchRSC(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
	base *v1alpha1.ReplicatedStorageClass,
) error {
	return r.cl.Patch(ctx, rsc, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}

// patchRSCStatus patches the RSC status subresource.
func (r *Reconciler) patchRSCStatus(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
	base *v1alpha1.ReplicatedStorageClass,
) error {
	return r.cl.Status().Patch(ctx, rsc, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}

// createRSP creates an RSP.
func (r *Reconciler) createRSP(ctx context.Context, rsp *v1alpha1.ReplicatedStoragePool) error {
	return r.cl.Create(ctx, rsp)
}

// patchRSP patches the RSP main resource.
func (r *Reconciler) patchRSP(
	ctx context.Context,
	rsp *v1alpha1.ReplicatedStoragePool,
	base *v1alpha1.ReplicatedStoragePool,
) error {
	return r.cl.Patch(ctx, rsp, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}

// patchRSPStatus patches the RSP status subresource.
func (r *Reconciler) patchRSPStatus(
	ctx context.Context,
	rsp *v1alpha1.ReplicatedStoragePool,
	base *v1alpha1.ReplicatedStoragePool,
) error {
	return r.cl.Status().Patch(ctx, rsp, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}

// deleteRSP deletes an RSP.
func (r *Reconciler) deleteRSP(ctx context.Context, rsp *v1alpha1.ReplicatedStoragePool) error {
	if rsp.DeletionTimestamp != nil {
		return nil
	}
	if err := client.IgnoreNotFound(r.cl.Delete(ctx, rsp, client.Preconditions{
		UID:             &rsp.UID,
		ResourceVersion: &rsp.ResourceVersion,
	})); err != nil {
		return err
	}
	rsp.DeletionTimestamp = ptr.To(metav1.Now())
	return nil
}

// rvoCacheRefs wraps RVO list from informer cache (UnsafeDisableDeepCopy).
// Items are READ-ONLY direct cache references - modifying them corrupts the cache.
//
// This optimization avoids N×DeepCopy allocations when listing RVOs.
// In large clusters with 2000+ ReplicatedVolumes, we may have 100-400 active RVOs
// during rolling operations. Skipping DeepCopy reduces GC pressure significantly.
//
// Usage:
//   - Use Len()/At(i) for iteration
//   - NEVER modify items directly - always DeepCopy first
//   - Pass the wrapper (not raw slice) to prevent accidental modifications
type rvoCacheRefs struct {
	items []v1alpha1.ReplicatedVolumeOperation
}

// Len returns the number of cached RVOs.
func (r *rvoCacheRefs) Len() int { return len(r.items) }

// At returns a READ-ONLY pointer to the item at index i.
// WARNING: Do NOT modify the returned value - it points to informer cache.
// Use item.DeepCopy() before any modification.
func (r *rvoCacheRefs) At(i int) *v1alpha1.ReplicatedVolumeOperation { return &r.items[i] }

// getRVOsCacheRefs fetches RVOs owned by a specific RSC, sorted by name.
// Returns rvoCacheRefs wrapper with direct cache references (UnsafeDisableDeepCopy).
// See rvoCacheRefs doc for usage and safety guidelines.
func (r *Reconciler) getRVOsCacheRefs(ctx context.Context, rscName string) (*rvoCacheRefs, error) {
	var list v1alpha1.ReplicatedVolumeOperationList
	if err := r.cl.List(ctx, &list,
		client.MatchingFields{indexes.IndexFieldRVOByRSCOwnerRef: rscName},
		client.UnsafeDisableDeepCopy,
	); err != nil {
		return nil, err
	}
	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].Name < list.Items[j].Name
	})
	return &rvoCacheRefs{items: list.Items}, nil
}

// createRVO creates a ReplicatedVolumeOperation.
func (r *Reconciler) createRVO(ctx context.Context, rvo *v1alpha1.ReplicatedVolumeOperation) error {
	return r.cl.Create(ctx, rvo)
}

// patchCompletedRVOsRemoveFinalizer removes the controller finalizer from completed RVOs
// whose cursor has advanced or rolling update is complete.
//
// NOTE: This is a batch patch operation — it patches multiple objects in a loop.
// It is NOT a single-call PatchReconcileHelper.
// Same pattern as patchScheduledReplicas in rvr_scheduling_controller.
//
// TODO(rvo-controller): Handle edge case where RV is deleted while RVO is in progress.
// If the target ReplicatedVolume is deleted, the RVO will be stuck in "terminating" state
// because the finalizer blocks GC. The RVO controller must detect this case (missing RV)
// and mark the operation as Completed (Failed or Succeeded) to allow finalizer removal.
func (r *Reconciler) patchCompletedRVOsRemoveFinalizer(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
	rvos *rvoCacheRefs,
) error {
	for i := 0; i < rvos.Len(); i++ {
		rvo := rvos.At(i) // Read-only cache ref.

		// Skip if no finalizer.
		if !controllerutil.ContainsFinalizer(rvo, v1alpha1.RSCControllerFinalizer) {
			continue
		}

		// Skip if not completed.
		completed := objutilv1.GetStatusCondition(rvo, v1alpha1.ReplicatedVolumeOperationCondCompletedType)
		if completed == nil || completed.Status != metav1.ConditionTrue {
			continue
		}

		// Parse cursor from RVO name.
		opCursor := parseRVOCursor(rvo.Name)

		// Skip RVOs with unparseable cursor (corrupted name).
		// These require manual cleanup or handling by RVO controller.
		if opCursor == "" {
			continue
		}

		// Determine if we should remove the finalizer.
		// Remove if:
		// 1. Rolling update is complete (cursor is empty), OR
		// 2. Cursor has moved forward (current cursor != operation cursor)
		currentCursor := rsc.Status.RollingOperationsCursor
		shouldRemove := currentCursor == "" || currentCursor != opCursor

		if !shouldRemove {
			continue
		}

		// Patch to remove finalizer.
		// rvo is read-only cache ref (rvoCacheRefs) - must DeepCopy before modifying.
		modified := rvo.DeepCopy()
		controllerutil.RemoveFinalizer(modified, v1alpha1.RSCControllerFinalizer)
		if err := r.cl.Patch(ctx, modified, client.MergeFrom(rvo)); err != nil {
			return fmt.Errorf("remove finalizer from RVO %s: %w", rvo.Name, err)
		}
	}

	return nil
}
