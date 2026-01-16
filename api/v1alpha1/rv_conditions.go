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
	// ReplicatedVolumeCondBackingVolumeCreatedType indicates whether backing volumes exist for all diskful replicas.
	//
	// Reasons describe readiness and waiting conditions for backing volumes.
	ReplicatedVolumeCondBackingVolumeCreatedType                           = "BackingVolumeCreated"
	ReplicatedVolumeCondBackingVolumeCreatedReasonAllBackingVolumesReady   = "AllBackingVolumesReady"   // All backing volumes are ready.
	ReplicatedVolumeCondBackingVolumeCreatedReasonBackingVolumesNotReady   = "BackingVolumesNotReady"   // Some backing volumes are not ready.
	ReplicatedVolumeCondBackingVolumeCreatedReasonWaitingForBackingVolumes = "WaitingForBackingVolumes" // Backing volumes are not yet observable/created.
)

const (
	// ReplicatedVolumeCondConfiguredType indicates whether all replicas are configured.
	//
	// Reasons describe configuration progress / mismatch.
	ReplicatedVolumeCondConfiguredType                          = "Configured"
	ReplicatedVolumeCondConfiguredReasonAllReplicasConfigured   = "AllReplicasConfigured"   // All replicas are configured.
	ReplicatedVolumeCondConfiguredReasonConfigurationInProgress = "ConfigurationInProgress" // Configuration is still in progress.
	ReplicatedVolumeCondConfiguredReasonReplicasNotConfigured   = "ReplicasNotConfigured"   // Some replicas are not configured yet.
)

const (
	// ReplicatedVolumeCondDataQuorumType indicates whether the volume has data quorum (diskful replicas).
	//
	// Reasons describe data quorum state (reached/degraded/lost).
	ReplicatedVolumeCondDataQuorumType                     = "DataQuorum"
	ReplicatedVolumeCondDataQuorumReasonDataQuorumDegraded = "DataQuorumDegraded" // Data quorum is reached but degraded.
	ReplicatedVolumeCondDataQuorumReasonDataQuorumLost     = "DataQuorumLost"     // Data quorum is lost.
	ReplicatedVolumeCondDataQuorumReasonDataQuorumReached  = "DataQuorumReached"  // Data quorum is reached.
)

const (
	// ReplicatedVolumeCondDeviceMinorAssignedType indicates whether a DRBD device minor is assigned to the volume.
	//
	// Reasons describe assignment success/failure.
	ReplicatedVolumeCondDeviceMinorAssignedType                   = "DeviceMinorAssigned"
	ReplicatedVolumeCondDeviceMinorAssignedReasonAssignmentFailed = "AssignmentFailed" // Assignment attempt failed.
	ReplicatedVolumeCondDeviceMinorAssignedReasonAssigned         = "Assigned"         // Minor is assigned.
	ReplicatedVolumeCondDeviceMinorAssignedReasonDuplicate        = "Duplicate"        // Duplicate assignment detected.
)

const (
	// ReplicatedVolumeCondIOReadyType indicates whether the volume has enough IOReady replicas.
	//
	// Reasons describe why IO is ready or blocked due to replica readiness.
	ReplicatedVolumeCondIOReadyType                              = "IOReady"
	ReplicatedVolumeCondIOReadyReasonIOReady                     = "IOReady"                     // IO is ready.
	ReplicatedVolumeCondIOReadyReasonInsufficientIOReadyReplicas = "InsufficientIOReadyReplicas" // Not enough IOReady replicas.
	ReplicatedVolumeCondIOReadyReasonNoIOReadyReplicas           = "NoIOReadyReplicas"           // No replicas are IOReady.
)

const (
	// ReplicatedVolumeCondInitializedType indicates whether enough replicas are initialized.
	//
	// Reasons describe initialization progress and waiting conditions.
	ReplicatedVolumeCondInitializedType                           = "Initialized"
	ReplicatedVolumeCondInitializedReasonInitialized              = "Initialized"              // Initialization requirements are met.
	ReplicatedVolumeCondInitializedReasonInitializationInProgress = "InitializationInProgress" // Initialization is still in progress.
	ReplicatedVolumeCondInitializedReasonWaitingForReplicas       = "WaitingForReplicas"       // Waiting for replicas to appear/initialize.
)

const (
	// ReplicatedVolumeCondQuorumType indicates whether the volume has quorum.
	//
	// Reasons describe quorum state (reached/degraded/lost).
	ReplicatedVolumeCondQuorumType                 = "Quorum"
	ReplicatedVolumeCondQuorumReasonQuorumDegraded = "QuorumDegraded" // Quorum is reached but degraded.
	ReplicatedVolumeCondQuorumReasonQuorumLost     = "QuorumLost"     // Quorum is lost.
	ReplicatedVolumeCondQuorumReasonQuorumReached  = "QuorumReached"  // Quorum is reached.
)

const (
	// ReplicatedVolumeCondScheduledType indicates whether all replicas have been scheduled.
	//
	// Reasons describe scheduling progress / deficit.
	ReplicatedVolumeCondScheduledType                       = "Scheduled"
	ReplicatedVolumeCondScheduledReasonAllReplicasScheduled = "AllReplicasScheduled" // All replicas are scheduled.
	ReplicatedVolumeCondScheduledReasonReplicasNotScheduled = "ReplicasNotScheduled" // Some replicas are not scheduled yet.
	ReplicatedVolumeCondScheduledReasonSchedulingInProgress = "SchedulingInProgress" // Scheduling is still in progress.
)
