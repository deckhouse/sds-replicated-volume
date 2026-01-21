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

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/world"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

type Reconciler struct {
	cl        client.Client
	worldGate world.WorldGate
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

func NewReconciler(cl client.Client, worldGate world.WorldGate) *Reconciler {
	return &Reconciler{
		cl:        cl,
		worldGate: worldGate,
	}
}

// Reconcile pattern: Pure orchestration
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	rf := flow.BeginRootReconcile(ctx)

	// Get the ReplicatedVolume
	rv := &v1alpha1.ReplicatedVolume{}
	if err := r.cl.Get(rf.Ctx(), req.NamespacedName, rv); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return rf.Failf(err, "getting ReplicatedVolume").ToCtrl()
		}

		// NotFound: object deleted, nothing to do.
		return rf.Done().ToCtrl()
	}

	// Reconcile main resource
	outcome := r.reconcileMain(rf.Ctx(), rv)
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	return rf.Done().ToCtrl()
}

// Reconcile pattern: Conditional desired evaluation
func (r *Reconciler) reconcileMain(ctx context.Context, rv *v1alpha1.ReplicatedVolume) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "main")
	defer rf.OnEnd(&outcome)

	if rv == nil {
		return rf.Continue()
	}

	if obju.HasLabelValue(rv, v1alpha1.ReplicatedStorageClassLabelKey, rv.Spec.ReplicatedStorageClassName) {
		return rf.Continue()
	}

	base := rv.DeepCopy()

	obju.SetLabel(rv, v1alpha1.ReplicatedStorageClassLabelKey, rv.Spec.ReplicatedStorageClassName)

	if err := r.cl.Patch(rf.Ctx(), rv, client.MergeFrom(base)); err != nil {
		return rf.Fail(err).Enrichf("patching ReplicatedVolume")
	}

	return rf.Continue()
}
