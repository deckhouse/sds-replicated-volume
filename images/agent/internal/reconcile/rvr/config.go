/*
Copyright 2025 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package rvr

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	SecretNamespace = "d8-sds-replicated-volume"
	SecretName      = "agent"
)

type ReconcilerClusterConfig struct {
	// TODO: updatable configuration will be there
}

func GetClusterConfig(_ context.Context, _ client.Client) (*ReconcilerClusterConfig, error) {
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
