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
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	kubecl "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	d8commonapi "github.com/deckhouse/sds-common-lib/api/v1alpha1"
	sncv1alpha1 "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	srvlinstor "github.com/deckhouse/sds-replicated-volume/api/linstor"
	srvv1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/config"
	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/kubeutils"
	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/migrator"
)

func main() {
	os.Exit(run())
}

func run() int {
	opt := &Opt{}
	opt.Parse()

	ctx := signals.SetupSignalHandler()

	// Convert log level string to slog.Level.
	var logLevel slog.Level
	switch opt.LogLevel {
	case "debug":
		logLevel = slog.LevelDebug
	case "info":
		logLevel = slog.LevelInfo
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	log, logCleanup, err := newLogger(logLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "linstor-migrator: %v\n", err)
		return 1
	}
	defer logCleanup()

	slog.SetDefault(log)

	log.Info("linstor-migrator started")

	scheme, err := newScheme()
	if err != nil {
		log.Error("failed to build scheme", "err", err)
		return 1
	}

	kConfig, err := kubeutils.KubernetesDefaultConfigCreate()
	if err != nil {
		log.Error("failed to create Kubernetes config", "err", err)
		return 1
	}

	kClient, err := kubecl.New(kConfig, kubecl.Options{
		Scheme: scheme,
	})
	if err != nil {
		log.Error("failed to create Kubernetes client", "err", err)
		return 1
	}

	dynClient, err := dynamic.NewForConfig(kConfig)
	if err != nil {
		log.Error("failed to create dynamic Kubernetes client", "err", err)
		return 1
	}

	m := migrator.New(kClient, dynClient, log, migrator.MigratorOptions{
		Stage2PollInterval: opt.Stage2PollInterval,
		Stage2WorkerCount:  opt.Stage2WorkerCount,
	})
	if err := m.Run(ctx); err != nil {
		log.Error("linstor-migrator exited with error", "err", err)
		return 1
	}

	log.Info("linstor-migrator gracefully shutdown")
	return 0
}

// newLogger returns a slog.Logger that writes the same log lines to stdout and to
// config.MigratorHostDir/config.MigratorLogFileName in append mode. logCleanup syncs and closes the file.
func newLogger(level slog.Level) (*slog.Logger, func(), error) {
	if err := os.MkdirAll(config.MigratorHostDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create migrator host directory %q: %w", config.MigratorHostDir, err)
	}
	logPath := filepath.Join(config.MigratorHostDir, config.MigratorLogFileName)
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file %q: %w", logPath, err)
	}
	cleanup := func() {
		_ = f.Sync()
		_ = f.Close()
	}
	mw := io.MultiWriter(os.Stdout, f)
	h := slog.NewTextHandler(mw, &slog.HandlerOptions{
		Level:     level,
		AddSource: false,
	})
	return slog.New(h), cleanup, nil
}

// newScheme creates a runtime.Scheme with all required types registered.
func newScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()

	schemeFuncs := []func(s *runtime.Scheme) error{
		appsv1.AddToScheme,
		corev1.AddToScheme,
		storagev1.AddToScheme,
		d8commonapi.AddToScheme,
		srvv1alpha1.AddToScheme,
		srvlinstor.AddToScheme,
		sncv1alpha1.AddToScheme,
		apiextensionsv1.AddToScheme,
	}

	for i, f := range schemeFuncs {
		if err := f(scheme); err != nil {
			return nil, fmt.Errorf("adding scheme %d: %w", i, err)
		}
	}

	return scheme, nil
}
