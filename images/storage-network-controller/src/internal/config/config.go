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

package config

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"

	"storage-network-controller/internal/logger"
)

const (
	ControllerNamespaceEnv = "CONTROLLER_NAMESPACE"
	DefaultDiscoverySec    = 15
	HardcodedControllerNS  = "d8-sds-replicated-volume"
	LogLevelEnv            = "LOG_LEVEL"
)

type StorageNetworkCIDR []string

type Options struct {
	ControllerNamespace string
	DiscoverySec				int
	DiscoveryMode				bool
	Loglevel            logger.Verbosity
	StorageNetworkCIDR  StorageNetworkCIDR
}

// String is an implementation of the flag.Value interface
func (i *StorageNetworkCIDR) String() string {
	return fmt.Sprintf("%v", *i)
}

// Set is an implementation of the flag.Value interface
func (i *StorageNetworkCIDR) Set(value string) error {
	_, _, err := net.ParseCIDR(value)

	if err != nil {
		return err
	}

	*i = append(*i, value)
	return nil
}

func NewConfig() (*Options, error) {
	var opts Options

	loglevel := os.Getenv(LogLevelEnv)
	if loglevel == "" {
		opts.Loglevel = logger.DebugLevel
	} else {
		opts.Loglevel = logger.Verbosity(loglevel)
	}

	opts.DiscoverySec = DefaultDiscoverySec

	opts.ControllerNamespace = os.Getenv(ControllerNamespaceEnv)
	if opts.ControllerNamespace == "" {
		namespace, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
		if err != nil {
			log.Printf("Failed to get namespace from filesystem: %v", err)
			log.Printf("Using hardcoded namespace: %s", HardcodedControllerNS)
			opts.ControllerNamespace = HardcodedControllerNS
		} else {
			log.Printf("Got namespace from filesystem: %s", string(namespace))
			opts.ControllerNamespace = string(namespace)
		}
	}

	fl := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fl.BoolVar(&opts.DiscoveryMode, "discovery", false, "Enable discovery mode. This mode filling an Node.Status.addresses field")
	fl.Var(&opts.StorageNetworkCIDR, "storage-network-cidr", "Set storage network CIDR blocks. Can be passed multiple times.")

	err := fl.Parse(os.Args[1:])
	if err != nil {
		fmt.Printf("error parsing flags, err: %s\n", err.Error())
		os.Exit(1)
	}

	return &opts, nil
}
