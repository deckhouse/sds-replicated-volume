package rvrdiskfulcount

import (
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

func BuildController(mgr manager.Manager) error {
	nameController := "rvr_diskful_count_controller"

	r := &Reconciler{
		cl:  mgr.GetClient(),
		log: mgr.GetLogger().WithName(nameController).WithName("Reconciler"),
	}

	return builder.ControllerManagedBy(mgr).
		Named(nameController).
		Watches(
			&v1alpha3.ReplicatedVolumeReplica{},
			handler.EnqueueRequestForOwner(mgr.GetScheme(), mgr.GetRESTMapper(), &v1alpha3.ReplicatedVolume{})).
		For(
			&v1alpha3.ReplicatedVolume{}).
		Complete(r)
}
