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

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// reconcileCloneSourceFinalizer keeps RVCloneSourceFinalizer on the source RV
// while this RV is being cloned from it, and removes the finalizer when the
// target RV either finishes formation (initial sync done) or starts deleting.
//
// The finalizer prevents the source RV from being fully deleted while the
// clone's primary replica still shares an LVM thin origin with it on a node
// and while peer replicas are catching up via DRBD initial sync.
//
// Reconcile pattern: Target-state driven (patches source RV finalizers).
func (r *Reconciler) reconcileCloneSourceFinalizer(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "clone-source-finalizer")
	defer rf.OnEnd(&outcome)

	if rv.Spec.DataSource == nil {
		return rf.Continue()
	}
	if rv.Spec.DataSource.Kind != v1alpha1.VolumeDataSourceKindReplicatedVolume {
		return rf.Continue()
	}
	sourceName := rv.Spec.DataSource.Name
	if sourceName == "" {
		return rf.Continue()
	}

	source, err := r.getRV(rf.Ctx(), sourceName)
	if err != nil {
		return rf.Failf(err, "getting clone source RV %q", sourceName)
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

	hasFinalizer := obju.HasFinalizer(source, v1alpha1.RVCloneSourceFinalizer)

	switch {
	case needFinalizer && !hasFinalizer:
		if source.DeletionTimestamp != nil {
			return rf.Continue()
		}
		base := source.DeepCopy()
		obju.AddFinalizer(source, v1alpha1.RVCloneSourceFinalizer)
		if err := r.patchRV(rf.Ctx(), source, base); err != nil {
			return rf.Failf(err, "adding clone-source finalizer to RV %q", source.Name)
		}
	case !needFinalizer && hasFinalizer:
		base := source.DeepCopy()
		obju.RemoveFinalizer(source, v1alpha1.RVCloneSourceFinalizer)
		if err := r.patchRV(rf.Ctx(), source, base); err != nil {
			return rf.Failf(err, "removing clone-source finalizer from RV %q", source.Name)
		}
	}

	return rf.Continue()
}
