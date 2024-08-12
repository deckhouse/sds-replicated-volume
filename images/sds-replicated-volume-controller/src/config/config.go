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

package config

import (
	"log"
	"os"

	"sds-replicated-volume-controller/pkg/logger"
)

// ScanInterval Scan block device interval seconds
const (
	ScanInterval                         = 10
	LinstorResourcesReconcileInterval    = 120
	ReplicatedStorageClassWatchInterval  = 120
	ConfigSecretName                     = "d8-sds-replicated-volume-controller-config"
	LinstorLeaseName                     = "linstor"
	NodeName                             = "NODE_NAME"
	DefaultHealthProbeBindAddressEnvName = "HEALTH_PROBE_BIND_ADDRESS"
	DefaultHealthProbeBindAddress        = ":8081"
	MetricsPortEnv                       = "METRICS_PORT"
	ControllerNamespaceEnv               = "CONTROLLER_NAMESPACE"
	HardcodedControllerNS                = "d8-sds-replicated-volume"
	ControllerName                       = "sds-replicated-volume-controller"
	LogLevel                             = "LOG_LEVEL"
)

type Options struct {
	ScanInterval                        int
	LinstorResourcesReconcileInterval   int
	ReplicatedStorageClassWatchInterval int
	ConfigSecretName                    string
	LinstorLeaseName                    string
	MetricsPort                         string
	HealthProbeBindAddress              string
	ControllerNamespace                 string
	Loglevel                            logger.Verbosity
}

func NewConfig() (*Options, error) {
	var opts Options
	opts.ScanInterval = ScanInterval
	opts.LinstorResourcesReconcileInterval = LinstorResourcesReconcileInterval
	opts.ReplicatedStorageClassWatchInterval = ReplicatedStorageClassWatchInterval
	opts.LinstorLeaseName = LinstorLeaseName
	opts.ConfigSecretName = ConfigSecretName

	loglevel := os.Getenv(LogLevel)
	if loglevel == "" {
		opts.Loglevel = logger.DebugLevel
	} else {
		opts.Loglevel = logger.Verbosity(loglevel)
	}

	opts.MetricsPort = os.Getenv(MetricsPortEnv)
	if opts.MetricsPort == "" {
		opts.MetricsPort = ":8080"
	}

	opts.HealthProbeBindAddress = os.Getenv(DefaultHealthProbeBindAddressEnvName)
	if opts.HealthProbeBindAddress == "" {
		opts.HealthProbeBindAddress = DefaultHealthProbeBindAddress
	}

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

	return &opts, nil
}

type SdsReplicatedVolumeOperatorConfig struct {
	NodeSelector map[string]string `yaml:"nodeSelector"`
}
