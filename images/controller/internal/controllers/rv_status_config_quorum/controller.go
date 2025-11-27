package rvrdiskfulcount // TODO change package if need

import (
	"log/slog"

	u "github.com/deckhouse/sds-common-lib/utils"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	e "github.com/deckhouse/sds-replicated-volume/images/controller/internal/errors"
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

	err := builder.ControllerManagedBy(mgr).
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

	if err != nil {
		// TODO issues/333 log errors early
		// TODO issues/333 use typed errors
		return u.LogError(rec.log, e.ErrUnknownf("building controller: %w", err))
	}

	return nil
}
