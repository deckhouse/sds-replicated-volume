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
	"crypto/tls"
	"net/http"
	"net/url"
	"os"

	"drbd-cluster-sync/config"
	"drbd-cluster-sync/crd_sync"

	lapi "github.com/LINBIT/golinstor/client"
	lsrv "github.com/deckhouse/sds-replicated-volume/api/linstor"
	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	srv2 "github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	"github.com/piraeusdatastore/linstor-csi/pkg/driver"
	lc "github.com/piraeusdatastore/linstor-csi/pkg/linstor/highlevelclient"
	log "github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
	v1 "k8s.io/api/core/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	"github.com/deckhouse/sds-common-lib/kubeclient"
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

	logOut := os.Stderr
	logFmt := &log.TextFormatter{}

	log.SetFormatter(logFmt)
	log.SetOutput(logOut)

	logger := log.NewEntry(log.New())
	level, err := log.ParseLevel(opts.LogLevel)
	if err != nil {
		os.Exit(1)
	}

	logger.Logger.SetLevel(level)
	logger.Logger.SetOutput(logOut)
	logger.Logger.SetFormatter(logFmt)

	// kc, err := kubeclient.New(resourcesSchemeFuncs...)
	// if err != nil {
	// 	logger.Errorf("failed to initialize kube client: %v", err)
	// 	os.Exit(1)
	// }

	kc, err := kubeclient.New(resourcesSchemeFuncs...)
	if err != nil {
		logger.Errorf("failed to initialize kube client: %v", err)
		os.Exit(1)
	}

	r := rate.Limit(opts.RPS)
	if r <= 0 {
		r = rate.Inf
	}

	linstorOpts := []lapi.Option{
		lapi.Limit(r, opts.Burst),
		lapi.UserAgent("linstor-csi/" + driver.Version),
		lapi.Log(logger),
	}

	if opts.LSEndpoint != "" {
		u, err := url.Parse(opts.LSEndpoint)
		if err != nil {
			log.Errorf("Failed to parse endpoint: %v", err)
			os.Exit(1)
		}

		linstorOpts = append(linstorOpts, lapi.BaseURL(u))
	}

	if opts.LSSkipTLSVerification {
		linstorOpts = append(linstorOpts, lapi.HTTPClient(&http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}))
	}

	if opts.BearerTokenFile != "" {
		token, err := os.ReadFile(opts.BearerTokenFile)
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

	if err = crd_sync.NewDRBDClusterSyncer(kc, lc, logger, opts).Sync(ctx); err != nil {
		log.Infof("failed to sync DRBD clusters: %v", err)
		os.Exit(1)
	}

	os.Exit(0)
}
