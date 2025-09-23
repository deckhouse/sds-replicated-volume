package rv

import (
	"context"
	"log/slog"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/reconcile/rv/cluster"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type resourceReconcileRequestHandler struct {
	ctx context.Context
	log *slog.Logger
	cl  client.Client
	cfg *ReconcilerClusterConfig
	rv  *v1alpha2.ReplicatedVolume
}

func (h *resourceReconcileRequestHandler) Handle() error {
	h.log.Info("controller: reconcile resource", "name", h.rv.Name)

	// TODO:
	clr := cluster.New(h.ctx, nil, nil, nil, nil, h.rv.Name, "shared-secret")

	clr.AddReplica("a-stefurishin-worker-0", "10.10.11.52", true, 0, 0).AddVolume("vg-1")
	clr.AddReplica("a-stefurishin-worker-1", "10.10.11.149", false, 0, 0).AddVolume("vg-1")
	clr.AddReplica("a-stefurishin-worker-2", "10.10.11.150", false, 0, 0) // diskless

	action, err := clr.Reconcile()
	if err != nil {
		return err
	}

	return h.processAction(action)
}

func (h *resourceReconcileRequestHandler) processAction(untypedAction cluster.Action) error {
	switch action := untypedAction.(type) {
	case cluster.Actions:
		for _, subaction := range action {
			return h.processAction(subaction)
		}
	case cluster.ParallelActions:
		// TODO:

	default:
		panic("unknown action type")
	}
	return nil
}
