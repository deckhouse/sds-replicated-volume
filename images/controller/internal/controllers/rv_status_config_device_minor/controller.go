package rvstatusconfigdeviceminor

import (
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	e "github.com/deckhouse/sds-replicated-volume/images/controller/internal/errors"
)

const ControllerName = "rv_status_config_device_minor_controller"

func BuildController(mgr manager.Manager) error {
	rec := &Reconciler{
		cl:  mgr.GetClient(),
		log: mgr.GetLogger().WithName(ControllerName).WithName("Reconciler"),
	}

	err := builder.ControllerManagedBy(mgr).
		Named(ControllerName).
		For(&v1alpha3.ReplicatedVolume{}).
		Complete(rec)

	if err != nil {
		return fmt.Errorf("building controller: %w", e.ErrUnknownf("%w", err))
	}

	return nil
}
