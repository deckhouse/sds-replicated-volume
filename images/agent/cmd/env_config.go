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
	"os"
)

const (
	NodeNameEnvVar                = "NODE_NAME"
	HealthProbeBindAddressEnvVar  = "HEALTH_PROBE_BIND_ADDRESS"
	DefaultHealthProbeBindAddress = ":4269"
	MetricsPortEnvVar             = "METRICS_BIND_ADDRESS"
	DefaultMetricsBindAddress     = ":4270"
)

type EnvConfig struct {
	NodeName               string
	HealthProbeBindAddress string
	MetricsBindAddress     string
}

func GetEnvConfig() (*EnvConfig, error) {
	cfg := &EnvConfig{}

	cfg.NodeName = os.Getenv(NodeNameEnvVar)
	if cfg.NodeName == "" {
		if hostName, err := os.Hostname(); err != nil {
			return nil, fmt.Errorf("getting hostname: %w", err)
		} else {
			cfg.NodeName = hostName
		}
	}

	cfg.HealthProbeBindAddress = os.Getenv(HealthProbeBindAddressEnvVar)
	if cfg.HealthProbeBindAddress == "" {
		cfg.HealthProbeBindAddress = DefaultHealthProbeBindAddress
	}

	cfg.MetricsBindAddress = os.Getenv(MetricsPortEnvVar)
	if cfg.MetricsBindAddress == "" {
		cfg.MetricsBindAddress = DefaultMetricsBindAddress
	}

	return cfg, nil
}
