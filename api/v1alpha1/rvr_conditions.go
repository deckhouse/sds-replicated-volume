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
	ReplicatedVolumeReplicaCondAttachedReasonAttached                = "Attached"                // Attached (primary).
	ReplicatedVolumeReplicaCondAttachedReasonPending                 = "Pending"                 // Waiting to become primary/attach.
	ReplicatedVolumeReplicaCondAttachedReasonAttachingNotApplicable  = "AttachingNotApplicable"  // Not applicable for this replica type.
	ReplicatedVolumeReplicaCondAttachedReasonAttachingNotInitialized = "AttachingNotInitialized" // Not enough status to decide.
	ReplicatedVolumeReplicaCondAttachedReasonDetached                = "Detached"                // Detached (secondary).
)

const (
	// ReplicatedVolumeReplicaCondBackingVolumeReadyType indicates whether the backing volume is ready.
	//
	// Reasons describe applicability, provisioning/resizing progress, and outcomes.
	ReplicatedVolumeReplicaCondBackingVolumeReadyType                     = "BackingVolumeReady"
	ReplicatedVolumeReplicaCondBackingVolumeReadyReasonNotApplicable      = "NotApplicable"      // Not applicable for this replica type.
	ReplicatedVolumeReplicaCondBackingVolumeReadyReasonNotReady           = "NotReady"           // Backing volume exists but become not ready.
	ReplicatedVolumeReplicaCondBackingVolumeReadyReasonProvisioning       = "Provisioning"       // Backing volume is being provisioned.
	ReplicatedVolumeReplicaCondBackingVolumeReadyReasonProvisioningFailed = "ProvisioningFailed" // Provisioning failed.
	ReplicatedVolumeReplicaCondBackingVolumeReadyReasonReady              = "Ready"              // Backing volume is ready.
	ReplicatedVolumeReplicaCondBackingVolumeReadyReasonReprovisioning     = "Reprovisioning"     // Backing volume is being reprovisioned (replacing existing).
	ReplicatedVolumeReplicaCondBackingVolumeReadyReasonResizeFailed       = "ResizeFailed"       // Resize failed.
	ReplicatedVolumeReplicaCondBackingVolumeReadyReasonResizing           = "Resizing"           // Backing volume is being resized.
)

const (
	// ReplicatedVolumeReplicaCondConfiguredType indicates whether the replica's DRBD resource is configured.
	//
	// Reasons describe configuration state or applicability.
	ReplicatedVolumeReplicaCondConfiguredType                = "Configured"
	ReplicatedVolumeReplicaCondConfiguredReasonNotApplicable = "NotApplicable" // Not applicable (replica is being deleted).
)

const (
	// ReplicatedVolumeReplicaCondReadyType indicates whether the replica is ready for I/O.
	//
	// Reasons describe why it is not ready, or confirm it is ready.
	ReplicatedVolumeReplicaCondReadyType        = "Ready"
	ReplicatedVolumeReplicaCondReadyReasonReady = "Ready" // Ready for I/O.
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
