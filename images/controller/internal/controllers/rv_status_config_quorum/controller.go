package rvrdiskfulcount // TODO change package if need

import (
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

func BuildController(mgr manager.Manager) error {

	// TODO issues/333 your global dependencies
	var rec = &Reconciler{
		cl:  mgr.GetClient(),
		rdr: mgr.GetAPIReader(),
		sch: mgr.GetScheme(),
		log: mgr.GetLogger().WithName("controller_rv_status_config_quorum"),
	}

	type TReq = Request
	type TQueue = workqueue.TypedRateLimitingInterface[TReq]

	return builder.ControllerManagedBy(mgr).
		Named("rv_status_config_quorum_controller").
		For(&v1alpha3.ReplicatedVolume{}).
		Watches(
			&v1alpha3.ReplicatedVolumeReplica{},
			handler.EnqueueRequestForOwner(
				mgr.GetScheme(),
				mgr.GetRESTMapper(),
				&v1alpha3.ReplicatedVolume{}),
		).
		Complete(rec)
}
