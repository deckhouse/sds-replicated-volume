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
	"net/http"
	"os"
	"os/signal"
	"syscall"

	v1 "k8s.io/api/core/v1"
	sv1 "k8s.io/api/storage/v1"
	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-local-volume/images/sds-local-volume-csi/config"
	"github.com/deckhouse/sds-local-volume/images/sds-local-volume-csi/driver"
	"github.com/deckhouse/sds-local-volume/images/sds-local-volume-csi/pkg/kubutils"
	"github.com/deckhouse/sds-local-volume/images/sds-local-volume-csi/pkg/logger"
	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	srv1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	srv2 "github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
)

var (
	resourcesSchemeFuncs = []func(*apiruntime.Scheme) error{
		snc.AddToScheme,
		clientgoscheme.AddToScheme,
		extv1.AddToScheme,
		v1.AddToScheme,
		sv1.AddToScheme,
		srv1.AddToScheme,
		srv2.AddToScheme,
	}
)

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, err := fmt.Fprint(w, "OK")
	if err != nil {
		klog.Fatalf("Error while generating healthcheck, err: %s", err.Error())
	}
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())

	cfgParams, err := config.NewConfig()
	if err != nil {
		klog.Fatalf("unable to create NewConfig, err: %s", err.Error())
		os.Exit(1)
	}

	log, err := logger.NewLogger(cfgParams.Loglevel)
	if err != nil {
		fmt.Printf("unable to create NewLogger, err: %v\n", err)
		os.Exit(1)
	}

	log.Info("version = ", cfgParams.Version)

	kConfig, err := kubutils.KubernetesDefaultConfigCreate()
	if err != nil {
		log.Error(err, "[main] unable to KubernetesDefaultConfigCreate")
		os.Exit(1)
	}
	log.Info("[main] kubernetes config has been successfully created.")

	scheme := runtime.NewScheme()
	for _, f := range resourcesSchemeFuncs {
		err := f(scheme)
		if err != nil {
			log.Error(err, "[main] unable to add scheme to func")
			os.Exit(1)
		}
	}
	log.Info("[main] successfully read scheme CR")

	cl, err := client.New(kConfig, client.Options{
		Scheme: scheme,
	})

	http.HandleFunc("/healthz", healthHandler)
	http.HandleFunc("/readyz", healthHandler)
	go func() {
		err = http.ListenAndServe(cfgParams.HealthProbeBindAddress, nil)
		if err != nil {
			log.Error(err, "[main] create probes")
		}
	}()

	drv, err := driver.NewDriver(cfgParams.CsiAddress, cfgParams.DriverName, cfgParams.Address, &cfgParams.NodeName, log, cl)
	if err != nil {
		log.Error(err, "[main] create NewDriver")
	}

	defer cancel()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-c
		cancel()
	}()

	if err := drv.Run(ctx); err != nil {
		log.Error(err, "[dev.Run]")
	}
}
