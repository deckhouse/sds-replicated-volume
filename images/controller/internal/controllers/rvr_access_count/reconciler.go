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

package rvraccesscount

import (
	"context"
	"errors"
	"slices"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

type Reconciler struct {
	cl     client.Client
	log    logr.Logger
	scheme *runtime.Scheme
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

// NewReconciler creates a new Reconciler instance.
// This is primarily used for testing, as fields are private.
func NewReconciler(cl client.Client, log logr.Logger, scheme *runtime.Scheme) *Reconciler {
	return &Reconciler{
		cl:     cl,
		log:    log,
		scheme: scheme,
	}
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := r.log.WithName("Reconcile").WithValues("req", req)
	log.Info("Reconciling")

	// Get ReplicatedVolume
	rv := &v1alpha3.ReplicatedVolume{}
	if err := r.cl.Get(ctx, req.NamespacedName, rv); err != nil {
		log.Error(err, "Getting ReplicatedVolume")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// Skip if RV is being deleted - Kubernetes GC will delete RVRs via ownerReference
	if rv.DeletionTimestamp != nil {
		log.Info("ReplicatedVolume is being deleted, skipping")
		return reconcile.Result{}, nil
	}

	// Get ReplicatedStorageClass to check volumeAccess
	rscName := rv.Spec.ReplicatedStorageClassName
	if rscName == "" {
		log.Info("ReplicatedStorageClassName is empty, skipping")
		return reconcile.Result{}, nil
	}

	rsc := &v1alpha1.ReplicatedStorageClass{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rscName}, rsc); err != nil {
		log.Error(err, "Getting ReplicatedStorageClass", "name", rscName)
		return reconcile.Result{}, err
	}

	// Skip if volumeAccess is Local - Access replicas are not needed for Local mode
	if rsc.Spec.VolumeAccess == v1alpha1.VolumeAccessLocal {
		log.V(1).Info("VolumeAccess is Local, Access replicas not needed")
		return reconcile.Result{}, nil
	}

	// Get all RVRs for this RV
	rvrList := &v1alpha3.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, rvrList); err != nil {
		log.Error(err, "Listing ReplicatedVolumeReplicas")
		return reconcile.Result{}, err
	}

	// Filter RVRs by replicatedVolumeName
	rvrList.Items = slices.DeleteFunc(rvrList.Items, func(item v1alpha3.ReplicatedVolumeReplica) bool {
		return item.Spec.ReplicatedVolumeName != rv.Name
	})

	// Build maps of nodes with replicas.
	// We need to know:
	// - Which nodes have "data presence" (Diskful/TieBreaker) - Access not needed there
	// - Which nodes have Access RVRs - to track what exists for deletion logic
	nodesWithDiskfulOrTieBreaker := make(map[string]struct{})
	nodesWithAccess := make(map[string]*v1alpha3.ReplicatedVolumeReplica)

	// ErrUnknownRVRType is logged when an unknown RVR type is encountered.
	var ErrUnknownRVRType = errors.New("unknown RVR type")

	for i := range rvrList.Items {
		rvr := &rvrList.Items[i]
		nodeName := rvr.Spec.NodeName
		if nodeName == "" {
			// RVR is waiting for scheduling by rvr-scheduling-controller
			log.V(2).Info("RVR has no nodeName, skipping (waiting for scheduling)", "rvr", rvr.Name)
			continue
		}

		switch rvr.Spec.Type {
		case v1alpha3.ReplicaTypeDiskful, v1alpha3.ReplicaTypeTieBreaker:
			// Both Diskful and TieBreaker mean node has "presence" in DRBD cluster.
			// Pod can access data from these nodes directly - no Access RVR needed.
			nodesWithDiskfulOrTieBreaker[nodeName] = struct{}{}
		case v1alpha3.ReplicaTypeAccess:
			// Only track non-deleting Access RVRs to avoid recreating during deletion
			if rvr.DeletionTimestamp == nil {
				nodesWithAccess[nodeName] = rvr
			}
		default:
			log.Error(ErrUnknownRVRType, "Skipping", "rvr", rvr.Name, "type", rvr.Spec.Type)
		}
	}

	// CREATE logic:
	// We need Access RVR on a node if:
	// 1. Node is in publishOn (pod wants to run there)
	// 2. Node has NO Diskful/TieBreaker (can't access data locally)
	// 3. Node has NO Access RVR yet (avoid duplicates)
	nodesNeedingAccess := make([]string, 0)
	for _, nodeName := range rv.Spec.PublishOn {
		_, hasDiskfulOrTieBreaker := nodesWithDiskfulOrTieBreaker[nodeName]
		_, hasAccess := nodesWithAccess[nodeName]

		if !hasDiskfulOrTieBreaker && !hasAccess {
			nodesNeedingAccess = append(nodesNeedingAccess, nodeName)
		}
	}

	// DELETE logic:
	// We should delete Access RVR if node is NOT needed anymore.
	// Node is "needed" if it's in publishOn OR publishedOn:
	// - publishOn = where pod WANTS to run (user intent via CSI)
	// - publishedOn = where pod IS running (current reality)
	// We keep Access if either is true to avoid disrupting running pods.
	publishOnSet := make(map[string]struct{})
	for _, nodeName := range rv.Spec.PublishOn {
		publishOnSet[nodeName] = struct{}{}
	}

	publishedOnSet := make(map[string]struct{})
	if rv.Status != nil {
		for _, nodeName := range rv.Status.PublishedOn {
			publishedOnSet[nodeName] = struct{}{}
		}
	}

	// Find Access RVRs to delete: exists but not in publishOn AND not in publishedOn
	accessRVRsToDelete := make([]*v1alpha3.ReplicatedVolumeReplica, 0)
	for nodeName, rvr := range nodesWithAccess {
		_, inPublishOn := publishOnSet[nodeName]
		_, inPublishedOn := publishedOnSet[nodeName]

		if !inPublishOn && !inPublishedOn {
			accessRVRsToDelete = append(accessRVRsToDelete, rvr)
		}
	}

	// Create Access RVRs for nodes that need them
	for _, nodeName := range nodesNeedingAccess {
		if err := r.createAccessRVR(ctx, rv, nodeName, log); err != nil {
			return reconcile.Result{}, err
		}
	}

	// Delete Access RVRs that are no longer needed
	for _, rvr := range accessRVRsToDelete {
		if err := r.deleteAccessRVR(ctx, rvr, log); err != nil {
			return reconcile.Result{}, err
		}
	}

	log.Info("Reconcile completed", "created", len(nodesNeedingAccess), "deleted", len(accessRVRsToDelete))
	return reconcile.Result{}, nil
}

func (r *Reconciler) createAccessRVR(ctx context.Context, rv *v1alpha3.ReplicatedVolume, nodeName string, log logr.Logger) error {
	rvr := &v1alpha3.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{
			// GenerateName: Kubernetes will append unique suffix, e.g. "pvc-xxx-" -> "pvc-xxx-abc12"
			GenerateName: rv.Name + "-",
		},
		Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: rv.Name,
			NodeName:             nodeName,
			Type:                 v1alpha3.ReplicaTypeAccess,
		},
	}

	if err := controllerutil.SetControllerReference(rv, rvr, r.scheme); err != nil {
		log.Error(err, "Setting controller reference", "nodeName", nodeName)
		return err
	}

	if err := r.cl.Create(ctx, rvr); err != nil {
		log.Error(err, "Creating Access RVR", "nodeName", nodeName)
		return err
	}

	log.Info("Created Access RVR", "rvr", rvr.Name, "nodeName", nodeName)
	return nil
}

func (r *Reconciler) deleteAccessRVR(ctx context.Context, rvr *v1alpha3.ReplicatedVolumeReplica, log logr.Logger) error {
	if err := r.cl.Delete(ctx, rvr); err != nil {
		log.Error(err, "Deleting Access RVR", "rvr", rvr.Name, "nodeName", rvr.Spec.NodeName)
		return client.IgnoreNotFound(err)
	}

	log.Info("Deleted Access RVR", "rvr", rvr.Name, "nodeName", rvr.Spec.NodeName)
	return nil
}
