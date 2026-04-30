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
	"fmt"
	"os"
	"sync"

	commonenv "github.com/deckhouse/sds-replicated-volume/lib/go/common/env"
)

const (
	PodNamespaceEnvVar         = "POD_NAMESPACE"
	SchedulerExtenderURLEnvVar = "SCHEDULER_EXTENDER_URL"

	// defaults are different for each app, do not merge them
	DefaultHealthProbeBindAddress = ":4271"
	DefaultMetricsBindAddress     = ":4272"
)

type Config struct {
	commonenv.CommonConfig
	podNamespace         string
	schedulerExtenderURL string
}

func (c *Config) PodNamespace() string {
	return c.podNamespace
}

func (c *Config) SchedulerExtenderURL() string {
	return c.schedulerExtenderURL
}

type ConfigProvider interface {
	PodNamespace() string
	SchedulerExtenderURL() string
	HealthProbeBindAddress() string
	MetricsBindAddress() string
	IsControllerEnabled(name string) bool
}

var _ ConfigProvider = &Config{}

var getConfigOnce = sync.OnceValues(loadConfig)

// GetConfig returns the process-wide environment configuration.
// The first call parses environment variables; subsequent calls return the
// cached result (and error, if any) wrapped with "getting env config".
// The returned *Config is immutable.
func GetConfig() (*Config, error) {
	cfg, err := getConfigOnce()
	if err != nil {
		return nil, fmt.Errorf("getting env config: %w", err)
	}
	return cfg, nil
}

func loadConfig() (*Config, error) {
	cfg := &Config{}

	// Pod namespace (required): used to discover agent pods.
	cfg.podNamespace = os.Getenv(PodNamespaceEnvVar)
	if cfg.podNamespace == "" {
		return nil, fmt.Errorf("%w: %s is required", commonenv.ErrInvalidConfig, PodNamespaceEnvVar)
	}

	// Scheduler extender URL (required): used to query LVG scores from the scheduler extender.
	cfg.schedulerExtenderURL = os.Getenv(SchedulerExtenderURLEnvVar)
	if cfg.schedulerExtenderURL == "" {
		return nil, fmt.Errorf("%w: %s is required", commonenv.ErrInvalidConfig, SchedulerExtenderURLEnvVar)
	}

	cfg.Load(DefaultHealthProbeBindAddress, DefaultMetricsBindAddress)

	return cfg, nil
}
