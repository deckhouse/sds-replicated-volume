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

package rvrdiskfulcount

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

type Reconciler struct {
	cl  client.Client
	sch *runtime.Scheme
	log logr.Logger
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

// NewReconciler is a small helper constructor that is primarily useful for tests.
func NewReconciler(
	cl client.Client,
	sch *runtime.Scheme,
	log logr.Logger,
) *Reconciler {
	return &Reconciler{
		cl:  cl,
		sch: sch,
		log: log,
	}
}

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	log := r.log.WithValues("request", req.NamespacedName).WithName("Reconcile")
	log.V(1).Info("Reconciling")

	var rv v1alpha3.ReplicatedVolume
	if err := r.cl.Get(ctx, req.NamespacedName, &rv); err != nil {
		log.Error(err, "unable to fetch ReplicatedVolume")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	if rv.Status == nil {
		log.V(1).Info("No status. Skipping")
		return reconcile.Result{}, nil
	}
	if !isRvReady(rv.Status) {
		log.V(1).Info("not ready for quorum calculations")
		log.V(2).Info("status is", "status", rv.Status)
		return reconcile.Result{}, nil
	}

	var rvrList v1alpha3.ReplicatedVolumeReplicaList
	if err := r.cl.List(ctx, &rvrList); err != nil {
		log.Error(err, "unable to fetch ReplicatedVolumeReplicaList")
		return reconcile.Result{}, err
	}

	// Removing non owned
	rvrList.Items = slices.DeleteFunc(rvrList.Items, func(rvr v1alpha3.ReplicatedVolumeReplica) bool {
		return !metav1.IsControlledBy(&rvr, &rv)
	})

	// TODO: Revisit this in the spec
	// Keeping only without deletion timestamp
	rvrList.Items = slices.DeleteFunc(
		rvrList.Items,
		func(rvr v1alpha3.ReplicatedVolumeReplica) bool {
			return !rvr.DeletionTimestamp.IsZero()
		},
	)

	diskfulCount := 0
	for _, rvr := range rvrList.Items {
		if rvr.Spec.Type == v1alpha3.ReplicaTypeDiskful {
			diskfulCount++
		}
	}

	log = log.WithValues("diskful", diskfulCount, "all", len(rvrList.Items))
	log.V(1).Info("calculated replica counts")

	// updating replicated volume
	from := client.MergeFrom(rv.DeepCopy())
	if updateReplicatedVolumeIfNeeded(rv.Status, diskfulCount, len(rvrList.Items)) {
		log.V(1).Info("Updating quorum")
		if err := r.cl.Status().Patch(ctx, &rv, from); err != nil {
			log.Error(err, "patching ReplicatedVolume status")
			return reconcile.Result{}, err
		}
	} else {
		log.V(2).Info("Nothing to update in ReplicatedVolume")
	}

	return reconcile.Result{}, nil
}

func updateReplicatedVolumeIfNeeded(
	rvStatus *v1alpha3.ReplicatedVolumeStatus,
	diskfulCount,
	all int,
) (changed bool) {
	quorum, qmr := CalculateQuorum(diskfulCount, all)
	if rvStatus.DRBD == nil {
		rvStatus.DRBD = &v1alpha3.DRBDResource{}
	}
	if rvStatus.DRBD.Config == nil {
		rvStatus.DRBD.Config = &v1alpha3.DRBDResourceConfig{}
	}

	changed = rvStatus.DRBD.Config.Quorum != quorum ||
		rvStatus.DRBD.Config.QuorumMinimumRedundancy != qmr

	rvStatus.DRBD.Config.Quorum = quorum
	rvStatus.DRBD.Config.QuorumMinimumRedundancy = qmr

	return changed
}

// CalculateQuorum calculates quorum and quorum minimum redundancy values
// based on the number of diskful and total replicas.
func CalculateQuorum(diskfulCount, all int) (quorum, qmr byte) {
	if diskfulCount > 1 {
		quorum = byte(max(2, all/2+1))

		// TODO: Revisit this logic — QMR should not be set when ReplicatedStorageClass.spec.replication == Availability.
		qmr = byte(max(2, diskfulCount/2+1))
	}
	return
}

// parseDiskfulReplicaCount parses the diskfulReplicaCount string in format "current/desired"
// and returns current and desired counts. Returns (0, 0, error) if parsing fails.
func parseDiskfulReplicaCount(diskfulReplicaCount string) (current, desired int, err error) {
	if diskfulReplicaCount == "" {
		return 0, 0, fmt.Errorf("diskfulReplicaCount is empty")
	}

	parts := strings.Split(diskfulReplicaCount, "/")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid diskfulReplicaCount format: expected 'current/desired', got '%s'", diskfulReplicaCount)
	}

	current, err = strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, fmt.Errorf("failed to parse current count: %w", err)
	}

	desired, err = strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, fmt.Errorf("failed to parse desired count: %w", err)
	}

	return current, desired, nil
}

func isRvReady(rvStatus *v1alpha3.ReplicatedVolumeStatus) bool {
	current, desired, err := parseDiskfulReplicaCount(rvStatus.DiskfulReplicaCount)
	if err != nil {
		return false
	}

	return current >= desired && current > 0 && conditions.IsTrue(rvStatus, v1alpha3.ConditionTypeConfigured)
}
