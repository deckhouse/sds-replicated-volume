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
	// Reasons describe attachment state or progress.
	ReplicatedVolumeReplicaCondAttachedType                        = "Attached"
	ReplicatedVolumeReplicaCondAttachedReasonAgentNotReady         = "AgentNotReady"         // Agent is not ready.
	ReplicatedVolumeReplicaCondAttachedReasonApplyingConfiguration = "ApplyingConfiguration" // Configuration is being applied.
	ReplicatedVolumeReplicaCondAttachedReasonAttached              = "Attached"              // Attached and ready for I/O.
	ReplicatedVolumeReplicaCondAttachedReasonAttachmentFailed      = "AttachmentFailed"      // Expected to be attached, but not attached.
	ReplicatedVolumeReplicaCondAttachedReasonDetachmentFailed      = "DetachmentFailed"      // Expected to be detached, but still attached.
	ReplicatedVolumeReplicaCondAttachedReasonIOSuspended           = "IOSuspended"           // Attached but I/O is suspended.
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
	ReplicatedVolumeReplicaCondBackingVolumeUpToDateType                          = "BackingVolumeUpToDate"
	ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonAbsent                  = "Absent"                  // Backing volume is not present.
	ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonAgentNotReady           = "AgentNotReady"           // Agent is not ready.
	ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonApplyingConfiguration   = "ApplyingConfiguration"   // Agent is applying DRBD configuration.
	ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonFailed                  = "Failed"                  // Backing volume failed due to I/O errors.
	ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonRequiresSynchronization = "RequiresSynchronization" // Backing volume requires synchronization from peer.
	ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonSynchronizing           = "Synchronizing"           // Backing volume is synchronizing.
	ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonUnknown                 = "Unknown"                 // Backing volume state is unknown.
	ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonUpToDate                = "UpToDate"                // Backing volume is fully up-to-date.
)

const (
	// ReplicatedVolumeReplicaCondConfiguredType indicates whether the replica configuration
	// matches the intended state from spec.
	//
	// True: replica is a datamesh member and its actual configuration matches spec.
	// False: there are pending changes (join/leave/role change/backing volume change)
	//        or prerequisites are not met (scheduling, eligibility).
	// Unknown: configuration data is not yet available.
	// Absent: replica is being deleted and is no longer a datamesh member.
	//
	// Reasons describe the pending operation or blocking condition.
	ReplicatedVolumeReplicaCondConfiguredType                             = "Configured"
	ReplicatedVolumeReplicaCondConfiguredReasonConfigured                 = "Configured"                 // Replica is configured as intended.
	ReplicatedVolumeReplicaCondConfiguredReasonNodeNotEligible            = "NodeNotEligible"            // Node is not in the eligible nodes list of the storage pool.
	ReplicatedVolumeReplicaCondConfiguredReasonPendingBackingVolumeChange = "PendingBackingVolumeChange" // Waiting to change backing volume.
	ReplicatedVolumeReplicaCondConfiguredReasonPendingConfiguration       = "PendingConfiguration"       // Configuration data is not yet available (e.g., RSP not found).
	ReplicatedVolumeReplicaCondConfiguredReasonPendingJoin                = "PendingJoin"                // Waiting to join the datamesh.
	ReplicatedVolumeReplicaCondConfiguredReasonPendingLeave               = "PendingLeave"               // Waiting to leave the datamesh (during deletion).
	ReplicatedVolumeReplicaCondConfiguredReasonPendingRoleChange          = "PendingRoleChange"          // Waiting to change role in the datamesh.
	ReplicatedVolumeReplicaCondConfiguredReasonPendingScheduling          = "PendingScheduling"          // Waiting for node or storage assignment.
	ReplicatedVolumeReplicaCondConfiguredReasonStorageNotEligible         = "StorageNotEligible"         // Intended storage (LVG/ThinPool) is not eligible on the node.
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
