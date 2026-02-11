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

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	u "github.com/deckhouse/sds-common-lib/utils"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/scheme"
)

type managerConfig interface {
	PodNamespace() string
	HealthProbeBindAddress() string
	MetricsBindAddress() string
	IsControllerEnabled(name string) bool
}

func newManager(
	ctx context.Context,
	log *slog.Logger,
	envConfig managerConfig,
) (manager.Manager, error) {
	config, err := config.GetConfig()
	if err != nil {
		return nil, u.LogError(log, fmt.Errorf("getting rest config: %w", err))
	}

	scheme, err := scheme.New()
	if err != nil {
		return nil, u.LogError(log, fmt.Errorf("building scheme: %w", err))
	}

	// Configure cache to only watch agent pods in the controller's namespace.
	// This reduces memory usage and API server load.
	cacheOpt := cache.Options{
		ByObject: map[client.Object]cache.ByObject{
			&corev1.Pod{}: {
				Namespaces: map[string]cache.Config{
					envConfig.PodNamespace(): {},
				},
				Label: labels.SelectorFromSet(labels.Set{"app": "agent"}),
			},
		},
	}

	mgrOpts := manager.Options{
		Scheme:                  scheme,
		BaseContext:             func() context.Context { return ctx },
		Logger:                  logr.FromSlogHandler(log.Handler()),
		HealthProbeBindAddress:  envConfig.HealthProbeBindAddress(),
		LeaderElection:          true,
		LeaderElectionNamespace: envConfig.PodNamespace(),
		LeaderElectionID:        "sds-replicated-volume-controller",
		Cache:                   cacheOpt,
		Metrics: server.Options{
			BindAddress: envConfig.MetricsBindAddress(),
		},
	}

	mgr, err := manager.New(config, mgrOpts)
	if err != nil {
		return nil, u.LogError(log, fmt.Errorf("creating manager: %w", err))
	}

	if err = mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return nil, u.LogError(log, fmt.Errorf("AddHealthzCheck: %w", err))
	}

	if err = mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return nil, u.LogError(log, fmt.Errorf("AddReadyzCheck: %w", err))
	}

	if err := controllers.BuildAll(mgr, envConfig.PodNamespace(), envConfig.IsControllerEnabled); err != nil {
		return nil, err
	}

	return mgr, nil
}
