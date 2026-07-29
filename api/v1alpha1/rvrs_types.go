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

// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=rvrs
// +kubebuilder:metadata:labels=module=sds-replicated-volume
// +kubebuilder:selectablefield:JSONPath=.spec.replicatedVolumeSnapshotName
// +kubebuilder:selectablefield:JSONPath=.spec.nodeName
// +kubebuilder:printcolumn:name="Snapshot",type=string,JSONPath=".spec.replicatedVolumeSnapshotName"
// +kubebuilder:printcolumn:name="Replica",type=string,JSONPath=".spec.replicatedVolumeReplicaName"
// +kubebuilder:printcolumn:name="Node",type=string,JSONPath=".spec.nodeName"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="ReadyToUse",type=boolean,JSONPath=".status.readyToUse"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
type ReplicatedVolumeReplicaSnapshot struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   ReplicatedVolumeReplicaSnapshotSpec   `json:"spec"`
	Status ReplicatedVolumeReplicaSnapshotStatus `json:"status,omitempty"`
}

// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
type ReplicatedVolumeReplicaSnapshotList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []ReplicatedVolumeReplicaSnapshot `json:"items"`
}

func (rvrs *ReplicatedVolumeReplicaSnapshot) GetStatusConditions() []metav1.Condition {
	return rvrs.Status.Conditions
}

func (rvrs *ReplicatedVolumeReplicaSnapshot) SetStatusConditions(conditions []metav1.Condition) {
	rvrs.Status.Conditions = conditions
}

// +kubebuilder:object:generate=true
type ReplicatedVolumeReplicaSnapshotSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[0-9A-Za-z.+_-]*$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="replicatedVolumeSnapshotName is immutable"
	ReplicatedVolumeSnapshotName string `json:"replicatedVolumeSnapshotName"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[0-9A-Za-z.+_-]*$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="replicatedVolumeReplicaName is immutable"
	ReplicatedVolumeReplicaName string `json:"replicatedVolumeReplicaName"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="nodeName is immutable"
	NodeName string `json:"nodeName"`
}

// +kubebuilder:object:generate=true
type ReplicatedVolumeReplicaSnapshotStatus struct {
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +kubebuilder:validation:Enum=Pending;InProgress;Ready;Failed;Deleting
	// +optional
	Phase ReplicatedVolumeReplicaSnapshotPhase `json:"phase,omitempty"`

	// +kubebuilder:validation:MaxLength=512
	// +optional
	Message string `json:"message,omitempty"`

	// +optional
	ReadyToUse bool `json:"readyToUse,omitempty"`

	// +kubebuilder:validation:MaxLength=253
	// +optional
	SnapshotHandle string `json:"snapshotHandle,omitempty"`

	// +optional
	CreationTime *metav1.Time `json:"creationTime,omitempty"`
}

type ReplicatedVolumeReplicaSnapshotPhase string

const (
	ReplicatedVolumeReplicaSnapshotPhasePending    ReplicatedVolumeReplicaSnapshotPhase = "Pending"
	ReplicatedVolumeReplicaSnapshotPhaseInProgress ReplicatedVolumeReplicaSnapshotPhase = "InProgress"
	ReplicatedVolumeReplicaSnapshotPhaseReady      ReplicatedVolumeReplicaSnapshotPhase = "Ready"
	ReplicatedVolumeReplicaSnapshotPhaseFailed     ReplicatedVolumeReplicaSnapshotPhase = "Failed"
	ReplicatedVolumeReplicaSnapshotPhaseDeleting   ReplicatedVolumeReplicaSnapshotPhase = "Deleting"
)

func (p ReplicatedVolumeReplicaSnapshotPhase) String() string { return string(p) }
