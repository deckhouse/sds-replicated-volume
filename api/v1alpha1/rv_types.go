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

	// DeviceMinor is a unique DRBD device minor number assigned to this ReplicatedVolume.
	// +optional
	DeviceMinor *DeviceMinor `json:"deviceMinor,omitempty"`

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

	// +optional
	ActualSize *resource.Quantity `json:"actualSize,omitempty"`

	// +optional
	Phase string `json:"phase,omitempty"`

	// DiskfulReplicaCount represents the current and desired number of diskful replicas in format "current/desired"
	// Example: "2/3" means 2 current diskful replicas out of 3 desired
	// +optional
	DiskfulReplicaCount string `json:"diskfulReplicaCount,omitempty"`

	// DiskfulReplicasInSync represents the number of diskful replicas that are in sync in format "inSync/total"
	// Example: "2/3" means 2 diskful replicas are in sync out of 3 total diskful replicas
	// +optional
	DiskfulReplicasInSync string `json:"diskfulReplicasInSync,omitempty"`

	// AttachedAndIOReadyCount represents the number of attached replicas that are IOReady in format "ready/attached"
	// Example: "1/2" means 1 replica is IOReady out of 2 attached
	// +optional
	AttachedAndIOReadyCount string `json:"attachedAndIOReadyCount,omitempty"`

	// StorageClass tracks the observed state of the referenced ReplicatedStorageClass.
	// +optional
	StorageClass *ReplicatedVolumeStorageClassReference `json:"storageClass,omitempty"`

	// RolloutTicket is assigned when the volume is created and updated when selected for rolling update.
	// Persists the last taken storage class configuration snapshot.
	// +optional
	RolloutTicket *ReplicatedVolumeRolloutTicket `json:"rolloutTicket,omitempty"`

	// TargetConfiguration is the desired configuration snapshot for this volume.
	// +optional
	TargetConfiguration *ReplicatedVolumeStorageClassConfiguration `json:"targetConfiguration,omitempty"`

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

// DeviceMinor is a DRBD device minor number.
//
// This is a named type (uint32-based) to keep RV status type-safe while preserving
// JSON/YAML encoding as a plain integer.
// +kubebuilder:validation:Minimum=0
// +kubebuilder:validation:Maximum=1048575
type DeviceMinor uint32

const (
	deviceMinorMin uint32 = 0
	// 1048575 = 2^20 - 1: maximum minor number supported by modern Linux kernels.
	deviceMinorMax uint32 = 1048575
)

func (DeviceMinor) Min() uint32 { return deviceMinorMin }

func (DeviceMinor) Max() uint32 { return deviceMinorMax }

// +kubebuilder:object:generate=true
type DRBDResourceDetails struct {
	// +patchStrategy=merge
	// +optional
	Config *DRBDResourceConfig `json:"config,omitempty" patchStrategy:"merge"`
}

// +kubebuilder:object:generate=true
type DRBDResourceConfig struct {
	// +optional
	// +kubebuilder:validation:MinLength=1
	SharedSecret string `json:"sharedSecret,omitempty"`

	// +optional
	// +kubebuilder:validation:Enum=SHA256;SHA1;DummyForTest
	SharedSecretAlg SharedSecretAlg `json:"sharedSecretAlg,omitempty"`

	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=8
	Quorum byte `json:"quorum,omitempty"`

	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=8
	QuorumMinimumRedundancy byte `json:"quorumMinimumRedundancy,omitempty"`

	// +kubebuilder:default=false
	AllowTwoPrimaries bool `json:"allowTwoPrimaries,omitempty"`
}

// DRBD quorum configuration constants for ReplicatedVolume
const (
	// QuorumMinValue is the minimum quorum value when diskfulCount > 1.
	// Quorum formula: max(QuorumMinValue, allReplicas/2+1)
	QuorumMinValue = 2

	// QuorumMinimumRedundancyDefault is the default minimum number of UpToDate
	// replicas required for quorum. Used for None and Availability replication modes.
	// This ensures at least one UpToDate replica is required for quorum.
	QuorumMinimumRedundancyDefault = 1

	// QuorumMinimumRedundancyMinForConsistency is the minimum QMR value
	// for ConsistencyAndAvailability replication mode when calculating majority-based QMR.
	// QMR formula for C&A: max(QuorumMinimumRedundancyMinForConsistency, diskfulCount/2+1)
	QuorumMinimumRedundancyMinForConsistency = 2
)

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

// SharedSecretAlgorithms returns the ordered list of supported shared secret algorithms.
// The order matters: algorithms are tried sequentially when one fails on any replica.
func SharedSecretAlgorithms() []SharedSecretAlg {
	return []SharedSecretAlg{
		// TODO: remove after testing
		SharedSecretAlgDummyForTest,
		SharedSecretAlgSHA256,
		SharedSecretAlgSHA1,
	}
}

// ReplicatedVolumeStorageClassConfiguration holds storage class configuration parameters
// that are tracked/snapshotted on ReplicatedVolume.
// +kubebuilder:object:generate=true
type ReplicatedVolumeStorageClassConfiguration struct {
	// Topology is the topology setting from the storage class.
	Topology ReplicatedStorageClassTopology `json:"topology"`
	// Replication is the replication mode from the storage class.
	Replication ReplicatedStorageClassReplication `json:"replication"`
	// VolumeAccess is the volume access mode from the storage class.
	VolumeAccess ReplicatedStorageClassVolumeAccess `json:"volumeAccess"`
	// Zones is the list of zones from the storage class.
	// +optional
	Zones []string `json:"zones,omitempty"`
	// SystemNetworkNames is the list of network names from the storage class.
	// +optional
	SystemNetworkNames []string `json:"systemNetworkNames,omitempty"`
}

// ReplicatedVolumeStorageClassReference tracks the observed state of the referenced storage class.
// +kubebuilder:object:generate=true
type ReplicatedVolumeStorageClassReference struct {
	// Name is the ReplicatedStorageClass name.
	Name string `json:"name"`
	// ObservedConfigurationGeneration is the RSC generation when configuration was observed.
	// +optional
	ObservedConfigurationGeneration int64 `json:"observedConfigurationGeneration,omitempty"`
	// ObservedEligibleNodesRevision is the eligible nodes revision when last observed.
	// +optional
	ObservedEligibleNodesRevision int64 `json:"observedEligibleNodesRevision,omitempty"`
}

// ReplicatedVolumeRolloutTicket represents a ticket for rolling out configuration changes.
// +kubebuilder:object:generate=true
type ReplicatedVolumeRolloutTicket struct {
	// StorageClassGeneration is the RSC generation this ticket was issued for.
	StorageClassGeneration int64 `json:"storageClassGeneration"`
	// Configuration is the configuration snapshot to roll out.
	Configuration ReplicatedVolumeStorageClassConfiguration `json:"configuration"`
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
