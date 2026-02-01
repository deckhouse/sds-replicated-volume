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

package v1alpha1

const (
	// ReplicatedVolumeReplicaCondAttachedType indicates whether the replica is attached.
	//
	// Reasons describe attachment state, progress, or applicability.
	ReplicatedVolumeReplicaCondAttachedType                          = "Attached"
	ReplicatedVolumeReplicaCondAttachedReasonAgentNotReady           = "AgentNotReady"           // Agent is not ready.
	ReplicatedVolumeReplicaCondAttachedReasonApplyingConfiguration   = "ApplyingConfiguration"   // Configuration is being applied.
	ReplicatedVolumeReplicaCondAttachedReasonAttached                = "Attached"                // Attached and ready for I/O.
	ReplicatedVolumeReplicaCondAttachedReasonAttachingNotApplicable  = "AttachingNotApplicable"  // Not applicable for this replica type.
	ReplicatedVolumeReplicaCondAttachedReasonAttachingNotInitialized = "AttachingNotInitialized" // Not enough status to decide.
	ReplicatedVolumeReplicaCondAttachedReasonAttachmentFailed        = "AttachmentFailed"        // Expected to be attached, but not attached.
	ReplicatedVolumeReplicaCondAttachedReasonDetached                = "Detached"                // Detached.
	ReplicatedVolumeReplicaCondAttachedReasonDetachmentFailed        = "DetachmentFailed"        // Expected to be detached, but still attached.
	ReplicatedVolumeReplicaCondAttachedReasonIOSuspended             = "IOSuspended"             // Attached but I/O is suspended.
	ReplicatedVolumeReplicaCondAttachedReasonNotApplicable           = "NotApplicable"           // No DRBDR exists.
	ReplicatedVolumeReplicaCondAttachedReasonPending                 = "Pending"                 // Waiting to become attached.
)

const (
	// ReplicatedVolumeReplicaCondBackingVolumeReadyType indicates whether the backing volume is ready.
	//
	// Reasons describe applicability, provisioning/resizing progress, and outcomes.
	ReplicatedVolumeReplicaCondBackingVolumeReadyType                             = "BackingVolumeReady"
	ReplicatedVolumeReplicaCondBackingVolumeReadyReasonNotApplicable              = "NotApplicable"              // Not applicable for this replica type.
	ReplicatedVolumeReplicaCondBackingVolumeReadyReasonNotReady                   = "NotReady"                   // Backing volume exists but become not ready.
	ReplicatedVolumeReplicaCondBackingVolumeReadyReasonProvisioning               = "Provisioning"               // Backing volume is being provisioned.
	ReplicatedVolumeReplicaCondBackingVolumeReadyReasonProvisioningFailed         = "ProvisioningFailed"         // Provisioning failed.
	ReplicatedVolumeReplicaCondBackingVolumeReadyReasonReady                      = "Ready"                      // Backing volume is ready.
	ReplicatedVolumeReplicaCondBackingVolumeReadyReasonReprovisioning             = "Reprovisioning"             // Backing volume is being reprovisioned (replacing existing).
	ReplicatedVolumeReplicaCondBackingVolumeReadyReasonResizeFailed               = "ResizeFailed"               // Resize failed.
	ReplicatedVolumeReplicaCondBackingVolumeReadyReasonResizing                   = "Resizing"                   // Backing volume is being resized.
	ReplicatedVolumeReplicaCondBackingVolumeReadyReasonPendingScheduling          = "PendingScheduling"          // Waiting for node or storage assignment.
	ReplicatedVolumeReplicaCondBackingVolumeReadyReasonWaitingForReplicatedVolume = "WaitingForReplicatedVolume" // Waiting for ReplicatedVolume to be ready.
)

const (
	// ReplicatedVolumeReplicaCondDRBDConfiguredType indicates whether the replica's DRBD is fully configured
	// for the current datamesh revision.
	//
	// "DRBDConfigured" (Status=True) means:
	//   - DRBD was configured to match the intended state derived from this datamesh revision.
	//   - Backing volume (if Diskful) was configured: exists and matches intended LVG/ThinPool/Size.
	//   - Backing volume (if Diskful) is ready: reported ready and actual size >= intended size.
	//   - DRBD agent confirmed successful configuration.
	//
	// Note: "configured" does NOT mean:
	//   - DRBD connections are established (happens asynchronously after configuration).
	//   - Backing volume is synchronized (resync happens asynchronously if the volume was newly added).
	//
	// Reasons describe configuration state or applicability.
	ReplicatedVolumeReplicaCondDRBDConfiguredType                             = "DRBDConfigured"
	ReplicatedVolumeReplicaCondDRBDConfiguredReasonAgentNotReady              = "AgentNotReady"              // Agent is not ready.
	ReplicatedVolumeReplicaCondDRBDConfiguredReasonApplyingConfiguration      = "ApplyingConfiguration"      // Agent is applying DRBD configuration.
	ReplicatedVolumeReplicaCondDRBDConfiguredReasonConfigurationFailed        = "ConfigurationFailed"        // Agent failed to apply DRBD configuration.
	ReplicatedVolumeReplicaCondDRBDConfiguredReasonConfigured                 = "Configured"                 // Replica is fully configured for the current datamesh revision.
	ReplicatedVolumeReplicaCondDRBDConfiguredReasonNotApplicable              = "NotApplicable"              // Not applicable (replica is being deleted).
	ReplicatedVolumeReplicaCondDRBDConfiguredReasonPendingDatameshJoin        = "PendingDatameshJoin"        // DRBD preconfigured; waiting for datamesh membership.
	ReplicatedVolumeReplicaCondDRBDConfiguredReasonPendingScheduling          = "PendingScheduling"          // Waiting for node assignment.
	ReplicatedVolumeReplicaCondDRBDConfiguredReasonWaitingForBackingVolume    = "WaitingForBackingVolume"    // Waiting for backing volume (creating, resizing, or replacing).
	ReplicatedVolumeReplicaCondDRBDConfiguredReasonWaitingForReplicatedVolume = "WaitingForReplicatedVolume" // Waiting for ReplicatedVolume datamesh to be initialized.
)

const (
	// ReplicatedVolumeReplicaCondBackingVolumeUpToDateType indicates whether the replica's backing volume is up-to-date.
	//
	// Reasons describe sync state or applicability.
	ReplicatedVolumeReplicaCondBackingVolumeUpToDateType                         = "BackingVolumeUpToDate"
	ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonAgentNotReady          = "AgentNotReady"          // Agent is not ready.
	ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonApplyingConfiguration  = "ApplyingConfiguration"  // Agent is applying DRBD configuration.
	ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonAttaching              = "Attaching"              // Backing volume is being attached.
	ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonDetaching              = "Detaching"              // Backing volume is being detached.
	ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonFailed                 = "Failed"                 // Backing volume failed due to I/O errors.
	ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonUpToDate               = "UpToDate"               // Backing volume is fully up-to-date.
	ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonAbsent                 = "Absent"                 // Backing volume is not present.
	ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonSynchronizationBlocked = "SynchronizationBlocked" // Sync blocked, awaiting peer.
	ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonSynchronizing          = "Synchronizing"          // Backing volume is synchronizing.
	ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonUnknownState           = "UnknownState"           // Backing volume state is unknown.
)

const (
	// ReplicatedVolumeReplicaCondFullyConnectedType indicates whether the replica is fully connected to all peers.
	//
	// Reasons describe connection state or applicability.
	ReplicatedVolumeReplicaCondFullyConnectedType                        = "FullyConnected"
	ReplicatedVolumeReplicaCondFullyConnectedReasonAgentNotReady         = "AgentNotReady"         // Agent is not ready.
	ReplicatedVolumeReplicaCondFullyConnectedReasonApplyingConfiguration = "ApplyingConfiguration" // Configuration is being applied.
	ReplicatedVolumeReplicaCondFullyConnectedReasonConnectedToAllPeers   = "ConnectedToAllPeers"   // All peers are connected but not all paths are established.
	ReplicatedVolumeReplicaCondFullyConnectedReasonFullyConnected        = "FullyConnected"        // Fully connected to all peers on all paths.
	ReplicatedVolumeReplicaCondFullyConnectedReasonNoPeers               = "NoPeers"               // No peers configured.
	ReplicatedVolumeReplicaCondFullyConnectedReasonNotApplicable         = "NotApplicable"         // No DRBDR exists.
	ReplicatedVolumeReplicaCondFullyConnectedReasonNotConnected          = "NotConnected"          // Not connected to any peer.
	ReplicatedVolumeReplicaCondFullyConnectedReasonPartiallyConnected    = "PartiallyConnected"    // Connected to some but not all peers.
)

const (
	// ReplicatedVolumeReplicaCondReadyType indicates whether the replica is ready for I/O.
	//
	// Reasons describe why it is not ready, or confirm it is ready.
	ReplicatedVolumeReplicaCondReadyType                        = "Ready"
	ReplicatedVolumeReplicaCondReadyReasonAgentNotReady         = "AgentNotReady"         // Agent is not ready.
	ReplicatedVolumeReplicaCondReadyReasonApplyingConfiguration = "ApplyingConfiguration" // Configuration is being applied.
	ReplicatedVolumeReplicaCondReadyReasonDeleting              = "Deleting"              // Replica is being deleted.
	ReplicatedVolumeReplicaCondReadyReasonQuorumLost            = "QuorumLost"            // Quorum is lost.
	ReplicatedVolumeReplicaCondReadyReasonReady                 = "Ready"                 // Ready for I/O.
)

const (
	// ReplicatedVolumeReplicaCondSatisfyEligibleNodesType indicates whether the replica satisfies
	// the eligible nodes requirements from its storage pool.
	//
	// Reasons describe mismatch type or confirmation of satisfaction.
	ReplicatedVolumeReplicaCondSatisfyEligibleNodesType                         = "SatisfyEligibleNodes"
	ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonLVMVolumeGroupMismatch = "LVMVolumeGroupMismatch" // Node is eligible, but LVMVolumeGroup is not in the allowed list for this node.
	ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonNodeMismatch           = "NodeMismatch"           // Node is not in the eligible nodes list.
	ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonPendingConfiguration   = "PendingConfiguration"   // Configuration not yet available (e.g., ReplicatedStoragePool not found).
	ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonSatisfied              = "Satisfied"              // Replica satisfies eligible nodes requirements.
	ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonThinPoolMismatch       = "ThinPoolMismatch"       // Node and LVMVolumeGroup are eligible, but ThinPool is not allowed for this LVMVolumeGroup.
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
