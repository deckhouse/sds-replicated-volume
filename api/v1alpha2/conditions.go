package v1alpha2

// Condition types for ReplicatedVolumeReplica status
const (
	// ConditionTypeReady indicates whether the replica is ready and operational
	ConditionTypeReady = "Ready"

	// ConditionTypePrimary indicates the primary/secondary state of the replica
	ConditionTypePrimary = "Primary"

	// ConditionTypeInitialSyncCompleted indicates whether the initial synchronization has been completed
	ConditionTypeInitialSyncCompleted = "InitialSyncCompleted"
)

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

// Condition reasons for Primary condition
const (
	// Primary condition reasons
	ReasonRoleCorrect     = "RoleCorrect"
	ReasonPromotionFailed = "PromotionFailed"
	ReasonPrimary         = "Primary"
	ReasonDemotionFailed  = "DemotionFailed"
	ReasonSecondary       = "Secondary"
)

// Condition reasons for InitialSyncCompleted condition
const (
	// InitialSyncCompleted condition reasons
	ReasonFirstPrimaryPromoted = "FirstPrimaryPromoted"
)
