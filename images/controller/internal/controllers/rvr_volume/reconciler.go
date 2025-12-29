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

package rvrvolume

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// TODO: Update sds-node-configurator to export this contants and reuse here
const (
	llvTypeThick = "Thick"
	llvTypeThin  = "Thin"
)

type Reconciler struct {
	cl     client.Client
	log    logr.Logger
	scheme *runtime.Scheme
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

// NewReconciler is a small helper constructor that is primarily useful for tests.
func NewReconciler(cl client.Client, log logr.Logger, scheme *runtime.Scheme) *Reconciler {
	return &Reconciler{
		cl:     cl,
		log:    log,
		scheme: scheme,
	}
}

// Reconcile reconciles a ReplicatedVolumeReplica by managing its associated LVMLogicalVolume.
// It handles creation, deletion, and status updates of LVMLogicalVolumes based on the RVR state.
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := r.log.WithName("Reconcile").WithValues("req", req)
	log.Info("Reconciling started")
	start := time.Now()
	defer func() {
		log.Info("Reconcile finished", "duration", time.Since(start).String())
	}()

	rvr := &v1alpha1.ReplicatedVolumeReplica{}
	err := r.cl.Get(ctx, req.NamespacedName, rvr)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("ReplicatedVolumeReplica not found, ignoring reconcile request")
			return reconcile.Result{}, nil
		}
		log.Error(err, "getting ReplicatedVolumeReplica")
		return reconcile.Result{}, err
	}

	if !rvr.DeletionTimestamp.IsZero() {
		return reconcile.Result{}, wrapReconcileLLVDeletion(ctx, r.cl, log, rvr)
	}

	// rvr.spec.nodeName will be set once and will not change again.
	if rvr.Spec.Type == v1alpha1.ReplicaTypeDiskful && rvr.Spec.NodeName != "" {
		return reconcile.Result{}, wrapReconcileLLVNormal(ctx, r.cl, r.scheme, log, rvr)
	}

	// RVR is not diskful, so we need to delete the LLV if it exists and the actual type is the same as the spec type.
	if rvr.Spec.Type != v1alpha1.ReplicaTypeDiskful && rvr.Status != nil && rvr.Status.ActualType == rvr.Spec.Type {
		return reconcile.Result{}, wrapReconcileLLVDeletion(ctx, r.cl, log, rvr)
	}

	return reconcile.Result{}, nil
}

// wrapReconcileLLVDeletion wraps reconcileLLVDeletion and updates the BackingVolumeCreated condition.
func wrapReconcileLLVDeletion(ctx context.Context, cl client.Client, log logr.Logger, rvr *v1alpha1.ReplicatedVolumeReplica) error {
	if err := reconcileLLVDeletion(ctx, cl, log, rvr); err != nil {
		reconcileErr := err
		// TODO: Can record the reconcile error in the message to the condition
		if conditionErr := updateBackingVolumeCreatedCondition(ctx, cl, log, rvr, metav1.ConditionTrue, v1alpha1.ReasonBackingVolumeDeletionFailed, "Backing volume deletion failed: "+reconcileErr.Error()); conditionErr != nil {
			return fmt.Errorf("updating BackingVolumeCreated condition: %w; reconcile error: %w", conditionErr, reconcileErr)
		}
		return reconcileErr
	}

	if err := updateBackingVolumeCreatedCondition(ctx, cl, log, rvr, metav1.ConditionFalse, v1alpha1.ReasonNotApplicable, "Replica is not diskful"); err != nil {
		return fmt.Errorf("updating BackingVolumeCreated condition: %w", err)
	}

	return nil
}

// reconcileLLVDeletion handles deletion of LVMLogicalVolume associated with the RVR.
// If LLV is not found, it clears the LVMLogicalVolumeName from RVR status.
// If LLV exists, it deletes it and clears the LVMLogicalVolumeName from RVR status when LLV is actually deleted.
func reconcileLLVDeletion(ctx context.Context, cl client.Client, log logr.Logger, rvr *v1alpha1.ReplicatedVolumeReplica) error {
	log = log.WithName("ReconcileLLVDeletion")

	if rvr.Status == nil || rvr.Status.LVMLogicalVolumeName == "" {
		log.V(4).Info("No LVMLogicalVolumeName in status, skipping deletion")
		return nil
	}

	llvName := rvr.Status.LVMLogicalVolumeName
	llv, err := getLLVByName(ctx, cl, llvName)
	switch {
	case err != nil && apierrors.IsNotFound(err):
		log.V(4).Info("LVMLogicalVolume not found in cluster, clearing status", "llvName", llvName)
		if err := ensureLVMLogicalVolumeNameInStatus(ctx, cl, rvr, ""); err != nil {
			return fmt.Errorf("clearing LVMLogicalVolumeName from status: %w", err)
		}
	case err != nil:
		return fmt.Errorf("checking if llv exists: %w", err)
	default:
		log.V(4).Info("LVMLogicalVolume found in cluster, deleting it", "llvName", llvName)
		if err := deleteLLV(ctx, cl, llv, log); err != nil {
			return fmt.Errorf("deleting llv: %w", err)
		}
	}

	return nil
}

// wrapReconcileLLVNormal wraps reconcileLLVNormal and updates the BackingVolumeCreated condition.
func wrapReconcileLLVNormal(ctx context.Context, cl client.Client, scheme *runtime.Scheme, log logr.Logger, rvr *v1alpha1.ReplicatedVolumeReplica) error {
	if err := reconcileLLVNormal(ctx, cl, scheme, log, rvr); err != nil {
		reconcileErr := err
		// TODO: Can record the reconcile error in the message to the condition
		if conditionErr := updateBackingVolumeCreatedCondition(ctx, cl, log, rvr, metav1.ConditionFalse, v1alpha1.ReasonBackingVolumeCreationFailed, "Backing volume creation failed: "+reconcileErr.Error()); conditionErr != nil {
			return fmt.Errorf("updating BackingVolumeCreated condition: %w; reconcile error: %w", conditionErr, reconcileErr)
		}
		return reconcileErr
	}
	return nil
}

// reconcileLLVNormal reconciles LVMLogicalVolume for a normal (non-deleting) RVR
// by finding it via ownerReference. If not found, creates a new LLV. If found and created,
// updates RVR status with the LLV name.
func reconcileLLVNormal(ctx context.Context, cl client.Client, scheme *runtime.Scheme, log logr.Logger, rvr *v1alpha1.ReplicatedVolumeReplica) error {
	log = log.WithName("ReconcileLLVNormal")

	llv, err := getLLVByRVR(ctx, cl, rvr)

	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("getting LVMLogicalVolume by name %s: %w", rvr.Name, err)
	}

	if llv == nil {
		log.V(4).Info("LVMLogicalVolume not found, creating it", "rvrName", rvr.Name)
		if err := createLLV(ctx, cl, scheme, rvr, log); err != nil {
			return fmt.Errorf("creating LVMLogicalVolume: %w", err)
		}

		if err := updateBackingVolumeCreatedCondition(ctx, cl, log, rvr, metav1.ConditionFalse, v1alpha1.ReasonBackingVolumeNotReady, "Backing volume is not ready"); err != nil {
			return fmt.Errorf("updating BackingVolumeCreated condition: %w", err)
		}

		// Finish reconciliation by returning nil. When LLV becomes ready we get another reconcile event.
		return nil
	}

	log.Info("LVMLogicalVolume found, checking if it is ready", "llvName", llv.Name)
	if !isLLVPhaseCreated(llv) {
		if err := updateBackingVolumeCreatedCondition(ctx, cl, log, rvr, metav1.ConditionFalse, v1alpha1.ReasonBackingVolumeNotReady, "Backing volume is not ready"); err != nil {
			return fmt.Errorf("updating BackingVolumeCreated condition: %w", err)
		}
		log.Info("LVMLogicalVolume is not ready, returning nil to wait for next reconcile event", "llvName", llv.Name)
		return nil
	}

	log.Info("LVMLogicalVolume is ready, updating status", "llvName", llv.Name)
	if err := ensureLVMLogicalVolumeNameInStatus(ctx, cl, rvr, llv.Name); err != nil {
		return fmt.Errorf("updating LVMLogicalVolumeName in status: %w", err)
	}

	if err := updateBackingVolumeCreatedCondition(ctx, cl, log, rvr, metav1.ConditionTrue, v1alpha1.ReasonBackingVolumeReady, "Backing volume is ready"); err != nil {
		return fmt.Errorf("updating BackingVolumeCreated condition: %w", err)
	}

	return nil
}

// getLLV gets a LVMLogicalVolume from the cluster by name.
// Returns the llv object and nil error if found, or nil and an error if not found or on failure.
// The error will be a NotFound error if the object doesn't exist.
func getLLVByName(ctx context.Context, cl client.Client, llvName string) (*snc.LVMLogicalVolume, error) {
	llv := &snc.LVMLogicalVolume{}
	if err := cl.Get(ctx, client.ObjectKey{Name: llvName}, llv); err != nil {
		return nil, fmt.Errorf("getting LVMLogicalVolume %s: %w", llvName, err)
	}
	return llv, nil
}

func getLLVByRVR(ctx context.Context, cl client.Client, rvr *v1alpha1.ReplicatedVolumeReplica) (*snc.LVMLogicalVolume, error) {
	llvName := rvr.Name
	if rvr.Status != nil && rvr.Status.LVMLogicalVolumeName != "" {
		llvName = rvr.Status.LVMLogicalVolumeName
	}

	return getLLVByName(ctx, cl, llvName)
}

// ensureLVMLogicalVolumeNameInStatus sets or clears the LVMLogicalVolumeName field in RVR status if needed.
// If llvName is empty string, the field is cleared. Otherwise, it is set to the provided value.
func ensureLVMLogicalVolumeNameInStatus(ctx context.Context, cl client.Client, rvr *v1alpha1.ReplicatedVolumeReplica, llvName string) error {
	if rvr.Status != nil && rvr.Status.LVMLogicalVolumeName == llvName {
		return nil
	}
	patch := client.MergeFrom(rvr.DeepCopy())
	if rvr.Status == nil {
		rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{}
	}
	rvr.Status.LVMLogicalVolumeName = llvName
	return cl.Status().Patch(ctx, rvr, patch)
}

// createLLV creates a LVMLogicalVolume with ownerReference pointing to RVR.
// It retrieves the ReplicatedVolume and determines the appropriate LVMVolumeGroup and ThinPool
// based on the RVR's node name, then creates the LLV with the correct configuration.
func createLLV(ctx context.Context, cl client.Client, scheme *runtime.Scheme, rvr *v1alpha1.ReplicatedVolumeReplica, log logr.Logger) error {
	log = log.WithValues("llvName", rvr.Name, "nodeName", rvr.Spec.NodeName)
	log.Info("Creating LVMLogicalVolume")

	rv, err := getReplicatedVolumeByName(ctx, cl, rvr.Spec.ReplicatedVolumeName)
	if err != nil {
		return fmt.Errorf("getting ReplicatedVolume: %w", err)
	}

	lvmVolumeGroupName, thinPoolName, err := getLVMVolumeGroupNameAndThinPoolName(ctx, cl, rv.Spec.ReplicatedStorageClassName, rvr.Spec.NodeName)
	if err != nil {
		return fmt.Errorf("getting LVMVolumeGroupName and ThinPoolName: %w", err)
	}

	llvNew := &snc.LVMLogicalVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: rvr.Name,
		},
		Spec: snc.LVMLogicalVolumeSpec{
			ActualLVNameOnTheNode: rvr.Name,
			LVMVolumeGroupName:    lvmVolumeGroupName,
			Size:                  rv.Spec.Size.String(),
		},
	}
	if thinPoolName == "" {
		llvNew.Spec.Type = llvTypeThick
	} else {
		llvNew.Spec.Type = llvTypeThin
		llvNew.Spec.Thin = &snc.LVMLogicalVolumeThinSpec{
			PoolName: thinPoolName,
		}
	}

	if err := controllerutil.SetControllerReference(rvr, llvNew, scheme); err != nil {
		return fmt.Errorf("setting controller reference: %w", err)
	}

	// TODO: Define in our spec how to handle IsAlreadyExists here (LLV with this name already exists)
	if err := cl.Create(ctx, llvNew); err != nil {
		return fmt.Errorf("creating LVMLogicalVolume: %w", err)
	}

	log.Info("LVMLogicalVolume created successfully", "llvName", llvNew.Name)
	return nil
}

// isLLVPhaseCreated checks if LLV status phase is "Created".
func isLLVPhaseCreated(llv *snc.LVMLogicalVolume) bool {
	return llv.Status != nil && llv.Status.Phase == "Created"
}

// deleteLLV deletes a LVMLogicalVolume from the cluster.
func deleteLLV(ctx context.Context, cl client.Client, llv *snc.LVMLogicalVolume, log logr.Logger) error {
	if llv.DeletionTimestamp != nil {
		return nil
	}
	if err := cl.Delete(ctx, llv); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting LVMLogicalVolume %s: %w", llv.Name, err)
	}
	log.Info("LVMLogicalVolume marked for deletion", "llvName", llv.Name)
	return nil
}

// getReplicatedVolumeByName gets a ReplicatedVolume from the cluster by name.
// Returns the ReplicatedVolume object and nil error if found, or nil and an error if not found or on failure.
func getReplicatedVolumeByName(ctx context.Context, cl client.Client, rvName string) (*v1alpha1.ReplicatedVolume, error) {
	rv := &v1alpha1.ReplicatedVolume{}
	if err := cl.Get(ctx, client.ObjectKey{Name: rvName}, rv); err != nil {
		return nil, err
	}
	return rv, nil
}

// getLVMVolumeGroupNameAndThinPoolName gets LVMVolumeGroupName and ThinPoolName from ReplicatedStorageClass.
// It retrieves the ReplicatedStorageClass, then the ReplicatedStoragePool, and finds the LVMVolumeGroup
// that matches the specified node name.
// Returns the LVMVolumeGroup name, ThinPool name (empty string for Thick volumes), and an error.
func getLVMVolumeGroupNameAndThinPoolName(ctx context.Context, cl client.Client, rscName, nodeName string) (string, string, error) {
	// Get ReplicatedStorageClass
	rsc := &v1alpha1.ReplicatedStorageClass{}
	if err := cl.Get(ctx, client.ObjectKey{Name: rscName}, rsc); err != nil {
		return "", "", err
	}

	// Get StoragePool name from ReplicatedStorageClass
	storagePoolName := rsc.Spec.StoragePool
	if storagePoolName == "" {
		return "", "", fmt.Errorf("ReplicatedStorageClass %s has empty StoragePool", rscName)
	}

	// Get ReplicatedStoragePool
	rsp := &v1alpha1.ReplicatedStoragePool{}
	if err := cl.Get(ctx, client.ObjectKey{Name: storagePoolName}, rsp); err != nil {
		return "", "", fmt.Errorf("getting ReplicatedStoragePool %s: %w", storagePoolName, err)
	}

	// Find LVMVolumeGroup that matches the node
	for _, rspLVG := range rsp.Spec.LVMVolumeGroups {
		// Get LVMVolumeGroup resource to check its node
		lvg := &snc.LVMVolumeGroup{}
		if err := cl.Get(ctx, client.ObjectKey{Name: rspLVG.Name}, lvg); err != nil {
			return "", "", fmt.Errorf("getting LVMVolumeGroup %s: %w", rspLVG.Name, err)
		}

		// Check if this LVMVolumeGroup is on the specified node
		if strings.EqualFold(lvg.Spec.Local.NodeName, nodeName) {
			return rspLVG.Name, rspLVG.ThinPoolName, nil
		}
	}

	return "", "", fmt.Errorf("no LVMVolumeGroup found in ReplicatedStoragePool %s for node %s", storagePoolName, nodeName)
}

// updateBackingVolumeCreatedCondition updates the BackingVolumeCreated condition on the RVR status
// with the provided status, reason, and message. It checks if the condition already has the same
// parameters before updating to avoid unnecessary status patches.
// Returns error if the patch failed, nil otherwise.
func updateBackingVolumeCreatedCondition(
	ctx context.Context,
	cl client.Client,
	log logr.Logger,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	conditionStatus metav1.ConditionStatus,
	reason,
	message string,
) error {
	// Initialize status if needed
	if rvr.Status == nil {
		rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{}
	}

	// Check if condition is already set correctly
	if rvr.Status.Conditions != nil {
		cond := meta.FindStatusCondition(rvr.Status.Conditions, v1alpha1.ConditionTypeBackingVolumeCreated)
		if cond != nil &&
			cond.Status == conditionStatus &&
			cond.Reason == reason &&
			cond.Message == message {
			// Already set correctly, no need to update
			return nil
		}
	}

	log.V(4).Info("Updating BackingVolumeCreated condition", "status", conditionStatus, "reason", reason, "message", message)

	// Create patch before making changes
	patch := client.MergeFrom(rvr.DeepCopy())

	// Apply changes
	meta.SetStatusCondition(
		&rvr.Status.Conditions,
		metav1.Condition{
			Type:    v1alpha1.ConditionTypeBackingVolumeCreated,
			Status:  conditionStatus,
			Reason:  reason,
			Message: message,
		},
	)

	// Patch the status in Kubernetes
	return cl.Status().Patch(ctx, rvr, patch)
}
