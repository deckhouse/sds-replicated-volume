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
)

var ReplicatedVolumeReplicaConditions = map[string]struct{ UseObservedGeneration bool }{
	ConditionTypeReady:                 {true},
	ConditionTypeInitialSync:           {false},
	ConditionTypeIsPrimary:             {false},
	ConditionTypeDevicesReady:          {false},
	ConditionTypeConfigurationAdjusted: {true},
}

// Condition reasons for [ConditionTypeReady] condition
const (
	ReasonDevicesAreNotReady = "DevicesAreNotReady"
	ReasonAdjustmentFailed   = "AdjustmentFailed"
	ReasonReady              = "Ready"
)

// Condition reasons for [ConditionTypeConfigurationAdjusted] condition
const (
	ReasonConfigurationFailed    = "ConfigurationFailed"
	ReasonMetadataCheckFailed    = "MetadataCheckFailed"
	ReasonMetadataCreationFailed = "MetadataCreationFailed"
	ReasonStatusCheckFailed      = "StatusCheckFailed"
	ReasonResourceUpFailed       = "ResourceUpFailed"
	ReasonAdjustmentSucceeded    = "AdjustmentSucceeded"
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
