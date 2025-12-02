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
	"strconv"

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
	// get target ReplicatedVolume
	rv := &v1alpha3.ReplicatedVolume{}
	if err := r.cl.Get(ctx, req.NamespacedName, rv); err != nil {
		log.Error(err, "Can't get ReplicatedVolume")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// controller logic depends on ReplicatedStorageClass policy; skip if not set
	if rv.Spec.ReplicatedStorageClassName == "" {
		log.Info("Empty ReplicatedStorageClassName")
		return reconcile.Result{}, nil
	}

	// get ReplicatedStorageClass to read replication and topology settings
	rsc := &v1alpha1.ReplicatedStorageClass{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rv.Spec.ReplicatedStorageClassName}, rsc); err != nil {
		log.Error(err, "Can't get ReplicatedStorageClass")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// list Nodes to build nodeName -> FD key map according to rsc.Spec.Topology
	nodes := &corev1.NodeList{}
	if err := r.cl.List(ctx, nodes); err != nil {
		return reconcile.Result{}, err
	}

	FDKeyByNodeName := make(map[string]string)
	for _, node := range nodes.Items {
		log := log.WithValues("node", node.Name)
		if rsc.Spec.Topology == "TransZonal" {
			zone, ok := node.Labels[NodeZoneLabel]
			if !ok {
				log.Error(ErrNoZoneLabel, "No zone label")
				return reconcile.Result{}, fmt.Errorf("%w: node is %s", ErrNoZoneLabel, node.Name)
			}
			FDKeyByNodeName[node.Name] = zone + "/" + node.Name
		} else {
			FDKeyByNodeName[node.Name] = node.Name
		}
	}

	rvrList := &v1alpha3.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, rvrList); err != nil {
		log.Error(err, "Can't List ReplicatedVolumeReplicaList")
		return reconcile.Result{}, err
	}

	rvrList.Items = slices.DeleteFunc(rvrList.Items, func(rvr v1alpha3.ReplicatedVolumeReplica) bool {
		return rv.Name != rvr.Spec.ReplicatedVolumeName || !rvr.DeletionTimestamp.IsZero()
	})

	// aggregate base replicas (Diskful+Access) per FD and collect existing TieBreaker replicas
	FDReplicaCount := make(map[string]int, len(FDKeyByNodeName))
	var diskfulCount int
	var tieBreakerCurrent []*v1alpha3.ReplicatedVolumeReplica

	for _, rvr := range rvrList.Items {
		switch rvr.Spec.Type {
		case "Diskful":
			diskfulCount++
			if rvr.Spec.NodeName != "" {
				if fd, ok := FDKeyByNodeName[rvr.Spec.NodeName]; ok {
					FDReplicaCount[fd]++
				} // TODO: записать 0 если нет
			}
		case "Access":
			if rvr.Spec.NodeName != "" {
				if fd, ok := FDKeyByNodeName[rvr.Spec.NodeName]; ok {
					FDReplicaCount[fd]++
				}
			}
		case "TieBreaker":
			tieBreakerCurrent = append(tieBreakerCurrent, &rvr)
		}
	}

	desiredTB, err := DesiredTieBreakerTotal(FDReplicaCount)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("calculate desired tie breaker count: %w", err)
	}

	// for Replication=Availability with at least two Diskful replicas,
	// compute the minimal required number of TieBreakers based on FD distribution
	// if rsc.Spec.Replication == "Availability" && diskfulCount == 2 {
	// 	desiredTB = desiredTieBreakerTotal(FDReplicaCount)
	// }

	currentTB := len(tieBreakerCurrent)

	// if the current number of TieBreaker replicas already matches the desired one, nothing to change
	if currentTB == desiredTB {
		log.Info("No need to change")
		return reconcile.Result{}, nil
	}

	// when there are fewer TieBreakers than required, create the missing ones
	if currentTB < desiredTB {
		if r.scheme == nil {
			return reconcile.Result{}, fmt.Errorf("reconciler scheme is nil")
		}

		toCreate := desiredTB - currentTB
		for i := 0; i < toCreate; i++ {
			rvr := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name:       rv.Name + "-tiebreaker-" + strconv.Itoa(i),
					Finalizers: []string{rvreconcile.ControllerFinalizerName},
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

	// when there are more TieBreakers than required, delete the excess ones
	toDelete := currentTB - desiredTB
	for i := 0; i < toDelete; i++ {
		rvr := tieBreakerCurrent[i]
		if err := r.cl.Delete(ctx, rvr); client.IgnoreNotFound(err) != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func DesiredTieBreakerTotal(FDReplicaCount map[string]int) (int, error) {
	// number of distinct failure domains
	fdCount := len(FDReplicaCount)

	// function is only guaranteed to work for fdCount < 4
	if fdCount >= 4 {
		return 0, fmt.Errorf("DesiredTieBreakerTotal is only supported for less than 4 failure domains, got %d", fdCount)
	}

	// if there is only one (or zero) failure domain, TieBreakers are not useful
	if fdCount <= 1 {
		return 0, nil
	}

	// count how many base (non-TieBreaker) replicas we already have across all FDs
	totalBaseReplicas := 0
	for _, v := range FDReplicaCount {
		totalBaseReplicas += v
	}
	// no base replicas means nothing to protect with TieBreakers
	if totalBaseReplicas == 0 {
		return 0, nil
	}

	// search over TieBreakerCount (number of TieBreakers to add) starting from 0:
	// we look for the minimal TieBreakerCount such that:
	// - totalReplicas = totalBaseReplicas + TieBreakersCount is odd
	// - replicas can be distributed over FDs with max(FD) - min(FD) <= 1
	// bound TieBreakersCount by fdCount: more than one TieBreaker per FD would not be minimal
	for tieBreakerCount := 0; tieBreakerCount <= fdCount; tieBreakerCount++ {
		// if totalReplicas is even, it's not a valid solution
		totalReplicas := totalBaseReplicas + tieBreakerCount
		if totalReplicas%2 == 0 {
			continue
		}

		// per-FD replica range [minReplicasPerFD, minReplicasPerFD+1] with at most fdsWithOneMoreReplica FDs at the upper bound
		minReplicasPerFD := totalReplicas / fdCount
		fdsWithOneMoreReplica := totalReplicas % fdCount

		fdsWithExtraReplica := 0
		ok := true

		// verify that base replicas can fit into [minReplicasPerFD, minReplicasPerFD+1] for each FD
		for _, baseReplicasInFD := range FDReplicaCount {
			// if a FD already has more than minReplicasPerFD+1 base replicas,
			// then even the "high" bucket cannot accommodate it for this totalReplicas
			if baseReplicasInFD > minReplicasPerFD+1 {
				ok = false
				break
			}
			// if a FD has strictly more than minReplicasPerFD base replicas,
			// it must be assigned to the "high" bucket (minReplicasPerFD+1)
			if baseReplicasInFD > minReplicasPerFD {
				fdsWithExtraReplica++
			}
		}

		if !ok {
			continue
		}

		// we cannot assign more than fdsWithOneMoreReplica FDs to the "high" bucket minReplicasPerFD+1
		if fdsWithExtraReplica > fdsWithOneMoreReplica {
			continue
		}

		// tieBreakerCount is the minimal number of TieBreakers needed
		return tieBreakerCount, nil
	}

	// fall-back: do not add TieBreakers if no suitable distribution was found
	return 0, nil
}
