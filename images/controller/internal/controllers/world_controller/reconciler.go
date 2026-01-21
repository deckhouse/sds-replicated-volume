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

package worldcontroller

import (
	"context"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/world_controller/world"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

type Reconciler struct {
	cl    client.Client
	log   logr.Logger
	world *world.World
}

var _ reconcile.TypedReconciler[Request] = (*Reconciler)(nil)

func NewReconciler(cl client.Client, log logr.Logger, w *world.World) *Reconciler {
	return &Reconciler{
		cl:    cl,
		log:   log,
		world: w,
	}
}

func (r *Reconciler) Reconcile(ctx context.Context, req Request) (reconcile.Result, error) {
	rf := flow.BeginRootReconcile(ctx)

	var initOutcome flow.ReconcileOutcome
	r.world.Initialize(func(setTheWorld func()) {
		initOutcome = r.reconcileWholeWorld(rf.Ctx(), setTheWorld)
	})
	if initOutcome.ShouldReturn() {
		return initOutcome.ToCtrl()
	}

	_ = rf.Log().WithValues("name", req.Name, "namespace", req.Namespace, "target", req.ReconcileTarget)

	// TODO: implement reconciliation logic
	// req.ReconcileTarget indicates which object is being reconciled:
	// - ReconcileTargetRSC: RSC was created or deleted
	// - ReconcileTargetRSCSpec: RSC spec changed (metadata.generation)
	// - ReconcileTargetRSCConfiguration: RSC status.configurationGeneration changed
	// - ReconcileTargetRV: the object at req.NamespacedName is a ReplicatedVolume
	// - ReconcileTargetNodeAdd: Node was created
	// - ReconcileTargetNodeDelete: Node was deleted
	// - ReconcileTargetNodeReady: Node Ready condition status changed
	// - ReconcileTargetNodeUnscheduled: Node spec.unschedulable changed
	// - ReconcileTargetNodeLabels: Node labels changed
	// - ReconcileTargetAgentPod: the object at req.NamespacedName is an agent Pod

	return rf.Done().ToCtrl()
}

func (r *Reconciler) reconcileWholeWorld(ctx context.Context, setTheWorld func()) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "whole-world")
	defer rf.OnEnd(&outcome)

	// TODO: implement initialization logic

	setTheWorld() // TODO there will be args with the world state
	return rf.Continue()
}
