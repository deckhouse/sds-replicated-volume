package suite

import (
	"encoding/json"
	"os"
	"testing"
)

// NodeConfig describes a node and its LVG for e2e tests.
type NodeConfig struct {
	Name    string `json:"name"`
	LVGName string `json:"lvgName"`
}

// AgentPodsConfig describes how to find agent pods in the cluster.
type AgentPodsConfig struct {
	Namespace     string `json:"namespace"`
	LabelSelector string `json:"labelSelector"`
}

// Config holds the e2e test environment configuration.
type Config struct {
	Nodes     []NodeConfig    `json:"nodes"`
	AgentPods AgentPodsConfig `json:"agentPods"`
}

// DiscoverConfig Discovers test configuration from a JSON file on the filesystem.
// The file path is read from the E2E_CONFIG_PATH environment variable; if not set,
// defaults to ".env.json" in the current directory.
// The configuration must have at least 2 nodes.
func DiscoverConfig(t *testing.T) Config {
	path := os.Getenv("E2E_CONFIG_PATH")
	if path == "" {
		path = ".env.json"
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading config from %s: %v", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parsing config from %s: %v", path, err)
	}

	if len(cfg.Nodes) < 2 {
		t.Fatalf("config must have at least 2 nodes, got %d", len(cfg.Nodes))
	}

	for i, node := range cfg.Nodes {
		if node.Name == "" {
			t.Fatalf("config: nodes[%d].name must not be empty", i)
		}
		if node.LVGName == "" {
			t.Fatalf("config: nodes[%d].lvgName must not be empty", i)
		}
	}

	if cfg.AgentPods.Namespace == "" {
		t.Fatal("config: agentPods.namespace must not be empty")
	}
	if cfg.AgentPods.LabelSelector == "" {
		t.Fatal("config: agentPods.labelSelector must not be empty")
	}

	return cfg
}
