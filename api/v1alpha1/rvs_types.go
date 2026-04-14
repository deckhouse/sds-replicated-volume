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

	// +kubebuilder:validation:Enum=Pending;InProgress;Synchronizing;Ready;Failed;Deleting
	// +optional
	Phase ReplicatedVolumeSnapshotPhase `json:"phase,omitempty"`

	// +kubebuilder:validation:MaxLength=512
	// +optional
	Message string `json:"message,omitempty"`

	// +optional
	ReadyToUse bool `json:"readyToUse,omitempty"`

	// +optional
	CreationTime *metav1.Time `json:"creationTime,omitempty"`

	Datamesh ReplicatedVolumeSnapshotDatamesh `json:"datamesh"`

	// SourceReplicaSnapshotName is the RVRS created from the attached (Primary)
	// replica. This snapshot has the most up-to-date data and is preferred
	// as the sync source and clone source.
	// +kubebuilder:validation:MaxLength=253
	// +optional
	SourceReplicaSnapshotName string `json:"sourceReplicaSnapshotName,omitempty"`

	// SyncDRBDResources lists temporary DRBDResource names created for
	// snapshot synchronization. The RVS controller uses this to monitor
	// sync progress and clean up resources after sync completes.
	// +listType=set
	// +optional
	SyncDRBDResources []string `json:"syncDRBDResources,omitempty"`

	// +optional
	PrepareRevision int64 `json:"prepareRevision,omitempty"`

	// +listType=atomic
	// +optional
	PrepareTransitions []ReplicatedVolumeDatameshTransition `json:"prepareTransitions,omitempty"`

	// +optional
	SyncRevision int64 `json:"syncRevision,omitempty"`

	// +listType=atomic
	// +optional
	SyncTransitions []ReplicatedVolumeDatameshTransition `json:"syncTransitions,omitempty"`
}

// +kubebuilder:object:generate=true
type ReplicatedVolumeSnapshotDatamesh struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=120
	ReplicatedVolumeName string `json:"replicatedVolumeName"`

	// +kubebuilder:validation:XValidation:rule="self.all(x, self.exists_one(y, x.name == y.name))",message="members[].name must be unique"
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:default={}
	// +listType=atomic
	Members []SnapshotDatameshMember `json:"members"`

	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=0
	ReadyCount int `json:"readyCount"`

	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=0
	TotalCount int `json:"totalCount"`
}

// +kubebuilder:object:generate=true
type SnapshotDatameshMember struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	NodeName string `json:"nodeName"`

	// +kubebuilder:validation:MaxLength=253
	// +optional
	SnapshotHandle string `json:"snapshotHandle,omitempty"`

	// +kubebuilder:default=false
	Ready bool `json:"ready"`
}

type ReplicatedVolumeSnapshotPhase string

const (
	ReplicatedVolumeSnapshotPhasePending       ReplicatedVolumeSnapshotPhase = "Pending"
	ReplicatedVolumeSnapshotPhaseInProgress    ReplicatedVolumeSnapshotPhase = "InProgress"
	ReplicatedVolumeSnapshotPhaseSynchronizing ReplicatedVolumeSnapshotPhase = "Synchronizing"
	ReplicatedVolumeSnapshotPhaseReady         ReplicatedVolumeSnapshotPhase = "Ready"
	ReplicatedVolumeSnapshotPhaseFailed        ReplicatedVolumeSnapshotPhase = "Failed"
	ReplicatedVolumeSnapshotPhaseDeleting      ReplicatedVolumeSnapshotPhase = "Deleting"
)

func (p ReplicatedVolumeSnapshotPhase) String() string { return string(p) }
