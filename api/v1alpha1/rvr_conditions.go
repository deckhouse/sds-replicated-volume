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

package v1alpha1

const (
	// ReplicatedVolumeReplicaCondAddressConfiguredType indicates whether replica address has been configured.
	//
	// Reasons describe address configuration result.
	ReplicatedVolumeReplicaCondAddressConfiguredType                                = "AddressConfigured"
	ReplicatedVolumeReplicaCondAddressConfiguredReasonAddressConfigurationSucceeded = "AddressConfigurationSucceeded" // Address configured successfully.
	ReplicatedVolumeReplicaCondAddressConfiguredReasonNoFreePortAvailable           = "NoFreePortAvailable"           // No free port available.
)

const (
	// ReplicatedVolumeReplicaCondAttachedType indicates whether the replica is attached.
	//
	// Reasons describe attachment state, progress, or applicability.
	ReplicatedVolumeReplicaCondAttachedType                          = "Attached"
	ReplicatedVolumeReplicaCondAttachedReasonAttached                = "Attached"                // Attached (primary).
	ReplicatedVolumeReplicaCondAttachedReasonAttachPending           = "AttachPending"           // Waiting to become primary/attach.
	ReplicatedVolumeReplicaCondAttachedReasonAttachingNotApplicable  = "AttachingNotApplicable"  // Not applicable for this replica type.
	ReplicatedVolumeReplicaCondAttachedReasonAttachingNotInitialized = "AttachingNotInitialized" // Not enough status to decide.
	ReplicatedVolumeReplicaCondAttachedReasonDetached                = "Detached"                // Detached (secondary).
)

const (
	// ReplicatedVolumeReplicaCondBackingVolumeCreatedType indicates whether the backing volume has been created.
	//
	// Reasons describe applicability and create/delete outcomes.
	ReplicatedVolumeReplicaCondBackingVolumeCreatedType                              = "BackingVolumeCreated"
	ReplicatedVolumeReplicaCondBackingVolumeCreatedReasonBackingVolumeCreationFailed = "BackingVolumeCreationFailed" // Creation failed.
	ReplicatedVolumeReplicaCondBackingVolumeCreatedReasonBackingVolumeDeletionFailed = "BackingVolumeDeletionFailed" // Deletion failed.
	ReplicatedVolumeReplicaCondBackingVolumeCreatedReasonBackingVolumeNotReady       = "BackingVolumeNotReady"       // Backing volume is not ready.
	ReplicatedVolumeReplicaCondBackingVolumeCreatedReasonBackingVolumeReady          = "BackingVolumeReady"          // Backing volume is ready.
	ReplicatedVolumeReplicaCondBackingVolumeCreatedReasonNotApplicable               = "NotApplicable"               // Not applicable for this replica type.
)

const (
	// ReplicatedVolumeReplicaCondConfigurationAdjustedType indicates whether a configuration adjustment has been applied successfully.
	// (Used by controllers that adjust configuration; currently no standardized reasons.)
	ReplicatedVolumeReplicaCondConfigurationAdjustedType = "ConfigurationAdjusted"
)

const (
	// ReplicatedVolumeReplicaCondConfiguredType indicates whether replica configuration has been applied successfully.
	//
	// Reasons describe success or the failure class.
	ReplicatedVolumeReplicaCondConfiguredType                                   = "Configured"
	ReplicatedVolumeReplicaCondConfiguredReasonConfigurationAdjustmentSucceeded = "ConfigurationAdjustmentSucceeded"
	ReplicatedVolumeReplicaCondConfiguredReasonConfigurationCommandFailed       = "ConfigurationCommandFailed"
	ReplicatedVolumeReplicaCondConfiguredReasonConfigurationFailed              = "ConfigurationFailed"
	ReplicatedVolumeReplicaCondConfiguredReasonConfigured                       = "Configured" // Configuration applied successfully.
	ReplicatedVolumeReplicaCondConfiguredReasonDemoteFailed                     = "DemoteFailed"
	ReplicatedVolumeReplicaCondConfiguredReasonFileSystemOperationFailed        = "FileSystemOperationFailed"
	ReplicatedVolumeReplicaCondConfiguredReasonPromoteFailed                    = "PromoteFailed"
	ReplicatedVolumeReplicaCondConfiguredReasonSharedSecretAlgSelectionFailed   = "SharedSecretAlgSelectionFailed"
)

const (
	// ReplicatedVolumeReplicaCondDataInitializedType indicates whether the replica has been initialized.
	// Once true, it does not reset unless the replica type changes.
	//
	// Reasons describe observed disk state and applicability.
	ReplicatedVolumeReplicaCondDataInitializedType                                 = "DataInitialized"
	ReplicatedVolumeReplicaCondDataInitializedReasonDiskHasBeenSeenInUpToDateState = "DiskHasBeenSeenInUpToDateState" // Observed as UpToDate at least once.
	ReplicatedVolumeReplicaCondDataInitializedReasonDiskNeverWasInUpToDateState    = "DiskNeverWasInUpToDateState"    // Never observed as UpToDate.
	ReplicatedVolumeReplicaCondDataInitializedReasonNotApplicableToDiskless        = "NotApplicableToDiskless"        // Diskless replicas do not require initialization.
	ReplicatedVolumeReplicaCondDataInitializedReasonUnknownDiskState               = "UnknownDiskState"               // Disk state is unknown.
)

const (
	// ReplicatedVolumeReplicaCondIOReadyType indicates whether the replica is ready for I/O.
	// (Conceptually: online + in sync.)
	//
	// Reasons describe why it is not IO ready, or confirm it is IO ready.
	ReplicatedVolumeReplicaCondIOReadyType                     = "IOReady"
	ReplicatedVolumeReplicaCondIOReadyReasonAgentNotReady      = "AgentNotReady"      // Agent is not ready.
	ReplicatedVolumeReplicaCondIOReadyReasonAgentPodMissing    = "AgentPodMissing"    // Agent pod is missing.
	ReplicatedVolumeReplicaCondIOReadyReasonAgentStatusUnknown = "AgentStatusUnknown" // Agent status unknown (API error).
	ReplicatedVolumeReplicaCondIOReadyReasonIOReady            = "IOReady"            // Ready for I/O.
	ReplicatedVolumeReplicaCondIOReadyReasonNodeNotReady       = "NodeNotReady"       // Node is not ready.
	ReplicatedVolumeReplicaCondIOReadyReasonOffline            = "Offline"            // Not online.
	ReplicatedVolumeReplicaCondIOReadyReasonOutOfSync          = "OutOfSync"          // Not in sync.
	ReplicatedVolumeReplicaCondIOReadyReasonUnscheduled        = "Unscheduled"        // Not scheduled yet.
)

const (
	// ReplicatedVolumeReplicaCondInQuorumType indicates whether the replica is in quorum.
	//
	// Reasons describe quorum state or missing observability.
	ReplicatedVolumeReplicaCondInQuorumType                   = "InQuorum"
	ReplicatedVolumeReplicaCondInQuorumReasonInQuorum         = "InQuorum"         // Replica is in quorum.
	ReplicatedVolumeReplicaCondInQuorumReasonQuorumLost       = "QuorumLost"       // Replica is not in quorum.
	ReplicatedVolumeReplicaCondInQuorumReasonUnknownDiskState = "UnknownDiskState" // Disk state is unknown.
)

const (
	// ReplicatedVolumeReplicaCondInSyncType indicates whether the replica data is synchronized.
	//
	// Reasons describe disk state / sync state.
	ReplicatedVolumeReplicaCondInSyncType                        = "InSync"
	ReplicatedVolumeReplicaCondInSyncReasonAttaching             = "Attaching"             // Attaching is in progress.
	ReplicatedVolumeReplicaCondInSyncReasonDetaching             = "Detaching"             // Detaching is in progress.
	ReplicatedVolumeReplicaCondInSyncReasonDiskless              = "Diskless"              // Diskless replica is in sync.
	ReplicatedVolumeReplicaCondInSyncReasonDiskLost              = "DiskLost"              // Disk is lost.
	ReplicatedVolumeReplicaCondInSyncReasonFailed                = "Failed"                // Disk state is failed.
	ReplicatedVolumeReplicaCondInSyncReasonInSync                = "InSync"                // Diskful replica is in sync.
	ReplicatedVolumeReplicaCondInSyncReasonInconsistent          = "Inconsistent"          // Disk is inconsistent.
	ReplicatedVolumeReplicaCondInSyncReasonNegotiating           = "Negotiating"           // Negotiating connection/state.
	ReplicatedVolumeReplicaCondInSyncReasonOutdated              = "Outdated"              // Disk is outdated.
	ReplicatedVolumeReplicaCondInSyncReasonReplicaNotInitialized = "ReplicaNotInitialized" // Replica actual type not initialized yet.
	ReplicatedVolumeReplicaCondInSyncReasonUnknownDiskState      = "UnknownDiskState"      // Disk state is unknown.
)

const (
	// ReplicatedVolumeReplicaCondOnlineType indicates whether the replica is online.
	// (Conceptually: scheduled + initialized + in quorum.)
	//
	// Reasons describe why it is not online, or confirm it is online.
	ReplicatedVolumeReplicaCondOnlineType                     = "Online"
	ReplicatedVolumeReplicaCondOnlineReasonAgentNotReady      = "AgentNotReady"
	ReplicatedVolumeReplicaCondOnlineReasonAgentPodMissing    = "AgentPodMissing"    // No agent pod found on the node.
	ReplicatedVolumeReplicaCondOnlineReasonAgentStatusUnknown = "AgentStatusUnknown" // Can't determine agent status (API error).
	ReplicatedVolumeReplicaCondOnlineReasonNodeNotReady       = "NodeNotReady"
	ReplicatedVolumeReplicaCondOnlineReasonOnline             = "Online"
	ReplicatedVolumeReplicaCondOnlineReasonQuorumLost         = "QuorumLost"
	ReplicatedVolumeReplicaCondOnlineReasonUninitialized      = "Uninitialized"
	ReplicatedVolumeReplicaCondOnlineReasonUnscheduled        = "Unscheduled"
)

const (
	// ReplicatedVolumeReplicaCondReadyType indicates whether the replica is ready and operational.
	// (Currently no standardized reasons.)
	ReplicatedVolumeReplicaCondReadyType = "Ready"
)

const (
	// ReplicatedVolumeReplicaCondScheduledType indicates whether the replica has been scheduled to a node.
	//
	// Reasons describe scheduling outcome or failure.
	ReplicatedVolumeReplicaCondScheduledType                            = "Scheduled"
	ReplicatedVolumeReplicaCondScheduledReasonNoAvailableNodes          = "NoAvailableNodes"          // No nodes are available.
	ReplicatedVolumeReplicaCondScheduledReasonReplicaScheduled          = "ReplicaScheduled"          // Scheduled successfully.
	ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed          = "SchedulingFailed"          // Scheduling failed.
	ReplicatedVolumeReplicaCondScheduledReasonSchedulingPending         = "SchedulingPending"         // Scheduling is pending.
	ReplicatedVolumeReplicaCondScheduledReasonTopologyConstraintsFailed = "TopologyConstraintsFailed" // Topology constraints prevent scheduling.
)
