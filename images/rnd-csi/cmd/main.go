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
	"github.com/sirupsen/logrus"
	"k8s.io/klog/v2"
	"os"
	"os/signal"
	"rnd-csi/config"
	"rnd-csi/driver"
	"syscall"
)

func main() {

	ctx, cancel := context.WithCancel(context.Background())

	cfgParams, err := config.NewConfig()
	if err != nil {
		klog.Fatal("unable to create NewConfig")
	}

	log := logrus.New().WithFields(logrus.Fields{
		//"version": cfgParams.Version,
	})

	log.Log(logrus.Level(cfgParams.LogLevel))

	log.Info("version = ", cfgParams.Version)

	var (
		endpoint   = flag.String("endpoint", "unix:///var/lib/kubelet/plugins/"+driver.DefaultDriverName+"/csi.sock", "CSI endpoint")
		driverName = flag.String("driver-name", driver.DefaultDriverName, "Name for the driver")
		address    = flag.String("address", driver.DefaultAddress, "Address to serve on")
	)

	flag.Parse()

	drv, err := driver.NewDriver(*endpoint, *driverName, *address, &cfgParams.NodeName, log)
	if err != nil {
		log.Error(err)
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
