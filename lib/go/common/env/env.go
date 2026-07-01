/*
Copyright 2026 Flant JSC

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

// Package env provides shared environment configuration parsing for
// sds-replicated-volume binaries.
//
// Concrete app-specific config types embed CommonConfig and call its Load
// method to populate the shared fields, supplying their own defaults for the
// bind addresses.
package env

import (
	"errors"
	"os"
	"strings"
)

const (
	HealthProbeBindAddressEnvVar = "HEALTH_PROBE_BIND_ADDRESS"
	MetricsBindAddressEnvVar     = "METRICS_BIND_ADDRESS"
	EnabledControllersEnvVar     = "ENABLED_CONTROLLERS"
)

// ErrInvalidConfig is returned (wrapped) when the loaded environment is
// invalid. Callers may use errors.Is to detect it.
var ErrInvalidConfig = errors.New("invalid config")

// CommonConfig holds env vars common to all sds-replicated-volume binaries.
// Concrete app configs embed CommonConfig and call Load to populate it.
type CommonConfig struct {
	healthProbeBindAddress string
	metricsBindAddress     string
	enabledControllers     map[string]struct{} // nil means all enabled
}

func (c *CommonConfig) HealthProbeBindAddress() string {
	return c.healthProbeBindAddress
}

func (c *CommonConfig) MetricsBindAddress() string {
	return c.metricsBindAddress
}

// IsControllerEnabled reports whether the named controller should be started.
// When ENABLED_CONTROLLERS is not set (or empty), all controllers are enabled.
// When set, only the listed controllers (comma-separated) are enabled.
func (c *CommonConfig) IsControllerEnabled(name string) bool {
	if c.enabledControllers == nil {
		return true
	}
	_, ok := c.enabledControllers[name]
	return ok
}

// Load populates the common fields from environment variables, falling back
// to the supplied defaults for the bind addresses when not set. This method
// has no failure modes and never returns an error.
func (c *CommonConfig) Load(defaultHealthProbeBindAddress, defaultMetricsBindAddress string) {
	c.healthProbeBindAddress = os.Getenv(HealthProbeBindAddressEnvVar)
	if c.healthProbeBindAddress == "" {
		c.healthProbeBindAddress = defaultHealthProbeBindAddress
	}

	c.metricsBindAddress = os.Getenv(MetricsBindAddressEnvVar)
	if c.metricsBindAddress == "" {
		c.metricsBindAddress = defaultMetricsBindAddress
	}

	if raw := os.Getenv(EnabledControllersEnvVar); raw != "" {
		c.enabledControllers = make(map[string]struct{})
		for name := range strings.SplitSeq(raw, ",") {
			name = strings.TrimSpace(name)
			if name != "" {
				c.enabledControllers[name] = struct{}{}
			}
		}
	}
}
