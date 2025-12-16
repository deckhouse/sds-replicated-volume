package rvr_scheduling_controller

import (
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

const controllerName = "rvr-scheduling-controller"

func BuildController(mgr manager.Manager) error {
	r := NewReconciler(
		mgr.GetClient(),
		mgr.GetLogger().WithName(controllerName).WithName("Reconciler"),
		mgr.GetScheme(),
	)

	return builder.ControllerManagedBy(mgr).
		Named(controllerName).
		For(&v1alpha3.ReplicatedVolume{}).
		Watches(
			&v1alpha3.ReplicatedVolumeReplica{},
			handler.EnqueueRequestForOwner(mgr.GetScheme(), mgr.GetRESTMapper(), &v1alpha3.ReplicatedVolume{}),
		).
		Complete(r)
}
