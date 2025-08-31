package v1alpha2

// Condition types for ReplicatedVolumeReplica status
const (
	// ConditionTypeReady indicates whether the replica is ready and operational
	ConditionTypeReady = "Ready"

	// ConditionTypeInitialSync indicates whether the initial synchronization has been completed
	ConditionTypeInitialSync = "InitialSync"

	// ConditionTypeIsPrimary indicates whether the replica is primary
	ConditionTypeIsPrimary = "Primary"

	// ConditionTypeAllDevicesAreUpToDate indicates whether all the devices in UpToDate state
	ConditionTypeDevicesReady = "DevicesReady"

	ConditionTypeConfigurationAdjusted = "ConfigurationAdjusted"
)

var ReplicatedVolumeReplicaConditions = map[string]struct{ UseObservedGeneration bool }{
	ConditionTypeReady:                 {true},
	ConditionTypeInitialSync:           {false},
	ConditionTypeIsPrimary:             {true},
	ConditionTypeDevicesReady:          {false},
	ConditionTypeConfigurationAdjusted: {false},
}

// Condition reasons for [ConditionTypeReady] condition
const (
	ReasonDevicesAreNotReady = "DevicesAreNotReady"
	ReasonAdjustmentFailed   = "AdjustmentFailed"

	ReasonConfigurationFailed    = "ConfigurationFailed"
	ReasonMetadataCheckFailed    = "MetadataCheckFailed"
	ReasonMetadataCreationFailed = "MetadataCreationFailed"
	ReasonStatusCheckFailed      = "StatusCheckFailed"
	ReasonResourceUpFailed       = "ResourceUpFailed"
	ReasonReady                  = "Ready"
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
