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
	"os"

	"drbd-cluster-sync/config"
	"drbd-cluster-sync/controller"
	kubutils "drbd-cluster-sync/kubeutils"
	"drbd-cluster-sync/logger"

	"github.com/deckhouse/sds-common-lib/kubeclient"
	lsrv "github.com/deckhouse/sds-replicated-volume/api/linstor"
	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	srv2 "github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

func main() {
	ctx := signals.SetupSignalHandler()
	opts := config.NewDefaultOptions()

	resourcesSchemeFuncs := []func(*apiruntime.Scheme) error{
		srv.AddToScheme,
		v1.AddToScheme,
		lsrv.AddToScheme,
		srv2.AddToScheme,
	}

	log, err := logger.NewLogger(logger.Verbosity("4"))
	if err != nil {
		os.Exit(1)
	}

	scheme := runtime.NewScheme()
	for _, f := range resourcesSchemeFuncs {
		if err := f(scheme); err != nil {
			log.Error(err, "[Main] unable to add scheme to func")
			os.Exit(1)
		}
	}
	log.Info("[Main] successfully read scheme CR")

	kConfig, err := kubutils.KubernetesDefaultConfigCreate()
	if err != nil {
		log.Error(err, "[Main] unable to KubernetesDefaultConfigCreate")
		os.Exit(1)
	}
	log.Info("[Main] kubernetes config has been successfully created.")

	managerOpts := manager.Options{
		Scheme:      scheme,
		Logger:      log.GetLogger(),
		BaseContext: func() context.Context { return ctx },
	}

	mgr, err := manager.New(kConfig, managerOpts)
	if err != nil {
		log.Error(err, "[Main] unable to create manager for creating controllers")
		os.Exit(1)
	}

	kc, err := kubeclient.New(resourcesSchemeFuncs...)
	if err != nil {
		log.Error(err, "[Main] failed to initialize kube client")
		os.Exit(1)
	}

	if err = controller.RunLayerResourceIDsWatcher(mgr, log, kc, opts); err != nil {
		log.Error(err, fmt.Sprintf("[Main] unable to run %s controller", controller.LVGLayerResourceIDsWatcherName))
		os.Exit(1)
	}
	log.Info(fmt.Sprintf("[Main] successfully ran %s controller", controller.LVGLayerResourceIDsWatcherName))

	err = mgr.Start(ctx)
	if err != nil {
		log.Error(err, "[Main] unable to mgr.Start()")
		os.Exit(1)
	}

	// r := rate.Limit(opts.RPS)
	// if r <= 0 {
	// 	r = rate.Inf
	// }

	// linstorOpts := []lapi.Option{
	// 	lapi.Limit(r, opts.Burst),
	// 	lapi.UserAgent("linstor-csi/" + driver.Version),
	// 	lapi.Log(log),
	// }

	// if opts.LSEndpoint != "" {
	// 	u, err := url.Parse(opts.LSEndpoint)
	// 	if err != nil {
	// 		log.Error(err, "[Main] Failed to parse endpoint")
	// 		os.Exit(1)
	// 	}

	// 	linstorOpts = append(linstorOpts, lapi.BaseURL(u))
	// }

	// if opts.LSSkipTLSVerification {
	// 	linstorOpts = append(linstorOpts, lapi.HTTPClient(&http.Client{
	// 		Transport: &http.Transport{
	// 			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	// 		},
	// 	}))
	// }

	// if opts.BearerTokenFile != "" {
	// 	token, err := os.ReadFile(opts.BearerTokenFile)
	// 	if err != nil {
	// 		log.Error(err, "[Main] failed to read bearer token file")
	// 		os.Exit(1)
	// 	}

	// 	linstorOpts = append(linstorOpts, lapi.BearerToken(string(token)))
	// }

	// lc, err := lc.NewHighLevelClient(linstorOpts...)
	// if err != nil {
	// 	log.Error(err, "[Main] failed to create linstor high level client")
	// 	os.Exit(1)
	// }

	// if err = crd_sync.NewDRBDClusterSyncer(kc, log, opts).Sync(ctx); err != nil {
	// 	log.Info(fmt.Sprintf("[Main] failed to sync DRBD clusters: %v", err.Error()))
	// }

	// os.Exit(0)
}
