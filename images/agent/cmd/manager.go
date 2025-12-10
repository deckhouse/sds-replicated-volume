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

package main

import (
	"context"
	"fmt"
	"log/slog"

	u "github.com/deckhouse/sds-common-lib/utils"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/scheme"
	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

type managerConfig interface {
	HealthProbeBindAddress() string
	MetricsBindAddress() string
}

func newManager(
	ctx context.Context,
	log *slog.Logger,
	cfg managerConfig,
) (manager.Manager, error) {
	config, err := config.GetConfig()
	if err != nil {
		return nil, u.LogError(log, fmt.Errorf("getting rest config: %w", err))
	}

	scheme, err := scheme.New()
	if err != nil {
		return nil, u.LogError(log, fmt.Errorf("building scheme: %w", err))
	}

	mgrOpts := manager.Options{
		Scheme:                 scheme,
		BaseContext:            func() context.Context { return ctx },
		Logger:                 logr.FromSlogHandler(log.Handler()),
		HealthProbeBindAddress: cfg.HealthProbeBindAddress(),
		Metrics: server.Options{
			BindAddress: cfg.MetricsBindAddress(),
		},
	}

	mgr, err := manager.New(config, mgrOpts)
	if err != nil {
		return nil, u.LogError(log, fmt.Errorf("creating manager: %w", err))
	}

	err = mgr.GetFieldIndexer().IndexField(
		ctx,
		&v1alpha3.ReplicatedVolumeReplica{},
		"spec.nodeName",
		func(rawObj client.Object) []string {
			replica := rawObj.(*v1alpha3.ReplicatedVolumeReplica)
			if replica.Spec.NodeName == "" {
				return nil
			}
			return []string{replica.Spec.NodeName}
		},
	)
	if err != nil {
		return nil,
			u.LogError(log, fmt.Errorf("indexing %s: %w", "spec.nodeName", err))
	}

	if err = mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return nil, u.LogError(log, fmt.Errorf("AddHealthzCheck: %w", err))
	}

	if err = mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return nil, u.LogError(log, fmt.Errorf("AddReadyzCheck: %w", err))
	}

	if err := controllers.BuildAll(mgr); err != nil {
		return nil, err
	}

	return mgr, nil
}
