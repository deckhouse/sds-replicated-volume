package controllers

import (
	"fmt"

	rvrstatusconfigaddress "github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/rvr_status_config_address"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

var registry []func(mgr manager.Manager) error

func init() {
	registry = append(registry, rvrstatusconfigaddress.BuildController)
	// ...
}

func BuildAll(mgr manager.Manager) error {
	for i, buildCtl := range registry {
		err := buildCtl(mgr)
		if err != nil {
			return fmt.Errorf("building controller %d: %w", i, err)
		}
	}
	return nil
}
