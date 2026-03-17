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
// +kubebuilder:resource:scope=Cluster,shortName=rvs
// +kubebuilder:metadata:labels=module=sds-replicated-volume
// +kubebuilder:selectablefield:JSONPath=.spec.replicatedVolumeName
// +kubebuilder:printcolumn:name="Volume",type=string,JSONPath=".spec.replicatedVolumeName"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="ReadyToUse",type=boolean,JSONPath=".status.readyToUse"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
type ReplicatedVolumeSnapshot struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   ReplicatedVolumeSnapshotSpec   `json:"spec"`
	Status ReplicatedVolumeSnapshotStatus `json:"status,omitempty"`
}

// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
type ReplicatedVolumeSnapshotList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []ReplicatedVolumeSnapshot `json:"items"`
}

func (rvs *ReplicatedVolumeSnapshot) GetStatusConditions() []metav1.Condition {
	return rvs.Status.Conditions
}

func (rvs *ReplicatedVolumeSnapshot) SetStatusConditions(conditions []metav1.Condition) {
	rvs.Status.Conditions = conditions
}

// +kubebuilder:object:generate=true
type ReplicatedVolumeSnapshotSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=120
	// +kubebuilder:validation:Pattern=`^[0-9A-Za-z.+_-]*$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="replicatedVolumeName is immutable"
	ReplicatedVolumeName string `json:"replicatedVolumeName"`
}

// +kubebuilder:object:generate=true
type ReplicatedVolumeSnapshotStatus struct {
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +kubebuilder:validation:Enum=Pending;InProgress;Ready;Failed;Deleting
	// +optional
	Phase ReplicatedVolumeSnapshotPhase `json:"phase,omitempty"`

	// +kubebuilder:validation:MaxLength=512
	// +optional
	Message string `json:"message,omitempty"`

	// +optional
	ReadyToUse bool `json:"readyToUse,omitempty"`

	// +optional
	CreationTime *metav1.Time `json:"creationTime,omitempty"`
}

type ReplicatedVolumeSnapshotPhase string

const (
	ReplicatedVolumeSnapshotPhasePending    ReplicatedVolumeSnapshotPhase = "Pending"
	ReplicatedVolumeSnapshotPhaseInProgress ReplicatedVolumeSnapshotPhase = "InProgress"
	ReplicatedVolumeSnapshotPhaseReady      ReplicatedVolumeSnapshotPhase = "Ready"
	ReplicatedVolumeSnapshotPhaseFailed     ReplicatedVolumeSnapshotPhase = "Failed"
	ReplicatedVolumeSnapshotPhaseDeleting   ReplicatedVolumeSnapshotPhase = "Deleting"
)

func (p ReplicatedVolumeSnapshotPhase) String() string { return string(p) }
