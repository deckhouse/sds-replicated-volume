/*
CSI Driver for Linstor
Copyright © 2018 LINBIT USA, LLC

This program is free software; you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation; either version 2 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program; if not, see <http://www.gnu.org/licenses/>.
*/

package main

import (
	"crypto/tls"
	"flag"
	"net/http"
	"net/url"
	"os"

	linstor "github.com/LINBIT/golinstor"
	lapi "github.com/LINBIT/golinstor/client"
	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/piraeusdatastore/linstor-csi/pkg/client"
	"github.com/piraeusdatastore/linstor-csi/pkg/driver"
	kubutils "github.com/piraeusdatastore/linstor-csi/pkg/kubeutils"
	lc "github.com/piraeusdatastore/linstor-csi/pkg/linstor/highlevelclient"
	"github.com/piraeusdatastore/linstor-csi/pkg/volume"
	log "github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	storageV1 "k8s.io/api/storage/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	kubecl "sigs.k8s.io/controller-runtime/pkg/client"
)

func main() {
	var resourcesSchemeFuncs = []func(*apiruntime.Scheme) error{
		srv.AddToScheme,
		snc.AddToScheme,
		storageV1.AddToScheme,
		corev1.AddToScheme,
	}

	var (
		lsEndpoint            = flag.String("linstor-endpoint", "", "Controller API endpoint for LINSTOR")
		lsSkipTLSVerification = flag.Bool("linstor-skip-tls-verification", false, "If true, do not verify tls")
		csiEndpoint           = flag.String("csi-endpoint", "unix:///var/lib/kubelet/plugins/replicated.csi.storage.deckhouse.io/csi.sock", "CSI endpoint")
		node                  = flag.String("node", "", "Node ID to pass to node service")
		logLevel              = flag.String("log-level", "info", "Enable debug log output. Choose from: panic, fatal, error, warn, info, debug")
		rps                   = flag.Float64("linstor-api-requests-per-second", 0, "Maximum allowed number of LINSTOR API requests per second. Default: Unlimited")
		burst                 = flag.Int("linstor-api-burst", 1, "Maximum number of API requests allowed before being limited by requests-per-second. Default: 1 (no bursting)")
		bearerTokenFile       = flag.String("bearer-token", "", "Read the bearer token from the given file and use it for authentication.")
		propNs                = flag.String("property-namespace", linstor.NamespcAuxiliary, "Limit the reported topology keys to properties from the given namespace.")
		labelBySP             = flag.Bool("label-by-storage-pool", true, "Set to false to disable labeling of nodes based on their configured storage pools.")
	)

	flag.Var(&volume.DefaultRemoteAccessPolicy, "default-remote-access-policy", "")

	flag.Parse()

	// TODO: Take log outputs and options from the command line.
	logOut := os.Stderr
	logFmt := &log.TextFormatter{}

	// Setup logging incase there are errors external to the driver/client.
	log.SetFormatter(logFmt)
	log.SetOutput(logOut)

	logger := log.NewEntry(log.New())
	level, err := log.ParseLevel(*logLevel)
	if err != nil {
		log.Fatal(err)
	}

	logger.Logger.SetLevel(level)
	logger.Logger.SetOutput(logOut)
	logger.Logger.SetFormatter(logFmt)

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
			log.Fatal(err)
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
			log.Fatal(err)
		}

		linstorOpts = append(linstorOpts, lapi.BearerToken(string(token)))
	}

	c, err := lc.NewHighLevelClient(linstorOpts...)
	if err != nil {
		log.Fatal(err)
	}

	linstorClient, err := client.NewLinstor(
		client.APIClient(c),
		client.LogFmt(logFmt),
		client.LogLevel(*logLevel),
		client.LogOut(logOut),
		client.PropertyNamespace(*propNs),
		client.LabelBySP(*labelBySP),
	)
	if err != nil {
		log.Fatal(err)
	}

	kConfig, err := kubutils.KubernetesDefaultConfigCreate()
	if err != nil {
		log.Fatal(err)
	}

	scheme := apiruntime.NewScheme()
	for _, f := range resourcesSchemeFuncs {
		err := f(scheme)
		if err != nil {
			log.Fatal(err)
		}
	}

	cl, err := kubecl.New(kConfig, kubecl.Options{
		Scheme: scheme,
	})
	if err != nil {
		log.Fatal(err)
	}

	drv, err := driver.NewDriver(
		driver.Assignments(linstorClient),
		driver.Endpoint(*csiEndpoint),
		driver.LogLevel(*logLevel),
		driver.LogOut(logOut),
		driver.Mounter(linstorClient),
		driver.NodeID(*node),
		driver.Snapshots(linstorClient),
		driver.Storage(linstorClient),
		driver.VolumeStatter(linstorClient),
		driver.Expander(linstorClient),
		driver.NodeInformer(linstorClient),
		driver.TopologyPrefix(*propNs),
		driver.ConfigureKubernetesIfAvailable(),
		driver.Kubeclient(cl),
		driver.LinstorHighClient(c),
	)
	if err != nil {
		log.Fatal(err)
	}

	//nolint:errcheck
	defer drv.Stop()

	if err := drv.Run(); err != nil {
		log.Fatal(err)
	}
}
