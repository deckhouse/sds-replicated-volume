package rv

import (
	"context"
	"fmt"
	"strconv"

	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ControllerConfigMapNamespace = "d8-sds-replicated-volume"
	ControllerConfigMapName      = "controller-config"
)

type ReconcilerClusterConfig struct {
	DRBDMinPort int
	DRBDMaxPort int
}

func GetClusterConfig(ctx context.Context, cl client.Client) (*ReconcilerClusterConfig, error) {
	cfg := &ReconcilerClusterConfig{}

	secret := &v1.ConfigMap{}

	err := cl.Get(
		ctx,
		client.ObjectKey{
			Namespace: ControllerConfigMapNamespace,
			Name:      ControllerConfigMapName,
		},
		secret,
	)
	if err != nil {
		return nil,
			fmt.Errorf(
				"getting %s/%s: %w",
				ControllerConfigMapNamespace, ControllerConfigMapName, err,
			)
	}

	cfg.DRBDMinPort, err = strconv.Atoi(secret.Data["drbdMinPort"])
	if err != nil {
		return nil,
			fmt.Errorf(
				"parsing %s/%s/drbdMinPort: %w",
				ControllerConfigMapNamespace, ControllerConfigMapName, err,
			)
	}

	cfg.DRBDMaxPort, err = strconv.Atoi(secret.Data["drbdMaxPort"])
	if err != nil {
		return nil,
			fmt.Errorf(
				"parsing %s/%s/drbdMaxPort: %w",
				ControllerConfigMapNamespace, ControllerConfigMapName, err,
			)
	}

	return cfg, nil
}
