package rvrstatusconfignodeid

import (
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

func BuildController(mgr manager.Manager) error {
	rec := NewReconciler(
		mgr.GetClient(),
		mgr.GetLogger().WithName(RVRStatusConfigNodeIDControllerName).WithName("Reconciler"),
	)

	return builder.ControllerManagedBy(mgr).
		Named(RVRStatusConfigNodeIDControllerName).
		For(&v1alpha3.ReplicatedVolumeReplica{}).
		Complete(rec)
}
