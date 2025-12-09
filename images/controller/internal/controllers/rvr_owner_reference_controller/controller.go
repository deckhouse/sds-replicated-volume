package rvrownerreferencecontroller

import (
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

func BuildController(mgr manager.Manager) error {
	nameController := "rvr_owner_reference_controller"

	r := &Reconciler{
		cl:     mgr.GetClient(),
		log:    mgr.GetLogger().WithName(nameController).WithName("Reconciler"),
		scheme: mgr.GetScheme(),
	}

	return builder.ControllerManagedBy(mgr).
		Named(nameController).
		For(&v1alpha3.ReplicatedVolumeReplica{}).
		Complete(r)
}
