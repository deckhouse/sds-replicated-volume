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
	// ReplicatedVolumeAttachmentCondAttachedType indicates whether the volume is attached to the requested node.
	//
	// Reasons describe attach/detach progress and blocking conditions.
	ReplicatedVolumeAttachmentCondAttachedType                                   = "Attached"
	ReplicatedVolumeAttachmentCondAttachedReasonAttached                         = "Attached"                         // Volume is attached and ready to serve I/O on the node.
	ReplicatedVolumeAttachmentCondAttachedReasonAttaching                        = "Attaching"                        // Volume is being attached to the node.
	ReplicatedVolumeAttachmentCondAttachedReasonDetached                         = "Detached"                         // Volume has been detached from the node.
	ReplicatedVolumeAttachmentCondAttachedReasonDetaching                        = "Detaching"                        // Volume is being detached from the node.
	ReplicatedVolumeAttachmentCondAttachedReasonVolumeAccessLocalityNotSatisfied = "VolumeAccessLocalityNotSatisfied" // Node has no local data replica; storage class requires local access.
	ReplicatedVolumeAttachmentCondAttachedReasonNodeNotEligible                  = "NodeNotEligible"                  // Node is not allowed by the storage pool configuration.
	ReplicatedVolumeAttachmentCondAttachedReasonPending                          = "Pending"                          // Waiting for conditions to be met; see message for details.
	ReplicatedVolumeAttachmentCondAttachedReasonReplicatedVolumeDeleting         = "ReplicatedVolumeDeleting"         // Volume is being deleted; new attachments are not allowed.
	ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplica                = "WaitingForReplica"                // Replica on this node is not yet available.
	ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplicatedVolume       = "WaitingForReplicatedVolume"       // ReplicatedVolume does not exist.
)

const (
	// ReplicatedVolumeAttachmentCondReadyType indicates whether the attachment is ready for use.
	// It is an aggregate condition: Attached=True AND ReplicaReady=True.
	//
	// Reasons describe which prerequisite is missing.
	ReplicatedVolumeAttachmentCondReadyType                  = "Ready"
	ReplicatedVolumeAttachmentCondReadyReasonDeleting        = "Deleting"        // Attachment is being deleted.
	ReplicatedVolumeAttachmentCondReadyReasonNotAttached     = "NotAttached"     // Attached=False.
	ReplicatedVolumeAttachmentCondReadyReasonReady           = "Ready"           // Attached=True and ReplicaReady=True.
	ReplicatedVolumeAttachmentCondReadyReasonReplicaNotReady = "ReplicaNotReady" // ReplicaReady=False.
)

const (
	// ReplicatedVolumeAttachmentCondReplicaReadyType indicates whether the replica on the requested node is Ready.
	// This condition mirrors RVR Ready (status/reason/message) for the replica on rva.spec.nodeName.
	//
	// Reasons mirror the replica's Ready reason; WaitingForReplica is used when no replica exists on the node.
	ReplicatedVolumeAttachmentCondReplicaReadyType                    = "ReplicaReady"
	ReplicatedVolumeAttachmentCondReplicaReadyReasonWaitingForReplica = "WaitingForReplica"
)
