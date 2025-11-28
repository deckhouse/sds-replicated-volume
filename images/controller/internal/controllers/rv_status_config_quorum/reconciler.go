package rvrdiskfulcount

import (
	"context"
	"slices"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// QuorumReconfFinalizer is the name of the finalizer used to manage quorum reconfiguration.
const QuorumReconfFinalizer = "quorum-reconf"

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
		if !rvr.Spec.Diskless {
			diskful++
		}
	}
	return
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
	for _, rvr := range rvrList.Items {
		if !slices.Contains(rvr.Finalizers, QuorumReconfFinalizer) {
			oldRvr := rvr.DeepCopy()
			rvr.Finalizers = append(rvr.Finalizers, QuorumReconfFinalizer)
			from := client.MergeFrom(oldRvr)
			if err := r.cl.Patch(*ctx, &rvr, from); err != nil {
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
	for _, rvr := range rvrList.Items {
		if slices.Contains(rvr.Finalizers, QuorumReconfFinalizer) && rvr.GetObjectMeta().GetDeletionTimestamp() != nil {
			oldRvr := rvr.DeepCopy()
			rvr.Finalizers = slices.DeleteFunc(rvr.Finalizers, func(f string) bool {
				return f == QuorumReconfFinalizer
			})
			from := client.MergeFrom(oldRvr)
			if err := r.cl.Patch(*ctx, &rvr, from); err != nil {
				return cnt, err
			}
			cnt++
		}
	}
	return cnt, nil
}

// prepareQuorumPatch calculates quorum fields and returns a MergeFrom patch baseline.
func (r *Reconciler) quorumPatch(
	ctx *context.Context,
	rv *v1alpha3.ReplicatedVolume,
	diskfulCount,
	all int,
) error {
	// ensure status structs are initialized before writing into them
	if rv.Status == nil {
		rv.Status = &v1alpha3.ReplicatedVolumeStatus{}
	}
	if rv.Status.Config == nil {
		rv.Status.Config = &v1alpha3.DRBDResourceConfig{}
	}

	var quorum, qmr byte
	if diskfulCount > 1 {
		quorum = byte(max(2, all/2+1))
		qmr = byte(max(2, diskfulCount/2+1))
	}

	// capture the original object state for a merge patch before making any changes
	old := rv.DeepCopy()
	old.Status.Config.Quorum = quorum
	old.Status.Config.QuorumMinimumRedundancy = qmr
	old.Status.Conditions = append(old.Status.Conditions, metav1.Condition{
		Type:               v1alpha3.ConditionTypeQuorumConfigured,
		Status:             metav1.ConditionTrue,
		LastTransitionTime: metav1.Now(),
		Reason:             "QuorumConfigured", // TODO: change reason
		Message:            "Quorum configuration completed",
	})

	from := client.MergeFrom(old)
	if err := r.cl.Patch(*ctx, rv, from); err != nil {
		return client.IgnoreNotFound(err)
	}
	return nil
}
