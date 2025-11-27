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

	var rvrList v1alpha3.ReplicatedVolumeReplicaList
	if err := r.cl.List(ctx, &rvrList); err != nil {
		log.Error(err, "unable to fetch ReplicatedVolumeReplicaList")
		return reconcile.Result{}, err
	}

	rvrList.Items = slices.DeleteFunc(rvrList.Items, func(rvr v1alpha3.ReplicatedVolumeReplica) bool {
		return !metav1.IsControlledBy(&rvr, &rv)
	})

	if conditions.IsTrue(rv.Status, v1alpha3.ConditionTypeDiskfulReplicaCountReached) &&
		conditions.IsTrue(rv.Status, v1alpha3.ConditionTypeAllReplicasReady) &&
		conditions.IsTrue(rv.Status, v1alpha3.ConditionTypeSharedSecretAlgorithmSelected) {

		diskfulCount, all := countDiskfulAndDisklessReplicas(&rvrList)

		log.Info("calculated replica counts", "diskful", diskfulCount, "all", all)

		// ensure status structs are initialized before writing into them
		if rv.Status == nil {
			rv.Status = &v1alpha3.ReplicatedVolumeStatus{}
		}
		if rv.Status.Config == nil {
			rv.Status.Config = &v1alpha3.DRBDResourceConfig{}
		}

		var quorum byte
		var qmr byte
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
			Reason:             "QuorumConfigured",
			Message:            "Quorum configuration completed",
		})

		from := client.MergeFrom(old)
		if err := r.cl.Patch(ctx, &rv, from); err != nil {
			log.Error(err, "unable to fetch ReplicatedVolume")
			return reconcile.Result{}, client.IgnoreNotFound(err)
		}
	}

	return reconcile.Result{}, nil
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
