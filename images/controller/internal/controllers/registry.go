package controllers

import (
	"fmt"

	rvrdiskfulcount "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_diskful_count"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

var registry []func(mgr manager.Manager) error

func init() {
	registry = append(registry, rvrdiskfulcount.BuildController)

	// TODO issues/333 register new controllers here
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
