package rvr

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	SecretNamespace = "d8-sds-replicated-volume"
	SecretName      = "sds-replicated-volume-agent"
)

type ReconcilerClusterConfig struct {
	// TODO: updatable configuration will be there
}

func GetClusterConfig(ctx context.Context, cl client.Client) (*ReconcilerClusterConfig, error) {
	cfg := &ReconcilerClusterConfig{}

	// TODO: updatable configuration will be there
	// secret := &v1.Secret{}

	// err := cl.Get(
	// 	ctx,
	// 	client.ObjectKey{Name: SecretName, Namespace: SecretNamespace},
	// 	secret,
	// )
	// if err != nil {
	// 	return nil, fmt.Errorf("getting %s/%s: %w", SecretNamespace, SecretName, err)
	// }

	// cfg.AAA = string(secret.Data["AAA"])

	return cfg, nil
}
