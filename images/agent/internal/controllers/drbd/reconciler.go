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

package drbd

import (
	"context"
	"log/slog"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// Reconciler reconciles DRBDResource objects.
type Reconciler struct {
	cl       client.Client
	log      *slog.Logger
	nodeName string
}

var _ reconcile.Reconciler = &Reconciler{}

// NewReconciler creates a new Reconciler.
func NewReconciler(cl client.Client, log *slog.Logger, nodeName string) *Reconciler {
	if log == nil {
		log = slog.Default()
	}
	return &Reconciler{
		cl:       cl,
		log:      log.With("nodeName", nodeName),
		nodeName: nodeName,
	}
}

// Reconcile handles a single DRBDResource reconciliation request.
func (r *Reconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	log := r.log.With("name", req.Name)
	log.Info("Reconciling DRBDResource")

	// Fetch the DRBDResource
	dr := &v1alpha1.DRBDResource{}
	if err := r.cl.Get(ctx, req.NamespacedName, dr); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("DRBDResource not found, skipping")
			return reconcile.Result{}, nil
		}
		log.Error("Failed to get DRBDResource", "error", err)
		return reconcile.Result{}, err
	}

	// Check if this resource belongs to this node
	if dr.Spec.NodeName != r.nodeName {
		log.Debug("DRBDResource belongs to different node, skipping",
			"resourceNodeName", dr.Spec.NodeName)
		return reconcile.Result{}, nil
	}

	// TODO: Implement actual reconciliation logic here
	log.Info("DRBDResource reconciled successfully")

	return reconcile.Result{}, nil
}
