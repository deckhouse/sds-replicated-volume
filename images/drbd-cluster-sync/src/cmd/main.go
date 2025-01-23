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
	"crypto/tls"
	"flag"
	"net/http"
	"net/url"
	"os"

	"drbd-cluster-sync/config"
	"drbd-cluster-sync/crd_sync"
	"drbd-cluster-sync/pkg/kubeutils"

	"golang.org/x/time/rate"

	lapi "github.com/LINBIT/golinstor/client"
	lsrv "github.com/deckhouse/sds-replicated-volume/api/linstor"
	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/piraeusdatastore/linstor-csi/pkg/driver"
	lc "github.com/piraeusdatastore/linstor-csi/pkg/linstor/highlevelclient"
	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	kubecl "sigs.k8s.io/controller-runtime/pkg/client"
)

func main() {
	var (
		logLevel              = flag.String("log-level", "info", "Enable debug log output. Choose from: panic, fatal, error, warn, info, debug")
		burst                 = flag.Int("linstor-api-burst", 1, "Maximum number of API requests allowed before being limited by requests-per-second. Default: 1 (no bursting)")
		rps                   = flag.Float64("linstor-api-requests-per-second", 0, "Maximum allowed number of LINSTOR API requests per second. Default: Unlimited")
		lsEndpoint            = flag.String("linstor-endpoint", "", "Controller API endpoint for LINSTOR")
		lsSkipTLSVerification = flag.Bool("linstor-skip-tls-verification", false, "If true, do not verify tls")
		bearerTokenFile       = flag.String("bearer-token", "", "Read the bearer token from the given file and use it for authentication.")
	)

	resourcesSchemeFuncs := []func(*apiruntime.Scheme) error{
		srv.AddToScheme,
		v1.AddToScheme,
		lsrv.AddToScheme,
	}

	logOut := os.Stderr
	logFmt := &log.TextFormatter{}

	log.SetFormatter(logFmt)
	log.SetOutput(logOut)

	logger := log.NewEntry(log.New())
	level, err := log.ParseLevel(*logLevel)
	if err != nil {
		os.Exit(1)
	}

	logger.Logger.SetLevel(level)
	logger.Logger.SetOutput(logOut)
	logger.Logger.SetFormatter(logFmt)

	kConfig, err := kubeutils.KubernetesDefaultConfigCreate()
	if err != nil {
		logger.Info("failed to create Kubernetes default config")
		os.Exit(1)
	}

	scheme := apiruntime.NewScheme()
	for _, f := range resourcesSchemeFuncs {
		err := f(scheme)
		if err != nil {
			logger.Infof("failed to add to scheme: %v", err)
			os.Exit(1)
		}
	}

	cl, err := kubecl.New(kConfig, kubecl.Options{
		Scheme: scheme,
	})
	if err != nil {
		logger.Infof("failed to create client: %v", err)
		os.Exit(1)
	}

	r := rate.Limit(*rps)
	if r <= 0 {
		r = rate.Inf
	}

	linstorOpts := []lapi.Option{
		lapi.Limit(r, *burst),
		lapi.UserAgent("linstor-csi/" + driver.Version),
		lapi.Log(logger),
	}

	if *lsEndpoint != "" {
		u, err := url.Parse(*lsEndpoint)
		if err != nil {
			log.Errorf("Failed to parse endpoint: %v", err)
			os.Exit(1)
		}

		linstorOpts = append(linstorOpts, lapi.BaseURL(u))
	}

	if *lsSkipTLSVerification {
		linstorOpts = append(linstorOpts, lapi.HTTPClient(&http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}))
	}

	if *bearerTokenFile != "" {
		token, err := os.ReadFile(*bearerTokenFile)
		if err != nil {
			log.Errorf("failed to read bearer token file: %v", err)
			os.Exit(1)
		}

		linstorOpts = append(linstorOpts, lapi.BearerToken(string(token)))
	}

	lc, err := lc.NewHighLevelClient(linstorOpts...)
	if err != nil {
		log.Errorf("failed to create linstor high level client: %v", err)
		os.Exit(1)
	}

	opts := config.NewDefaultOptions()

	if err = crd_sync.NewDRBDClusterSyncer(cl, lc, logger, opts).Sync(context.Background()); err != nil {
		log.Infof("failed to sync DRBD clusters: %v", err)
		os.Exit(1)
	}

	os.Exit(0)
}
