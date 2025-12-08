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

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	rvreconcile "github.com/deckhouse/sds-replicated-volume/images/controller/internal/reconcile/rv"
)

const (
	NodeZoneLabel = "topology.kubernetes.io/zone"
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

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	log := r.log.WithName("Reconcile").WithValues("request", req)
	rv, err := r.getReplicatedVolume(ctx, req, log)
	if err != nil {
		return reconcile.Result{}, err
	}
	if rv == nil {
		return reconcile.Result{}, nil
	}

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

	NodeNameToFdMap, err := r.GetNodeNameToFdMap(ctx, rsc, log)
	if err != nil {
		return reconcile.Result{}, err
	}

	replicasForRVList, err := r.listReplicasForRV(ctx, rv, log)
	if err != nil {
		return reconcile.Result{}, err
	}

	FDToReplicaCountMap, existingTieBreakers := aggregateReplicas(NodeNameToFdMap, replicasForRVList, rsc)

	return r.syncTieBreakers(ctx, rv, FDToReplicaCountMap, existingTieBreakers, log)
}

func (r *Reconciler) getReplicatedVolume(
	ctx context.Context,
	req reconcile.Request,
	log logr.Logger,
) (*v1alpha3.ReplicatedVolume, error) {
	rv := &v1alpha3.ReplicatedVolume{}
	if err := r.cl.Get(ctx, req.NamespacedName, rv); err != nil {
		log.Error(err, "Can't get ReplicatedVolume")
		if client.IgnoreNotFound(err) == nil {
			return nil, nil
		}
		return nil, err
	}
	return rv, nil
}

func shouldSkipRV(rv *v1alpha3.ReplicatedVolume, log logr.Logger) bool {
	if rv.Spec.ReplicatedStorageClassName == "" {
		log.Info("Empty ReplicatedStorageClassName")
		return true
	}
	return false
}

func (r *Reconciler) getReplicatedStorageClass(
	ctx context.Context,
	rv *v1alpha3.ReplicatedVolume,
	log logr.Logger,
) (*v1alpha1.ReplicatedStorageClass, error) {
	rsc := &v1alpha1.ReplicatedStorageClass{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rv.Spec.ReplicatedStorageClassName}, rsc); err != nil {
		log.Error(err, "Can't get ReplicatedStorageClass")
		if client.IgnoreNotFound(err) == nil {
			return nil, nil
		}
		return nil, err
	}
	return rsc, nil
}

func (r *Reconciler) GetNodeNameToFdMap(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
	log logr.Logger,
) (map[string]string, error) {
	nodes := &corev1.NodeList{}
	if err := r.cl.List(ctx, nodes); err != nil {
		return nil, err
	}

	NodeNameToFdMap := make(map[string]string)
	for _, node := range nodes.Items {
		nodeLog := log.WithValues("node", node.Name)
		if rsc.Spec.Topology == "TransZonal" {
			zone, ok := node.Labels[NodeZoneLabel]
			if !ok {
				nodeLog.Error(ErrNoZoneLabel, "No zone label")
				return nil, fmt.Errorf("%w: node is %s", ErrNoZoneLabel, node.Name)
			}

			if slices.Contains(rsc.Spec.Zones, zone) {
				NodeNameToFdMap[node.Name] = zone
			}

		} else {
			NodeNameToFdMap[node.Name] = node.Name
		}
	}

	return NodeNameToFdMap, nil
}

func (r *Reconciler) listReplicasForRV(
	ctx context.Context,
	rv *v1alpha3.ReplicatedVolume,
	log logr.Logger,
) ([]v1alpha3.ReplicatedVolumeReplica, error) {
	rvrList := &v1alpha3.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, rvrList); err != nil {
		log.Error(err, "Can't List ReplicatedVolumeReplicaList")
		return nil, err
	}

	replicasForRV := slices.DeleteFunc(rvrList.Items, func(rvr v1alpha3.ReplicatedVolumeReplica) bool {
		return rv.Name != rvr.Spec.ReplicatedVolumeName || !rvr.DeletionTimestamp.IsZero()
	})

	return replicasForRV, nil
}

func aggregateReplicas(
	NodeNameToFdMap map[string]string,
	replicasForRVList []v1alpha3.ReplicatedVolumeReplica,
	rsc *v1alpha1.ReplicatedStorageClass,
) (map[string]int, []*v1alpha3.ReplicatedVolumeReplica) {
	FDToReplicaCountMap := make(map[string]int, len(NodeNameToFdMap))

	for _, zone := range rsc.Spec.Zones {
		if _, ok := FDToReplicaCountMap[zone]; !ok {
			FDToReplicaCountMap[zone] = 0
		}
	}

	var existingTieBreakersList []*v1alpha3.ReplicatedVolumeReplica

	for _, rvr := range replicasForRVList {
		switch rvr.Spec.Type {
		case "Diskful", "Access":
			if rvr.Spec.NodeName != "" {
				if fd, ok := NodeNameToFdMap[rvr.Spec.NodeName]; ok {
					FDToReplicaCountMap[fd]++
				}
			}
		case "TieBreaker":
			existingTieBreakersList = append(existingTieBreakersList, &rvr)
		}
	}

	return FDToReplicaCountMap, existingTieBreakersList
}

func (r *Reconciler) syncTieBreakers(
	ctx context.Context,
	rv *v1alpha3.ReplicatedVolume,
	FDToReplicaCountMap map[string]int,
	existingTieBreakersList []*v1alpha3.ReplicatedVolumeReplica,
	log logr.Logger,
) (reconcile.Result, error) {
	desiredTB, err := CalculateDesiredTieBreakerTotal(FDToReplicaCountMap)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("calculate desired tie breaker count: %w", err)
	}

	currentTB := len(existingTieBreakersList)

	if currentTB == desiredTB {
		log.Info("No need to change")
		return reconcile.Result{}, nil
	}

	if currentTB < desiredTB {
		if r.scheme == nil {
			return reconcile.Result{}, fmt.Errorf("reconciler scheme is nil")
		}

		toCreate := desiredTB - currentTB
		for i := 0; i < toCreate; i++ {
			rvr := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: rv.Name + "-tiebreaker-",
					Finalizers:   []string{rvreconcile.ControllerFinalizerName},
				},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: rv.Name,
					Type:                 "TieBreaker",
				},
			}

			if err := controllerutil.SetControllerReference(rv, rvr, r.scheme); err != nil {
				return reconcile.Result{}, err
			}

			if err := r.cl.Create(ctx, rvr); err != nil {
				return reconcile.Result{}, err
			}
		}
		return reconcile.Result{}, nil
	}

	toDelete := currentTB - desiredTB
	for i := 0; i < toDelete; i++ {
		rvr := existingTieBreakersList[i]
		if err := r.cl.Delete(ctx, rvr); client.IgnoreNotFound(err) != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func CalculateDesiredTieBreakerTotal(FDReplicaCount map[string]int) (int, error) {
	fdCount := len(FDReplicaCount)

	if fdCount <= 1 {
		return 0, nil
	}

	totalBaseReplicas := 0
	for _, v := range FDReplicaCount {
		totalBaseReplicas += v
	}
	if totalBaseReplicas == 0 {
		return 0, nil
	}

	// TODO: tieBreakerCount <= totalBaseReplicas is not the best approach, need to rework later
	for tieBreakerCount := 0; tieBreakerCount <= totalBaseReplicas; tieBreakerCount++ {
		if IsThisTieBreakerCountEnough(FDReplicaCount, fdCount, totalBaseReplicas, tieBreakerCount) {
			return tieBreakerCount, nil
		}
	}

	return 0, nil
}

func IsThisTieBreakerCountEnough(
	FDReplicaCount map[string]int,
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

	replicasPerFDMin := totalReplicas / fdCount // 2 (и 1 в остатке)
	if replicasPerFDMin == 0 {
		replicasPerFDMin = 1
	}
	maxFDsWithExtraReplica := totalReplicas % fdCount // 1

	/*
		fd 1: [replica] [replica]
		fd 2: [replica] [replica]
		fd 3: [replica] [replica]   *[extra replica]*

		maxFDsWithExtraReplica == 1 means that 1 of these fds take an extra replica
	*/

	fdsAlreadyAboveMin := 0 // how many FDs have min+1 replica

	/*
		FDReplicaCount {
			"1" : 3
			"2" : 2
			"3" : 1
		}
	*/

	for _, replicasAlreadyInFD := range FDReplicaCount {
		// 3 - 2 = 1
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

	// quorum := totalReplicas/2 + 1

	// maxReplicasInFD := replicasPerFDMin
	// if maxFDsWithExtraReplica > 0 {
	// 	maxReplicasInFD = replicasPerFDMin + 1
	// }

	// if totalReplicas-maxReplicasInFD < quorum {
	// 	return false
	// }

	// majoritySize := fdCount/2 + 1
	// minoritySize := fdCount - majoritySize
	// if minoritySize <= 0 {
	// 	return true
	// }

	// kExtra := maxFDsWithExtraReplica
	// if kExtra > minoritySize {
	// 	kExtra = minoritySize
	// }
	// sumLargestMinority := kExtra*(replicasPerFDMin+1) + (minoritySize-kExtra)*replicasPerFDMin
	// if sumLargestMinority >= quorum {
	// 	return false
	// }

	return true
}
