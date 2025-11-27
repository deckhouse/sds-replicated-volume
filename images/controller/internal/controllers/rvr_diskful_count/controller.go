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
	h := &Handler{
		cl:  mgr.GetClient(),
		log: mgr.GetLogger().WithName(nameController).WithName("Handler"),
	}

	return builder.ControllerManagedBy(mgr).
		Named(nameController).
		Watches(
			&v1alpha3.ReplicatedVolumeReplica{},
			handler.EnqueueRequestsFromMapFunc(h.HandleReplicatedVolumeReplica)).
		Watches(
			&v1alpha3.ReplicatedVolume{},
			handler.EnqueueRequestsFromMapFunc(h.HandleReplicatedVolume)).
		Complete(r)
}
