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

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ReplicatedVolumeAttachment is a Kubernetes Custom Resource that represents an attachment intent/state
// of a ReplicatedVolume to a specific node.
// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=rva
// +kubebuilder:metadata:labels=module=sds-replicated-volume
// +kubebuilder:selectablefield:JSONPath=.spec.nodeName
// +kubebuilder:selectablefield:JSONPath=.spec.replicatedVolumeName
// +kubebuilder:selectablefield:JSONPath=.status.phase
// +kubebuilder:printcolumn:name="Volume",type=string,JSONPath=".spec.replicatedVolumeName"
// +kubebuilder:printcolumn:name="Node",type=string,JSONPath=".spec.nodeName"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="ReplicaReady",type=string,JSONPath=".status.conditions[?(@.type=='ReplicaReady')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Message",type=string,priority=1,JSONPath=".status.message"
type ReplicatedVolumeAttachment struct {
	metav1.TypeMeta `json:",inline"`

	metav1.ObjectMeta `json:"metadata"`

	Spec ReplicatedVolumeAttachmentSpec `json:"spec"`

	Status ReplicatedVolumeAttachmentStatus `json:"status,omitempty"`
}

// ReplicatedVolumeAttachmentList contains a list of ReplicatedVolumeAttachment
// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
type ReplicatedVolumeAttachmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []ReplicatedVolumeAttachment `json:"items"`
}

// GetStatusConditions is an adapter method to satisfy objutilv1.StatusConditionObject.
// It returns the root object's `.status.conditions`.
func (rva *ReplicatedVolumeAttachment) GetStatusConditions() []metav1.Condition {
	return rva.Status.Conditions
}

// SetStatusConditions is an adapter method to satisfy objutilv1.StatusConditionObject.
// It sets the root object's `.status.conditions`.
func (rva *ReplicatedVolumeAttachment) SetStatusConditions(conditions []metav1.Condition) {
	rva.Status.Conditions = conditions
}

// GetStatusPhase returns .status.phase as a string.
func (rva *ReplicatedVolumeAttachment) GetStatusPhase() string { return string(rva.Status.Phase) }

// +kubebuilder:object:generate=true
type ReplicatedVolumeAttachmentSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=127
	// +kubebuilder:validation:Pattern=`^[0-9A-Za-z.+_-]*$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="replicatedVolumeName is immutable"
	ReplicatedVolumeName string `json:"replicatedVolumeName"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="nodeName is immutable"
	NodeName string `json:"nodeName"`
}

// +kubebuilder:object:generate=true
type ReplicatedVolumeAttachmentStatus struct {
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Phase is a quick operational state summary.
	// +kubebuilder:validation:Enum=Pending;Attaching;Attached;Detaching;Terminating
	// +optional
	Phase ReplicatedVolumeAttachmentPhase `json:"phase,omitempty"`

	// Message is a human-readable detail about the current state.
	// +kubebuilder:validation:MaxLength=512
	// +optional
	Message string `json:"message,omitempty"`

	// DevicePath is the block device path on the node.
	// Only set when the device is available on the node. Example: /dev/drbd10012.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	DevicePath string `json:"devicePath,omitempty"`

	// IOSuspended indicates whether I/O is suspended on the device.
	// Only set when the device is available on the node.
	// +optional
	IOSuspended *bool `json:"ioSuspended,omitempty"`

	// InUse indicates whether the block device is currently in use by a process.
	// Only set when the device is available on the node.
	// +optional
	InUse *bool `json:"inUse,omitempty"`
}

// ReplicatedVolumeAttachmentPhase enumerates possible values for ReplicatedVolumeAttachment status.phase field.
type ReplicatedVolumeAttachmentPhase string

const (
	// ReplicatedVolumeAttachmentPhasePending indicates waiting for prerequisites (replica, quorum, slot, etc.).
	ReplicatedVolumeAttachmentPhasePending ReplicatedVolumeAttachmentPhase = "Pending"
	// ReplicatedVolumeAttachmentPhaseAttaching indicates the volume is being attached to the node.
	ReplicatedVolumeAttachmentPhaseAttaching ReplicatedVolumeAttachmentPhase = "Attaching"
	// ReplicatedVolumeAttachmentPhaseAttached indicates the volume is attached and serving IO on the node.
	ReplicatedVolumeAttachmentPhaseAttached ReplicatedVolumeAttachmentPhase = "Attached"
	// ReplicatedVolumeAttachmentPhaseDetaching indicates the volume is being detached from the node.
	ReplicatedVolumeAttachmentPhaseDetaching ReplicatedVolumeAttachmentPhase = "Detaching"
	// ReplicatedVolumeAttachmentPhaseTerminating indicates the attachment is terminating.
	ReplicatedVolumeAttachmentPhaseTerminating ReplicatedVolumeAttachmentPhase = "Terminating"
)

func (p ReplicatedVolumeAttachmentPhase) String() string { return string(p) }

// FormatReplicatedVolumeAttachmentName returns the RVA name for the given ReplicatedVolume name and node name.
// The node name is used as provided (callers should pass the same casing stored in spec.nodeName, typically lower-case).
// Names longer than 253 characters are truncated and suffixed with a stable hash.
func FormatReplicatedVolumeAttachmentName(replicatedVolumeName, nodeName string) string {
	base := "csi-" + replicatedVolumeName + "-" + nodeName
	if len(base) <= 253 {
		return base
	}

	sum := sha1.Sum([]byte(base))
	hash := hex.EncodeToString(sum[:])[:8]

	// "csi-" + vol + "-" + node + "-" + hash
	const prefixLen = 4 // len("csi-")
	const sepCount = 2  // "-" between parts + "-" before hash
	const hashLen = 8
	maxPartsLen := 253 - prefixLen - sepCount - hashLen
	if maxPartsLen < 2 {
		// Should never happen, but keep a valid, bounded name.
		return "csi-" + hash
	}

	volMax := maxPartsLen / 2
	nodeMax := maxPartsLen - volMax

	volPart := truncateReplicatedVolumeAttachmentNamePart(replicatedVolumeName, volMax)
	nodePart := truncateReplicatedVolumeAttachmentNamePart(nodeName, nodeMax)
	return "csi-" + volPart + "-" + nodePart + "-" + hash
}

func truncateReplicatedVolumeAttachmentNamePart(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(s) <= maxLen {
		return s
	}
	// Make the truncation stable and avoid trailing '-' (purely cosmetic, but improves readability).
	out := s[:maxLen]
	out = strings.TrimSuffix(out, "-")
	out = strings.TrimSuffix(out, ".")
	return out
}
