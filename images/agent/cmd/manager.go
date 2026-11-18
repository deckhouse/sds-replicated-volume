/*
Copyright 2026 Flant JSC

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
	"net/http"

	sdn "github.com/deckhouse/sdn/api/network.deckhouse.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/deckhouse/sds-common-lib/logger"
	u "github.com/deckhouse/sds-common-lib/utils"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/capabilities"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/scheme"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdutils"
)

type managerConfig interface {
	SDNEnabled() bool
	PodName() string
	PodNamespace() string
	NodeName() string
	HealthProbeBindAddress() string
	MetricsBindAddress() string
}

func newManager(
	ctx context.Context,
	log logger.Logger,
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

	nodeName := cfg.NodeName()
	httpClient, err := rest.HTTPClientFor(config)
	if err != nil {
		return nil, fmt.Errorf("creating discovery HTTP client: %w", err)
	}
	mapper, err := apiutil.NewDynamicRESTMapper(config, httpClient)
	if err != nil {
		return nil, fmt.Errorf("creating REST mapper: %w", err)
	}
	sdnAvailable, err := checkSDNKind(cfg.SDNEnabled(), mapper)
	if err != nil {
		return nil, err
	}
	if cfg.SDNEnabled() && !sdnAvailable {
		log.GetLogger().Info("NodeNetworkStatus Kind is unavailable; SDN disabled until agent restart")
	}
	nodeNameSelector := fields.SelectorFromSet(fields.Set{"spec.nodeName": nodeName})

	mgrOpts := manager.Options{
		Scheme:                 scheme,
		MapperProvider:         func(*rest.Config, *http.Client) (meta.RESTMapper, error) { return mapper, nil },
		BaseContext:            func() context.Context { return ctx },
		Logger:                 log.GetLogger(),
		HealthProbeBindAddress: cfg.HealthProbeBindAddress(),
		Metrics: server.Options{
			BindAddress: cfg.MetricsBindAddress(),
		},
		Cache: cache.Options{
			DefaultTransform: cache.TransformStripManagedFields(),
			ByObject: map[client.Object]cache.ByObject{
				&v1alpha1.DRBDResource{}:          {Field: nodeNameSelector},
				&v1alpha1.DRBDMapper{}:            {Field: nodeNameSelector},
				&v1alpha1.DRBDResourceOperation{}: {Field: nodeNameSelector},
				&corev1.Node{}: {
					Field: fields.SelectorFromSet(fields.Set{"metadata.name": nodeName}),
				},
			},
		},
	}
	configureSDNCache(&mgrOpts.Cache, nodeName, sdnAvailable)

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

	if err := mgr.Add(capabilities.NewReporter(mgr.GetClient(), client.ObjectKey{
		Name: cfg.PodName(), Namespace: cfg.PodNamespace(),
	}, drbdutils.FlantExtensionsSupported)); err != nil {
		return nil, fmt.Errorf("adding agent capability reporter: %w", err)
	}

	if err := controllers.BuildAll(mgr, sdnAvailable); err != nil {
		return nil, err
	}

	return mgr, nil
}

func checkSDNKind(enabled bool, mapper meta.RESTMapper) (bool, error) {
	if !enabled {
		return false, nil
	}
	_, err := mapper.RESTMapping(sdn.NodeNetworkStatusGVK.GroupKind(), sdn.NodeNetworkStatusGVK.Version)
	if meta.IsNoMatchError(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("discovering NodeNetworkStatus Kind: %w", err)
	}
	return true, nil
}

func configureSDNCache(options *cache.Options, nodeName string, available bool) {
	if available {
		// SDN creates NodeNetworkStatus with the node name as metadata.name and uses
		// the same cache filter; its CRD does not declare spec.nodeName selectable.
		options.ByObject[&sdn.NodeNetworkStatus{}] = cache.ByObject{Field: fields.OneTermEqualSelector("metadata.name", nodeName)}
	}
}
