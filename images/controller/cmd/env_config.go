package main

import (
	"os"
)

const (
	HealthProbeBindAddressEnvVar  = "HEALTH_PROBE_BIND_ADDRESS"
	DefaultHealthProbeBindAddress = ":4271"
	MetricsPortEnvVar             = "METRICS_BIND_ADDRESS"
	DefaultMetricsBindAddress     = ":4272"
)

type EnvConfig struct {
	HealthProbeBindAddress string
	MetricsBindAddress     string
}

func GetEnvConfig() (*EnvConfig, error) {
	cfg := &EnvConfig{}

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
