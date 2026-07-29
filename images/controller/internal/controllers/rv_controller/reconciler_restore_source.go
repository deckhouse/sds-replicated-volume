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

package rvcontroller

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// reconcileRestoreSourceFinalizer keeps RVSRestoreSourceFinalizer on the
// ReplicatedVolumeSnapshot this RV is being restored from, and removes it once
// the target RV either finishes formation or starts deleting.
//
// This is the snapshot-side counterpart of reconcileCloneSourceFinalizer. Until
// the restore has caught up, the target's replicas still read from the
// per-replica snapshots behind the RVS, so deleting the snapshot mid-restore
// would pull the data out from under them. The finalizer keeps the RVS object
// alive, and rvs-controller additionally defers its child cleanup while the
// finalizer is present.
//
// Reconcile pattern: Target-state driven (patches source RVS finalizers).
func (r *Reconciler) reconcileRestoreSourceFinalizer(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "restore-source-finalizer")
	defer rf.OnEnd(&outcome)

	if rv.Spec.DataSource == nil {
		return rf.Continue()
	}
	if rv.Spec.DataSource.Kind != v1alpha1.VolumeDataSourceKindReplicatedVolumeSnapshot {
		return rf.Continue()
	}
	sourceName := rv.Spec.DataSource.Name
	if sourceName == "" {
		return rf.Continue()
	}

	source, err := r.getRVS(rf.Ctx(), sourceName)
	if err != nil {
		return rf.Failf(err, "getting restore source RVS %q", sourceName)
	}
	if source == nil {
		return rf.Continue()
	}

	needFinalizer := rv.DeletionTimestamp == nil
	if needFinalizer {
		if forming, _ := isFormationInProgress(rv); !forming {
			needFinalizer = false
		}
	}

	hasFinalizer := obju.HasFinalizer(source, v1alpha1.RVSRestoreSourceFinalizer)

	switch {
	case needFinalizer && !hasFinalizer:
		// Do not add a finalizer to an object that is already going away: the
		// restore either completes with the data it has or fails, and holding a
		// deleting RVS open forever helps nobody.
		if source.DeletionTimestamp != nil {
			return rf.Continue()
		}
		base := source.DeepCopy()
		obju.AddFinalizer(source, v1alpha1.RVSRestoreSourceFinalizer)
		if err := r.patchRVS(rf.Ctx(), source, base); err != nil {
			return rf.Failf(err, "adding restore-source finalizer to RVS %q", source.Name)
		}
	case !needFinalizer && hasFinalizer:
		base := source.DeepCopy()
		obju.RemoveFinalizer(source, v1alpha1.RVSRestoreSourceFinalizer)
		if err := r.patchRVS(rf.Ctx(), source, base); err != nil {
			return rf.Failf(err, "removing restore-source finalizer from RVS %q", source.Name)
		}
	}

	return rf.Continue()
}

func (r *Reconciler) getRVS(ctx context.Context, name string) (*v1alpha1.ReplicatedVolumeSnapshot, error) {
	rvs := &v1alpha1.ReplicatedVolumeSnapshot{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, rvs); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return rvs, nil
}

func (r *Reconciler) patchRVS(ctx context.Context, obj, base *v1alpha1.ReplicatedVolumeSnapshot) error {
	return r.cl.Patch(ctx, obj, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}
