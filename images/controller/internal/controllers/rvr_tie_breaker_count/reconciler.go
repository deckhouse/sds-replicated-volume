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

package rvrtiebreakercount

import (
	"context"
	"errors"
	"fmt"

	uslices "github.com/deckhouse/sds-common-lib/utils/slices"
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	interrors "github.com/deckhouse/sds-replicated-volume/images/controller/internal/errors"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type Reconciler struct {
	cl     client.Client
	log    logr.Logger
	scheme *runtime.Scheme
}

func NewReconciler(cl client.Client, log logr.Logger, scheme *runtime.Scheme) (*Reconciler, error) {
	if err := interrors.ValidateArgNotNil(cl, "cl"); err != nil {
		return nil, err
	}
	if err := interrors.ValidateArgNotNil(scheme, "scheme"); err != nil {
		return nil, err
	}
	return &Reconciler{
		cl:     cl,
		log:    log,
		scheme: scheme,
	}, nil
}

var _ reconcile.Reconciler = &Reconciler{}
var ErrNoZoneLabel = errors.New("can't find zone label")

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	log := r.log.WithName("Reconcile").WithValues("request", req)
	rv, err := r.getReplicatedVolume(ctx, req, log)
	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	// TODO: fail ReplicatedVolume if it has empty ReplicatedStorageClassName
	if shouldSkipRV(rv, log) {
		return reconcile.Result{}, nil
	}

	rsc, err := r.getReplicatedStorageClass(ctx, rv, log)
	if err != nil {
		return reconcile.Result{}, err
	}
	if rsc == nil {
		return reconcile.Result{}, nil
	}

	fds, tbs, nonFDtbs, err := r.loadFailureDomains(ctx, log, rv.Name, rsc)
	if err != nil {
		return reconcile.Result{}, err
	}

	// delete TBs, which are scheduled to FDs, which are outside our FDs
	for i, tbToDelete := range nonFDtbs {
		rvr := (*v1alpha1.ReplicatedVolumeReplica)(tbToDelete)
		if err := r.cl.Delete(ctx, rvr); client.IgnoreNotFound(err) != nil {
			return reconcile.Result{},
				logError(log.WithValues("tbToDelete", tbToDelete.Name), fmt.Errorf("deleting nonFDtbs rvr: %w", err))
		}

		log.Info(fmt.Sprintf("deleted rvr %d/%d", i+1, len(nonFDtbs)), "tbToDelete", tbToDelete.Name)
	}

	return r.syncTieBreakers(ctx, log, rv, fds, tbs)
}

func (r *Reconciler) getReplicatedVolume(
	ctx context.Context,
	req reconcile.Request,
	log logr.Logger,
) (*v1alpha1.ReplicatedVolume, error) {
	rv := &v1alpha1.ReplicatedVolume{}
	if err := r.cl.Get(ctx, req.NamespacedName, rv); err != nil {
		log.Error(err, "Can't get ReplicatedVolume")
		return nil, err
	}
	return rv, nil
}

func shouldSkipRV(rv *v1alpha1.ReplicatedVolume, log logr.Logger) bool {
	if !v1alpha1.HasControllerFinalizer(rv) {
		log.Info("No controller finalizer on ReplicatedVolume")
		return true
	}

	if rv.Status == nil {
		log.Info("Status is empty on ReplicatedVolume")
		return true
	}

	if !meta.IsStatusConditionTrue(rv.Status.Conditions, v1alpha1.ConditionTypeRVInitialized) {
		log.Info("ReplicatedVolume is not initialized yet")
		return true
	}

	if rv.Spec.ReplicatedStorageClassName == "" {
		log.Info("Empty ReplicatedStorageClassName")
		return true
	}
	return false
}

func (r *Reconciler) getReplicatedStorageClass(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	log logr.Logger,
) (*v1alpha1.ReplicatedStorageClass, error) {
	rsc := &v1alpha1.ReplicatedStorageClass{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rv.Spec.ReplicatedStorageClassName}, rsc); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.V(1).Info("ReplicatedStorageClass not found", "name", rv.Spec.ReplicatedStorageClassName)
			return nil, nil
		}
		log.Error(err, "Can't get ReplicatedStorageClass")
		return nil, err
	}
	return rsc, nil
}

func (r *Reconciler) loadFailureDomains(
	ctx context.Context,
	log logr.Logger,
	rvName string,
	rsc *v1alpha1.ReplicatedStorageClass,
) (fds map[string]*failureDomain, tbs []tb, nonFDtbs []tb, err error) {
	// initialize empty failure domains
	nodeList := &corev1.NodeList{}
	if err := r.cl.List(ctx, nodeList); err != nil {
		return nil, nil, nil, logError(r.log, fmt.Errorf("listing nodes: %w", err))
	}

	if rsc.Spec.Topology == "TransZonal" {
		// each zone is a failure domain
		fds = make(map[string]*failureDomain, len(rsc.Spec.Zones))
		for _, zone := range rsc.Spec.Zones {
			fds[zone] = &failureDomain{}
		}

		for node := range uslices.Ptrs(nodeList.Items) {
			zone, ok := node.Labels[corev1.LabelTopologyZone]
			if !ok {
				log.WithValues("node", node.Name).Error(ErrNoZoneLabel, "No zone label")
				return nil, nil, nil, fmt.Errorf("%w: node is %s", ErrNoZoneLabel, node.Name)
			}

			if fd, ok := fds[zone]; ok {
				fd.nodeNames = append(fd.nodeNames, node.Name)
			}
		}
	} else {
		// each node is a failure domain
		fds = make(map[string]*failureDomain, len(nodeList.Items))

		for node := range uslices.Ptrs(nodeList.Items) {
			fds[node.Name] = &failureDomain{nodeNames: []string{node.Name}}
		}
	}

	// init failure domains with RVRs
	rvrList := &v1alpha1.ReplicatedVolumeReplicaList{}
	if err = r.cl.List(ctx, rvrList); err != nil {
		return nil, nil, nil, logError(log, fmt.Errorf("listing rvrs: %w", err))
	}

	for rvr := range uslices.Ptrs(rvrList.Items) {
		if rvr.Spec.ReplicatedVolumeName != rvName {
			continue
		}

		// ignore non-scheduled base replicas
		if rvr.Spec.NodeName == "" && rvr.Spec.Type != v1alpha1.ReplicaTypeTieBreaker {
			continue
		}

		if rvr.Spec.Type == v1alpha1.ReplicaTypeTieBreaker {
			var fdTB bool
			if rvr.Spec.NodeName != "" {
				for _, fd := range fds {
					if fd.addTBReplica(rvr) {
						// rvr always maps to single fd
						fdTB = true
						break
					}
				}
			} else {
				fdTB = true
			}

			if fdTB {
				tbs = append(tbs, rvr)
			} else {
				nonFDtbs = append(nonFDtbs, rvr)
			}
		} else {
			for _, fd := range fds {
				if fd.addBaseReplica(rvr) {
					// rvr always maps to single fd
					break
				}
			}
			// ignore non-fb base replicas
		}
	}

	return fds, tbs, nonFDtbs, nil
}

func (r *Reconciler) syncTieBreakers(
	ctx context.Context,
	log logr.Logger,
	rv *v1alpha1.ReplicatedVolume,
	fds map[string]*failureDomain,
	tbs []tb,
) (reconcile.Result, error) {

	var maxBaseReplicaCount, totalBaseReplicaCount int
	for _, fd := range fds {
		fdBaseReplicaCount := fd.baseReplicaCount()
		maxBaseReplicaCount = max(maxBaseReplicaCount, fdBaseReplicaCount)
		totalBaseReplicaCount += fdBaseReplicaCount
	}

	currentTB := len(tbs)

	var desiredTB int
	for _, fd := range fds {
		baseReplicaCountDiffFromMax := maxBaseReplicaCount - fd.baseReplicaCount()
		if baseReplicaCountDiffFromMax >= 2 {
			desiredTB += baseReplicaCountDiffFromMax - 1
		}
	}

	desiredTotalReplicaCount := totalBaseReplicaCount + desiredTB
	if desiredTotalReplicaCount > 0 && desiredTotalReplicaCount%2 == 0 {
		// add one more in order to keep total number of replicas odd
		desiredTB++
	}

	if currentTB == desiredTB {
		log.Info("No need to change")
		return reconcile.Result{}, nil
	}

	for i := range desiredTB - currentTB {
		// creating
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: rv.Name + "-",
				Finalizers:   []string{v1alpha1.ControllerAppFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: rv.Name,
				Type:                 v1alpha1.ReplicaTypeTieBreaker,
			},
		}

		if err := controllerutil.SetControllerReference(rv, rvr, r.scheme); err != nil {
			return reconcile.Result{}, err
		}

		if err := r.cl.Create(ctx, rvr); err != nil {
			return reconcile.Result{}, err
		}

		log.Info(fmt.Sprintf("created rvr %d/%d", i+1, desiredTB-currentTB), "newRVR", rvr.Name)
	}

	for i := range currentTB - desiredTB {
		// deleting starting from scheduled TBs
		var tbToDelete *v1alpha1.ReplicatedVolumeReplica
		for _, fd := range fds {
			if fd.tbReplicaCount() == 0 {
				continue
			}

			wantFDTotalReplicaCount := fd.baseReplicaCount() + fd.tbReplicaCount()

			// can we remove one tb from this fd?
			wantFDTotalReplicaCount--

			baseReplicaCountDiffFromMax := maxBaseReplicaCount - wantFDTotalReplicaCount
			if baseReplicaCountDiffFromMax < 2 {
				// found tb, which is not necessary for this fd
				tbToDelete = fd.popTBReplica()

				break
			}
		}

		if tbToDelete == nil {
			for _, tb := range tbs {
				// take the first non-scheduled
				if tb.Spec.NodeName == "" {
					tbToDelete = tb
					break
				}
			}
		}

		if tbToDelete == nil {
			// this should not happen, but let's be safe
			log.V(1).Info("failed to select TB to delete")
			return reconcile.Result{}, nil
		}

		if err := r.cl.Delete(ctx, tbToDelete); client.IgnoreNotFound(err) != nil {
			return reconcile.Result{},
				logError(log.WithValues("tbToDelete", tbToDelete.Name), fmt.Errorf("deleting tb rvr: %w", err))
		}

		log.Info(fmt.Sprintf("deleted rvr %d/%d", i+1, currentTB-desiredTB), "tbToDelete", tbToDelete.Name)
	}

	return reconcile.Result{}, nil
}

func logError(log logr.Logger, err error) error {
	if err != nil {
		log.Error(err, err.Error())
		return err
	}
	return nil
}
