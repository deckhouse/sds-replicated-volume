package v1alpha2

// Condition types for ReplicatedVolumeReplica status
const (
	// ConditionTypeReady indicates whether the replica is ready and operational
	ConditionTypeReady = "Ready"

	// ConditionTypeInitialSync indicates whether the initial synchronization has been completed
	ConditionTypeInitialSync = "InitialSync"
)

// ReplicatedVolumeReplicaConditionTypes lists all condition types used by RVR status
var ReplicatedVolumeReplicaConditionTypes = []string{
	ConditionTypeReady,
	ConditionTypeInitialSync,
}

// Condition reasons for Ready condition
const (
	// Ready condition reasons
	ReasonConfigurationFailed    = "ConfigurationFailed"
	ReasonMetadataCheckFailed    = "MetadataCheckFailed"
	ReasonMetadataCreationFailed = "MetadataCreationFailed"
	ReasonStatusCheckFailed      = "StatusCheckFailed"
	ReasonResourceUpFailed       = "ResourceUpFailed"
	ReasonAdjustmentFailed       = "AdjustmentFailed"
	ReasonReady                  = "Ready"
)

// Condition reasons for InitialSync condition
const (
	ReasonSafeForInitialSync     = "SafeForInitialSync"
	ReasonInitialUpToDateReached = "InitialUpToDateReached"
)
