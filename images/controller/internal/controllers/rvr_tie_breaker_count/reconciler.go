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
	"log/slog"
	"strconv"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	e "github.com/deckhouse/sds-replicated-volume/images/controller/internal/errors"
	rvreconcile "github.com/deckhouse/sds-replicated-volume/images/controller/internal/reconcile/rv"
)

const (
	nodeZoneLabel = "topology.kubernetes.io/zone"
)

type Reconciler struct {
	cl     client.Client
	rdr    client.Reader
	sch    *runtime.Scheme
	log    *slog.Logger
	logAlt logr.Logger
}

var _ reconcile.TypedReconciler[Request] = &Reconciler{}

type fdKey string // fd stands for 'Failure Domain'

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req Request,
) (reconcile.Result, error) {
	switch typed := req.(type) {
	case RecalculateRequest:
		var err error
		err = r.reconcileRecalculate(ctx, typed)
		return reconcile.Result{}, err
	default:
		return reconcile.Result{}, e.ErrUnknownf("unknown request type %T", req)
	}
}

func (r *Reconciler) reconcileRecalculate(
	ctx context.Context,
	req RecalculateRequest,
) error {
	// get target ReplicatedVolume
	rv := &v1alpha3.ReplicatedVolume{}
	if err := r.rdr.Get(ctx, client.ObjectKey{Name: req.VolumeName}, rv); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return e.ErrUnknownf("get rv %q: %w", req.VolumeName, err)
	}

	// controller logic depends on ReplicatedStorageClass policy; skip if not set
	if rv.Spec.ReplicatedStorageClassName == "" {
		return nil
	}

	// get ReplicatedStorageClass to read replication and topology settings
	rsc := &v1alpha1.ReplicatedStorageClass{}
	if err := r.rdr.Get(ctx, client.ObjectKey{Name: rv.Spec.ReplicatedStorageClassName}, rsc); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return e.ErrUnknownf("get replicatedStorageClass %q: %w", rv.Spec.ReplicatedStorageClassName, err)
	}

	// list Nodes to build nodeName -> FD key map according to rsc.Spec.Topology
	nodes := &corev1.NodeList{}
	if err := r.rdr.List(ctx, nodes); err != nil {
		return e.ErrUnknownf("list nodes: %w", err)
	}

	FDKeyByNodeName := map[string]fdKey{}
	for _, node := range nodes.Items {
		if rsc.Spec.Topology == "TransZonal" {
			zone := node.Labels[nodeZoneLabel]
			FDKeyByNodeName[node.Name] = fdKey(zone + "/" + node.Name)
		} else {
			FDKeyByNodeName[node.Name] = fdKey(node.Name)
		}
	}

	rvrList := &v1alpha3.ReplicatedVolumeReplicaList{}
	if err := r.rdr.List(ctx, rvrList); err != nil {
		return e.ErrUnknownf("list replicatedVolumeReplicas: %w", err)
	}

	// aggregate base replicas (Diskful+Access) per FD and collect existing TieBreaker replicas
	FDReplicaCount := map[fdKey]int{}
	var diskfulCount int
	var tieBreakerCurrent []*v1alpha3.ReplicatedVolumeReplica

	for _, rvr := range rvrList.Items {
		// only replicas belonging to this RV and not being deleted are considered
		if rvr.Spec.ReplicatedVolumeName != rv.Name {
			continue
		}
		if !rvr.DeletionTimestamp.IsZero() {
			continue
		}

		switch rvr.Spec.Type {
		case "Diskful":
			diskfulCount++
			if rvr.Spec.NodeName != "" {
				if fd, ok := FDKeyByNodeName[rvr.Spec.NodeName]; ok {
					FDReplicaCount[fd]++
				}
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

	desiredTB := desiredTieBreakerTotal(FDReplicaCount)

	// for Replication=Availability with at least two Diskful replicas,
	// compute the minimal required number of TieBreakers based on FD distribution
	// if rsc.Spec.Replication == "Availability" && diskfulCount == 2 {
	// 	desiredTB = desiredTieBreakerTotal(FDReplicaCount)
	// }

	currentTB := len(tieBreakerCurrent)

	// if the current number of TieBreaker replicas already matches the desired one, nothing to change
	if currentTB == desiredTB {
		return nil
	}

	// when there are fewer TieBreakers than required, create the missing ones
	if currentTB < desiredTB {
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

			if err := controllerutil.SetControllerReference(rv, rvr, r.sch); err != nil {
				return e.ErrUnknownf("set controller reference for rvr: %w", err)
			}

			if err := r.cl.Create(ctx, rvr); err != nil {
				if apierrors.IsAlreadyExists(err) {
					continue
				}
				return e.ErrUnknownf("create rvr: %w", err)
			}
		}
		return nil
	}

	// when there are more TieBreakers than required, delete the excess ones
	toDelete := currentTB - desiredTB
	for i := 0; i < toDelete; i++ {
		rvr := tieBreakerCurrent[i]
		if err := r.cl.Delete(ctx, rvr); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return e.ErrUnknownf("delete rvr %q: %w", rvr.Name, err)
		}
	}

	return nil
}

func desiredTieBreakerTotal(FDReplicaCount map[fdKey]int) int {
	// number of distinct failure domains
	fdCount := len(FDReplicaCount)

	// if there is only one (or zero) failure domain, TieBreakers are not useful
	if fdCount <= 1 {
		return 0
	}

	// count how many base (non-TieBreaker) replicas we already have across all FDs
	totalBaseReplicas := 0
	for _, v := range FDReplicaCount {
		totalBaseReplicas += v
	}
	// no base replicas means nothing to protect with TieBreakers
	if totalBaseReplicas == 0 {
		return 0
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
		return tieBreakerCount
	}

	// fall-back: do not add TieBreakers if no suitable distribution was found
	return 0
}
