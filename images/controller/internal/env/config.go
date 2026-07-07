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
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/labels"

	commonenv "github.com/deckhouse/sds-replicated-volume/lib/go/common/env"
)

const (
	PodNamespaceEnvVar         = "POD_NAMESPACE"
	SchedulerExtenderURLEnvVar = "SCHEDULER_EXTENDER_URL"
	// DataNodeSelectorEnvVar holds the module-wide data-node label selector
	// (sdsReplicatedVolume.dataNodes.nodeSelector) serialized as a JSON object
	// of string labels. An empty/unset value means "match all nodes".
	DataNodeSelectorEnvVar = "DATA_NODE_SELECTOR"

	// defaults are different for each app, do not merge them
	DefaultHealthProbeBindAddress = ":4271"
	DefaultMetricsBindAddress     = ":4272"
)

type Config struct {
	commonenv.CommonConfig
	podNamespace         string
	schedulerExtenderURL string
	dataNodeSelector     labels.Set
}

func (c *Config) PodNamespace() string {
	return c.podNamespace
}

func (c *Config) SchedulerExtenderURL() string {
	return c.schedulerExtenderURL
}

// DataNodeSelector returns the module-wide data-node label selector
// (from sdsReplicatedVolume.dataNodes.nodeSelector). A nil/empty set means
// "match all nodes".
func (c *Config) DataNodeSelector() labels.Set {
	return c.dataNodeSelector
}

type ConfigProvider interface {
	PodNamespace() string
	SchedulerExtenderURL() string
	DataNodeSelector() labels.Set
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

	// Data-node selector (optional): restricts which nodes may become data nodes.
	dataNodeSelector, err := parseDataNodeSelector(os.Getenv(DataNodeSelectorEnvVar))
	if err != nil {
		return nil, err
	}
	cfg.dataNodeSelector = dataNodeSelector

	cfg.Load(DefaultHealthProbeBindAddress, DefaultMetricsBindAddress)

	return cfg, nil
}

// parseDataNodeSelector parses the DATA_NODE_SELECTOR env var, a JSON object of
// string labels (e.g. `{"kubernetes.io/os":"linux"}`). An empty, "{}" or "null"
// value yields a nil set, which callers treat as "match all nodes". The label
// keys and values are validated so that a malformed selector fails fast at
// startup instead of silently selecting nothing.
func parseDataNodeSelector(raw string) (labels.Set, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" || raw == "null" {
		return nil, nil
	}

	m := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, fmt.Errorf("%w: %s must be a JSON object of string labels: %w",
			commonenv.ErrInvalidConfig, DataNodeSelectorEnvVar, err)
	}
	if len(m) == 0 {
		return nil, nil
	}

	set := labels.Set(m)
	if _, err := labels.ValidatedSelectorFromSet(set); err != nil {
		return nil, fmt.Errorf("%w: %s is not a valid label selector: %w",
			commonenv.ErrInvalidConfig, DataNodeSelectorEnvVar, err)
	}

	return set, nil
}
