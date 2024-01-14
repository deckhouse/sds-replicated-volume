/*
Copyright 2023 Flant JSC

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
	goruntime "runtime"
	"sds-drbd-controller/api/v1alpha1"
	"sds-drbd-controller/config"
	"sds-drbd-controller/pkg/controller"
	kubutils "sds-drbd-controller/pkg/kubeutils"
	"sds-drbd-controller/pkg/logger"

	controllerruntime "sigs.k8s.io/controller-runtime"

	lapi "github.com/LINBIT/golinstor/client"
	v1 "k8s.io/api/core/v1"
	sv1 "k8s.io/api/storage/v1"
	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

var (
	resourcesSchemeFuncs = []func(*apiruntime.Scheme) error{
		v1alpha1.AddToScheme,
		clientgoscheme.AddToScheme,
		extv1.AddToScheme,
		v1.AddToScheme,
		sv1.AddToScheme,
	}
)

func main() {

	ctx := context.Background()

	cfgParams, err := config.NewConfig()
	if err != nil {
		fmt.Println("unable to create NewConfig " + err.Error())
	}

	log, err := logger.NewLogger(cfgParams.Loglevel)
	if err != nil {
		fmt.Println(fmt.Sprintf("unable to create NewLogger, err: %v", err))
		os.Exit(1)
	}

	log.Info(fmt.Sprintf("Go Version:%s ", goruntime.Version()))
	log.Info(fmt.Sprintf("OS/Arch:Go OS/Arch:%s/%s ", goruntime.GOOS, goruntime.GOARCH))

	// Create default config Kubernetes client
	kConfig, err := kubutils.KubernetesDefaultConfigCreate()
	if err != nil {
		log.Error(err, "error read kubernetes configuration")
	}
	log.Info("read Kubernetes config")

	// Setup scheme for all resources
	scheme := apiruntime.NewScheme()
	for _, f := range resourcesSchemeFuncs {
		err := f(scheme)
		if err != nil {
			log.Error(err, "failed to add to scheme")
			os.Exit(1)
		}
	}
	log.Info("read scheme CR")

	cacheOpt := cache.Options{
		DefaultNamespaces: map[string]cache.Config{
			cfgParams.ControllerNamespace: {},
		},
	}

	managerOpts := manager.Options{
		Scheme: scheme,
		// MetricsBindAddress: cfgParams.MetricsPort,
		Cache:                   cacheOpt,
		LeaderElection:          true,
		LeaderElectionNamespace: cfgParams.ControllerNamespace,
		LeaderElectionID:        config.ControllerName,
	}

	mgr, err := manager.New(kConfig, managerOpts)
	if err != nil {
		log.Error(err, "failed create manager")
		os.Exit(1)
	}

	log.Info("create kubernetes manager in namespace: " + cfgParams.ControllerNamespace)

	controllerruntime.SetLogger(log.GetLogger())
	lc, err := lapi.NewClient(lapi.Log(log))
	if err != nil {
		log.Error(err, "failed create linstor client")
		os.Exit(1)
	}

	if _, err := controller.NewLinstorNode(ctx, mgr, lc, cfgParams.ConfigSecretName, cfgParams.ScanInterval, *log); err != nil {
		log.Error(err, "failed create controller NewLinstorNode", err)
		os.Exit(1)
	}
	log.Info("controller NewLinstorNode start")

	if _, err := controller.NewDRBDStorageClass(mgr, cfgParams.ScanInterval, *log); err != nil {
		log.Error(err, "failed create controller NewDRBDStorageClass")
		os.Exit(1)
	}
	log.Info("controller NewDRBDStorageClass start")

	if _, err := controller.NewDRBDStoragePool(mgr, lc, cfgParams.ScanInterval, *log); err != nil {
		log.Error(err, "failed create controller NewDRBDStoragePool", err)
		os.Exit(1)
	}
	log.Info("controller NewDRBDStoragePool start")

	if _, err := controller.NewLinstorLeader(mgr, cfgParams.LinstorLeaseName, cfgParams.ScanInterval, *log); err != nil {
		log.Error(err, "failed create controller NewLinstorLeader", err)
		os.Exit(1)
	}
	log.Info("controller NewLinstorLeader start")

	if _, err := controller.NewLinstorResourcesWatcher(mgr, lc, cfgParams.LinstorResourcesReconcileInterval, *log); err != nil {
		log.Error(err, "failed create controller NewDRBDStoragePool", err)
		os.Exit(1)
	}

	log.Info("controller NewLinstorResourcesWatcher start")

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	err = mgr.Start(ctx)
	if err != nil {
		log.Error(err, "error start manager")
		os.Exit(1)
	}

	log.Info("starting the manager")
}
