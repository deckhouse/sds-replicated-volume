package rvrdiskfulcount // TODO change package if need

import (
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

func BuildController(mgr manager.Manager) error {

	rec := &Reconciler{
		cl:  mgr.GetClient(),
		rdr: mgr.GetAPIReader(),
		sch: mgr.GetScheme(),
		log: mgr.GetLogger().WithName("controller_rv_status_config_quorum"),
	}

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
