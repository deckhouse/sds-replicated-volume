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
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

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

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	// always will come an event on ReplicatedVolume, even if the event happened on ReplicatedVolumeReplica

	log := r.log.WithName("Reconcile").WithValues("req", req)
	log.Info("Reconciling started")
	start := time.Now()
	defer func() {
		log.Info("Reconcile finished", "duration", time.Since(start).String())
	}()

	rvr := &v1alpha3.ReplicatedVolumeReplica{}
	err := r.cl.Get(ctx, client.ObjectKey{Name: req.Name}, rvr)
	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.Info("ReplicatedVolumeReplica not found, ignoring reconcile request")
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("getting ReplicatedVolumeReplica: %w", err)
	}

	if rvr.DeletionTimestamp != nil {
		return requestToDeleteLLV(ctx, req, log, rvr)
	}

	if rvr.Spec.Type == "Diskful" {
		return requestToCreateLLV(ctx, req, log, rvr)
	}

	if rvr.Status != nil && rvr.Status.ActualType == rvr.Spec.Type {
		return requestToDeleteLLV(ctx, req, log, rvr)
	}

	return reconcile.Result{}, nil
}

func requestToDeleteLLV(ctx context.Context, req reconcile.Request, log logr.Logger, rvr *v1alpha3.ReplicatedVolumeReplica) (reconcile.Result, error) {
	log = log.WithName("RequestToDeleteLLV")
	log.Info("111111")

	return reconcile.Result{}, nil
}

func requestToCreateLLV(ctx context.Context, req reconcile.Request, log logr.Logger, rvr *v1alpha3.ReplicatedVolumeReplica) (reconcile.Result, error) {
	log = log.WithName("ReconcileCreateLLV")
	log.Info("222222")

	return reconcile.Result{}, nil
}
