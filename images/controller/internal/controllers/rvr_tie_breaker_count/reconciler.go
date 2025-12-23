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
	"slices"

	uslices "github.com/deckhouse/sds-common-lib/utils/slices"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

type Reconciler struct {
	cl     client.Client
	log    logr.Logger
	scheme *runtime.Scheme
}

func NewReconciler(cl client.Client, log logr.Logger, scheme *runtime.Scheme) *Reconciler {

	return &Reconciler{
		cl:     cl,
		log:    log,
		scheme: scheme,
	}
}

var _ reconcile.Reconciler = &Reconciler{}
var ErrNoZoneLabel = errors.New("can't find zone label")

type baseReplica *v1alpha1.ReplicatedVolumeReplica

type tb *v1alpha1.ReplicatedVolumeReplica

type failureDomain struct {
	nodeNames    []string // for Any/Zonal topology it is always single node
	baseReplicas []baseReplica
	tbs          []tb
}

func (fd *failureDomain) baseReplicaCount() int {
	return len(fd.baseReplicas)
}
func (fd *failureDomain) tbReplicaCount() int {
	return len(fd.tbs)
}

func (fd *failureDomain) addReplica(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
	if rvr.Spec.Type == v1alpha1.ReplicaTypeTieBreaker {
		fd.tbs = append(fd.tbs, tb(rvr))
	} else {
		if rvr.Spec.NodeName == "" || !slices.Contains(fd.nodeNames, rvr.Spec.NodeName) {
			return false
		}

		fd.baseReplicas = append(fd.baseReplicas, baseReplica(rvr))
	}
	return true
}

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

	fds, err := r.loadFailureDomains(ctx, log, rv.Name, rsc)
	if err != nil {
		return reconcile.Result{}, err
	}

	return r.syncTieBreakers(ctx, log, rv, fds)
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

	if !meta.IsStatusConditionTrue(rv.Status.Conditions, v1alpha1.ConditionTypeRVScheduled) {
		log.Info("ReplicatedVolume is not scheduled yet")
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
) (fds map[string]*failureDomain, err error) {
	// initialize empty failure domains
	nodeList := &corev1.NodeList{}
	if err := r.cl.List(ctx, nodeList); err != nil {
		return nil, logError(r.log, fmt.Errorf("listing nodes: %w", err))
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
				return nil, fmt.Errorf("%w: node is %s", ErrNoZoneLabel, node.Name)
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
		return nil, logError(log, fmt.Errorf("listing rvrs: %w", err))
	}

	for rvr := range uslices.Ptrs(rvrList.Items) {
		if rvr.Spec.ReplicatedVolumeName != rvName {
			continue
		}
		for _, fd := range fds {
			if fd.addReplica(rvr) {
				// rvr maps to single fd
				break
			}
		}
	}

	return fds, nil
}

func (r *Reconciler) syncTieBreakers(
	ctx context.Context,
	log logr.Logger,
	rv *v1alpha1.ReplicatedVolume,
	fds map[string]*failureDomain,
) (reconcile.Result, error) {

	var maxBaseReplicaCount, totalBaseReplicaCount int
	var currentTB int
	for _, fd := range fds {
		fdBaseReplicaCount := fd.baseReplicaCount()
		maxBaseReplicaCount = max(maxBaseReplicaCount, fdBaseReplicaCount)
		totalBaseReplicaCount += fdBaseReplicaCount
		currentTB += fd.tbReplicaCount()
	}

	var desiredTB int
	for _, fd := range fds {
		baseReplicaCountDiffFromMax := maxBaseReplicaCount - fd.baseReplicaCount()
		if baseReplicaCountDiffFromMax >= 2 {
			desiredTB += baseReplicaCountDiffFromMax - 1
		}
	}
	if (totalBaseReplicaCount+desiredTB)%2 == 0 {
		desiredTB++
	}

	if currentTB == desiredTB {
		log.Info("No need to change")
		return reconcile.Result{}, nil
	}

	for i := range desiredTB - currentTB {
		// to create
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

	if currentTB-desiredTB > 0 {
		// delete 1 TB per reconcile, starting from tbs ... TODO
	}

	for i := range currentTB - desiredTB {
		rvr := tbs[i]
		if err := r.cl.Delete(ctx, rvr); client.IgnoreNotFound(err) != nil {
			return reconcile.Result{}, err
		}
		log.Info(fmt.Sprintf("deleted rvr %d/%d", i+1, desiredTB-currentTB), "deletedRVR", rvr.Name)
	}

	return reconcile.Result{}, nil
}

func CalculateDesiredTieBreakerTotal(fdReplicaCount map[string]int) (int, error) {
	fdCount := len(fdReplicaCount)

	if fdCount <= 1 {
		return 0, nil
	}

	totalBaseReplicas := 0
	for _, v := range fdReplicaCount {
		totalBaseReplicas += v
	}
	if totalBaseReplicas == 0 {
		return 0, nil
	}

	// TODO: tbCount <= totalBaseReplicas is not the best approach, need to rework later
	for tbCount := 0; tbCount <= totalBaseReplicas; tbCount++ {
		if IsThisTieBreakerCountEnough(fdReplicaCount, fdCount, totalBaseReplicas, tbCount) {
			return tbCount, nil
		}
	}

	return 0, nil
}

func IsThisTieBreakerCountEnough(
	fdReplicaCount map[string]int,
	fdCount int,
	totalBaseReplicas int,
	tieBreakerCount int,
) bool {
	totalReplicas := totalBaseReplicas + tieBreakerCount
	if totalReplicas%2 == 0 {
		return false
	}

	/*
		example:
		totalReplicas 7
		fdCount 3
	*/

	replicasPerFDMin := totalReplicas / fdCount // 7/3 = 2 (+ 1 remains (modulo))
	if replicasPerFDMin == 0 {
		replicasPerFDMin = 1
	}
	maxFDsWithExtraReplica := totalReplicas % fdCount // 1 (modulo)

	/*
		This method takes the actual state of the replica distribution and attempts to convert it to the desired state

		Desired state of replica distribution, calculated from totalReplicas (example):
		fd 1: [replica] [replica]
		fd 2: [replica] [replica]
		fd 3: [replica] [replica]   *[extra replica]*

		maxFDsWithExtraReplica == 1 means that 1 of these fds take an extra replica

		Actual state (example):
		FDReplicaCount {
			"1" : 3
			"2" : 2
			"3" : 1
		}

		Desired state can be achieved:
		FDReplicaCount {
			"1" : 3 (+0) = 2
			"2" : 2 (+0) = 2
			"3" : 1 (+1) = 3
		}
	*/

	fdsAlreadyAboveMin := 0 // how many FDs have min+1 replica
	for _, replicasAlreadyInFD := range fdReplicaCount {
		delta := replicasAlreadyInFD - replicasPerFDMin

		if delta > 1 {
			return false
		}

		if delta == 1 {
			fdsAlreadyAboveMin++
		}
	}

	// we expext fdsWithMaxReplicaPossible (which ew calculated just now) to be
	// not more then we predicted earlier (maxFDsWithExtraReplica)
	if fdsAlreadyAboveMin > maxFDsWithExtraReplica {
		return false
	}

	return true
}

func logError(log logr.Logger, err error) error {
	if err != nil {
		log.Error(err, err.Error())
		return err
	}
	return nil
}
