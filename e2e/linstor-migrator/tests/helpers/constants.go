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

package helpers

import (
	"os"
	"strings"
	"time"
)

// Default timeout constants for all wait operations in the migrator e2e tests.
// All operations use the same timeout for consistency.
const (
	// DefaultWaitTimeout is the standard timeout for all wait operations (30 minutes).
	defaultWaitTimeout = 30 * time.Minute

	// NodeCount is the expected number of Ready nodes in the cluster.
	nodeCount = 4

	// DiskSize is the size of the disks attached to each node (30 GiB).
	diskSize = "30Gi"

	// TestNamespace is the namespace used for creating test resources.
	TestNamespace = "default"

	// LinstorStateFilePrefix is the prefix for linstor state files.
	linstorStateFilePrefix = "/tmp/migrator-e2e-linstor-state-"

	// VirtualDiskPrefix is the prefix for VirtualDisk names.
	virtualDiskPrefix = "migrator-e2e"

	// BlockDeviceWaitTimeout is the timeout for waiting BlockDevices to become consumable.
	blockDeviceWaitTimeout = defaultWaitTimeout

	// LVMVolumeGroupWaitTimeout is the timeout for waiting LVMVolumeGroups to become Ready.
	lvmVolumeGroupWaitTimeout = defaultWaitTimeout

	// ReplicatedStoragePoolWaitTimeout is the timeout for waiting RSPs to become Ready.
	replicatedStoragePoolWaitTimeout = defaultWaitTimeout

	// PodReadyWaitTimeout is the timeout for waiting Pods to become Ready.
	podReadyWaitTimeout = defaultWaitTimeout

	// MigrationWaitTimeout is the timeout for waiting migration to complete.
	MigrationWaitTimeout = 30 * time.Minute

	// PollingInterval is the default interval for polling operations.
	pollingInterval = 10 * time.Second

	// ModuleNameSNC is the module name for sds-node-configurator.
	moduleNameSNC = "sds-node-configurator"

	// ModuleNameSRV is the module name for sds-replicated-volume.
	moduleNameSRV = "sds-replicated-volume"

	// NamespaceSRV is the namespace for sds-replicated-volume components.
	namespaceSRV = "d8-sds-replicated-volume"

	// NoPersistentVolumeLabelKey marks RVs created without a corresponding PV.
	// In this scenario all RVs are PV-backed, so this label must be absent.
	noPersistentVolumeLabelKey = "sds-replicated-volume.deckhouse.io/no-persistent-volume"

	// switchToAutoConfigurationLabelKey is set by linstor-migrator on RVs that
	// have a matching PV and must be switched from Manual to Auto configuration
	// during stage 4. After successful switch, this label is removed.
	switchToAutoConfigurationLabelKey = "sds-replicated-volume.deckhouse.io/switch-to-auto-configuration"

	// autoConfigurationBlockedLabelKey is set by linstor-migrator stage 4 on RVs
	// that cannot be switched to Auto configuration (e.g. RSC is missing or
	// in a terminal phase). Must be absent in the happy path.
	autoConfigurationBlockedLabelKey = "sds-replicated-volume.deckhouse.io/auto-configuration-blocked"

	// migrationConversionRSPLabelKey is set by linstor-migrator stage 4 on
	// temporary ReplicatedStoragePools created for RSC conversion support.
	// After stage 4 cleanup, this label must be absent from all RSPs.
	migrationConversionRSPLabelKey = "sds-replicated-volume.deckhouse.io/migration-conversion-rsp"

	// AutoReplicatedStoragePoolNamePrefix is the name prefix for ReplicatedStoragePool objects
	// created by linstor-migrator from LINSTOR data. Must stay in sync with
	// images/linstor-migrator/internal/config.AutoReplicatedStoragePoolNamePrefix.
	AutoReplicatedStoragePoolNamePrefix = "linstor-auto-"

	// LinstorControllerDeployment is the deployment name for linstor-controller.
	linstorControllerDeployment = "linstor-controller"

	// LinstorNodeDaemonSet is the daemonset name for linstor-node.
	linstorNodeDaemonSet = "linstor-node"

	// NewControllerDeployment is the deployment name for the new control plane controller.
	newControllerDeployment = "controller"

	// NewAgentDaemonSet is the daemonset name for the new control plane agent.
	newAgentDaemonSet = "agent"
)

// IsTestDebugEnabled returns true when TEST_DEBUG is set to a non-empty value.
func IsTestDebugEnabled() bool {
	return strings.TrimSpace(os.Getenv("TEST_DEBUG")) != ""
}
