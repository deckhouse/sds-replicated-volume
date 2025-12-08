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
	"strconv"
)

const (
	NodeNameEnvVar = "NODE_NAME"

	DRBDMinPortEnvVar = "DRBD_MIN_PORT"
	DRBDMaxPortEnvVar = "DRBD_MAX_PORT"

	DRBDMinPortDefault uint = 7000
	DRBDMaxPortDefault uint = 7999

	HealthProbeBindAddressEnvVar = "HEALTH_PROBE_BIND_ADDRESS"
	MetricsPortEnvVar            = "METRICS_BIND_ADDRESS"

	// defaults are different for each app, do not merge them
	DefaultHealthProbeBindAddress = ":4269"
	DefaultMetricsBindAddress     = ":4270"
)

var ErrInvalidConfig = errors.New("invalid config")

type config struct {
	nodeName               string
	drbdMinPort            uint
	drbdMaxPort            uint
	healthProbeBindAddress string
	metricsBindAddress     string
}

func (c *config) HealthProbeBindAddress() string {
	return c.healthProbeBindAddress
}

func (c *config) MetricsBindAddress() string {
	return c.metricsBindAddress
}

func (c *config) DRBDMaxPort() uint {
	return c.drbdMaxPort
}

func (c *config) DRBDMinPort() uint {
	return c.drbdMinPort
}

func (c *config) NodeName() string {
	return c.nodeName
}

type Config interface {
	NodeName() string
	DRBDMinPort() uint
	DRBDMaxPort() uint
	HealthProbeBindAddress() string
	MetricsBindAddress() string
}

var _ Config = &config{}

func GetConfig() (*config, error) {
	cfg := &config{}

	//
	cfg.nodeName = os.Getenv(NodeNameEnvVar)
	if cfg.nodeName == "" {
		hostName, err := os.Hostname()
		if err != nil {
			return nil, fmt.Errorf("getting hostname: %w", err)
		}
		cfg.nodeName = hostName
	}

	//
	minPortStr := os.Getenv(DRBDMinPortEnvVar)
	if minPortStr == "" {
		cfg.drbdMinPort = DRBDMinPortDefault
	} else {
		minPort, err := strconv.ParseUint(minPortStr, 10, 32)
		if err != nil {
			return cfg, fmt.Errorf("parsing %s: %w", DRBDMinPortEnvVar, err)
		}
		cfg.drbdMinPort = uint(minPort)
	}

	//
	maxPortStr := os.Getenv(DRBDMaxPortEnvVar)
	if maxPortStr == "" {
		cfg.drbdMaxPort = DRBDMaxPortDefault
	} else {
		maxPort, err := strconv.ParseUint(maxPortStr, 10, 32)
		if err != nil {
			return cfg, fmt.Errorf("parsing %s: %w", DRBDMaxPortEnvVar, err)
		}
		cfg.drbdMaxPort = uint(maxPort)
	}

	//
	if cfg.drbdMaxPort < cfg.drbdMinPort {
		return cfg, fmt.Errorf("%w: invalid port range %d-%d", ErrInvalidConfig, cfg.drbdMinPort, cfg.drbdMaxPort)
	}

	//
	cfg.healthProbeBindAddress = os.Getenv(HealthProbeBindAddressEnvVar)
	if cfg.healthProbeBindAddress == "" {
		cfg.healthProbeBindAddress = DefaultHealthProbeBindAddress
	}

	//
	cfg.metricsBindAddress = os.Getenv(MetricsPortEnvVar)
	if cfg.metricsBindAddress == "" {
		cfg.metricsBindAddress = DefaultMetricsBindAddress
	}

	return cfg, nil
}
