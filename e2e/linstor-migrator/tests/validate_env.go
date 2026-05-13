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

package tests

import (
	"fmt"
	"os"
)

// validateMigratorEnv mirrors storage-e2e internal/config.ValidateEnvironment checks
// (after bootstrapenv applies defaults). It must run before cluster.CreateOrConnectToTestCluster.
func validateMigratorEnv() error {
	if os.Getenv("SSH_USER") == "" {
		return fmt.Errorf("SSH_USER environment variable is required but not set")
	}
	if os.Getenv("SSH_HOST") == "" {
		return fmt.Errorf("SSH_HOST environment variable is required but not set")
	}
	if os.Getenv("TEST_CLUSTER_STORAGE_CLASS") == "" {
		return fmt.Errorf("TEST_CLUSTER_STORAGE_CLASS environment variable is required but not set")
	}
	if os.Getenv("TEST_MIGRATOR_MPO_IMAGE_TAG") == "" {
		return fmt.Errorf("TEST_MIGRATOR_MPO_IMAGE_TAG environment variable is required but not set")
	}
	if os.Getenv("DKP_LICENSE_KEY") == "" {
		return fmt.Errorf("DKP_LICENSE_KEY environment variable is required but not set")
	}
	if os.Getenv("REGISTRY_DOCKER_CFG") == "" {
		return fmt.Errorf("REGISTRY_DOCKER_CFG environment variable is required but not set")
	}

	ip := os.Getenv("IMAGE_PULL_POLICY")
	if ip != "Always" && ip != "IfNotExists" {
		return fmt.Errorf("IMAGE_PULL_POLICY has invalid value %q (must be Always or IfNotExists)", ip)
	}

	mode := os.Getenv("TEST_CLUSTER_CREATE_MODE")
	if mode == "" {
		return fmt.Errorf("TEST_CLUSTER_CREATE_MODE is required (alwaysUseExisting, alwaysCreateNew, or commander)")
	}
	if mode != "alwaysUseExisting" && mode != "alwaysCreateNew" && mode != "commander" {
		return fmt.Errorf("TEST_CLUSTER_CREATE_MODE has invalid value %q", mode)
	}

	if mode == "commander" {
		if os.Getenv("COMMANDER_URL") == "" {
			return fmt.Errorf("COMMANDER_URL is required when TEST_CLUSTER_CREATE_MODE is commander")
		}
		if os.Getenv("COMMANDER_TOKEN") == "" {
			return fmt.Errorf("COMMANDER_TOKEN is required when TEST_CLUSTER_CREATE_MODE is commander")
		}
		if os.Getenv("COMMANDER_CREATE_IF_NOT_EXISTS") == "true" && os.Getenv("COMMANDER_TEMPLATE_NAME") == "" {
			return fmt.Errorf("COMMANDER_TEMPLATE_NAME is required when COMMANDER_CREATE_IF_NOT_EXISTS is true")
		}
	}

	ll := os.Getenv("LOG_LEVEL")
	if ll != "debug" && ll != "info" && ll != "warn" && ll != "error" {
		return fmt.Errorf("LOG_LEVEL has invalid value %q", ll)
	}

	lte := os.Getenv("LOG_TIMESTAMPS_ENABLED")
	if lte != "true" && lte != "false" {
		return fmt.Errorf("LOG_TIMESTAMPS_ENABLED has invalid value %q (must be true or false)", lte)
	}

	return nil
}
