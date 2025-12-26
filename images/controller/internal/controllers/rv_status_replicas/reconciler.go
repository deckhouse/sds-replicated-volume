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

package rvstatusreplicas

import (
	"context"
	"fmt"
	"log/slog"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

type Reconciler struct {
	cl  client.Client
	log *slog.Logger
}

var _ reconcile.Reconciler = &Reconciler{}

func NewReconciler(cl client.Client, log *slog.Logger) *Reconciler {
	if log == nil {
		log = slog.Default()
	}
	return &Reconciler{
		cl:  cl,
		log: log,
	}
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	rv := &v1alpha1.ReplicatedVolume{}
	if err := r.cl.Get(ctx, req.NamespacedName, rv); err != nil {
		if client.IgnoreNotFound(err) == nil {
			r.log.Info("ReplicatedVolume not found, probably deleted")
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("getting rv: %w", err)
	}

	log := r.log.With("rvName", rv.Name)

	// Get all RVRs for this RV
	rvrList := &v1alpha1.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, rvrList, client.MatchingFields{IndexRVRByReplicatedVolumeName: rv.Name}); err != nil {
		return reconcile.Result{}, fmt.Errorf("listing rvrs by rv name %s: %w", rv.Name, err)
	}

	// Build replicas status from RVRs
	newReplicas := r.buildReplicasStatus(rvrList.Items)

	// Patch status with optimistic concurrency check
	// Create patch before modifying the object
	patch := client.MergeFromWithOptions(rv.DeepCopy(), client.MergeFromWithOptimisticLock{})

	if rv.Status == nil {
		rv.Status = &v1alpha1.ReplicatedVolumeStatus{}
	}
	rv.Status.Replicas = newReplicas

	if err := r.cl.Status().Patch(ctx, rv, patch); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.Info("ReplicatedVolume was deleted during reconciliation, skipping patch")
			return reconcile.Result{}, nil
		}
		// Conflict error means optimistic lock failed - return error to trigger retry
		if apierrors.IsConflict(err) {
			log.Warn("conflict updating rv status replicas, will retry")
			return reconcile.Result{}, fmt.Errorf("conflict patching rv status replicas: %w", err)
		}
		return reconcile.Result{}, fmt.Errorf("patching rv status replicas: %w", err)
	}

	log.Debug("successfully patched rv status replicas", "count", len(newReplicas))
	return reconcile.Result{}, nil
}

// buildReplicasStatus builds the replicas status from RVRs
func (r *Reconciler) buildReplicasStatus(rvrs []v1alpha1.ReplicatedVolumeReplica) []v1alpha1.RVReplicaInfo {
	if len(rvrs) == 0 {
		return nil
	}

	replicas := make([]v1alpha1.RVReplicaInfo, 0, len(rvrs))
	for i := range rvrs {
		replicas = append(replicas, v1alpha1.RVReplicaInfo{
			Name:              rvrs[i].Name,
			NodeName:          rvrs[i].Spec.NodeName,
			Type:              rvrs[i].Spec.Type,
			DeletionTimestamp: rvrs[i].DeletionTimestamp,
		})
	}

	return replicas
}
