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
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	u "github.com/deckhouse/sds-common-lib/utils"
	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers"
)

func newManager(
	ctx context.Context,
	log *slog.Logger,
	envConfig *EnvConfig,
) (manager.Manager, error) {
	config, err := config.GetConfig()
	if err != nil {
		return nil, u.LogError(log, fmt.Errorf("getting rest config: %w", err))
	}

	scheme, err := newScheme()
	if err != nil {
		return nil, u.LogError(log, fmt.Errorf("building scheme: %w", err))
	}

	mgrOpts := manager.Options{
		Scheme:                 scheme,
		BaseContext:            func() context.Context { return ctx },
		Logger:                 logr.FromSlogHandler(log.Handler()),
		HealthProbeBindAddress: envConfig.HealthProbeBindAddress,
		Metrics: server.Options{
			BindAddress: envConfig.MetricsBindAddress,
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

	if err := controllers.BuildAll(mgr); err != nil {
		return nil, err
	}

	return mgr, nil
}

func newScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()

	var schemeFuncs = []func(s *runtime.Scheme) error{
		corev1.AddToScheme,
		storagev1.AddToScheme,
		v1alpha3.AddToScheme,
		snc.AddToScheme,
	}

	for i, f := range schemeFuncs {
		if err := f(scheme); err != nil {
			return nil, fmt.Errorf("adding scheme %d: %w", i, err)
		}
	}

	return scheme, nil
}
