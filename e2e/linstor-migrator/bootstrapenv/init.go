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

// Package bootstrapenv applies storage-e2e default environment variables via os.Setenv
// before any github.com/deckhouse/storage-e2e package is imported. This mirrors the
// default-value portion of storage-e2e/internal/config.ValidateEnvironment.
package bootstrapenv

import (
	"fmt"
	"os"
)

func init() {
	applyDefaults()
}

func setenv(key, val string) {
	if err := os.Setenv(key, val); err != nil {
		panic(fmt.Sprintf("bootstrapenv: os.Setenv(%q): %v", key, err))
	}
}

func applyDefaults() {
	if os.Getenv("YAML_CONFIG_FILENAME") == "" {
		setenv("YAML_CONFIG_FILENAME", "cluster_config.yml")
	}

	tc := os.Getenv("TEST_CLUSTER_CLEANUP")
	if tc == "" || (tc != "true" && tc != "True") {
		setenv("TEST_CLUSTER_CLEANUP", "false")
	}

	if os.Getenv("SSH_PRIVATE_KEY") == "" {
		setenv("SSH_PRIVATE_KEY", "~/.ssh/id_rsa")
	}
	if os.Getenv("SSH_VM_USER") == "" {
		setenv("SSH_VM_USER", "cloud")
	}
	if os.Getenv("SSH_PUBLIC_KEY") == "" {
		setenv("SSH_PUBLIC_KEY", "~/.ssh/id_rsa.pub")
	}
	if os.Getenv("TEST_CLUSTER_NAMESPACE") == "" {
		setenv("TEST_CLUSTER_NAMESPACE", "e2e-linstor-migrator")
	}

	if os.Getenv("IMAGE_PULL_POLICY") == "" {
		setenv("IMAGE_PULL_POLICY", "IfNotExists")
	}

	if os.Getenv("LOG_LEVEL") == "" {
		setenv("LOG_LEVEL", "debug")
	}

	if os.Getenv("LOG_TIMESTAMPS_ENABLED") == "" {
		setenv("LOG_TIMESTAMPS_ENABLED", "true")
	}

	if os.Getenv("TEST_CLUSTER_CREATE_MODE") == "commander" {
		if os.Getenv("COMMANDER_CLUSTER_NAME") == "" {
			setenv("COMMANDER_CLUSTER_NAME", "e2e-linstor-migrator")
		}
		if os.Getenv("COMMANDER_CREATE_IF_NOT_EXISTS") == "" {
			setenv("COMMANDER_CREATE_IF_NOT_EXISTS", "false")
		}
		if os.Getenv("COMMANDER_WAIT_TIMEOUT") == "" {
			setenv("COMMANDER_WAIT_TIMEOUT", "30m")
		}
	}
}
