/*
Copyright 2024 Flant JSC

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
	"os/signal"
	goruntime "runtime"
	"syscall"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"storage-network-controller/internal/config"
	"storage-network-controller/internal/logger"
	"storage-network-controller/pkg/discoverer"
)

func KubernetesDefaultConfigCreate() (*rest.Config, error) {
	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{},
	)
	// Get a config to talk to API server
	config, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("config kubernetes error %w", err)
	}
	return config, nil
}

func runController(cfg config.Options, log *logger.Logger) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// add logger to root context to make it available everywhere, where root context is used
	ctx = logger.WithLogger(ctx, log)

	log.Info(fmt.Sprintf("Go Version:%s ", goruntime.Version()))
	log.Info(fmt.Sprintf("OS/Arch:Go OS/Arch:%s/%s ", goruntime.GOOS, goruntime.GOARCH))

	if cfg.DiscoveryMode {
		log.Info("Starting up in discovery mode...")
		err := discoverer.DiscoveryLoop(ctx, *cfg, mgr)
		if err != nil {
			log.Error(err, "failed to discovery node")
			cancel()
			// disable exitAfterDefer: os.Exit will exit, and `defer cancel()` will not run
			// because we explicitly call cancel() before exiting with error code
			os.Exit(2) //nolint:gocritic
		}
	}

}

func main() {
	cfg, err := config.NewConfig()
	if err != nil {
		fmt.Println("unable to create NewConfig " + err.Error())
		os.Exit(1)
	}

	log, err := logger.NewLogger(cfg.Loglevel)
	if err != nil {
		fmt.Printf("unable to create NewLogger, err: %v\n", err)
		os.Exit(1)
	}

	// Create default config Kubernetes client
	kConfig, err := KubernetesDefaultConfigCreate()
	if err != nil {
		log.Error(err, "error by reading a kubernetes configuration")
		os.Exit(1)
	}
	log.Info("read Kubernetes config")

	cacheOpt := cache.Options{}

	managerOpts := manager.Options{
		Cache:          cacheOpt,
		LeaderElection: false,
	}

	mgr, err := manager.New(kConfig, managerOpts)
	if err != nil {
		log.Error(err, "failed to create a manager")
		os.Exit(1)
	}
	log.Info("created kubernetes manager in namespace: " + cfg.ControllerNamespace)

	controllerruntime.SetLogger(log.GetLogger())

	// listen for interrupts or the Linux SIGTERM signal and cancel
	// our context, which the leader election code will observe and
	// step down
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		log.Info("Received termination, signaling shutdown")
		cancel()
	}()
}
