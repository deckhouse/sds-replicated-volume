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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=rv
// +kubebuilder:metadata:labels=module=sds-replicated-volume
// +kubebuilder:validation:XValidation:rule="size(self.metadata.name) <= 120",message="metadata.name must be at most 120 characters (to fit derived RVR/LLV names)"
// +kubebuilder:printcolumn:name="IOReady",type=string,JSONPath=".status.conditions[?(@.type=='IOReady')].status"
// +kubebuilder:printcolumn:name="Size",type=string,JSONPath=".spec.size"
// +kubebuilder:printcolumn:name="ActualSize",type=string,JSONPath=".status.actualSize"
// +kubebuilder:printcolumn:name="DiskfulReplicas",type=string,JSONPath=".status.diskfulReplicaCount"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
type ReplicatedVolume struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec ReplicatedVolumeSpec `json:"spec"`
	// +patchStrategy=merge
	Status ReplicatedVolumeStatus `json:"status,omitempty" patchStrategy:"merge"`
}

// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
type ReplicatedVolumeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []ReplicatedVolume `json:"items"`
}

// GetStatusConditions is an adapter method to satisfy objutilv1.StatusConditionObject.
// It returns the root object's `.status.conditions`.
func (o *ReplicatedVolume) GetStatusConditions() []metav1.Condition { return o.Status.Conditions }

// SetStatusConditions is an adapter method to satisfy objutilv1.StatusConditionObject.
// It sets the root object's `.status.conditions`.
func (o *ReplicatedVolume) SetStatusConditions(conditions []metav1.Condition) {
	o.Status.Conditions = conditions
}

// +kubebuilder:object:generate=true
type ReplicatedVolumeSpec struct {
	// +kubebuilder:validation:Required
	Size resource.Quantity `json:"size"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ReplicatedStorageClassName string `json:"replicatedStorageClassName"`
}

// +kubebuilder:object:generate=true
type ReplicatedVolumeStatus struct {
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// +patchStrategy=merge
	// +optional
	DRBD *DRBDResourceDetails `json:"drbd,omitempty" patchStrategy:"merge"`

	// +kubebuilder:validation:MaxItems=2
	// +kubebuilder:validation:Items={type=string,minLength=1,maxLength=253}
	// +optional
	ActuallyAttachedTo []string `json:"actuallyAttachedTo,omitempty"`

	// DesiredAttachTo is the desired set of nodes where the volume should be attached (up to 2 nodes).
	// It is computed by controllers from ReplicatedVolumeAttachment (RVA) objects.
	// +kubebuilder:validation:MaxItems=2
	// +kubebuilder:validation:Items={type=string,minLength=1,maxLength=253}
	// +optional
	DesiredAttachTo []string `json:"desiredAttachTo,omitempty"`

	// Configuration is the desired configuration snapshot for this volume.
	// +optional
	Configuration *ReplicatedStorageClassConfiguration `json:"configuration,omitempty"`

	// ConfigurationGeneration is the RSC generation from which configuration was taken.
	// +optional
	ConfigurationGeneration int64 `json:"configurationGeneration,omitempty"`

	// ConfigurationObservedGeneration is the RSC generation when configuration was last observed/acknowledged.
	// +optional
	ConfigurationObservedGeneration int64 `json:"configurationObservedGeneration,omitempty"`

	// EligibleNodesViolations lists replicas placed on non-eligible nodes.
	// +optional
	EligibleNodesViolations []ReplicatedVolumeEligibleNodesViolation `json:"eligibleNodesViolations,omitempty"`

	// DatameshRevision is a counter incremented when datamesh configuration changes.
	DatameshRevision int64 `json:"datameshRevision"`

	// Datamesh is the computed datamesh configuration for the volume.
	// +patchStrategy=merge
	Datamesh ReplicatedVolumeDatamesh `json:"datamesh" patchStrategy:"merge"`
}

// ReplicatedVolumeDatamesh holds datamesh configuration for the volume.
// +kubebuilder:object:generate=true
type ReplicatedVolumeDatamesh struct {
	// SystemNetworkNames is the list of system network names for DRBD communication.
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=64
	SystemNetworkNames []string `json:"systemNetworkNames"`
	// AllowTwoPrimaries enables two primaries mode for the datamesh.
	// +kubebuilder:default=false
	AllowTwoPrimaries bool `json:"allowTwoPrimaries"`
	// Size is the desired size of the volume.
	// +kubebuilder:validation:Required
	Size resource.Quantity `json:"size"`
	// Members is the list of datamesh members.
	// +kubebuilder:validation:MaxItems=24
	// +patchMergeKey=name
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=name
	Members []ReplicatedVolumeDatameshMember `json:"members" patchStrategy:"merge" patchMergeKey:"name"`
	// Quorum is the quorum value for the datamesh.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=13
	// +kubebuilder:default=0
	Quorum byte `json:"quorum"`
	// QuorumMinimumRedundancy is the minimum redundancy required for quorum.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=8
	// +kubebuilder:default=0
	QuorumMinimumRedundancy byte `json:"quorumMinimumRedundancy"`
}

// ReplicatedVolumeDatameshMember represents a member of the datamesh.
// +kubebuilder:object:generate=true
// +kubebuilder:validation:XValidation:rule="self.type == 'Diskful' ? (!has(self.typeTransition) || self.typeTransition == 'ToDiskless') : (!has(self.typeTransition) || self.typeTransition == 'ToDiskful')",message="typeTransition must be ToDiskless for Diskful type, or ToDiskful for Access/TieBreaker types"
type ReplicatedVolumeDatameshMember struct {
	// Name is the member name (used as list map key).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// Type is the member type (Diskful, Access, or TieBreaker).
	// +kubebuilder:validation:Required
	Type ReplicaType `json:"type"`
	// TypeTransition indicates the desired type transition for this member.
	// +kubebuilder:validation:Enum=ToDiskful;ToDiskless
	// +optional
	TypeTransition ReplicatedVolumeDatameshMemberTypeTransition `json:"typeTransition,omitempty"`
	// Role is the DRBD role of this member.
	// +optional
	Role DRBDRole `json:"role,omitempty"`
	// NodeName is the Kubernetes node name where the member is located.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	NodeName string `json:"nodeName"`
	// Zone is the zone where the member is located.
	// +optional
	Zone string `json:"zone,omitempty"`
	// Addresses is the list of DRBD addresses for this member.
	// +kubebuilder:validation:MaxItems=16
	Addresses []DRBDResourceAddressStatus `json:"addresses"`
}

// ReplicatedVolumeDatameshMemberTypeTransition enumerates possible type transitions for datamesh members.
type ReplicatedVolumeDatameshMemberTypeTransition string

const (
	// ReplicatedVolumeDatameshMemberTypeTransitionToDiskful indicates transition to Diskful type.
	ReplicatedVolumeDatameshMemberTypeTransitionToDiskful ReplicatedVolumeDatameshMemberTypeTransition = "ToDiskful"
	// ReplicatedVolumeDatameshMemberTypeTransitionToDiskless indicates transition to a diskless type (Access or TieBreaker).
	ReplicatedVolumeDatameshMemberTypeTransitionToDiskless ReplicatedVolumeDatameshMemberTypeTransition = "ToDiskless"
)

func (t ReplicatedVolumeDatameshMemberTypeTransition) String() string {
	return string(t)
}

// +kubebuilder:object:generate=true
type DRBDResourceDetails struct {
	// +patchStrategy=merge
	// +optional
	Config *DRBDResourceConfig `json:"config,omitempty" patchStrategy:"merge"`
}

// +kubebuilder:object:generate=true
type DRBDResourceConfig struct {
	// +kubebuilder:default=false
	AllowTwoPrimaries bool `json:"allowTwoPrimaries,omitempty"`
}

type SharedSecretAlg string

// Shared secret hashing algorithms
const (
	// SharedSecretAlgSHA256 is the SHA256 hashing algorithm for shared secrets
	SharedSecretAlgSHA256 = "SHA256"
	// SharedSecretAlgSHA1 is the SHA1 hashing algorithm for shared secrets
	SharedSecretAlgSHA1         = "SHA1"
	SharedSecretAlgDummyForTest = "DummyForTest"
)

func (a SharedSecretAlg) String() string {
	return string(a)
}

// ReplicatedVolumeEligibleNodesViolation describes a replica placed on a non-eligible node.
// +kubebuilder:object:generate=true
type ReplicatedVolumeEligibleNodesViolation struct {
	// NodeName is the node where the replica is placed.
	NodeName string `json:"nodeName"`
	// ReplicaName is the ReplicatedVolumeReplica name.
	ReplicaName string `json:"replicaName"`
	// Reason describes why this placement violates eligible nodes constraints.
	Reason ReplicatedVolumeEligibleNodesViolationReason `json:"reason"`
}

// ReplicatedVolumeEligibleNodesViolationReason enumerates possible reasons for eligible nodes violation.
type ReplicatedVolumeEligibleNodesViolationReason string

const (
	// ReplicatedVolumeEligibleNodesViolationReasonOutOfEligibleNodes means replica is on a node not in eligible nodes list.
	ReplicatedVolumeEligibleNodesViolationReasonOutOfEligibleNodes ReplicatedVolumeEligibleNodesViolationReason = "OutOfEligibleNodes"
	// ReplicatedVolumeEligibleNodesViolationReasonNodeTopologyMismatch means replica is on a node with wrong topology.
	ReplicatedVolumeEligibleNodesViolationReasonNodeTopologyMismatch ReplicatedVolumeEligibleNodesViolationReason = "NodeTopologyMismatch"
)

func (r ReplicatedVolumeEligibleNodesViolationReason) String() string { return string(r) }
