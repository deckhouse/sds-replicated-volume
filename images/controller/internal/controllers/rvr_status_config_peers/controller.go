package rvrdiskfulcount

import (
	"log/slog"

	u "github.com/deckhouse/sds-common-lib/utils"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	e "github.com/deckhouse/sds-replicated-volume/images/controller/internal/errors"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

func BuildController(mgr manager.Manager) error {
	r := &Reconciler{
		cl:  mgr.GetClient(),
		log: mgr.GetLogger().WithName("rvr_status_config_peers_controller").WithName("Reconciler"),
	}
	h := &Handler{
		cl:  mgr.GetClient(),
		log: mgr.GetLogger().WithName("rvr_status_config_peers_controller").WithName("Handler"),
	}

	return builder.ControllerManagedBy(mgr).
		Named("rvr_status_config_peers_controller").
		Watches(
			&v1alpha3.ReplicatedVolumeReplica{},
			handler.EnqueueRequestsFromMapFunc(h.HandleReplicatedVolumeReplica)).
		Complete(r)
}
