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
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	"github.com/deckhouse/sds-common-lib/kubeclient"
	"github.com/deckhouse/sds-common-lib/logger"
	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/csi-driver/config"
	"github.com/deckhouse/sds-replicated-volume/images/csi-driver/driver"
)

func healthHandler(log logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := fmt.Fprint(w, "OK"); err != nil {
			// Writing the body failed, which says something about this one
			// connection and nothing about the process: klog.Fatal used to take
			// the whole driver down for it.
			log.Error("writing the healthcheck response", logger.Err(err))
		}
	}
}

func main() {
	// run and not main: the early exits below used to be klog.Fatal, which skipped
	// the deferred cancel just as os.Exit would.
	os.Exit(run())
}

func run() int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-c
		cancel()
	}()

	cfgParams, err := config.NewConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "unable to create NewConfig: %s\n", err)
		return 1
	}

	log, err := logger.NewLogger(cfgParams.Loglevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "unable to create NewLogger: %s\n", err)
		return 1
	}

	log.Info("starting csi-driver", "version", cfgParams.Version)

	cl, err := kubeclient.New(
		snc.AddToScheme,
		v1alpha1.AddToScheme,
		clientgoscheme.AddToScheme,
		extv1.AddToScheme,
		v1.AddToScheme,
		sv1.AddToScheme,
	)
	if err != nil {
		log.Error("unable to create kubeclient", logger.Err(err))
		return 1
	}

	if cfgParams.MetricsBindAddress == "" {
		http.HandleFunc("/healthz", healthHandler(log))
		http.HandleFunc("/readyz", healthHandler(log))
		go func() {
			err = http.ListenAndServe(cfgParams.HealthProbeBindAddress, nil)
			if err != nil {
				log.Error("create probes", logger.Err(err))
			}
		}()
	}

	drv, err := driver.NewDriver(cfgParams.CsiAddress, cfgParams.DriverName, cfgParams.Address,
		&cfgParams.NodeName, log, cl, cfgParams.MetricsBindAddress)
	if err != nil {
		log.Error("create NewDriver", logger.Err(err))
		return 1
	}

	if err := drv.Run(ctx); err != nil {
		log.Error("the CSI driver stopped", logger.Err(err))
		return 1
	}

	return 0
}
