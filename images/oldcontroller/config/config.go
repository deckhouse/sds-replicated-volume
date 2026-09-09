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
	"log"
	"os"

	"github.com/deckhouse/sds-common-lib/logger"
	"github.com/deckhouse/sds-common-lib/monitoring"
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
	ControllerNamespaceEnv               = "CONTROLLER_NAMESPACE"
	HardcodedControllerNS                = "d8-sds-replicated-volume"
	ControllerName                       = "d8-controller"
)

type Options struct {
	ScanInterval                        int
	LinstorResourcesReconcileInterval   int
	ReplicatedStorageClassWatchInterval int
	ConfigSecretName                    string
	LinstorLeaseName                    string
	MetricsBindAddress                  string
	HealthProbeBindAddress              string
	ControllerNamespace                 string
	Loglevel                            logger.Verbosity
}

// defaultMetricsBindAddress keeps the endpoint on loopback when the chart says
// nothing: the kube-rbac-proxy of the same pod is what publishes it. The fleet-wide
// fallback is the wildcard ":8080", which would both answer unauthenticated on the
// pod IP and collide with the proxy, since the two carry the same port number.
const defaultMetricsBindAddress = "127.0.0.1:8080"

func NewConfig() (*Options, error) {
	var opts Options
	opts.ScanInterval = ScanInterval
	opts.LinstorResourcesReconcileInterval = LinstorResourcesReconcileInterval
	opts.ReplicatedStorageClassWatchInterval = ReplicatedStorageClassWatchInterval
	opts.LinstorLeaseName = LinstorLeaseName
	opts.ConfigSecretName = ConfigSecretName

	// Info, not Debug: an unset LOG_LEVEL has to mean the same thing in every
	// binary of the fleet, and the chart sets the variable in every container
	// anyway, so this is only what a container started outside the chart gets.
	opts.Loglevel = logger.VerbosityFromEnv(logger.InfoLevel)

	// METRICS_BIND_ADDRESS, the name the rest of the fleet reads. The value used to
	// be parsed from METRICS_PORT and then thrown away: the manager option that
	// consumed it had been commented out since controller-runtime moved it into
	// Metrics.BindAddress, so the endpoint ran on the library default.
	opts.MetricsBindAddress = monitoring.BindAddressFromEnvOr(defaultMetricsBindAddress)

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

// SdsReplicatedVolumeOperatorConfig mirrors the structure of the controller-config Secret
// (stringData."config"). The Secret is rendered by Helm from values / config-values.yaml
// and consumed by the controller at runtime.
//
// Struct tags are `json:` (not `yaml:`) because the controller uses
// sigs.k8s.io/yaml, which converts YAML to JSON and then uses encoding/json
// — `yaml:` tags have no effect there.
type SdsReplicatedVolumeOperatorConfig struct {
	NodeSelector map[string]string `yaml:"nodeSelector" json:"nodeSelector"`
	// StorageClassLabelIgnoredPrefixes holds the union of the system list (from
	// internal values) and the user-configured list (from ModuleConfig). Labels
	// on a ReplicatedStorageClass whose keys start with any of these prefixes are
	// NOT propagated to the managed Kubernetes StorageClass.
	StorageClassLabelIgnoredPrefixes []string `json:"storageClassLabelIgnoredPrefixes"`
}
