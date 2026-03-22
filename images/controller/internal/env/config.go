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
	"strings"
)

const (
	PodNamespaceEnvVar           = "POD_NAMESPACE"
	SchedulerExtenderURLEnvVar   = "SCHEDULER_EXTENDER_URL"
	HealthProbeBindAddressEnvVar = "HEALTH_PROBE_BIND_ADDRESS"
	MetricsPortEnvVar            = "METRICS_BIND_ADDRESS"
	EnabledControllersEnvVar     = "ENABLED_CONTROLLERS"

	// defaults are different for each app, do not merge them
	DefaultHealthProbeBindAddress = ":4271"
	DefaultMetricsBindAddress     = ":4272"
)

var ErrInvalidConfig = errors.New("invalid config")

type Config struct {
	podNamespace           string
	schedulerExtenderURL   string
	healthProbeBindAddress string
	metricsBindAddress     string
	enabledControllers     map[string]struct{} // nil means all enabled
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

func (c *Config) SchedulerExtenderURL() string {
	return c.schedulerExtenderURL
}

// IsControllerEnabled reports whether the named controller should be started.
// When ENABLED_CONTROLLERS is not set (or empty), all controllers are enabled.
// When set, only the listed controllers (comma-separated) are enabled.
func (c *Config) IsControllerEnabled(name string) bool {
	if c.enabledControllers == nil {
		return true // not set → all enabled
	}
	_, ok := c.enabledControllers[name]
	return ok
}

type ConfigProvider interface {
	PodNamespace() string
	SchedulerExtenderURL() string
	HealthProbeBindAddress() string
	MetricsBindAddress() string
	IsControllerEnabled(name string) bool
}

var _ ConfigProvider = &Config{}

func GetConfig() (*Config, error) {
	cfg := &Config{}

	// Pod namespace (required): used to discover agent pods.
	cfg.podNamespace = os.Getenv(PodNamespaceEnvVar)
	if cfg.podNamespace == "" {
		return nil, fmt.Errorf("%w: %s is required", ErrInvalidConfig, PodNamespaceEnvVar)
	}

	// Scheduler extender URL (required): used to query LVG scores from the scheduler extender.
	cfg.schedulerExtenderURL = os.Getenv(SchedulerExtenderURLEnvVar)
	if cfg.schedulerExtenderURL == "" {
		return nil, fmt.Errorf("%w: %s is required", ErrInvalidConfig, SchedulerExtenderURLEnvVar)
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

	// Enabled controllers (optional): comma-separated list of controller names.
	// When not set or empty, all controllers are enabled.
	if raw := os.Getenv(EnabledControllersEnvVar); raw != "" {
		cfg.enabledControllers = make(map[string]struct{})
		for _, name := range strings.Split(raw, ",") {
			name = strings.TrimSpace(name)
			if name != "" {
				cfg.enabledControllers[name] = struct{}{}
			}
		}
	}

	return cfg, nil
}
