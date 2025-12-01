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

package rvrstatusconfigaddress

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/go-logr/logr"
)

type Reconciler struct {
	cl  client.Client
	log logr.Logger
}

var _ reconcile.Reconciler = &Reconciler{}

func (r *Reconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	log := r.log.WithName("Reconcile").WithValues("request", request)
	var rv v1alpha3.ReplicatedVolume
	if err := r.cl.Get(ctx, request.NamespacedName, &rv); err != nil {
		log.Error(err, "Can't get ReplicatedVolume")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	var rvrList v1alpha3.ReplicatedVolumeReplicaList
	if err := r.cl.List(ctx, &rvrList); err != nil {
		log.Error(err, "Can't list ")
	}

	panic("not implemented")
}
