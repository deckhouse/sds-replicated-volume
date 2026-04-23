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

package rvscontroller

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvs_controller/snapmesh"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

func (r *Reconciler) reconcilePrepareMesh(
	ctx context.Context,
	rvs *v1alpha1.ReplicatedVolumeSnapshot,
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	childRVRSs []*v1alpha1.ReplicatedVolumeReplicaSnapshot,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "prepare-mesh")
	defer rf.OnEnd(&outcome)

	l := log.FromContext(rf.Ctx())
	l.Info("prepare-mesh: enter",
		"rvs", rvs.Name,
		"prepareRevision", rvs.Status.PrepareRevision,
		"prepareTransitions", len(rvs.Status.PrepareTransitions),
		"rvrs", len(rvrs),
		"childRVRSs", len(childRVRSs))

	base := rvs.DeepCopy()
	changed := snapmesh.ProcessPrepare(rf.Ctx(), rvs, rv, rvrs, childRVRSs, r.cl, r.scheme)

	l.Info("prepare-mesh: after ProcessPrepare",
		"changed", changed,
		"prepareRevision", rvs.Status.PrepareRevision,
		"prepareTransitions", len(rvs.Status.PrepareTransitions))

	if changed {
		if err := r.patchRVSStatus(rf.Ctx(), rvs, base); err != nil {
			return rf.Fail(err)
		}
		return rf.DoneAndRequeue()
	}

	return rf.Done()
}

func prepareNeedsRun(rvs *v1alpha1.ReplicatedVolumeSnapshot) bool {
	return rvs.Status.PrepareRevision == 0 || len(rvs.Status.PrepareTransitions) > 0
}
