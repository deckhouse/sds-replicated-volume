package cluster

import (
	"context"
	"fmt"
	"strconv"

	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ConfigMapNamespace = "d8-sds-replicated-volume"
	ConfigMapName      = "agent-config"
)

// TODO issues/333 put run-time settings here
type Settings struct {
	DRBDMinPort int
	DRBDMaxPort int
}

func GetSettings(ctx context.Context, cl client.Client) (*Settings, error) {
	settings := &Settings{}

	// TODO to avoid resetting after each deploy, migrate to ModuleConfig settings
	cm := &v1.ConfigMap{}

	err := cl.Get(
		ctx,
		client.ObjectKey{
			Namespace: ConfigMapNamespace,
			Name:      ConfigMapName,
		},
		cm,
	)
	if err != nil {
		return nil,
			fmt.Errorf(
				"getting %s/%s: %w",
				ConfigMapNamespace, ConfigMapName, err,
			)
	}

	settings.DRBDMinPort, err = strconv.Atoi(cm.Data["drbdMinPort"])
	if err != nil {
		return nil,
			fmt.Errorf(
				"parsing %s/%s/drbdMinPort: %w",
				ConfigMapNamespace, ConfigMapName, err,
			)
	}

	settings.DRBDMaxPort, err = strconv.Atoi(cm.Data["drbdMaxPort"])
	if err != nil {
		return nil,
			fmt.Errorf(
				"parsing %s/%s/drbdMaxPort: %w",
				ConfigMapNamespace, ConfigMapName, err,
			)
	}

	return settings, nil
}
