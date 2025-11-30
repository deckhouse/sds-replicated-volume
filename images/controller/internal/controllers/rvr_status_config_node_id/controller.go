package rvrstatusconfignodeid

import (
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

func BuildController(mgr manager.Manager) error {
	r := &Reconciler{
		cl:  mgr.GetClient(),
		log: mgr.GetLogger().WithName(RVRStatusConfigNodeIDControllerName).WithName("Reconciler"),
	}

	return builder.ControllerManagedBy(mgr).
		Named(RVRStatusConfigNodeIDControllerName).
		For(&v1alpha3.ReplicatedVolumeReplica{}).
		Complete(r)
}
