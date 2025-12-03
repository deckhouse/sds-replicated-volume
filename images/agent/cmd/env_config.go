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
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/cluster"
)

const (
	NodeNameEnvVar                = "NODE_NAME"
	HealthProbeBindAddressEnvVar  = "HEALTH_PROBE_BIND_ADDRESS"
	DefaultHealthProbeBindAddress = ":4269"
	MetricsPortEnvVar             = "METRICS_BIND_ADDRESS"
	DefaultMetricsBindAddress     = ":4270"

	DRBDMinPortEnvVar       = "DRBD_MIN_PORT"
	DRBDMinPortDefault uint = 7000

	DRBDMaxPortEnvVar       = "DRBD_MAX_PORT"
	DRBDMaxPortDefault uint = 7999
)

var ErrInvalidConfig = errors.New("invalid config")

func GetEnvConfig() (cluster.Config, error) {
	cfg := cluster.Config{}

	cfg.NodeName = os.Getenv(NodeNameEnvVar)
	if cfg.NodeName == "" {
		hostName, err := os.Hostname()
		if err != nil {
			return cfg, fmt.Errorf("getting hostname: %w", err)
		}
		cfg.NodeName = hostName
	}

	cfg.HealthProbeBindAddress = os.Getenv(HealthProbeBindAddressEnvVar)
	if cfg.HealthProbeBindAddress == "" {
		cfg.HealthProbeBindAddress = DefaultHealthProbeBindAddress
	}

	cfg.MetricsBindAddress = os.Getenv(MetricsPortEnvVar)
	if cfg.MetricsBindAddress == "" {
		cfg.MetricsBindAddress = DefaultMetricsBindAddress
	}

	minPortStr := os.Getenv(DRBDMinPortEnvVar)
	if minPortStr == "" {
		cfg.DRBD.MinPort = DRBDMinPortDefault
	} else {
		minPort, err := strconv.ParseUint(minPortStr, 10, 32)
		if err != nil {
			return cfg, fmt.Errorf("parsing %s: %w", DRBDMinPortEnvVar, err)
		}
		cfg.DRBD.MinPort = uint(minPort)
	}

	maxPortStr := os.Getenv(DRBDMaxPortEnvVar)
	if maxPortStr == "" {
		cfg.DRBD.MaxPort = DRBDMaxPortDefault
	} else {
		maxPort, err := strconv.ParseUint(maxPortStr, 10, 32)
		if err != nil {
			return cfg, fmt.Errorf("parsing %s: %w", DRBDMaxPortEnvVar, err)
		}
		cfg.DRBD.MaxPort = uint(maxPort)
	}

	if cfg.DRBD.MaxPort < cfg.DRBD.MinPort {
		return cfg, fmt.Errorf("%w: invalid port range %d-%d", ErrInvalidConfig, cfg.DRBD.MinPort, cfg.DRBD.MaxPort)
	}

	return cfg, nil
}
