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

package v1alpha2

// Condition types for [ReplicatedVolumeReplica] status
const (
	// [ConditionTypeReady] indicates whether the replica is ready and operational
	ConditionTypeReady = "Ready"

	// [ConditionTypeInitialSync] indicates whether the initial synchronization has been completed
	ConditionTypeInitialSync = "InitialSync"

	// [ConditionTypeIsPrimary] indicates whether the replica is primary
	ConditionTypeIsPrimary = "Primary"

	// [ConditionTypeDevicesReady] indicates whether all the devices in UpToDate state
	ConditionTypeDevicesReady = "DevicesReady"

	// [ConditionTypeConfigurationAdjusted] indicates whether replica configuration has been applied successfully
	ConditionTypeConfigurationAdjusted = "ConfigurationAdjusted"

	// [ConditionTypeQuorum] indicates whether replica has achieved quorum
	ConditionTypeQuorum = "Quorum"

	// [ConditionTypeDiskIOSuspended] indicates whether replica has achieved quorum
	ConditionTypeDiskIOSuspended = "DiskIOSuspended"
)

var ReplicatedVolumeReplicaConditions = map[string]struct{ UseObservedGeneration bool }{
	ConditionTypeReady:                 {true},
	ConditionTypeInitialSync:           {false},
	ConditionTypeIsPrimary:             {false},
	ConditionTypeDevicesReady:          {false},
	ConditionTypeConfigurationAdjusted: {true},
	ConditionTypeQuorum:                {false},
	ConditionTypeDiskIOSuspended:       {false},
}

// Condition reasons for [ConditionTypeReady] condition
const (
	ReasonWaitingForInitialSync = "WaitingForInitialSync"
	ReasonDevicesAreNotReady    = "DevicesAreNotReady"
	ReasonAdjustmentFailed      = "AdjustmentFailed"
	ReasonNoQuorum              = "NoQuorum"
	ReasonDiskIOSuspended       = "DiskIOSuspended"
	ReasonReady                 = "Ready"
)

// Condition reasons for [ConditionTypeConfigurationAdjusted] condition
const (
	ReasonConfigurationFailed                           = "ConfigurationFailed"
	ReasonMetadataCheckFailed                           = "MetadataCheckFailed"
	ReasonMetadataCreationFailed                        = "MetadataCreationFailed"
	ReasonStatusCheckFailed                             = "StatusCheckFailed"
	ReasonResourceUpFailed                              = "ResourceUpFailed"
	ReasonConfigurationAdjustFailed                     = "ConfigurationAdjustFailed"
	ReasonConfigurationAdjustmentPausedUntilInitialSync = "ConfigurationAdjustmentPausedUntilInitialSync"
	ReasonPromotionDemotionFailed                       = "PromotionDemotionFailed"
	ReasonConfigurationAdjustmentSucceeded              = "ConfigurationAdjustmentSucceeded"
)

// Condition reasons for [ConditionTypeInitialSync] condition
const (
	ReasonInitialSyncRequiredButNotReady = "InitialSyncRequiredButNotReady"
	ReasonSafeForInitialSync             = "SafeForInitialSync"
	ReasonInitialDeviceReadinessReached  = "InitialDeviceReadinessReached"
)

// Condition reasons for [ConditionTypeDevicesReady] condition
const (
	ReasonDeviceIsNotReady = "DeviceIsNotReady"
	ReasonDeviceIsReady    = "DeviceIsReady"
)

// Condition reasons for [ConditionTypeIsPrimary] condition
const (
	ReasonResourceRoleIsPrimary    = "ResourceRoleIsPrimary"
	ReasonResourceRoleIsNotPrimary = "ResourceRoleIsNotPrimary"
)

// Condition reasons for [ConditionTypeQuorum] condition
const (
	ReasonNoQuorumStatus = "NoQuorumStatus"
	ReasonQuorumStatus   = "QuorumStatus"
)

// Condition reasons for [ConditionTypeDiskIOSuspended] condition
const (
	ReasonDiskIONotSuspendedStatus     = "DiskIONotSuspendedStatus"
	ReasonDiskIOSuspendedUnknownReason = "DiskIOSuspendedUnknownReason"
	ReasonDiskIOSuspendedByUser        = "DiskIOSuspendedByUser"
	ReasonDiskIOSuspendedNoData        = "DiskIOSuspendedNoData"
	ReasonDiskIOSuspendedFencing       = "DiskIOSuspendedFencing"
	ReasonDiskIOSuspendedQuorum        = "DiskIOSuspendedQuorum"
)
