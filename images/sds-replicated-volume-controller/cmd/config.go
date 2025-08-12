package main

import (
	"fmt"
	"os"
)

const (
	NodeNameEnvVar                = "NODE_NAME"
	HealthProbeBindAddressEnvVar  = "HEALTH_PROBE_BIND_ADDRESS"
	DefaultHealthProbeBindAddress = ":4271"
	MetricsPortEnvVar             = "METRICS_BIND_ADDRESS"
	DefaultMetricsBindAddress     = ":4272"
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
