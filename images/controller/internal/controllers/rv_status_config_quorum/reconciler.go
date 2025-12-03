package rvrdiskfulcount

import (
	"context"
	"slices"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

// QuorumReconfFinalizer is the name of the finalizer used to manage quorum reconfiguration.
const QuorumReconfFinalizer = "quorum-reconf"

// CalculateQuorum calculates quorum and quorum minimum redundancy values
// based on the number of diskful and total replicas.
func CalculateQuorum(diskfulCount, all int) (quorum, qmr byte) {
	if diskfulCount > 1 {
		quorum = byte(max(2, all/2+1))
		qmr = byte(max(2, diskfulCount/2+1))
	}
	return
}

func isRvReady(rvStatus *v1alpha3.ReplicatedVolumeStatus) bool {
	return conditions.IsTrue(rvStatus, v1alpha3.ConditionTypeDiskfulReplicaCountReached) &&
		conditions.IsTrue(rvStatus, v1alpha3.ConditionTypeAllReplicasReady) &&
		conditions.IsTrue(rvStatus, v1alpha3.ConditionTypeSharedSecretAlgorithmSelected)
}

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

	// Finding out deleted from owned
	deletedRVRList := slices.DeleteFunc(
		slices.Clone(rvrList.Items),
		func(rvr v1alpha3.ReplicatedVolumeReplica) bool {
			return rvr.DeletionTimestamp == nil
		},
	)

	// Keeping only without deletion timestamp
	rvrList.Items = slices.DeleteFunc(
		rvrList.Items,
		func(rvr v1alpha3.ReplicatedVolumeReplica) bool {
			return rvr.DeletionTimestamp != nil
		},
	)

	diskfulCount := 0
	for _, rvr := range rvrList.Items {
		if rvr.Spec.Type == "Diskful" { // TODO: Replace with api function
			diskfulCount++
		}
	}

	log = log.WithValues("diskful", diskfulCount, "all", len(rvrList.Items))
	log.V(1).Info("calculated replica counts")

	// adding finalizers
	for i := range rvrList.Items {
		rvr := &rvrList.Items[i]
		log := log.WithValues("rvr", rvr.Name)

		if slices.Contains(rvr.Finalizers, QuorumReconfFinalizer) {
			log.V(2).Info("finalizer already present, skipping")
			continue
		}

		from := client.MergeFrom(rvr.DeepCopy())
		// Load the object fresh for PatchWithConflictRetry
		rvr.Finalizers = append(rvr.Finalizers, QuorumReconfFinalizer)
		if err := r.cl.Patch(ctx, rvr, from); err != nil {
			log.Error(err, "patching ReplicatedVolumeReplica")
			return reconcile.Result{}, err
		}

		log.V(1).Info("finalizer added")
	}

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

	// removing finalizers from deleted replicas
	for i := range deletedRVRList {
		rvr := &deletedRVRList[i]
		log := log.WithValues("rvr", rvr.Name)

		if !slices.Contains(rvr.Finalizers, QuorumReconfFinalizer) {
			log.V(2).Info("Finalizer is not set. Nothing to update")
			continue
		}

		from := client.MergeFrom(rvr.DeepCopy())
		rvr.Finalizers = slices.DeleteFunc(rvr.Finalizers, func(s string) bool {
			return s == QuorumReconfFinalizer
		})

		if err := r.cl.Patch(ctx, rvr, from); err != nil {
			log.Error(err, "patching ReplicatedVolumeReplica")
			return reconcile.Result{}, err
		}

		log.V(1).Info("finalizer removed")
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

	if !conditions.IsTrue(rvStatus, v1alpha3.ConditionTypeQuorumConfigured) {
		conditions.Set(rvStatus, metav1.Condition{
			Type:    v1alpha3.ConditionTypeQuorumConfigured,
			Status:  metav1.ConditionTrue,
			Reason:  "QuorumConfigured", // TODO: change reason
			Message: "Quorum configuration completed",
		})
		changed = true
	}
	return changed
}
