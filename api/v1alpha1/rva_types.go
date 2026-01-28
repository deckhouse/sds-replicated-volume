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

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// ReplicatedVolumeAttachment is a Kubernetes Custom Resource that represents an attachment intent/state
// of a ReplicatedVolume to a specific node.
// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=rva
// +kubebuilder:metadata:labels=module=sds-replicated-volume
// +kubebuilder:selectablefield:JSONPath=.spec.nodeName
// +kubebuilder:selectablefield:JSONPath=.spec.replicatedVolumeName
// +kubebuilder:printcolumn:name="Volume",type=string,JSONPath=".spec.replicatedVolumeName"
// +kubebuilder:printcolumn:name="Node",type=string,JSONPath=".spec.nodeName"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Attached",type=string,JSONPath=".status.conditions[?(@.type=='Attached')].status"
// +kubebuilder:printcolumn:name="ReplicaReady",type=string,JSONPath=".status.conditions[?(@.type=='ReplicaReady')].status"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
type ReplicatedVolumeAttachment struct {
	metav1.TypeMeta `json:",inline"`

	metav1.ObjectMeta `json:"metadata"`

	Spec ReplicatedVolumeAttachmentSpec `json:"spec"`

	// +patchStrategy=merge
	Status ReplicatedVolumeAttachmentStatus `json:"status,omitempty" patchStrategy:"merge"`
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
	// +kubebuilder:validation:Enum=Pending;Attaching;Attached;Detaching
	// +optional
	Phase ReplicatedVolumeAttachmentPhase `json:"phase,omitempty"`

	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}

// ReplicatedVolumeAttachmentPhase enumerates possible values for ReplicatedVolumeAttachment status.phase field.
type ReplicatedVolumeAttachmentPhase string

// ReplicatedVolumeAttachment status.phase possible values.
// Keep these in sync with `ReplicatedVolumeAttachmentStatus.Phase` validation enum.
const (
	// ReplicatedVolumeAttachmentPhasePending means the attachment is not started yet.
	ReplicatedVolumeAttachmentPhasePending ReplicatedVolumeAttachmentPhase = "Pending"
	// ReplicatedVolumeAttachmentPhaseAttaching means the system is attaching the volume.
	ReplicatedVolumeAttachmentPhaseAttaching ReplicatedVolumeAttachmentPhase = "Attaching"
	// ReplicatedVolumeAttachmentPhaseAttached means the volume is attached.
	ReplicatedVolumeAttachmentPhaseAttached ReplicatedVolumeAttachmentPhase = "Attached"
	// ReplicatedVolumeAttachmentPhaseDetaching means the system is detaching the volume.
	ReplicatedVolumeAttachmentPhaseDetaching ReplicatedVolumeAttachmentPhase = "Detaching"
)

func (p ReplicatedVolumeAttachmentPhase) String() string {
	return string(p)
}
