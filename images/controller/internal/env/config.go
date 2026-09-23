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
	"strconv"
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
	// ResyncSlotsPerNodeEnvVar holds how many resync ends one node may carry at a time.
	// An unset value means DefaultResyncSlotsPerNode; 0 switches the limit off entirely.
	ResyncSlotsPerNodeEnvVar = "RESYNC_SLOTS_PER_NODE"

	// defaults are different for each app, do not merge them.
	// The controller runs in the pod network, so these ports hold nothing on the
	// host; they stay outside 4200-4299, which is where the modules' host-network
	// ports live (docs/documentation/_data/deckhouse-ports.yml in deckhouse).
	// 8080 is taken in the pod by the kube-rbac-proxy.
	DefaultHealthProbeBindAddress = ":8081"
	// Loopback, like the agent: the kube-rbac-proxy of the same pod is what
	// publishes this endpoint, and a wildcard bind would answer unauthenticated
	// on the pod IP.
	DefaultMetricsBindAddress = "127.0.0.1:8082"

	// DefaultResyncSlotsPerNode is the resync-end budget assumed for a node when
	// RESYNC_SLOTS_PER_NODE is not set. Four: the limiter is on by default, because the
	// controller heals a lost diskful copy by itself and a mass heal — a node gone, every volume
	// that lost a copy refilling at once — is exactly the load the limiter was written to bound.
	// Four ends let a node send and receive at the same time without saturating one interface;
	// 0 still switches the limit off for a cluster that asks for it.
	DefaultResyncSlotsPerNode = 4
)

type Config struct {
	commonenv.CommonConfig
	podNamespace         string
	schedulerExtenderURL string
	dataNodeSelector     labels.Set
	resyncSlotsPerNode   int
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

// ResyncSlotsPerNode returns how many resync ends one node may carry at a time.
// Zero means no limit. See syncbudget.Limit.
func (c *Config) ResyncSlotsPerNode() int {
	return c.resyncSlotsPerNode
}

type ConfigProvider interface {
	PodNamespace() string
	SchedulerExtenderURL() string
	DataNodeSelector() labels.Set
	ResyncSlotsPerNode() int
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

	// Resync-slot budget (optional): caps concurrent resync ends per node.
	resyncSlotsPerNode, err := parseResyncSlotsPerNode(os.Getenv(ResyncSlotsPerNodeEnvVar))
	if err != nil {
		return nil, err
	}
	cfg.resyncSlotsPerNode = resyncSlotsPerNode

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

// parseResyncSlotsPerNode parses the RESYNC_SLOTS_PER_NODE env var, the per-node resync-end
// budget. An empty value yields DefaultResyncSlotsPerNode; an explicit 0 switches the limit off.
// A non-numeric or negative value fails fast at startup rather than silently disabling the
// limiter: "off" has to be asked for in the one documented way.
func parseResyncSlotsPerNode(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultResyncSlotsPerNode, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%w: %s must be a non-negative integer (0 disables the limit): %w",
			commonenv.ErrInvalidConfig, ResyncSlotsPerNodeEnvVar, err)
	}
	if value < 0 {
		return 0, fmt.Errorf("%w: %s must be a non-negative integer (0 disables the limit), got %d",
			commonenv.ErrInvalidConfig, ResyncSlotsPerNodeEnvVar, value)
	}

	return value, nil
}
