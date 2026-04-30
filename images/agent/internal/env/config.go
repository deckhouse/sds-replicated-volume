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
	"strconv"
	"sync"

	commonenv "github.com/deckhouse/sds-replicated-volume/lib/go/common/env"
)

const (
	NodeNameEnvVar = "NODE_NAME"

	DRBDMinPortEnvVar = "DRBD_MIN_PORT"
	DRBDMaxPortEnvVar = "DRBD_MAX_PORT"

	// defaults are different for each app, do not merge them
	DefaultHealthProbeBindAddress = ":4269"
	DefaultMetricsBindAddress     = ":4270"
)

type config struct {
	commonenv.CommonConfig
	nodeName    string
	drbdMinPort uint
	drbdMaxPort uint
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
	IsControllerEnabled(name string) bool
}

var _ Config = &config{}

var getConfigOnce = sync.OnceValues(loadConfig)

// GetConfig returns the process-wide environment configuration.
// The first call parses environment variables; subsequent calls return the
// cached result (and error, if any) wrapped with "getting env config".
// The returned Config is immutable.
func GetConfig() (Config, error) {
	cfg, err := getConfigOnce()
	if err != nil {
		return nil, fmt.Errorf("getting env config: %w", err)
	}
	return cfg, nil
}

func loadConfig() (Config, error) {
	cfg := &config{}

	cfg.nodeName = os.Getenv(NodeNameEnvVar)
	if cfg.nodeName == "" {
		hostName, err := os.Hostname()
		if err != nil {
			return nil, fmt.Errorf("getting hostname: %w", err)
		}
		cfg.nodeName = hostName
	}

	minPortStr := os.Getenv(DRBDMinPortEnvVar)
	if minPortStr == "" {
		return nil, fmt.Errorf("%w: %s is required", commonenv.ErrInvalidConfig, DRBDMinPortEnvVar)
	}
	minPort, err := strconv.ParseUint(minPortStr, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", DRBDMinPortEnvVar, err)
	}
	cfg.drbdMinPort = uint(minPort)

	maxPortStr := os.Getenv(DRBDMaxPortEnvVar)
	if maxPortStr == "" {
		return nil, fmt.Errorf("%w: %s is required", commonenv.ErrInvalidConfig, DRBDMaxPortEnvVar)
	}
	maxPort, err := strconv.ParseUint(maxPortStr, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", DRBDMaxPortEnvVar, err)
	}
	cfg.drbdMaxPort = uint(maxPort)

	if cfg.drbdMaxPort < cfg.drbdMinPort {
		return nil, fmt.Errorf("%w: invalid port range %d-%d", commonenv.ErrInvalidConfig, cfg.drbdMinPort, cfg.drbdMaxPort)
	}

	cfg.Load(DefaultHealthProbeBindAddress, DefaultMetricsBindAddress)

	return cfg, nil
}
