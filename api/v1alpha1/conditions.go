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

import (
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TODO split RV/RVR conditions :ConditionTypeRVInitialized

// ConditionSpecAgnosticEqual compares only meaning of a condition,
// ignoring ObservedGeneration and LastTransitionTime.
func ConditionSpecAgnosticEqual(a, b *metav1.Condition) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Type == b.Type &&
		a.Status == b.Status &&
		a.Reason == b.Reason &&
		a.Message == b.Message
}

// ConditionSpecAwareEqual compares meaning of a condition and also
// requires ObservedGeneration to match. It still ignores LastTransitionTime.
func ConditionSpecAwareEqual(a, b *metav1.Condition) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Type == b.Type &&
		a.Status == b.Status &&
		a.Reason == b.Reason &&
		a.Message == b.Message &&
		a.ObservedGeneration == b.ObservedGeneration
}

// IsConditionPresentAndSpecAgnosticEqual checks that a condition with the same Type as expected exists in conditions
// and is equal to expected ignoring ObservedGeneration and LastTransitionTime.
func IsConditionPresentAndSpecAgnosticEqual(conditions []metav1.Condition, expected metav1.Condition) bool {
	actual := meta.FindStatusCondition(conditions, expected.Type)
	return actual != nil && ConditionSpecAgnosticEqual(actual, &expected)
}

// IsConditionPresentAndSpecAwareEqual checks that a condition with the same Type as expected exists in conditions
// and is equal to expected requiring ObservedGeneration to match, but ignoring LastTransitionTime.
func IsConditionPresentAndSpecAwareEqual(conditions []metav1.Condition, expected metav1.Condition) bool {
	actual := meta.FindStatusCondition(conditions, expected.Type)
	return actual != nil && ConditionSpecAwareEqual(actual, &expected)
}

// =============================================================================
// Condition types managed by rvr_status_conditions controller
// =============================================================================

const (
	// [RVRCondOnlineType] indicates whether replica is online (Scheduled AND Initialized AND InQuorum)
	RVRCondOnlineType = "Online"

	// [RVRCondIOReadyType] indicates whether replica is ready for I/O operations (Online AND InSync)
	RVRCondIOReadyType = "IOReady"
)

// =============================================================================
// Condition types managed by rv_status_conditions controller
// =============================================================================

const (
	// [RVCondScheduledType] indicates whether all RVRs have been scheduled
	RVCondScheduledType = "Scheduled"

	// [RVCondBackingVolumeCreatedType] indicates whether all diskful RVRs have backing volumes created
	RVCondBackingVolumeCreatedType = "BackingVolumeCreated"

	// [RVCondConfiguredType] indicates whether all RVRs are configured
	RVCondConfiguredType = "Configured"

	// [RVCondInitializedType] indicates whether enough RVRs are initialized
	RVCondInitializedType = "Initialized"

	// [RVCondQuorumType] indicates whether RV has quorum
	RVCondQuorumType = "Quorum"

	// [RVCondDataQuorumType] indicates whether RV has data quorum (diskful replicas)
	RVCondDataQuorumType = "DataQuorum"

	// [RVCondIOReadyType] indicates whether RV has enough IOReady replicas
	RVCondIOReadyType = "IOReady"
)

// =============================================================================
// Condition types for other RV controllers (not used by rv_status_conditions)
// =============================================================================

const (
	// [RVRCondConfigurationAdjustedType] indicates whether replica configuration has been applied successfully
	RVRCondConfigurationAdjustedType = "ConfigurationAdjusted"

	// [RVCondDeviceMinorAssignedType] indicates whether deviceMinor has been assigned to ReplicatedVolume.
	RVCondDeviceMinorAssignedType = "DeviceMinorAssigned"
)

// =============================================================================
// Condition types read by rvr_status_conditions controller (managed by other controllers)
// =============================================================================

const (
	// [RVRCondScheduledType] indicates whether replica has been scheduled to a node
	RVRCondScheduledType = "Scheduled"

	// [RVRCondDataInitializedType] indicates whether replica has been initialized.
	// Does not reset after True, unless replica type has changed.
	RVRCondDataInitializedType = "DataInitialized"

	// [RVRCondInQuorumType] indicates whether replica is in quorum
	RVRCondInQuorumType = "InQuorum"

	// [RVRCondInSyncType] indicates whether replica data is synchronized
	RVRCondInSyncType = "InSync"
)

// =============================================================================
// Condition types read by rv_status_conditions controller (managed by other RVR controllers)
// =============================================================================

// NOTE: BackingVolumeCreated is represented by [RVRCondBackingVolumeCreatedType].

// =============================================================================
// Condition types for RVR controllers
// =============================================================================

const (
	// [RVRCondReadyType] indicates whether the replica is ready and operational
	RVRCondReadyType = "Ready"

	// [RVRCondConfiguredType] indicates whether replica configuration has been applied successfully
	RVRCondConfiguredType = "Configured"

	// [RVRCondAddressConfiguredType] indicates whether replica address has been configured
	RVRCondAddressConfiguredType = "AddressConfigured"

	// [RVRCondBackingVolumeCreatedType] indicates whether the backing volume (LVMLogicalVolume) has been created
	RVRCondBackingVolumeCreatedType = "BackingVolumeCreated"

	// [RVRCondAttachedType] indicates whether the replica has been attached
	RVRCondAttachedType = "Attached"
)

// =============================================================================
// Condition types and reasons for RVA (ReplicatedVolumeAttachment) controllers
// =============================================================================

const (
	// [RVACondReadyType] indicates whether the attachment is ready for use:
	// Attached=True AND ReplicaIOReady=True.
	RVACondReadyType = "Ready"

	// [RVACondAttachedType] indicates whether the volume is attached to the requested node.
	// This condition is the former RVA "Ready" condition and contains detailed attach progress reasons.
	RVACondAttachedType = "Attached"

	// [RVACondReplicaIOReadyType] indicates whether the replica on the requested node is IOReady.
	// It mirrors ReplicatedVolumeReplica condition IOReady (Status/Reason/Message) for the replica on rva.spec.nodeName.
	RVACondReplicaIOReadyType = "ReplicaIOReady"
)

const (
	// RVA Ready condition reasons reported via [RVACondReadyType] (aggregate).
	RVACondReadyReasonReady             = "Ready"
	RVACondReadyReasonNotAttached       = "NotAttached"
	RVACondReadyReasonReplicaNotIOReady = "ReplicaNotIOReady"
)

const (
	// RVA Attached condition reasons reported via [RVACondAttachedType].
	RVACondAttachedReasonWaitingForActiveAttachmentsToDetach = "WaitingForActiveAttachmentsToDetach"
	RVACondAttachedReasonWaitingForReplicatedVolume          = "WaitingForReplicatedVolume"
	RVACondAttachedReasonWaitingForReplicatedVolumeIOReady   = "WaitingForReplicatedVolumeIOReady"
	RVACondAttachedReasonWaitingForReplica                   = "WaitingForReplica"
	RVACondAttachedReasonConvertingTieBreakerToAccess        = "ConvertingTieBreakerToAccess"
	RVACondAttachedReasonUnableToProvideLocalVolumeAccess    = "UnableToProvideLocalVolumeAccess"
	RVACondAttachedReasonLocalityNotSatisfied                = "LocalityNotSatisfied"
	RVACondAttachedReasonSettingPrimary                      = "SettingPrimary"
	RVACondAttachedReasonAttached                            = "Attached"
)

const (
	// RVA ReplicaIOReady condition reasons reported via [RVACondReplicaIOReadyType].
	// Most of the time this condition mirrors the replica's IOReady condition reason;
	// this reason is used only when replica/condition is not yet observable.
	RVACondReplicaIOReadyReasonWaitingForReplica = "WaitingForReplica"
)

// Replication values for [ReplicatedStorageClass] spec
const (
	ReplicationNone                       = "None"
	ReplicationAvailability               = "Availability"
	ReplicationConsistencyAndAvailability = "ConsistencyAndAvailability"
)

// =============================================================================
// Condition reasons used by rvr_status_conditions controller
// =============================================================================

// Condition reasons for [RVRCondOnlineType] condition
const (
	RVRCondOnlineReasonOnline             = "Online"
	RVRCondOnlineReasonUnscheduled        = "Unscheduled"
	RVRCondOnlineReasonUninitialized      = "Uninitialized"
	RVRCondOnlineReasonQuorumLost         = "QuorumLost"
	RVRCondOnlineReasonNodeNotReady       = "NodeNotReady"
	RVRCondOnlineReasonAgentNotReady      = "AgentNotReady"
	RVRCondOnlineReasonAgentPodMissing    = "AgentPodMissing"    // No agent pod found on node
	RVRCondOnlineReasonAgentStatusUnknown = "AgentStatusUnknown" // Can't determine status (API error)
)

// Condition reasons for [RVRCondIOReadyType] condition
const (
	RVRCondIOReadyReasonIOReady     = "IOReady"
	RVRCondIOReadyReasonOffline     = "Offline"
	RVRCondIOReadyReasonOutOfSync   = "OutOfSync"
	RVRCondIOReadyReasonUnscheduled = "Unscheduled"

	// Unavailability reasons also used for IOReady
	RVRCondIOReadyReasonNodeNotReady       = "NodeNotReady"
	RVRCondIOReadyReasonAgentNotReady      = "AgentNotReady"
	RVRCondIOReadyReasonAgentPodMissing    = "AgentPodMissing"
	RVRCondIOReadyReasonAgentStatusUnknown = "AgentStatusUnknown"
)

// =============================================================================
// Condition reasons used by rv_status_conditions controller
// =============================================================================

// Condition reasons for [RVCondScheduledType] condition
const (
	RVCondScheduledReasonAllReplicasScheduled = "AllReplicasScheduled"
	RVCondScheduledReasonReplicasNotScheduled = "ReplicasNotScheduled"
	RVCondScheduledReasonSchedulingInProgress = "SchedulingInProgress"
)

// Condition reasons for [RVCondBackingVolumeCreatedType] condition
const (
	RVCondBackingVolumeCreatedReasonAllBackingVolumesReady   = "AllBackingVolumesReady"
	RVCondBackingVolumeCreatedReasonBackingVolumesNotReady   = "BackingVolumesNotReady"
	RVCondBackingVolumeCreatedReasonWaitingForBackingVolumes = "WaitingForBackingVolumes"
)

// Condition reasons for [RVCondConfiguredType] condition
const (
	RVCondConfiguredReasonAllReplicasConfigured   = "AllReplicasConfigured"
	RVCondConfiguredReasonReplicasNotConfigured   = "ReplicasNotConfigured"
	RVCondConfiguredReasonConfigurationInProgress = "ConfigurationInProgress"
)

// Condition reasons for [RVCondInitializedType] condition
const (
	RVCondInitializedReasonInitialized              = "Initialized"
	RVCondInitializedReasonInitializationInProgress = "InitializationInProgress"
	RVCondInitializedReasonWaitingForReplicas       = "WaitingForReplicas"
)

// Condition reasons for [RVCondQuorumType] condition
const (
	RVCondQuorumReasonQuorumReached  = "QuorumReached"
	RVCondQuorumReasonQuorumDegraded = "QuorumDegraded"
	RVCondQuorumReasonQuorumLost     = "QuorumLost"
)

// Condition reasons for [RVCondDataQuorumType] condition
const (
	RVCondDataQuorumReasonDataQuorumReached  = "DataQuorumReached"
	RVCondDataQuorumReasonDataQuorumDegraded = "DataQuorumDegraded"
	RVCondDataQuorumReasonDataQuorumLost     = "DataQuorumLost"
)

// Condition reasons for [RVCondIOReadyType] condition
const (
	RVCondIOReadyReasonIOReady                     = "IOReady"
	RVCondIOReadyReasonNoIOReadyReplicas           = "NoIOReadyReplicas"
	RVCondIOReadyReasonInsufficientIOReadyReplicas = "InsufficientIOReadyReplicas"
)

// =============================================================================
// Condition reasons reserved for other controllers (not used yet)
// =============================================================================

// Condition reasons for [RVRCondConfiguredType] condition
const (
	RVRCondConfiguredReasonConfigurationFailed              = "ConfigurationFailed"
	RVRCondConfiguredReasonConfigurationAdjustmentSucceeded = "ConfigurationAdjustmentSucceeded"
)

// Condition reasons for [RVCondDeviceMinorAssignedType] condition
const (
	// status=True
	RVCondDeviceMinorAssignedReasonAssigned = "Assigned"
	// status=False
	RVCondDeviceMinorAssignedReasonAssignmentFailed = "AssignmentFailed"
	RVCondDeviceMinorAssignedReasonDuplicate        = "Duplicate"
)

// Condition reasons for [RVRCondScheduledType] condition
const (
	RVRCondScheduledReasonReplicaScheduled          = "ReplicaScheduled"
	RVRCondScheduledReasonSchedulingPending         = "SchedulingPending"
	RVRCondScheduledReasonSchedulingFailed          = "SchedulingFailed"
	RVRCondScheduledReasonTopologyConstraintsFailed = "TopologyConstraintsFailed"
	RVRCondScheduledReasonNoAvailableNodes          = "NoAvailableNodes"
)

// Condition reasons for [RVRCondAddressConfiguredType] condition
const (
	RVRCondAddressConfiguredReasonAddressConfigurationSucceeded = "AddressConfigurationSucceeded"
	RVRCondAddressConfiguredReasonNoFreePortAvailable           = "NoFreePortAvailable"
)

// Condition reasons for [RVRCondBackingVolumeCreatedType] condition
const (
	RVRCondBackingVolumeCreatedReasonNotApplicable               = "NotApplicable"
	RVRCondBackingVolumeCreatedReasonBackingVolumeDeletionFailed = "BackingVolumeDeletionFailed"
	RVRCondBackingVolumeCreatedReasonBackingVolumeCreationFailed = "BackingVolumeCreationFailed"
	RVRCondBackingVolumeCreatedReasonBackingVolumeReady          = "BackingVolumeReady"
	RVRCondBackingVolumeCreatedReasonBackingVolumeNotReady       = "BackingVolumeNotReady"
)

// Condition reasons for [RVRCondDataInitializedType] condition
const (
	// status=Unknown
	RVRCondDataInitializedReasonUnknownDiskState = "UnknownDiskState"
	// status=False
	RVRCondDataInitializedReasonNotApplicableToDiskless     = "NotApplicableToDiskless"
	RVRCondDataInitializedReasonDiskNeverWasInUpToDateState = "DiskNeverWasInUpToDateState"
	// status=True
	RVRCondDataInitializedReasonDiskHasBeenSeenInUpToDateState = "DiskHasBeenSeenInUpToDateState"
)

// Condition reasons for [RVRCondInQuorumType] condition
const (
	RVRCondInQuorumReasonInQuorum         = "InQuorum"
	RVRCondInQuorumReasonQuorumLost       = "QuorumLost"
	RVRCondInQuorumReasonUnknownDiskState = "UnknownDiskState"
)

// Condition reasons for [RVRCondInSyncType] condition
const (
	// status=True
	RVRCondInSyncReasonInSync   = "InSync"
	RVRCondInSyncReasonDiskless = "Diskless"

	// status=False
	RVRCondInSyncReasonDiskLost              = "DiskLost"
	RVRCondInSyncReasonAttaching             = "Attaching"
	RVRCondInSyncReasonDetaching             = "Detaching"
	RVRCondInSyncReasonFailed                = "Failed"
	RVRCondInSyncReasonNegotiating           = "Negotiating"
	RVRCondInSyncReasonInconsistent          = "Inconsistent"
	RVRCondInSyncReasonOutdated              = "Outdated"
	RVRCondInSyncReasonUnknownDiskState      = "UnknownDiskState"
	RVRCondInSyncReasonReplicaNotInitialized = "ReplicaNotInitialized"
)

// Condition reasons for [RVRCondConfiguredType] condition
const (
	// status=True
	RVRCondConfiguredReasonConfigured = "Configured"
	// status=False
	RVRCondConfiguredReasonFileSystemOperationFailed      = "FileSystemOperationFailed"
	RVRCondConfiguredReasonConfigurationCommandFailed     = "ConfigurationCommandFailed"
	RVRCondConfiguredReasonSharedSecretAlgSelectionFailed = "SharedSecretAlgSelectionFailed"
	RVRCondConfiguredReasonPromoteFailed                  = "PromoteFailed"
	RVRCondConfiguredReasonDemoteFailed                   = "DemoteFailed"
)

// Condition reasons for [RVRCondAttachedType] condition (reserved, not used yet)
const (
	// status=True
	RVRCondAttachedReasonAttached = "Attached"
	// status=False
	RVRCondAttachedReasonDetached               = "Detached"
	RVRCondAttachedReasonAttachPending          = "AttachPending"
	RVRCondAttachedReasonAttachingNotApplicable = "AttachingNotApplicable"
	// status=Unknown
	RVRCondAttachedReasonAttachingNotInitialized = "AttachingNotInitialized"
)
