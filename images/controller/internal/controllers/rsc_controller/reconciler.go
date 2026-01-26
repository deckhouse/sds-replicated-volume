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
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"slices"
	"sort"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// ──────────────────────────────────────────────────────────────────────────────
// Wiring / construction
//

type Reconciler struct {
	cl client.Client
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

const replicatedStorageClassFinalizerName = "replicatedstorageclass.storage.deckhouse.io"

func NewReconciler(cl client.Client) *Reconciler {
	return &Reconciler{cl: cl}
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile
//

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

	// Reconcile StorageClass.
	outcome := r.reconcileStorageClass(rf.Ctx(), rsc)
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	// Reconcile migration from RSP (deprecated storagePool field).
	outcome = r.reconcileMigrationFromRSP(rf.Ctx(), rsc)
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	// Reconcile main (finalizer management).
	outcome = r.reconcileMain(rf.Ctx(), rsc, rvs)
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	// Reconcile status.
	outcome = r.reconcileStatus(rf.Ctx(), rsc, rvs)
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	// Release storage pools that are no longer used.
	return r.reconcileUnusedRSPs(rf.Ctx(), rsc).ToCtrl()
}

// reconcileMigrationFromRSP migrates StoragePool to spec.Storage.
//
// Reconcile pattern: Target-state driven
//
// Logic:
//   - If storagePool is empty → Continue (nothing to migrate)
//   - If storagePool set AND RSP not found → set conditions (Ready=False, StoragePoolReady=False), patch status, return Done
//   - If storagePool set AND RSP found → copy type+lvmVolumeGroups to spec.storage, clear storagePool
func (r *Reconciler) reconcileMigrationFromRSP(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "migration-from-rsp")
	defer rf.OnEnd(&outcome)

	// Nothing to migrate.
	if rsc.Spec.StoragePool == "" {
		return rf.Continue()
	}

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
			if err := r.patchRSCStatus(rf.Ctx(), rsc, base, false); err != nil {
				return rf.Fail(err)
			}
		}
		return rf.Done()
	}

	// RSP found, migrate storage configuration.
	targetStorage := computeTargetStorageFromRSP(rsp)

	base := rsc.DeepCopy()
	applyStorageMigration(rsc, targetStorage)

	if err := r.patchRSC(rf.Ctx(), rsc, base, true); err != nil {
		return rf.Fail(err)
	}

	return rf.Continue()
}

// reconcileMain manages the finalizer.
//
// Reconcile pattern: Target-state driven
//
// Logic:
//   - If no finalizer → add it
//   - If deletionTimestamp set AND no RVs → remove finalizer
func (r *Reconciler) reconcileMain(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
	rvs []rvView,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "main")
	defer rf.OnEnd(&outcome)

	// Compute target for finalizers.
	actualControllerFinalizerPresent := computeActualFinalizerPresent(rsc)
	targetControllerFinalizerPresent := computeTargetFinalizerPresent(rsc, rvs)
	actualLegacyFinalizerPresent := computeActualLegacyFinalizerPresent(rsc)
	targetLegacyFinalizerPresent := computeTargetLegacyFinalizerPresent(rsc)

	// If nothing changed, continue.
	if targetControllerFinalizerPresent == actualControllerFinalizerPresent &&
		targetLegacyFinalizerPresent == actualLegacyFinalizerPresent {
		return rf.Continue()
	}

	base := rsc.DeepCopy()
	applyFinalizers(rsc, targetControllerFinalizerPresent, targetLegacyFinalizerPresent)

	if err := r.patchRSC(rf.Ctx(), rsc, base, true); err != nil {
		return rf.Fail(err)
	}

	// If controller finalizer was removed, we're done (object will be deleted).
	if !targetControllerFinalizerPresent {
		return rf.Done()
	}

	return rf.Continue()
}

// computeTargetStorageFromRSP computes the target Storage from the RSP spec.
func computeTargetStorageFromRSP(rsp *v1alpha1.ReplicatedStoragePool) v1alpha1.ReplicatedStorageClassStorage {
	// Clone LVMVolumeGroups to avoid aliasing.
	lvmVolumeGroups := make([]v1alpha1.ReplicatedStoragePoolLVMVolumeGroups, len(rsp.Spec.LVMVolumeGroups))
	copy(lvmVolumeGroups, rsp.Spec.LVMVolumeGroups)

	return v1alpha1.ReplicatedStorageClassStorage{
		Type:            rsp.Spec.Type,
		LVMVolumeGroups: lvmVolumeGroups,
	}
}

// applyStorageMigration applies the target storage and clears the storagePool field.
func applyStorageMigration(rsc *v1alpha1.ReplicatedStorageClass, targetStorage v1alpha1.ReplicatedStorageClassStorage) {
	rsc.Spec.Storage = targetStorage
	rsc.Spec.StoragePool = ""
}

// computeActualFinalizerPresent returns whether the controller finalizer is present on the RSC.
func computeActualFinalizerPresent(rsc *v1alpha1.ReplicatedStorageClass) bool {
	return objutilv1.HasFinalizer(rsc, v1alpha1.RSCControllerFinalizer)
}

// computeTargetFinalizerPresent returns whether the controller finalizer should be present.
// The finalizer should be present unless the RSC is being deleted AND has no RVs.
func computeTargetFinalizerPresent(rsc *v1alpha1.ReplicatedStorageClass, rvs []rvView) bool {
	isDeleting := rsc.DeletionTimestamp != nil
	hasRVs := len(rvs) > 0

	// Keep finalizer if not deleting or if there are still RVs.
	return !isDeleting || hasRVs
}

func computeActualLegacyFinalizerPresent(rsc *v1alpha1.ReplicatedStorageClass) bool {
	return objutilv1.HasFinalizer(rsc, replicatedStorageClassFinalizerName)
}

func computeTargetLegacyFinalizerPresent(rsc *v1alpha1.ReplicatedStorageClass) bool {
	return rsc.DeletionTimestamp == nil
}

func applyFinalizers(rsc *v1alpha1.ReplicatedStorageClass, targetControllerFinalizerPresent, targetLegacyFinalizerPresent bool) {
	if targetControllerFinalizerPresent {
		objutilv1.AddFinalizer(rsc, v1alpha1.RSCControllerFinalizer)
	} else {
		objutilv1.RemoveFinalizer(rsc, v1alpha1.RSCControllerFinalizer)
	}

	if targetLegacyFinalizerPresent {
		objutilv1.AddFinalizer(rsc, replicatedStorageClassFinalizerName)
	} else {
		objutilv1.RemoveFinalizer(rsc, replicatedStorageClassFinalizerName)
	}
}

// --- Reconcile: status ---

// reconcileStatus reconciles the RSC status.
//
// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileStatus(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
	rvs []rvView,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "status")
	defer rf.OnEnd(&outcome)

	// Compute target storage pool name (cached if already computed for this generation).
	targetStoragePoolName := computeTargetStoragePool(rsc)

	// Ensure auto-generated RSP exists and is configured.
	outcome, rsp := r.reconcileRSP(rf.Ctx(), rsc, targetStoragePoolName)
	if outcome.ShouldReturn() {
		return outcome
	}

	// Take patch base before mutations.
	base := rsc.DeepCopy()

	eo := flow.MergeEnsures(
		// Ensure storagePool name and condition are up to date.
		ensureStoragePool(rf.Ctx(), rsc, targetStoragePoolName, rsp),

		// Ensure configuration is up to date based on RSP state.
		ensureConfiguration(rf.Ctx(), rsc, rsp),

		// Ensure volume summary and conditions.
		ensureVolumeSummaryAndConditions(rf.Ctx(), rsc, rvs),
	)

	// Patch if changed.
	if eo.DidChange() {
		if err := r.patchRSCStatus(rf.Ctx(), rsc, base, eo.OptimisticLockRequired()); err != nil {
			return rf.Fail(err)
		}
	}

	return rf.Done()
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
//  3. If RSP.EligibleNodesRevision != rsc.status.StoragePoolEligibleNodesRevision:
//     - Validate RSP.EligibleNodes against topology/replication requirements.
//     - If invalid: Ready=False (InsufficientEligibleNodes) and return.
//     - Update rsc.status.StoragePoolEligibleNodesRevision.
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

	// 2. If StoragePoolReady != True: set Ready=False and return.
	if !objutilv1.IsStatusConditionPresentAndTrue(rsc, v1alpha1.ReplicatedStorageClassCondStoragePoolReadyType) {
		changed = applyReadyCondFalse(rsc,
			v1alpha1.ReplicatedStorageClassCondReadyReasonWaitingForStoragePool,
			"Waiting for ReplicatedStoragePool to become ready")
		return ef.Ok().ReportChangedIf(changed)
	}

	// 3. Validate eligibleNodes if revision changed.
	if rsp.Status.EligibleNodesRevision != rsc.Status.StoragePoolEligibleNodesRevision {
		if err := validateEligibleNodes(rsp.Status.EligibleNodes, rsc.Spec.Topology, rsc.Spec.Replication); err != nil {
			changed = applyReadyCondFalse(rsc,
				v1alpha1.ReplicatedStorageClassCondReadyReasonInsufficientEligibleNodes,
				err.Error())
			return ef.Ok().ReportChangedIf(changed)
		}

		// Update StoragePoolEligibleNodesRevision.
		rsc.Status.StoragePoolEligibleNodesRevision = rsp.Status.EligibleNodesRevision
		changed = true
	}

	// 4. If configuration is in sync, we're done.
	if isConfigurationInSync(rsc) {
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

	return ef.Ok().ReportChanged().RequireOptimisticLock()
}

// ensureVolumeSummaryAndConditions computes and applies volume summary and conditions in-place.
//
// Sets ConfigurationRolledOut and VolumesSatisfyEligibleNodes conditions based on
// volume counters (StaleConfiguration, InConflictWithEligibleNodes, PendingObservation).
func ensureVolumeSummaryAndConditions(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
	rvs []rvView,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "volume-summary-and-conditions")
	defer ef.OnEnd(&outcome)

	// Compute and apply volume summary.
	summary := computeActualVolumesSummary(rsc, rvs)
	changed := applyVolumesSummary(rsc, summary)

	maxParallelConfigurationRollouts, maxParallelConflictResolutions := computeRollingStrategiesConfiguration(rsc)

	// Apply VolumesSatisfyEligibleNodes condition (calculated regardless of acknowledgment).
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

	// ConfigurationRolledOut requires all volumes to acknowledge.
	if *rsc.Status.Volumes.PendingObservation > 0 {
		msg := fmt.Sprintf("%d volume(s) pending observation", *rsc.Status.Volumes.PendingObservation)
		changed = applyConfigurationRolledOutCondUnknown(rsc,
			v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonNewConfigurationNotYetObserved,
			msg,
		) || changed
		// Don't process configuration rolling updates until all volumes acknowledge.
		return ef.Ok().ReportChangedIf(changed)
	}

	// Apply ConfigurationRolledOut condition.
	if *rsc.Status.Volumes.StaleConfiguration > 0 {
		if maxParallelConfigurationRollouts > 0 {
			changed = applyConfigurationRolledOutCondFalse(rsc,
				v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonConfigurationRolloutInProgress,
				"not implemented",
			) || changed
		} else {
			changed = applyConfigurationRolledOutCondFalse(rsc,
				v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonConfigurationRolloutDisabled,
				"not implemented",
			) || changed
		}
	} else {
		changed = applyConfigurationRolledOutCondTrue(rsc,
			v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonRolledOutToAllVolumes,
			"All volumes have configuration matching the storage class",
		) || changed
	}

	return ef.Ok().ReportChangedIf(changed)
}

// ──────────────────────────────────────────────────────────────────────────────
// View types
//

// rvView is a lightweight projection of ReplicatedVolume fields used by this controller.
type rvView struct {
	name                            string
	configurationStoragePoolName    string
	configurationObservedGeneration int64
	conditions                      rvViewConditions
}

type rvViewConditions struct {
	satisfyEligibleNodes bool
	configurationReady   bool
}

// newRVView creates an rvView from a ReplicatedVolume.
// The unsafeRV may come from cache without DeepCopy; rvView copies only the needed scalar fields.
func newRVView(unsafeRV *v1alpha1.ReplicatedVolume) rvView {
	view := rvView{
		name:                            unsafeRV.Name,
		configurationObservedGeneration: unsafeRV.Status.ConfigurationObservedGeneration,
		conditions: rvViewConditions{
			satisfyEligibleNodes: objutilv1.IsStatusConditionPresentAndTrue(unsafeRV, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType),
			configurationReady:   objutilv1.IsStatusConditionPresentAndTrue(unsafeRV, v1alpha1.ReplicatedVolumeCondConfigurationReadyType),
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
		return fmt.Errorf("no eligible nodes")
	}

	// Count nodes and nodes with disks.
	totalNodes := len(eligibleNodes)
	nodesWithDisks := 0
	for _, n := range eligibleNodes {
		if isNodeSchedulable(n) {
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
			if isNodeSchedulable(n) {
				zonesWithDisks++
				break
			}
		}
	}

	switch replication {
	case v1alpha1.ReplicationNone:
		// At least 1 node required.
		if totalNodes < 1 {
			return fmt.Errorf("replication None requires at least 1 node, have %d", totalNodes)
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
			return fmt.Errorf("replication Availability with TransZonal topology requires nodes in at least 3 zones, have %d", len(nodesByZone))
		}
		if zonesWithDisks < 2 {
			return fmt.Errorf("replication Availability with TransZonal topology requires at least 2 zones with disks, have %d", zonesWithDisks)
		}

	case v1alpha1.TopologyZonal:
		// Per zone: at least 3 nodes, at least 2 with disks.
		for zone, nodes := range nodesByZone {
			zoneNodesWithDisks := 0
			for _, n := range nodes {
				if isNodeSchedulable(n) {
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
	nodesByZone map[string][]v1alpha1.ReplicatedStoragePoolEligibleNode,
	zonesWithDisks int,
) error {
	switch topology {
	case v1alpha1.TopologyTransZonal:
		// 2 different zones with disks.
		if zonesWithDisks < 2 {
			return fmt.Errorf("replication Consistency with TransZonal topology requires at least 2 zones with disks, have %d", zonesWithDisks)
		}

	case v1alpha1.TopologyZonal:
		// Per zone: at least 2 nodes with disks.
		for zone, nodes := range nodesByZone {
			zoneNodesWithDisks := 0
			for _, n := range nodes {
				if isNodeSchedulable(n) {
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
	nodesByZone map[string][]v1alpha1.ReplicatedStoragePoolEligibleNode,
	zonesWithDisks int,
) error {
	switch topology {
	case v1alpha1.TopologyTransZonal:
		// 3 zones with disks.
		if zonesWithDisks < 3 {
			return fmt.Errorf("replication ConsistencyAndAvailability with TransZonal topology requires at least 3 zones with disks, have %d", zonesWithDisks)
		}

	case v1alpha1.TopologyZonal:
		// Per zone: at least 3 nodes with disks.
		for zone, nodes := range nodesByZone {
			zoneNodesWithDisks := 0
			for _, n := range nodes {
				if isNodeSchedulable(n) {
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

// isNodeSchedulable checks if a node is eligible for scheduling new replicas.
// A node is schedulable if:
//  1. Node is not explicitly marked unschedulable
//  2. Node (Kubernetes) is ready
//  3. Agent (sds-replicated-volume) is ready
//  4. Node has at least one ready and schedulable LVG
func isNodeSchedulable(node v1alpha1.ReplicatedStoragePoolEligibleNode) bool {
	if node.Unschedulable || !node.NodeReady || !node.AgentReady {
		return false
	}

	for _, lvg := range node.LVMVolumeGroups {
		if lvg.Ready && !lvg.Unschedulable {
			return true
		}
	}

	return false
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
		if !rv.conditions.satisfyEligibleNodes {
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

		if !rv.conditions.configurationReady {
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

// --- Reconcile: RSP ---

// reconcileRSP ensures the auto-generated RSP exists and is properly configured.
// Creates RSP if not found, updates finalizer and usedBy if needed.
//
// Reconcile pattern: Conditional desired evaluation
func (r *Reconciler) reconcileRSP(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
	targetStoragePoolName string,
) (outcome flow.ReconcileOutcome, rsp *v1alpha1.ReplicatedStoragePool) {
	rf := flow.BeginReconcile(ctx, "rsp")
	defer rf.OnEnd(&outcome)

	// Get existing RSP.
	var err error
	rsp, err = r.getRSP(rf.Ctx(), targetStoragePoolName)
	if err != nil {
		return rf.Fail(err), nil
	}

	// If RSP doesn't exist, create it.
	if rsp == nil {
		rsp = newRSP(targetStoragePoolName, rsc)
		if err := r.createRSP(rf.Ctx(), rsp); err != nil {
			return rf.Fail(err), nil
		}
		// Continue to ensure usedBy is set below.
	}

	// Ensure finalizer is set.
	if !objutilv1.HasFinalizer(rsp, v1alpha1.RSCControllerFinalizer) {
		base := rsp.DeepCopy()
		applyRSPFinalizer(rsp, true)
		if err := r.patchRSP(rf.Ctx(), rsp, base, true); err != nil {
			return rf.Fail(err), nil
		}
	}

	// Ensure usedBy is set.
	if !slices.Contains(rsp.Status.UsedBy.ReplicatedStorageClassNames, rsc.Name) {
		base := rsp.DeepCopy()
		applyRSPUsedBy(rsp, rsc.Name)
		if err := r.patchRSPStatus(rf.Ctx(), rsp, base, true); err != nil {
			return rf.Fail(err), nil
		}
	}

	return rf.Continue(), rsp
}

// reconcileUnusedRSPs releases storage pools that are no longer used by this RSC.
//
// Reconcile pattern: Pure orchestration
func (r *Reconciler) reconcileUnusedRSPs(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "unused-rsps")
	defer rf.OnEnd(&outcome)

	// Get all RSPs that reference this RSC.
	usedStoragePoolNames, err := r.getUsedStoragePoolNames(rf.Ctx(), rsc.Name)
	if err != nil {
		return rf.Fail(err)
	}

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

// reconcileRSPRelease releases the RSP from this RSC.
// Removes RSC from usedBy, and if no more users - deletes the RSP.
//
// Reconcile pattern: Conditional desired evaluation
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
	if err := r.patchRSPStatus(rf.Ctx(), rsp, base, true); err != nil {
		return rf.Fail(err)
	}

	// If no more users - delete RSP.
	if len(rsp.Status.UsedBy.ReplicatedStorageClassNames) == 0 {
		// Remove finalizer first (if present).
		if objutilv1.HasFinalizer(rsp, v1alpha1.RSCControllerFinalizer) {
			base := rsp.DeepCopy()
			applyRSPFinalizer(rsp, false)
			if err := r.patchRSP(rf.Ctx(), rsp, base, true); err != nil {
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

// --- Helpers: RSP ---

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

// createRSP creates an RSP.
func (r *Reconciler) createRSP(ctx context.Context, rsp *v1alpha1.ReplicatedStoragePool) error {
	return r.cl.Create(ctx, rsp)
}

// patchRSP patches the RSP main resource.
func (r *Reconciler) patchRSP(
	ctx context.Context,
	rsp *v1alpha1.ReplicatedStoragePool,
	base *v1alpha1.ReplicatedStoragePool,
	optimisticLock bool,
) error {
	var patch client.Patch
	if optimisticLock {
		patch = client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	} else {
		patch = client.MergeFrom(base)
	}
	return r.cl.Patch(ctx, rsp, patch)
}

// patchRSPStatus patches the RSP status subresource.
func (r *Reconciler) patchRSPStatus(
	ctx context.Context,
	rsp *v1alpha1.ReplicatedStoragePool,
	base *v1alpha1.ReplicatedStoragePool,
	optimisticLock bool,
) error {
	var patch client.Patch
	if optimisticLock {
		patch = client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	} else {
		patch = client.MergeFrom(base)
	}
	return r.cl.Status().Patch(ctx, rsp, patch)
}

// deleteRSP deletes an RSP.
func (r *Reconciler) deleteRSP(ctx context.Context, rsp *v1alpha1.ReplicatedStoragePool) error {
	return r.cl.Delete(ctx, rsp, client.Preconditions{
		UID:             &rsp.UID,
		ResourceVersion: &rsp.ResourceVersion,
	})
}
