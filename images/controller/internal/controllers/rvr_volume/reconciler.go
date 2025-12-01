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

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
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
		return requestToDeleteLLV(ctx, r.cl, req, log, rvr)
	}

	if rvr.Spec.Type == "Diskful" {
		return requestToCreateLLV(ctx, req, log, rvr)
	}

	if rvr.Status != nil && rvr.Status.ActualType == rvr.Spec.Type {
		return requestToDeleteLLV(ctx, r.cl, req, log, rvr)
	}

	return reconcile.Result{}, nil
}

func requestToDeleteLLV(ctx context.Context, cl client.Client, req reconcile.Request, log logr.Logger, rvr *v1alpha3.ReplicatedVolumeReplica) (reconcile.Result, error) {
	log = log.WithName("RequestToDeleteLLV")

	if rvr.Status == nil || rvr.Status.LVMLogicalVolumeName == "" {
		llv, err := findLLVWithOwnerReference(ctx, cl, rvr.Name)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("checking for llv with ownerReference: %w", err)
		}
		if llv == nil {
			log.V(4).Info("No llv found with ownerReference", "rvrName", rvr.Name)
		} else {
			log.V(4).Info("Found llv with ownerReference", "rvrName", rvr.Name, "llvName", llv.Name)
			// TODO: handle case when llv exists but LVMLogicalVolumeName is empty
		}
	} else {
		// delete llv
	}

	return reconcile.Result{}, nil
}

func requestToCreateLLV(ctx context.Context, req reconcile.Request, log logr.Logger, rvr *v1alpha3.ReplicatedVolumeReplica) (reconcile.Result, error) {
	log = log.WithName("ReconcileCreateLLV")
	log.Info("222222")

	return reconcile.Result{}, nil
}

// findLLVWithOwnerReference finds a LVMLogicalVolume in the cluster with ownerReference
// pointing to ReplicatedVolumeReplica with the specified name.
// Returns the llv object if found, nil otherwise.
func findLLVWithOwnerReference(ctx context.Context, cl client.Client, rvrName string) (*snc.LVMLogicalVolume, error) {
	var llvList snc.LVMLogicalVolumeList
	if err := cl.List(ctx, &llvList); err != nil {
		return nil, fmt.Errorf("listing LVMLogicalVolumes: %w", err)
	}

	for i := range llvList.Items {
		llv := &llvList.Items[i]
		for _, ownerRef := range llv.OwnerReferences {
			if ownerRef.Kind == "ReplicatedVolumeReplica" && ownerRef.Name == rvrName {
				return llv, nil
			}
		}
	}

	return nil, nil
}
