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

package drbdconfig

import (
	"context"
	"fmt"
	"log/slog"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	e "github.com/deckhouse/sds-replicated-volume/images/agent/internal/errors"
)

type Reconciler struct {
	cl       client.Client
	rdr      client.Reader
	log      *slog.Logger
	nodeName string
}

var _ reconcile.TypedReconciler[Request] = &Reconciler{}

// NewReconciler constructs a Reconciler; exported for tests.
func NewReconciler(cl client.Client, rdr client.Reader, log *slog.Logger, nodeName string) *Reconciler {
	if log == nil {
		log = slog.Default()
	}
	return &Reconciler{
		cl:       cl,
		rdr:      rdr,
		log:      log,
		nodeName: nodeName,
	}
}

func (r *Reconciler) OnRVUpdate(
	ctx context.Context,
	rvOld *v1alpha3.ReplicatedVolume,
	rvNew *v1alpha3.ReplicatedVolume,
	q TQueue,
) {
	if rvNew.Status == nil || rvNew.Status.DRBD == nil ||
		rvNew.Status.DRBD.Config == nil || rvNew.Status.DRBD.Config.SharedSecretAlg == "" {
		return
	}

	if rvOld.Status == nil || rvOld.Status.DRBD == nil || rvOld.Status.DRBD.Config == nil ||
		rvOld.Status.DRBD.Config.SharedSecretAlg != rvNew.Status.DRBD.Config.SharedSecretAlg {
		q.Add(
			SharedSecretAlgRequest{
				RVName:          rvNew.Name,
				SharedSecretAlg: rvNew.Status.DRBD.Config.SharedSecretAlg,
			},
		)
	}
}

func (r *Reconciler) OnRVRCreateOrUpdate(
	ctx context.Context,
	rvr *v1alpha3.ReplicatedVolumeReplica,
	q TQueue,
) {
	if !r.rvrOnThisNode(rvr) {
		return
	}

	if rvr.DeletionTimestamp != nil {
		r.log.Info("deletionTimestamp, check finalizers", "rvrName", rvr.Name)

		for _, f := range rvr.Finalizers {
			if f != v1alpha3.AgentAppFinalizer {
				r.log.Info("non-agent finalizer found, ignore", "rvrName", rvr.Name)
				return
			}
		}

		r.log.Debug("down resource", "rvrName", rvr.Name)
		q.Add(DownRequest{rvrRequest{rvr.Name}})
		return
	}

	rv := &v1alpha3.ReplicatedVolume{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rvr.Spec.ReplicatedVolumeName}, rv); err != nil {
		r.log.Error("getting rv", "err", err, "rvrName", rvr.Name)
		return
	}

	if !r.rvrInitialized(rvr, rv) {
		return
	}

	r.log.Debug("up resource", "rvrName", rvr.Name)
	q.Add(UpRequest{rvrRequest{rvr.Name}})
}

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req Request,
) (reconcile.Result, error) {
	if rvrReq, ok := req.(RVRRequest); ok {
		return r.reconcileRVRRequest(ctx, rvrReq)
	}
	return r.reconcileOtherRequest(ctx, req)
}

func (r *Reconciler) reconcileOtherRequest(
	ctx context.Context,
	req Request,
) (reconcile.Result, error) {
	switch typedReq := req.(type) {
	case SharedSecretAlgRequest:
		handler := SharedSecretAlgHandler{
			cl:              r.cl,
			rdr:             r.rdr,
			log:             r.log.With("handler", "sharedSecretAlg"),
			nodeName:        r.nodeName,
			rvName:          typedReq.RVName,
			sharedSecretAlg: typedReq.SharedSecretAlg,
		}
		return reconcile.Result{}, handler.Handle(ctx)
	default:
		r.log.Error("unknown req type", "typedReq", typedReq)
		return reconcile.Result{}, e.ErrNotImplemented
	}
}

func (r *Reconciler) reconcileRVRRequest(
	ctx context.Context,
	req RVRRequest,
) (reconcile.Result, error) {
	rvr := &v1alpha3.ReplicatedVolumeReplica{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: req.RVRName()}, rvr); err != nil {
		if client.IgnoreNotFound(err) == nil {
			// deleted
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("get rvr: %w", err)
	}
	if !r.rvrOnThisNode(rvr) {
		r.log.Error("rvr to be reconciled is not on this node")
		return reconcile.Result{}, nil
	}

	var llv *v1alpha1.LVMLogicalVolume
	if rvr.Spec.Type == "Diskful" {
		llv = &v1alpha1.LVMLogicalVolume{}
		if err := r.cl.Get(
			ctx,
			client.ObjectKey{Name: rvr.Status.LVMLogicalVolumeName},
			llv,
		); err != nil {
			r.log.Error("getting llv", "err", err)
			return reconcile.Result{}, err
		}
	}

	switch typedReq := req.(type) {
	case UpRequest:
		rv := &v1alpha3.ReplicatedVolume{}
		if err := r.cl.Get(ctx, client.ObjectKey{Name: rvr.Spec.ReplicatedVolumeName}, rv); err != nil {
			r.log.Error("getting rv", "err", err)
			return reconcile.Result{}, err
		}

		if !r.rvrInitialized(rvr, rv) {
			r.log.Error("rvr to be reconciled is not initialized")
			return reconcile.Result{}, nil
		}

		handler := &UpHandler{
			cl:       r.cl,
			log:      r.log.With("handler", "up"),
			rvr:      rvr,
			rv:       rv,
			llv:      llv,
			nodeName: r.nodeName,
		}

		if llv != nil {
			handler.lvg = &v1alpha1.LVMVolumeGroup{}
			if err := r.cl.Get(
				ctx,
				client.ObjectKey{Name: handler.llv.Spec.LVMVolumeGroupName},
				handler.lvg,
			); err != nil {
				r.log.Error("getting lvg", "err", err)
				return reconcile.Result{}, err
			}
		}

		return reconcile.Result{}, handler.Handle(ctx)
	case DownRequest:
		handler := &DownHandler{
			cl:  r.cl,
			log: r.log.With("handler", "down"),
			rvr: rvr,
			llv: llv,
		}
		return reconcile.Result{}, handler.Handle(ctx)
	default:
		r.log.Error("unknown req type", "typedReq", typedReq)
		return reconcile.Result{}, e.ErrNotImplemented
	}
}

func (r *Reconciler) rvrOnThisNode(rvr *v1alpha3.ReplicatedVolumeReplica) bool {
	if rvr.Spec.NodeName == "" {
		return false
	}
	if rvr.Spec.NodeName != r.nodeName {
		r.log.Debug("invalid node - skip",
			"rvrName", rvr.Name,
			"rvrNodeName", rvr.Spec.NodeName,
			"nodeName", r.nodeName,
		)
		return false
	}
	return true
}

func (r *Reconciler) sharedSecretAlgUpdated(
	rv *v1alpha3.ReplicatedVolume,
	rvr *v1alpha3.ReplicatedVolumeReplica,
	oldRVR *v1alpha3.ReplicatedVolumeReplica,
) bool {
	return rv.Status != nil && rv.Status.DRBD != nil && rv.Status.DRBD.Config != nil && rv.Status.DRBD.Config.SharedSecretAlg != ""
}

func (r *Reconciler) rvrInitialized(rvr *v1alpha3.ReplicatedVolumeReplica, rv *v1alpha3.ReplicatedVolume) bool {
	var logNotInitializedField = func(field string) {
		r.log.Debug("rvr not initialized", "field", field)
	}

	if rvr.Spec.ReplicatedVolumeName == "" {
		logNotInitializedField("spec.replicatedVolumeName")
		return false
	}
	if rvr.Status == nil || rvr.Status.DRBD == nil || rvr.Status.DRBD.Config == nil {
		logNotInitializedField("status.drbd.config")
		return false
	}
	if rvr.Status.DRBD.Config.NodeId == nil {
		logNotInitializedField("status.drbd.config.nodeId")
		return false
	}
	if rvr.Status.DRBD.Config.Address == nil {
		logNotInitializedField("status.drbd.config.address")
		return false
	}
	if !rvr.Status.DRBD.Config.PeersInitialized {
		logNotInitializedField("status.drbd.config.peersInitialized")
		return false
	}
	if rvr.Spec.Type == "Diskful" && rvr.Status.LVMLogicalVolumeName == "" {
		logNotInitializedField("status.lvmLogicalVolumeName")
		return false
	}
	if rv.Status == nil || rv.Status.DRBD == nil || rv.Status.DRBD.Config == nil {
		logNotInitializedField("rv.status.drbd.config")
		return false
	}
	if rv.Status.DRBD.Config.SharedSecret == "" {
		logNotInitializedField("rv.status.drbd.config.sharedSecret")
		return false
	}
	if rv.Status.DRBD.Config.SharedSecretAlg == "" {
		logNotInitializedField("rv.status.drbd.config.sharedSecretAlg")
		return false
	}
	return true
}
