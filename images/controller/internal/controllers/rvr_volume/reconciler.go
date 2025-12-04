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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-common-lib/utils"
	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

type Reconciler struct {
	cl  client.Client
	log logr.Logger
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

// NewReconciler is a small helper constructor that is primarily useful for tests.
func NewReconciler(cl client.Client, log logr.Logger) *Reconciler {
	return &Reconciler{
		cl:  cl,
		log: log,
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

	rvr := &v1alpha3.ReplicatedVolumeReplica{}
	err := r.cl.Get(ctx, req.NamespacedName, rvr)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("ReplicatedVolumeReplica not found, ignoring reconcile request")
			return reconcile.Result{}, nil
		}
		log.Error(err, "getting ReplicatedVolume")
		return reconcile.Result{}, err
	}

	if rvr.DeletionTimestamp != nil {
		return reconcile.Result{}, reconcileLLVDeletion(ctx, r.cl, log, rvr)
	}

	// rvr.spec.nodeName will be set once and will not change again.
	// "Diskful" will appear as a variable after merging rvr-diskfull-count-controller.
	if rvr.Spec.Type == "Diskful" && rvr.Spec.NodeName != "" {
		if rvr.Status == nil || rvr.Status.LVMLogicalVolumeName == "" {
			return reconcile.Result{}, reconcileLLVNormalByOwnerReference(ctx, r.cl, log, rvr)
		}
	}

	// RVR is not diskful, so we need to delete the LLV.
	if rvr.Status != nil && rvr.Status.ActualType == rvr.Spec.Type {
		return reconcile.Result{}, reconcileLLVDeletion(ctx, r.cl, log, rvr)
	}

	return reconcile.Result{}, nil
}

// reconcileLLVDeletion handles deletion of LVMLogicalVolume associated with the RVR.
// If LLV is not found, it clears the LVMLogicalVolumeName from RVR status.
// If LLV exists, it deletes it (or removes the finalizer if already marked for deletion).
func reconcileLLVDeletion(ctx context.Context, cl client.Client, log logr.Logger, rvr *v1alpha3.ReplicatedVolumeReplica) error {
	log = log.WithName("ReconcileLLVDeletion")

	if rvr.Status == nil || rvr.Status.LVMLogicalVolumeName == "" {
		log.V(4).Info("No LVMLogicalVolumeName in status, skipping delete")
		return nil
	}

	llvName := rvr.Status.LVMLogicalVolumeName
	llv, err := getLLVByName(ctx, cl, llvName)
	switch {
	case err != nil && apierrors.IsNotFound(err):
		log.V(4).Info("LVMLogicalVolume not found in cluster, clearing status", "llvName", llvName)
		if err := setLVMLogicalVolumeNameInStatus(ctx, cl, rvr, ""); err != nil {
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

// reconcileLLVNormalByOwnerReference reconciles LVMLogicalVolume for a normal (non-deleting) RVR
// by finding it via ownerReference. If not found, creates a new LLV. If found and created,
// updates RVR status with the LLV name.
func reconcileLLVNormalByOwnerReference(ctx context.Context, cl client.Client, log logr.Logger, rvr *v1alpha3.ReplicatedVolumeReplica) error {
	llv, err := getLLVByOwnerReference(ctx, cl, rvr.Name)
	switch {
	case err != nil && apierrors.IsNotFound(err):
		log.V(4).Info("No LVMLogicalVolume found with ownerReference, creating it", "rvrName", rvr.Name)
		if err := createLLV(ctx, cl, rvr, log); err != nil {
			return fmt.Errorf("creating llv: %w", err)
		}
	case err != nil:
		return fmt.Errorf("checking for llv with ownerReference: %w", err)
	case isLLVPhaseCreated(llv):
		log.V(4).Info("LVMLogicalVolume is already created", "llvName", llv.Name)
		// Update status with llv name if not set
		if rvr.Status != nil && rvr.Status.LVMLogicalVolumeName == llv.Name {
			return nil
		}
		if err := setLVMLogicalVolumeNameInStatus(ctx, cl, rvr, llv.Name); err != nil {
			return fmt.Errorf("updating LVMLogicalVolumeName in status: %w", err)
		}
	default:
		log.V(4).Info("LVMLogicalVolume is not yet created, waiting", "llvName", llv.Name, "phase", getLLVPhase(llv))
	}

	return nil
}

// getLLVByOwnerReference finds a LVMLogicalVolume in the cluster with ownerReference
// pointing to ReplicatedVolumeReplica with the specified name.
// Returns the llv object and nil error if found, or nil and a NotFound error if not found, or nil and an error on failure.
func getLLVByOwnerReference(ctx context.Context, cl client.Client, rvrName string) (*snc.LVMLogicalVolume, error) {
	var llvList snc.LVMLogicalVolumeList
	if err := cl.List(ctx, &llvList); err != nil {
		return nil, fmt.Errorf("listing LVMLogicalVolumes: %w", err)
	}

	for i := range llvList.Items {
		llv := &llvList.Items[i]
		for _, ownerRef := range llv.OwnerReferences {
			if ownerRef.Kind == "ReplicatedVolumeReplica" && ownerRef.Name == rvrName {
				return llv, nil
			}
		}
	}

	return nil, apierrors.NewNotFound(
		schema.GroupResource{Group: snc.SchemeGroupVersion.Group, Resource: "lvmlogicalvolumes"},
		rvrName,
	)
}

// getLLVByName gets a LVMLogicalVolume from the cluster by name.
// Returns the llv object and nil error if found, or nil and an error if not found or on failure.
// The error will be a NotFound error if the object doesn't exist.
func getLLVByName(ctx context.Context, cl client.Client, llvName string) (*snc.LVMLogicalVolume, error) {
	llv := &snc.LVMLogicalVolume{}
	if err := cl.Get(ctx, client.ObjectKey{Name: llvName}, llv); err != nil {
		return nil, fmt.Errorf("getting LVMLogicalVolume %s: %w", llvName, err)
	}
	return llv, nil
}

// setLVMLogicalVolumeNameInStatus sets or clears the LVMLogicalVolumeName field in RVR status.
// If llvName is empty string, the field is cleared. Otherwise, it is set to the provided value.
func setLVMLogicalVolumeNameInStatus(ctx context.Context, cl client.Client, rvr *v1alpha3.ReplicatedVolumeReplica, llvName string) error {
	patch := client.MergeFrom(rvr.DeepCopy())
	if rvr.Status == nil {
		rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{}
	}
	rvr.Status.LVMLogicalVolumeName = llvName
	return cl.Status().Patch(ctx, rvr, patch)
}

// createLLV creates a LVMLogicalVolume with ownerReference pointing to RVR.
// It retrieves the ReplicatedVolume and determines the appropriate LVMVolumeGroup and ThinPool
// based on the RVR's node name, then creates the LLV with the correct configuration.
func createLLV(ctx context.Context, cl client.Client, rvr *v1alpha3.ReplicatedVolumeReplica, log logr.Logger) error {
	generateLLVName := fmt.Sprintf("%s-", rvr.Spec.ReplicatedVolumeName)

	log = log.WithValues("generateLLVName", generateLLVName, "nodeName", rvr.Spec.NodeName)
	log.Info("Creating LVMLogicalVolume")

	// Set ownerReference manually
	gvk := schema.GroupVersionKind{
		Group:   v1alpha3.SchemeGroupVersion.Group,
		Version: v1alpha3.SchemeGroupVersion.Version,
		Kind:    "ReplicatedVolumeReplica",
	}
	ownerRef := metav1.OwnerReference{
		APIVersion:         gvk.GroupVersion().String(),
		Kind:               gvk.Kind,
		Name:               rvr.Name,
		UID:                rvr.UID,
		Controller:         utils.Ptr(true),
		BlockOwnerDeletion: utils.Ptr(true),
	}

	rv, err := getReplicatedVolumeByName(ctx, cl, rvr.Spec.ReplicatedVolumeName)
	if err != nil {
		return fmt.Errorf("getting ReplicatedVolume: %w", err)
	}
	if rv == nil {
		return fmt.Errorf("ReplicatedVolume not found")
	}

	lvmVolumeGroupName, thinPoolName, err := getLVMVolumeGroupNameAndThinPoolName(ctx, cl, rv.Spec.ReplicatedStorageClassName, rvr.Spec.NodeName)
	if err != nil {
		return fmt.Errorf("getting LVMVolumeGroupName and ThinPoolName: %w", err)
	}

	llvNew := &snc.LVMLogicalVolume{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName:    fmt.Sprintf("%s-", rvr.Name),
			Finalizers:      []string{finalizerName},
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Spec: snc.LVMLogicalVolumeSpec{
			ActualLVNameOnTheNode: rvr.Spec.ReplicatedVolumeName,
			LVMVolumeGroupName:    lvmVolumeGroupName,
			Size:                  rv.Spec.Size.String(),
		},
	}
	if thinPoolName == "" {
		llvNew.Spec.Type = "Thick"
	} else {
		llvNew.Spec.Type = "Thin"
		llvNew.Spec.Thin = &snc.LVMLogicalVolumeThinSpec{
			PoolName: thinPoolName,
		}
	}

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

// getLLVPhase returns the phase of LLV or empty string if status is nil.
func getLLVPhase(llv *snc.LVMLogicalVolume) string {
	if llv.Status == nil {
		return ""
	}
	return llv.Status.Phase
}

// deleteLLV deletes a LVMLogicalVolume from the cluster.
// If the object is already marked for deletion (has DeletionTimestamp), it removes only our finalizer.
func deleteLLV(ctx context.Context, cl client.Client, llv *snc.LVMLogicalVolume, log logr.Logger) error {
	if llv.DeletionTimestamp == nil {
		if err := cl.Delete(ctx, llv); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting LVMLogicalVolume %s: %w", llv.Name, err)
		}
		log.Info("LVMLogicalVolume marked for deletion", "llvName", llv.Name)
	}

	// Remove only our finalizer, leaving others intact
	hasOurFinalizer := false
	newFinalizers := make([]string, 0, len(llv.Finalizers))
	for _, finalizer := range llv.Finalizers {
		if finalizer == finalizerName {
			hasOurFinalizer = true
		} else {
			newFinalizers = append(newFinalizers, finalizer)
		}
	}

	if hasOurFinalizer {
		log.V(4).Info("LVMLogicalVolume is marked for deletion, removing our finalizer", "llvName", llv.Name)
		patch := client.MergeFrom(llv.DeepCopy())
		llv.Finalizers = newFinalizers
		if err := cl.Patch(ctx, llv, patch); err != nil {
			return fmt.Errorf("removing finalizer from LVMLogicalVolume %s: %w", llv.Name, err)
		}
		log.Info("Finalizer removed successfully", "llvName", llv.Name)
	} else {
		log.V(4).Info("LVMLogicalVolume is marked for deletion, but our finalizer is not present", "llvName", llv.Name)
	}

	return nil
}

// getReplicatedVolumeByName gets a ReplicatedVolume from the cluster by name.
// Returns the ReplicatedVolume object and nil error if found, or nil and nil if not found (NotFound is handled and returns nil, nil),
// or nil and an error on failure.
func getReplicatedVolumeByName(ctx context.Context, cl client.Client, rvName string) (*v1alpha3.ReplicatedVolume, error) {
	rv := &v1alpha3.ReplicatedVolume{}
	key := client.ObjectKey{Name: rvName}
	if err := cl.Get(ctx, key, rv); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("getting ReplicatedVolume %s: %w", rvName, err)
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
	key := client.ObjectKey{Name: rscName}
	if err := cl.Get(ctx, key, rsc); err != nil {
		return "", "", err
	}

	// Get StoragePool name from ReplicatedStorageClass
	storagePoolName := rsc.Spec.StoragePool
	if storagePoolName == "" {
		return "", "", fmt.Errorf("ReplicatedStorageClass %s has empty StoragePool", rscName)
	}

	// Get ReplicatedStoragePool
	rsp := &v1alpha1.ReplicatedStoragePool{}
	key = client.ObjectKey{Name: storagePoolName}
	if err := cl.Get(ctx, key, rsp); err != nil {
		return "", "", fmt.Errorf("getting ReplicatedStoragePool %s: %w", storagePoolName, err)
	}

	// Find LVMVolumeGroup that matches the node
	for _, rspLVG := range rsp.Spec.LVMVolumeGroups {
		// Get LVMVolumeGroup resource to check its node
		lvg := &snc.LVMVolumeGroup{}
		key = client.ObjectKey{Name: rspLVG.Name}
		if err := cl.Get(ctx, key, lvg); err != nil {
			return "", "", fmt.Errorf("getting LVMVolumeGroup %s: %w", rspLVG.Name, err)
		}

		// Check if this LVMVolumeGroup is on the specified node
		if strings.EqualFold(lvg.Spec.Local.NodeName, nodeName) {
			return rspLVG.Name, rspLVG.ThinPoolName, nil
		}
	}

	return "", "", fmt.Errorf("no LVMVolumeGroup found in ReplicatedStoragePool %s for node %s", storagePoolName, nodeName)
}
