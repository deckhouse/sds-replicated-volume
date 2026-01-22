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

package env

import (
	"errors"
	"fmt"
	"os"
)

const (
	PodNamespaceEnvVar           = "POD_NAMESPACE"
	HealthProbeBindAddressEnvVar = "HEALTH_PROBE_BIND_ADDRESS"
	MetricsPortEnvVar            = "METRICS_BIND_ADDRESS"

	// defaults are different for each app, do not merge them
	DefaultHealthProbeBindAddress = ":4271"
	DefaultMetricsBindAddress     = ":4272"
)

var ErrInvalidConfig = errors.New("invalid config")

type Config struct {
	podNamespace           string
	healthProbeBindAddress string
	metricsBindAddress     string
}

func (c *Config) HealthProbeBindAddress() string {
	return c.healthProbeBindAddress
}

func (c *Config) MetricsBindAddress() string {
	return c.metricsBindAddress
}

func (c *Config) PodNamespace() string {
	return c.podNamespace
}

type ConfigProvider interface {
	PodNamespace() string
	HealthProbeBindAddress() string
	MetricsBindAddress() string
}

var _ ConfigProvider = &Config{}

func GetConfig() (*Config, error) {
	cfg := &Config{}

	// Pod namespace (required): used to discover agent pods.
	cfg.podNamespace = os.Getenv(PodNamespaceEnvVar)
	if cfg.podNamespace == "" {
		return nil, fmt.Errorf("%w: %s is required", ErrInvalidConfig, PodNamespaceEnvVar)
	}

	// Health probe bind address (optional, has default).
	cfg.healthProbeBindAddress = os.Getenv(HealthProbeBindAddressEnvVar)
	if cfg.healthProbeBindAddress == "" {
		cfg.healthProbeBindAddress = DefaultHealthProbeBindAddress
	}

	// Metrics bind address (optional, has default).
	cfg.metricsBindAddress = os.Getenv(MetricsPortEnvVar)
	if cfg.metricsBindAddress == "" {
		cfg.metricsBindAddress = DefaultMetricsBindAddress
	}

	return cfg, nil
}
