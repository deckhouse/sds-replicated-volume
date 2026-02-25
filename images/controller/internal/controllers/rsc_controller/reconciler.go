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
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
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
	cl           client.Client
	controllerNS string
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

func NewReconciler(cl client.Client, controllerNamespace string) *Reconciler {
	return &Reconciler{
		cl:           cl,
		controllerNS: controllerNamespace,
	}
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

	if rsc == nil {
		usedStoragePoolNames, err := r.getUsedStoragePoolNames(rf.Ctx(), req.Name)
		if err != nil {
			return rf.Fail(err).ToCtrl()
		}
		return r.reconcileDeletion(rf.Ctx(), req.Name, nil, usedStoragePoolNames).ToCtrl()
	}

	// Get RVs referencing this RSC.
	rvs, err := r.getSortedRVsByRSC(rf.Ctx(), req.Name)
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}

	outcome := r.reconcileStorageClass(rf.Ctx(), rsc)
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
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

	// Take patch base before mutations.
	base := rsc.DeepCopy()

	eo := flow.MergeEnsures(
		// Clear legacy phase/reason fields set by the old controller.
		ensureLegacyFieldsCleared(rf.Ctx(), rsc),

		// Ensure storagePool name and condition are up to date.
		ensureStoragePool(rf.Ctx(), rsc, targetStoragePoolName, rsp),

		// Ensure configuration is up to date based on RSP state.
		ensureConfiguration(rf.Ctx(), rsc, rsp),

		// Ensure volume summary and conditions.
		ensureVolumeSummaryAndConditions(rf.Ctx(), rsc, rvs),
	)

	// Patch if changed.
	if eo.DidChange() {
		if err := r.patchRSCStatus(rf.Ctx(), rsc, base); err != nil {
			return rf.Fail(err).ToCtrl()
		}
	}

	// Release storage pools that are no longer used.
	return r.reconcileUnusedRSPs(rf.Ctx(), rsc, usedStoragePoolNames).ToCtrl()
}

// rscShouldBeDeleted returns true if the RSC is marked for deletion and has no RVs blocking it.
func rscShouldBeDeleted(rsc *v1alpha1.ReplicatedStorageClass, rvs []rvView) bool {
	return rsc == nil || (rsc.DeletionTimestamp != nil && len(rvs) == 0)
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: storageclass
//

// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileStorageClass(ctx context.Context, rsc *v1alpha1.ReplicatedStorageClass) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "storageclass")
	defer rf.OnEnd(&outcome)

	oldSC, err := r.getStorageClass(rf.Ctx(), rsc.Name)
	if err != nil {
		return rf.Fail(err)
	}

	if oldSC != nil && oldSC.Provisioner != v1alpha1.StorageClassProvisioner {
		return rf.Fail(fmt.Errorf("reconcile StorageClass with provisioner %s is not allowed", oldSC.Provisioner))
	}

	if rsc.DeletionTimestamp != nil {
		if oldSC == nil {
			return rf.Continue()
		}

		if len(oldSC.Finalizers) > 0 {
			if len(oldSC.Finalizers) != 1 {
				return rf.Fail(fmt.Errorf("deletion of StorageClass with multiple(%v) finalizers is not allowed", oldSC.Finalizers))
			}
			if oldSC.Finalizers[0] != v1alpha1.StorageClassFinalizerName {
				return rf.Fail(fmt.Errorf("deletion of StorageClass with finalizer %s is not allowed", oldSC.Finalizers[0]))
			}

			base := oldSC.DeepCopy()
			oldSC.Finalizers = nil
			if err := r.patchStorageClass(rf.Ctx(), oldSC, base); err != nil {
				return rf.Fail(err)
			}
		}

		if err := r.deleteStorageClass(rf.Ctx(), oldSC); err != nil {
			return rf.Fail(err)
		}
		return rf.Continue()
	}

	virtualizationEnabled := false
	if rsc.Spec.VolumeAccess == v1alpha1.VolumeAccessLocal {
		virtualizationEnabled, err = r.getVirtualizationModuleEnabled(rf.Ctx())
		if err != nil {
			return rf.Fail(err)
		}
	}

	newSC := computeIntendedStorageClass(rsc, virtualizationEnabled)

	if oldSC == nil {
		if err := r.createStorageClass(rf.Ctx(), newSC); err != nil {
			return rf.Fail(err)
		}
		return rf.Continue()
	}

	doUpdateStorageClass(newSC, oldSC)

	equal, _ := compareStorageClasses(newSC, oldSC)
	if !equal {
		canRecreate, msg := canRecreateStorageClass(newSC, oldSC)
		if !canRecreate {
			return rf.Fail(fmt.Errorf("the StorageClass cannot be recreated because its parameters are not equal: %s", msg))
		}

		if len(oldSC.Finalizers) > 0 {
			if len(oldSC.Finalizers) != 1 {
				return rf.Fail(fmt.Errorf("deletion of StorageClass with multiple(%v) finalizers is not allowed", oldSC.Finalizers))
			}
			if oldSC.Finalizers[0] != v1alpha1.StorageClassFinalizerName {
				return rf.Fail(fmt.Errorf("deletion of StorageClass with finalizer %s is not allowed", oldSC.Finalizers[0]))
			}

			base := oldSC.DeepCopy()
			oldSC.Finalizers = nil
			if err := r.patchStorageClass(rf.Ctx(), oldSC, base); err != nil {
				return rf.Fail(err)
			}
		}

		if err := r.deleteStorageClass(rf.Ctx(), oldSC); err != nil {
			return rf.Fail(err)
		}
		if err := r.createStorageClass(rf.Ctx(), newSC); err != nil {
			return rf.Fail(err)
		}

		return rf.Continue()
	}

	needsUpdate := !maps.Equal(oldSC.Labels, newSC.Labels) ||
		!maps.Equal(oldSC.Annotations, newSC.Annotations) ||
		!areSlicesEqualIgnoreOrder(newSC.Finalizers, oldSC.Finalizers)
	if needsUpdate {
		base := oldSC.DeepCopy()
		oldSC.Labels = maps.Clone(newSC.Labels)
		oldSC.Annotations = maps.Clone(newSC.Annotations)
		oldSC.Finalizers = slices.Clone(newSC.Finalizers)

		if err := r.patchStorageClass(rf.Ctx(), oldSC, base); err != nil {
			return rf.Fail(err)
		}
	}

	return rf.Continue()
}

func computeIntendedStorageClass(rsc *v1alpha1.ReplicatedStorageClass, virtualizationEnabled bool) *storagev1.StorageClass {
	allowVolumeExpansion := true
	reclaimPolicy := corev1.PersistentVolumeReclaimPolicy(rsc.Spec.ReclaimPolicy)

	params := map[string]string{
		v1alpha1.ReplicatedStorageClassParamNameKey: rsc.Name,
	}

	var volumeBindingMode storagev1.VolumeBindingMode
	switch rsc.Spec.VolumeAccess {
	case v1alpha1.VolumeAccessLocal:
		volumeBindingMode = storagev1.VolumeBindingWaitForFirstConsumer
	case v1alpha1.VolumeAccessEventuallyLocal:
		volumeBindingMode = storagev1.VolumeBindingWaitForFirstConsumer
	case v1alpha1.VolumeAccessPreferablyLocal:
		volumeBindingMode = storagev1.VolumeBindingWaitForFirstConsumer
	case v1alpha1.VolumeAccessAny:
		volumeBindingMode = storagev1.VolumeBindingImmediate
	}

	annotations := map[string]string{
		v1alpha1.RSCStorageClassVolumeSnapshotClassAnnotationKey: v1alpha1.RSCStorageClassVolumeSnapshotClassAnnotationValue,
	}

	if rsc.Spec.VolumeAccess == v1alpha1.VolumeAccessLocal && virtualizationEnabled {
		ignoreLocal, _ := strconv.ParseBool(rsc.Annotations[v1alpha1.StorageClassIgnoreLocalAnnotationKey])
		if !ignoreLocal {
			annotations[v1alpha1.StorageClassVirtualizationAnnotationKey] = v1alpha1.StorageClassVirtualizationAnnotationValue
		}
	}

	return &storagev1.StorageClass{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.StorageClassKind,
			APIVersion: v1alpha1.StorageClassAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        rsc.Name,
			Namespace:   rsc.Namespace,
			Finalizers:  []string{v1alpha1.StorageClassFinalizerName},
			Labels:      map[string]string{v1alpha1.ManagedLabelKey: v1alpha1.ManagedLabelValue},
			Annotations: annotations,
		},
		AllowVolumeExpansion: &allowVolumeExpansion,
		Parameters:           params,
		Provisioner:          v1alpha1.StorageClassProvisioner,
		ReclaimPolicy:        &reclaimPolicy,
		VolumeBindingMode:    &volumeBindingMode,
	}
}

func compareStorageClasses(newSC, oldSC *storagev1.StorageClass) (bool, string) {
	var b strings.Builder
	equal := true
	b.WriteString("Old StorageClass and New StorageClass are not equal: ")

	if !maps.Equal(oldSC.Parameters, newSC.Parameters) {
		equal = false
		b.WriteString(fmt.Sprintf("Parameters are not equal (ReplicatedStorageClass parameters: %+v, StorageClass parameters: %+v); ", newSC.Parameters, oldSC.Parameters))
	}
	if oldSC.Provisioner != newSC.Provisioner {
		equal = false
		b.WriteString(fmt.Sprintf("Provisioner are not equal (Old StorageClass: %s, New StorageClass: %s); ", oldSC.Provisioner, newSC.Provisioner))
	}
	if oldSC.ReclaimPolicy != nil && newSC.ReclaimPolicy != nil && *oldSC.ReclaimPolicy != *newSC.ReclaimPolicy {
		equal = false
		b.WriteString(fmt.Sprintf("ReclaimPolicy are not equal (Old StorageClass: %s, New StorageClass: %s", string(*oldSC.ReclaimPolicy), string(*newSC.ReclaimPolicy)))
	}
	if oldSC.VolumeBindingMode != nil && newSC.VolumeBindingMode != nil && *oldSC.VolumeBindingMode != *newSC.VolumeBindingMode {
		equal = false
		b.WriteString(fmt.Sprintf("VolumeBindingMode are not equal (Old StorageClass: %s, New StorageClass: %s); ", string(*oldSC.VolumeBindingMode), string(*newSC.VolumeBindingMode)))
	}

	return equal, b.String()
}

func canRecreateStorageClass(newSC, oldSC *storagev1.StorageClass) (bool, string) {
	newSCCopy := *newSC
	oldSCCopy := *oldSC

	newSCCopy.Parameters = nil
	oldSCCopy.Parameters = nil

	return compareStorageClasses(&newSCCopy, &oldSCCopy)
}

func areSlicesEqualIgnoreOrder(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]struct{}, len(a))
	for _, item := range a {
		set[item] = struct{}{}
	}
	for _, item := range b {
		if _, found := set[item]; !found {
			return false
		}
	}
	return true
}

func doUpdateStorageClass(newSC *storagev1.StorageClass, oldSC *storagev1.StorageClass) {
	if len(oldSC.Labels) > 0 {
		if newSC.Labels == nil {
			newSC.Labels = maps.Clone(oldSC.Labels)
		} else {
			updateMap(newSC.Labels, oldSC.Labels)
		}
	}

	copyAnnotations := maps.Clone(oldSC.Annotations)
	delete(copyAnnotations, v1alpha1.StorageClassVirtualizationAnnotationKey)

	if len(copyAnnotations) > 0 {
		if newSC.Annotations == nil {
			newSC.Annotations = copyAnnotations
		} else {
			updateMap(newSC.Annotations, copyAnnotations)
		}
	}

	if len(oldSC.Finalizers) > 0 {
		finalizersSet := make(map[string]struct{}, len(newSC.Finalizers))
		for _, f := range newSC.Finalizers {
			finalizersSet[f] = struct{}{}
		}
		for _, f := range oldSC.Finalizers {
			if _, exists := finalizersSet[f]; !exists {
				newSC.Finalizers = append(newSC.Finalizers, f)
				finalizersSet[f] = struct{}{}
			}
		}
	}
}

func updateMap(dst, src map[string]string) {
	for k, v := range src {
		if _, exists := dst[k]; !exists {
			dst[k] = v
		}
	}
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

// ensureLegacyFieldsCleared clears legacy status.phase and status.reason fields
// that were written by the old controller (sds-replicated-volume-controller).
func ensureLegacyFieldsCleared(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "legacy-fields-cleared")
	defer ef.OnEnd(&outcome)

	changed := false
	if rsc.Status.Phase != "" {
		rsc.Status.Phase = ""
		changed = true
	}
	if rsc.Status.Reason != "" {
		rsc.Status.Reason = ""
		changed = true
	}

	return ef.Ok().ReportChangedIf(changed)
}

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
				"Not implemented",
			) || changed
		} else {
			changed = applyVolumesSatisfyEligibleNodesCondFalse(rsc,
				v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesReasonManualConflictResolution,
				"Not implemented",
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
				"Not implemented",
			) || changed
		} else {
			changed = applyConfigurationRolledOutCondFalse(rsc,
				v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonConfigurationRolloutDisabled,
				"Not implemented",
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
		return fmt.Errorf("no nodes available in the storage pool")
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
				if isNodeSchedulable(n) {
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
				if isNodeSchedulable(n) {
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
				if isNodeSchedulable(n) {
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

// getStorageClass fetches a StorageClass by name. Returns (nil, nil) if not found.
func (r *Reconciler) getStorageClass(ctx context.Context, name string) (*storagev1.StorageClass, error) {
	var sc storagev1.StorageClass
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, &sc); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &sc, nil
}

// createStorageClass creates a StorageClass.
func (r *Reconciler) createStorageClass(ctx context.Context, sc *storagev1.StorageClass) error {
	return r.cl.Create(ctx, sc)
}

// patchStorageClass patches the StorageClass main resource.
func (r *Reconciler) patchStorageClass(ctx context.Context, sc *storagev1.StorageClass, base *storagev1.StorageClass) error {
	return r.cl.Patch(ctx, sc, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}

// deleteStorageClass deletes a StorageClass.
func (r *Reconciler) deleteStorageClass(ctx context.Context, sc *storagev1.StorageClass) error {
	if sc.DeletionTimestamp != nil {
		return nil
	}
	if err := client.IgnoreNotFound(r.cl.Delete(ctx, sc)); err != nil {
		return err
	}
	sc.DeletionTimestamp = ptr.To(metav1.Now())
	return nil
}

func (r *Reconciler) getVirtualizationModuleEnabled(ctx context.Context) (bool, error) {
	var cm corev1.ConfigMap
	if err := r.cl.Get(ctx, client.ObjectKey{Namespace: r.controllerNS, Name: v1alpha1.ControllerConfigMapName}, &cm); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	value, exists := cm.Data[v1alpha1.VirtualizationModuleEnabledKey]
	if !exists {
		return false, nil
	}
	return value == "true", nil
}
