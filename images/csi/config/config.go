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

	"github.com/deckhouse/sds-local-volume/images/sds-local-volume-csi/driver"
	"github.com/deckhouse/sds-local-volume/images/sds-local-volume-csi/pkg/logger"
)

const (
	NodeName                             = "KUBE_NODE_NAME"
	LogLevel                             = "LOG_LEVEL"
	DefaultHealthProbeBindAddressEnvName = "HEALTH_PROBE_BIND_ADDRESS"
	DefaultHealthProbeBindAddress        = ":8081"
)

type Options struct {
	NodeName               string
	Version                string
	Loglevel               logger.Verbosity
	HealthProbeBindAddress string
	CsiAddress             string
	DriverName             string
	Address                string
}

func NewConfig() (*Options, error) {
	var opts Options

	opts.NodeName = os.Getenv(NodeName)
	if opts.NodeName == "" {
		return nil, fmt.Errorf("[NewConfig] required %s env variable is not specified", NodeName)
	}

	opts.HealthProbeBindAddress = os.Getenv(DefaultHealthProbeBindAddressEnvName)
	if opts.HealthProbeBindAddress == "" {
		opts.HealthProbeBindAddress = DefaultHealthProbeBindAddress
	}

	loglevel := os.Getenv(LogLevel)
	if loglevel == "" {
		opts.Loglevel = logger.DebugLevel
	} else {
		opts.Loglevel = logger.Verbosity(loglevel)
	}

	opts.Version = "dev"

	fl := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fl.StringVar(&opts.CsiAddress, "csi-address", "unix:///var/lib/kubelet/plugins/"+driver.DefaultDriverName+"/csi.sock", "CSI address")
	fl.StringVar(&opts.DriverName, "driver-name", driver.DefaultDriverName, "Name for the driver")
	fl.StringVar(&opts.Address, "address", driver.DefaultAddress, "Address to serve on")

	err := fl.Parse(os.Args[1:])
	if err != nil {
		return &opts, err
	}

	return &opts, nil
}
