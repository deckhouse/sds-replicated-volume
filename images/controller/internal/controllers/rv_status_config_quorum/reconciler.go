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
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/api"
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

func isRvReady(rv *v1alpha3.ReplicatedVolume) bool {
	return conditions.IsTrue(rv.Status, v1alpha3.ConditionTypeDiskfulReplicaCountReached) &&
		conditions.IsTrue(rv.Status, v1alpha3.ConditionTypeAllReplicasReady) &&
		conditions.IsTrue(rv.Status, v1alpha3.ConditionTypeSharedSecretAlgorithmSelected)
}

// countDiskfulAndDisklessReplicas returns the number of diskful and diskless replicas
// among the provided ReplicatedVolumeReplica list.
func countDiskfulAndDisklessReplicas(list *v1alpha3.ReplicatedVolumeReplicaList) (diskful, all int) {
	all = len(list.Items)
	for _, rvr := range list.Items {
		// Type can be "Diskful", "Access", or "TieBreaker"
		// Diskful replicas have Type == "Diskful", others are diskless
		if rvr.Spec.Type == "Diskful" {
			diskful++
		}
	}
	return
}

type Reconciler struct {
	cl  client.Client
	rdr client.Reader
	sch *runtime.Scheme
	log logr.Logger
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

// NewReconciler is a small helper constructor that is primarily useful for tests.
func NewReconciler(
	cl client.Client,
	rdr client.Reader,
	sch *runtime.Scheme,
	log logr.Logger,
) *Reconciler {
	return &Reconciler{
		cl:  cl,
		rdr: rdr,
		sch: sch,
		log: log,
	}
}

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	log := r.log.WithValues("request", req.NamespacedName).WithName("Reconcile")

	var rv v1alpha3.ReplicatedVolume
	if err := r.cl.Get(ctx, req.NamespacedName, &rv); err != nil {
		log.Error(err, "unable to fetch ReplicatedVolume")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	if isRvReady(&rv) {
		if err := r.recalculateQuorum(&ctx, &rv, log); err != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func (r *Reconciler) recalculateQuorum(ctx *context.Context, rv *v1alpha3.ReplicatedVolume, log logr.Logger) error {
	rvrList, err := r.getRvrList(ctx, rv)
	if err != nil {
		log.Error(err, "unable to fetch ReplicatedVolumeReplicaList")
		return err
	}

	diskfulCount, all := countDiskfulAndDisklessReplicas(&rvrList)
	log.Info("calculated replica counts", "diskful", diskfulCount, "all", all)

	cnt, err := r.setFinalizers(ctx, &rvrList)
	log.Info("added finalizers to rvr", "finalizer", QuorumReconfFinalizer, "count", cnt)
	if err != nil {
		log.Error(err, "unable to add finalizers")
		return err
	}

	if err := r.quorumPatch(ctx, rv, diskfulCount, all); err != nil {
		log.Error(err, "unable to fetch ReplicatedVolume")
		return client.IgnoreNotFound(err)
	}

	cnt, err = r.unsetFinalizers(ctx, &rvrList)
	log.Info("remove finalizers from rvr", "finalizer", QuorumReconfFinalizer, "count", cnt)
	if err != nil {
		log.Error(err, "unable to remove finalizers")
		return err
	}
	return nil
}

func (r *Reconciler) getRvrList(ctx *context.Context, rv *v1alpha3.ReplicatedVolume) (v1alpha3.ReplicatedVolumeReplicaList, error) {
	var rvrList v1alpha3.ReplicatedVolumeReplicaList
	if err := r.cl.List(*ctx, &rvrList); err != nil {
		return v1alpha3.ReplicatedVolumeReplicaList{}, err
	}

	rvrList.Items = slices.DeleteFunc(rvrList.Items, func(rvr v1alpha3.ReplicatedVolumeReplica) bool {
		return !metav1.IsControlledBy(&rvr, rv)
	})
	return rvrList, nil
}

func (r *Reconciler) setFinalizers(
	ctx *context.Context,
	rvrList *v1alpha3.ReplicatedVolumeReplicaList,
) (cnt int32, err error) {
	for i := range rvrList.Items {
		rvr := &rvrList.Items[i]
		if !slices.Contains(rvr.Finalizers, QuorumReconfFinalizer) {
			// Load the object fresh for PatchWithConflictRetry
			target := &v1alpha3.ReplicatedVolumeReplica{}
			if err := r.cl.Get(*ctx, client.ObjectKeyFromObject(rvr), target); err != nil {
				return cnt, err
			}
			if err := api.PatchWithConflictRetry(*ctx, r.cl, target, func(rvr *v1alpha3.ReplicatedVolumeReplica) error {
				rvr.Finalizers = append(rvr.Finalizers, QuorumReconfFinalizer)
				return nil
			}); err != nil {
				return cnt, err
			}
			cnt++
		}
	}
	return cnt, nil
}

func (r *Reconciler) unsetFinalizers(
	ctx *context.Context,
	rvrList *v1alpha3.ReplicatedVolumeReplicaList,
) (cnt int32, err error) {
	for i := range rvrList.Items {
		rvr := &rvrList.Items[i]
		// Check condition: must have finalizer and DeletionTimestamp
		target := &v1alpha3.ReplicatedVolumeReplica{}
		if err := r.cl.Get(*ctx, client.ObjectKeyFromObject(rvr), target); err != nil {
			return cnt, err
		}
		if slices.Contains(target.Finalizers, QuorumReconfFinalizer) && target.DeletionTimestamp != nil {
			if err := api.PatchWithConflictRetry(*ctx, r.cl, target, func(rvr *v1alpha3.ReplicatedVolumeReplica) error {
				rvr.Finalizers = slices.DeleteFunc(rvr.Finalizers, func(f string) bool {
					return f == QuorumReconfFinalizer
				})
				return nil
			}); err != nil {
				return cnt, err
			}
			cnt++
		}
	}
	return cnt, nil
}

// quorumPatch calculates quorum fields and patches the status using PatchStatusWithConflictRetry.
func (r *Reconciler) quorumPatch(
	ctx *context.Context,
	rv *v1alpha3.ReplicatedVolume,
	diskfulCount,
	all int,
) error {
	quorum, qmr := CalculateQuorum(diskfulCount, all)

	return api.PatchStatusWithConflictRetry(*ctx, r.cl, rv, func(rv *v1alpha3.ReplicatedVolume) error {
		// ensure status structs are initialized before writing into them
		if rv.Status == nil {
			rv.Status = &v1alpha3.ReplicatedVolumeStatus{}
		}
		if rv.Status.DRBD == nil {
			rv.Status.DRBD = &v1alpha3.DRBDResource{}
		}
		if rv.Status.DRBD.Config == nil {
			rv.Status.DRBD.Config = &v1alpha3.DRBDResourceConfig{}
		}

		rv.Status.DRBD.Config.Quorum = quorum
		rv.Status.DRBD.Config.QuorumMinimumRedundancy = qmr
		rv.Status.Conditions = append(rv.Status.Conditions, metav1.Condition{
			Type:               v1alpha3.ConditionTypeQuorumConfigured,
			Status:             metav1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
			Reason:             "QuorumConfigured", // TODO: change reason
			Message:            "Quorum configuration completed",
		})
		return nil
	})
}
