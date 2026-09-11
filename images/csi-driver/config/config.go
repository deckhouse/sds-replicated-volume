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

package config

import (
	"flag"
	"fmt"
	"os"

	"github.com/deckhouse/sds-common-lib/logger"
	"github.com/deckhouse/sds-common-lib/monitoring"
	"github.com/deckhouse/sds-replicated-volume/images/csi-driver/driver"
)

const (
	NodeName                             = "KUBE_NODE_NAME"
	DefaultHealthProbeBindAddressEnvName = "HEALTH_PROBE_BIND_ADDRESS"
	DefaultHealthProbeBindAddress        = ":8081"
)

type Options struct {
	NodeName               string
	Version                string
	Loglevel               logger.Verbosity
	HealthProbeBindAddress string
	// MetricsBindAddress is where the CSI operation metrics are served, taken from
	// METRICS_BIND_ADDRESS. Empty means no endpoint: this binary runs both in the CSI
	// controller Deployment, which is on the pod network, and in the CSI node
	// DaemonSet, which is hostNetwork -- where a bound port is a node-level resource
	// and needs an entry in the Deckhouse port registry. The chart sets it only where
	// that holds.
	MetricsBindAddress string
	CsiAddress         string
	DriverName         string
	Address            string
}

func NewConfig() (*Options, error) {
	var opts Options

	opts.NodeName = os.Getenv(NodeName)
	if opts.NodeName == "" {
		return nil, fmt.Errorf("[NewConfig] required %s env variable is not specified", NodeName)
	}

	opts.MetricsBindAddress = os.Getenv(monitoring.EnvMetricsBindAddress)

	opts.HealthProbeBindAddress = os.Getenv(DefaultHealthProbeBindAddressEnvName)
	if opts.HealthProbeBindAddress == "" {
		opts.HealthProbeBindAddress = DefaultHealthProbeBindAddress
	}

	// Info, not Debug: an unset LOG_LEVEL has to mean the same thing in every
	// binary of the fleet, and the chart sets the variable in every container
	// anyway, so this is only what a container started outside the chart gets.
	opts.Loglevel = logger.VerbosityFromEnv(logger.InfoLevel)

	opts.Version = "dev"

	fl := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fl.StringVar(&opts.CsiAddress, "csi-endpoint", "unix:///var/lib/kubelet/plugins/"+driver.DefaultDriverName+"/csi.sock", "CSI endpoint")
	fl.StringVar(&opts.DriverName, "driver-name", driver.DefaultDriverName, "Name for the driver")
	fl.StringVar(&opts.Address, "address", driver.DefaultAddress, "Address to serve on")

	err := fl.Parse(os.Args[1:])
	if err != nil {
		return &opts, err
	}

	return &opts, nil
}
