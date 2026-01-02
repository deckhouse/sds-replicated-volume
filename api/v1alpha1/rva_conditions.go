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
	// ReplicatedVolumeAttachmentCondAttachedType indicates whether the volume is attached to the requested node.
	//
	// Reasons describe attach/detach progress and blocking conditions.
	ReplicatedVolumeAttachmentCondAttachedType = "Attached"

	ReplicatedVolumeAttachmentCondAttachedReasonAttached                            = "Attached"
	ReplicatedVolumeAttachmentCondAttachedReasonConvertingTieBreakerToAccess        = "ConvertingTieBreakerToAccess"
	ReplicatedVolumeAttachmentCondAttachedReasonLocalityNotSatisfied                = "LocalityNotSatisfied"
	ReplicatedVolumeAttachmentCondAttachedReasonSettingPrimary                      = "SettingPrimary"
	ReplicatedVolumeAttachmentCondAttachedReasonUnableToProvideLocalVolumeAccess    = "UnableToProvideLocalVolumeAccess"
	ReplicatedVolumeAttachmentCondAttachedReasonWaitingForActiveAttachmentsToDetach = "WaitingForActiveAttachmentsToDetach"
	ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplica                   = "WaitingForReplica"
	ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplicatedVolume          = "WaitingForReplicatedVolume"
	ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplicatedVolumeIOReady   = "WaitingForReplicatedVolumeIOReady"
)

const (
	// ReplicatedVolumeAttachmentCondReadyType indicates whether the attachment is ready for use.
	// It is an aggregate condition: Attached=True AND ReplicaIOReady=True.
	//
	// Reasons describe which prerequisite is missing.
	ReplicatedVolumeAttachmentCondReadyType                    = "Ready"
	ReplicatedVolumeAttachmentCondReadyReasonNotAttached       = "NotAttached"       // Attached=False.
	ReplicatedVolumeAttachmentCondReadyReasonReady             = "Ready"             // Attached=True and ReplicaIOReady=True.
	ReplicatedVolumeAttachmentCondReadyReasonReplicaNotIOReady = "ReplicaNotIOReady" // ReplicaIOReady=False.
)

const (
	// ReplicatedVolumeAttachmentCondReplicaIOReadyType indicates whether the replica on the requested node is IOReady.
	// This condition mirrors RVR IOReady (status/reason/message) for the replica on rva.spec.nodeName.
	//
	// Reasons typically mirror the replica's IOReady reason; this one is used when it is not yet observable.
	ReplicatedVolumeAttachmentCondReplicaIOReadyType                    = "ReplicaIOReady"
	ReplicatedVolumeAttachmentCondReplicaIOReadyReasonWaitingForReplica = "WaitingForReplica"
)
