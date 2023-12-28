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
)

// ScanInterval Scan block device interval seconds
const (
	ScanInterval                      = 10
	LinstorResourcesReconcileInterval = 60
	ConfigSecretName                  = "d8-sds-drbd-controller-config"
	LinstorLeaseName                  = "linstor"
	NodeName                          = "NODE_NAME"
	MetricsPortEnv                    = "METRICS_PORT"
	ControllerNamespaceEnv            = "CONTROLLER_NAMESPACE"
	HardcodedControllerNS             = "d8-sds-drbd-controller"
)

type Options struct {
	ScanInterval                      int
	LinstorResourcesReconcileInterval int
	ConfigSecretName                  string
	LinstorLeaseName                  string
	MetricsPort                       string
	ControllerNamespace               string
}

func NewConfig() (*Options, error) {
	var opts Options
	opts.ScanInterval = ScanInterval
	opts.LinstorResourcesReconcileInterval = LinstorResourcesReconcileInterval
	opts.LinstorLeaseName = LinstorLeaseName
	opts.ConfigSecretName = ConfigSecretName

	opts.MetricsPort = os.Getenv(MetricsPortEnv)
	if opts.MetricsPort == "" {
		opts.MetricsPort = ":8080"
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
