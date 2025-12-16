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
	"errors"
	"fmt"
	"log/slog"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	u "github.com/deckhouse/sds-common-lib/utils"
	uslices "github.com/deckhouse/sds-common-lib/utils/slices"
	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

type Reconciler struct {
	cl       client.Client
	log      *slog.Logger
	nodeName string
}

var _ reconcile.Reconciler = &Reconciler{}

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	log := r.log.With("rvName", req.Name)

	rv, rvr, err := r.selectRVR(ctx, req, log)
	if err != nil {
		return reconcile.Result{}, err
	}

	if rvr == nil {
		log.Info("RVR not found for this node - skip")
		return reconcile.Result{}, nil
	}

	log = log.With("rvrName", rvr.Name)

	var llv *snc.LVMLogicalVolume
	if rvr.Spec.Type == "Diskful" && rvr.Status != nil && rvr.Status.LVMLogicalVolumeName != "" {
		if llv, err = r.selectLLV(ctx, log, rvr.Status.LVMLogicalVolumeName); err != nil {
			return reconcile.Result{}, err
		}
		log = log.With("llvName", llv.Name)
	}

	switch {
	case rvr.DeletionTimestamp != nil:
		log.Info("deletionTimestamp on rvr, check finalizers")

		for _, f := range rvr.Finalizers {
			if f != v1alpha3.AgentAppFinalizer {
				log.Info("non-agent finalizer found, ignore")
				return reconcile.Result{}, nil
			}
		}

		log.Info("down resource")

		h := &DownHandler{
			cl:  r.cl,
			log: log.With("handler", "down"),
			rvr: rvr,
			llv: llv,
		}

		return reconcile.Result{}, h.Handle(ctx)
	case !rvrFullyInitialized(log, rv, rvr):
		return reconcile.Result{}, nil
	default:
		h := &UpAndAdjustHandler{
			cl:       r.cl,
			log:      log.With("handler", "upAndAdjust"),
			rvr:      rvr,
			rv:       rv,
			llv:      llv,
			nodeName: r.nodeName,
		}

		if llv != nil {
			if h.lvg, err = r.selectLVG(ctx, log, llv.Spec.LVMVolumeGroupName); err != nil {
				return reconcile.Result{}, err
			}
		}
		return reconcile.Result{}, h.Handle(ctx)
	}
}

func (r *Reconciler) selectRVR(
	ctx context.Context,
	req reconcile.Request,
	log *slog.Logger,
) (*v1alpha3.ReplicatedVolume, *v1alpha3.ReplicatedVolumeReplica, error) {
	rv := &v1alpha3.ReplicatedVolume{}
	if err := r.cl.Get(ctx, req.NamespacedName, rv); err != nil {
		return nil, nil, u.LogError(log, fmt.Errorf("getting rv: %w", err))
	}

	rvrList := &v1alpha3.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, rvrList); err != nil {
		return nil, nil, u.LogError(log, fmt.Errorf("listing rvr: %w", err))
	}

	var rvr *v1alpha3.ReplicatedVolumeReplica
	for rvrItem := range uslices.Ptrs(rvrList.Items) {
		if rvrItem.Spec.NodeName == r.nodeName && rvrItem.Spec.ReplicatedVolumeName == req.Name {
			if rvr != nil {
				return nil, nil,
					u.LogError(
						log.With("firstRVR", rvr.Name).With("secondRVR", rvrItem.Name),
						errors.New("selecting rvr: more then one rvr exists"),
					)
			}
			rvr = rvrItem
		}
	}

	return rv, rvr, nil
}

func (r *Reconciler) selectLLV(
	ctx context.Context,
	log *slog.Logger,
	llvName string,
) (*snc.LVMLogicalVolume, error) {
	llv := &snc.LVMLogicalVolume{}
	if err := r.cl.Get(
		ctx,
		client.ObjectKey{Name: llvName},
		llv,
	); err != nil {
		return nil, u.LogError(log, fmt.Errorf("getting llv: %w", err))
	}
	return llv, nil
}

func (r *Reconciler) selectLVG(
	ctx context.Context,
	log *slog.Logger,
	lvgName string,
) (*snc.LVMVolumeGroup, error) {
	lvg := &snc.LVMVolumeGroup{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: lvgName}, lvg); err != nil {
		return nil, u.LogError(log, fmt.Errorf("getting lvg: %w", err))
	}
	return lvg, nil
}

// NewReconciler constructs a Reconciler; exported for tests.
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

func rvrFullyInitialized(log *slog.Logger, rv *v1alpha3.ReplicatedVolume, rvr *v1alpha3.ReplicatedVolumeReplica) bool {
	var logNotInitializedField = func(field string) {
		log.Info("rvr not initialized", "field", field)
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
