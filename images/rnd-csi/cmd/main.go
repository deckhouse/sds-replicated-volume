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
	"flag"
	"fmt"
	"os"
	"os/signal"
	"rnd-csi/api/v1alpha1"
	"rnd-csi/config"
	"rnd-csi/driver"
	"rnd-csi/pkg/kubutils"
	"rnd-csi/pkg/logger"

	"syscall"

	v1 "k8s.io/api/core/v1"
	sv1 "k8s.io/api/storage/v1"
	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

	ctx, cancel := context.WithCancel(context.Background())

	cfgParams, err := config.NewConfig()
	if err != nil {
		klog.Fatal("unable to create NewConfig")
	}

	var (
		endpoint   = flag.String("endpoint", "unix:///var/lib/kubelet/plugins/"+driver.DefaultDriverName+"/csi.sock", "CSI endpoint")
		driverName = flag.String("driver-name", driver.DefaultDriverName, "Name for the driver")
		address    = flag.String("address", driver.DefaultAddress, "Address to serve on")
	)

	log, err := logger.NewLogger(cfgParams.Loglevel)
	if err != nil {
		fmt.Println(fmt.Sprintf("unable to create NewLogger, err: %v", err))
		os.Exit(1)
	}

	log.Info("version = ", cfgParams.Version)

	kConfig, err := kubutils.KubernetesDefaultConfigCreate()
	if err != nil {
		log.Error(err, "[main] unable to KubernetesDefaultConfigCreate")
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
		Scheme:         scheme,
		WarningHandler: client.WarningHandlerOptions{},
	})

	drv, err := driver.NewDriver(*endpoint, *driverName, *address, &cfgParams.NodeName, log, cl)
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
