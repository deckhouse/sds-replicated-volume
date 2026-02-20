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
// +kubebuilder:printcolumn:name="Quorum",type=string,JSONPath=".status.conditions[?(@.type=='Quorum')].status"
// +kubebuilder:printcolumn:name="Scheduled",type=string,JSONPath=".status.conditions[?(@.type=='Scheduled')].status"
// +kubebuilder:printcolumn:name="Configured",type=string,JSONPath=".status.conditions[?(@.type=='Configured')].status"
// +kubebuilder:printcolumn:name="Size",type=string,JSONPath=".spec.size"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="ConfigurationReady",type=string,priority=1,JSONPath=".status.conditions[?(@.type=='ConfigurationReady')].status"
// +kubebuilder:printcolumn:name="SatisfyEligibleNodes",type=string,priority=1,JSONPath=".status.conditions[?(@.type=='SatisfyEligibleNodes')].status"
// +kubebuilder:printcolumn:name="StorageClass",type=string,priority=1,JSONPath=".spec.replicatedStorageClassName"
type ReplicatedVolume struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   ReplicatedVolumeSpec   `json:"spec"`
	Status ReplicatedVolumeStatus `json:"status,omitempty"`
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
func (rv *ReplicatedVolume) GetStatusConditions() []metav1.Condition { return rv.Status.Conditions }

// SetStatusConditions is an adapter method to satisfy objutilv1.StatusConditionObject.
// It sets the root object's `.status.conditions`.
func (rv *ReplicatedVolume) SetStatusConditions(conditions []metav1.Condition) {
	rv.Status.Conditions = conditions
}

// +kubebuilder:object:generate=true
type ReplicatedVolumeSpec struct {
	// +kubebuilder:validation:Required
	//	+kubebuilder:validation:XValidation:rule="quantity(string(self)).isGreaterThan(quantity('0'))",message="size must be greater than 0"
	Size resource.Quantity `json:"size"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ReplicatedStorageClassName string `json:"replicatedStorageClassName"`

	// MaxAttachments is the maximum number of nodes this volume can be attached to simultaneously.
	//
	// WARNING: Values greater than 1 enable multi-attach mode. In this mode the block device
	// is writable from multiple nodes concurrently. The consuming application MUST guarantee
	// that it never writes to the same disk regions from different nodes simultaneously.
	// In particular:
	//   - Mounting the volume as a regular filesystem (ext4, xfs, etc.) from more than one node
	//     is NOT safe unless ALL mounts are read-only. Use a cluster-aware filesystem (e.g. GFS2,
	//     OCFS2) or application-level coordination instead.
	//   - Concurrent writes to overlapping regions from different nodes produce UNDEFINED behavior:
	//     different Diskful replicas may apply writes in different order, leading to divergent data.
	//   - Use at your own risk and with full understanding of the implications.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=32
	// +kubebuilder:default=1
	MaxAttachments byte `json:"maxAttachments"`
}

// +kubebuilder:object:generate=true
type ReplicatedVolumeStatus struct {
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// TODO: Remove this field. It is no longer used (except for CSI driver, which will use RVA objects instead).
	// +kubebuilder:validation:XValidation:rule="self.all(x, self.exists_one(y, x == y))",message="actuallyAttachedTo must be unique"
	// +kubebuilder:validation:MaxItems=2
	// +kubebuilder:validation:Items={type=string,minLength=1,maxLength=253}
	// +listType=atomic
	// +optional
	ActuallyAttachedTo []string `json:"actuallyAttachedTo,omitempty"`

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
	// +kubebuilder:validation:XValidation:rule="self.all(x, self.exists_one(y, x.replicaName == y.replicaName))",message="eligibleNodesViolations[].replicaName must be unique"
	// +kubebuilder:validation:MaxItems=32
	// +listType=atomic
	// +optional
	EligibleNodesViolations []ReplicatedVolumeEligibleNodesViolation `json:"eligibleNodesViolations,omitempty"`

	// DatameshRevision is a counter incremented when datamesh configuration changes.
	DatameshRevision int64 `json:"datameshRevision"`

	// Datamesh is the computed datamesh configuration for the volume.
	Datamesh ReplicatedVolumeDatamesh `json:"datamesh"`

	// DatameshTransitions is the list of active datamesh transitions.
	// +listType=atomic
	// +optional
	DatameshTransitions []ReplicatedVolumeDatameshTransition `json:"datameshTransitions,omitempty"`

	// DatameshPendingReplicaTransitions is the list of pending replica transitions.
	// +kubebuilder:validation:XValidation:rule="self.all(x, self.exists_one(y, x.name == y.name))",message="datameshPendingReplicaTransitions[].name must be unique"
	// +kubebuilder:validation:MaxItems=32
	// +listType=atomic
	// +optional
	DatameshPendingReplicaTransitions []ReplicatedVolumeDatameshPendingReplicaTransition `json:"datameshPendingReplicaTransitions,omitempty"`
}

// ReplicatedVolumeDatameshPendingReplicaTransition represents a pending transition for a single replica.
// +kubebuilder:object:generate=true
type ReplicatedVolumeDatameshPendingReplicaTransition struct {
	// Name is the replica name.
	// Must have format "prefix-N" where N is 0-31.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=123
	// +kubebuilder:validation:Pattern=`^.+-([0-9]|[12][0-9]|3[01])$`
	Name string `json:"name"`

	// Message is an optional human-readable message about the transition state and progress.
	// +kubebuilder:validation:MaxLength=512
	// +optional
	Message string `json:"message,omitempty"`

	// Transition is the pending datamesh transition details from the replica.
	// +kubebuilder:validation:Required
	Transition ReplicatedVolumeReplicaStatusDatameshPendingTransition `json:"transition"`

	// FirstObservedAt is the timestamp when this transition was first observed.
	// +kubebuilder:validation:Required
	FirstObservedAt metav1.Time `json:"firstObservedAt"`
}

// ID extracts ID from the replica name (e.g., "pvc-xxx-5" → 5).
func (t ReplicatedVolumeDatameshPendingReplicaTransition) ID() uint8 {
	return idFromName(t.Name)
}

// ReplicatedVolumeDatameshTransition represents an active datamesh transition.
// +kubebuilder:object:generate=true
//
//	+kubebuilder:validation:XValidation:rule="self.type != 'Formation' || has(self.formation)",message="formation is required when type is Formation"
//	+kubebuilder:validation:XValidation:rule="!has(self.formation) || self.type == 'Formation'",message="formation is only allowed when type is Formation"
//	+kubebuilder:validation:XValidation:rule="self.type != 'Formation' || !has(self.datameshRevision) || self.datameshRevision == 0",message="datameshRevision must be absent (or zero) when type is Formation"
//	+kubebuilder:validation:XValidation:rule="self.type == 'Formation' || self.type == 'EnableMultiattach' || self.type == 'DisableMultiattach' || has(self.replicaName)",message="replicaName is required for Attach, Detach, AddAccessReplica, RemoveAccessReplica transitions"
//	+kubebuilder:validation:XValidation:rule="!has(self.replicaName) || !(self.type == 'Formation' || self.type == 'EnableMultiattach' || self.type == 'DisableMultiattach')",message="replicaName must not be set for Formation, EnableMultiattach, DisableMultiattach transitions"
type ReplicatedVolumeDatameshTransition struct {
	// Type is the transition type.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Formation;AddAccessReplica;Attach;Detach;DisableMultiattach;EnableMultiattach;RemoveAccessReplica
	Type ReplicatedVolumeDatameshTransitionType `json:"type"`

	// DatameshRevision is the datamesh revision when this transition was introduced.
	// Zero means unset. For Formation transitions, must be absent (zero).
	// +optional
	DatameshRevision int64 `json:"datameshRevision,omitempty"`

	// Message is an optional human-readable message about the transition.
	// +optional
	Message string `json:"message,omitempty"`

	// StartedAt is the timestamp when this transition started.
	// +kubebuilder:validation:Required
	StartedAt metav1.Time `json:"startedAt"`

	// ReplicaName is the name of the replica this transition applies to.
	// Required for Attach, Detach, AddAccessReplica, RemoveAccessReplica transitions.
	// Must not be set for Formation, EnableMultiattach, DisableMultiattach.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=123
	// +kubebuilder:validation:Pattern=`^.+-([0-9]|[12][0-9]|3[01])$`
	// +optional
	ReplicaName string `json:"replicaName,omitempty"`

	// Formation holds formation-specific details.
	// Required when type is "Formation"; must not be set otherwise.
	// +optional
	Formation *ReplicatedVolumeDatameshTransitionFormation `json:"formation,omitempty"`
}

// ReplicaID extracts ID from the replica name (e.g., "pvc-xxx-5" → 5).
// Panics if ReplicaName is not set (transition types without ReplicaName must not call this).
func (t ReplicatedVolumeDatameshTransition) ReplicaID() uint8 {
	if t.ReplicaName == "" {
		panic("ReplicaID called on transition without replicaName (type: " + string(t.Type) + ")")
	}
	return idFromName(t.ReplicaName)
}

// ReplicatedVolumeDatameshTransitionType enumerates possible datamesh transition types.
type ReplicatedVolumeDatameshTransitionType string

const (
	// ReplicatedVolumeDatameshTransitionTypeFormation indicates initial datamesh formation.
	ReplicatedVolumeDatameshTransitionTypeFormation ReplicatedVolumeDatameshTransitionType = "Formation"

	// ReplicatedVolumeDatameshTransitionTypeAddAccessReplica indicates adding an Access replica to the datamesh.
	ReplicatedVolumeDatameshTransitionTypeAddAccessReplica ReplicatedVolumeDatameshTransitionType = "AddAccessReplica"
	// ReplicatedVolumeDatameshTransitionTypeAttach indicates attaching a replica (promoting to Primary in DRBD terms).
	ReplicatedVolumeDatameshTransitionTypeAttach ReplicatedVolumeDatameshTransitionType = "Attach"
	// ReplicatedVolumeDatameshTransitionTypeDetach indicates detaching a replica (demoting from Primary in DRBD terms).
	ReplicatedVolumeDatameshTransitionTypeDetach ReplicatedVolumeDatameshTransitionType = "Detach"
	// ReplicatedVolumeDatameshTransitionTypeDisableMultiattach indicates disabling multi-attach (allowTwoPrimaries).
	ReplicatedVolumeDatameshTransitionTypeDisableMultiattach ReplicatedVolumeDatameshTransitionType = "DisableMultiattach"
	// ReplicatedVolumeDatameshTransitionTypeEnableMultiattach indicates enabling multi-attach (allowTwoPrimaries).
	ReplicatedVolumeDatameshTransitionTypeEnableMultiattach ReplicatedVolumeDatameshTransitionType = "EnableMultiattach"
	// ReplicatedVolumeDatameshTransitionTypeRemoveAccessReplica indicates removing an Access replica from the datamesh.
	ReplicatedVolumeDatameshTransitionTypeRemoveAccessReplica ReplicatedVolumeDatameshTransitionType = "RemoveAccessReplica"
)

func (t ReplicatedVolumeDatameshTransitionType) String() string {
	return string(t)
}

// ReplicatedVolumeFormationPhase enumerates formation sub-phases.
type ReplicatedVolumeFormationPhase string

const (
	// ReplicatedVolumeFormationPhasePreconfigure is the initial phase where replicas are being created and preconfigured.
	ReplicatedVolumeFormationPhasePreconfigure ReplicatedVolumeFormationPhase = "Preconfigure"
	// ReplicatedVolumeFormationPhaseEstablishConnectivity is the phase where replicas establish DRBD connectivity.
	ReplicatedVolumeFormationPhaseEstablishConnectivity ReplicatedVolumeFormationPhase = "EstablishConnectivity"
	// ReplicatedVolumeFormationPhaseBootstrapData is the phase where initial data synchronization is bootstrapped.
	ReplicatedVolumeFormationPhaseBootstrapData ReplicatedVolumeFormationPhase = "BootstrapData"
)

func (p ReplicatedVolumeFormationPhase) String() string { return string(p) }

// ReplicatedVolumeDatameshTransitionFormation holds formation-specific transition details.
// +kubebuilder:object:generate=true
type ReplicatedVolumeDatameshTransitionFormation struct {
	// Phase is the current formation phase.
	// +kubebuilder:validation:Required
	// +kubebuilder:default="Preconfigure"
	// +kubebuilder:validation:Enum=Preconfigure;EstablishConnectivity;BootstrapData
	Phase ReplicatedVolumeFormationPhase `json:"phase"`
}

// ReplicatedVolumeDatamesh holds datamesh configuration for the volume.
// +kubebuilder:object:generate=true
type ReplicatedVolumeDatamesh struct {
	// SystemNetworkNames is the list of system network names for DRBD communication.
	// +kubebuilder:validation:XValidation:rule="self.all(x, self.exists_one(y, x == y))",message="systemNetworkNames must be unique"
	// +kubebuilder:validation:MaxItems=10
	// +kubebuilder:validation:items:MaxLength=64
	// +kubebuilder:default={}
	// +listType=atomic
	SystemNetworkNames []string `json:"systemNetworkNames"`

	// SharedSecret is the shared secret for DRBD authentication.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	SharedSecret string `json:"sharedSecret,omitempty"`

	// SharedSecretAlg is the hashing algorithm for the shared secret.
	// +kubebuilder:validation:Enum=SHA256;SHA1;DummyForTest
	// +optional
	SharedSecretAlg SharedSecretAlg `json:"sharedSecretAlg,omitempty"`

	// Multiattach enables multiattach mode for the datamesh.
	// +kubebuilder:default=false
	Multiattach bool `json:"multiattach,omitempty"`

	// Size is the desired size of the volume.
	// +kubebuilder:validation:Required
	Size resource.Quantity `json:"size"`

	// Members is the list of datamesh members.
	// +kubebuilder:validation:XValidation:rule="self.all(x, self.exists_one(y, x.name == y.name))",message="members[].name must be unique"
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:default={}
	// +listType=atomic
	Members []ReplicatedVolumeDatameshMember `json:"members"`

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

// FindMemberByName returns a pointer to the member with the given name, or nil if not found.
func (dm *ReplicatedVolumeDatamesh) FindMemberByName(name string) *ReplicatedVolumeDatameshMember {
	for i := range dm.Members {
		if dm.Members[i].Name == name {
			return &dm.Members[i]
		}
	}
	return nil
}

// SharedSecretAlg enumerates possible hashing algorithms for DRBD shared secrets.
type SharedSecretAlg string

// Shared secret hashing algorithms.
const (
	// SharedSecretAlgSHA256 is the SHA256 hashing algorithm for shared secrets.
	SharedSecretAlgSHA256 SharedSecretAlg = "SHA256"
	// SharedSecretAlgSHA1 is the SHA1 hashing algorithm for shared secrets.
	SharedSecretAlgSHA1 SharedSecretAlg = "SHA1"
	// SharedSecretAlgDummyForTest is a dummy algorithm for testing.
	SharedSecretAlgDummyForTest SharedSecretAlg = "DummyForTest"
)

func (a SharedSecretAlg) String() string {
	return string(a)
}

// ReplicatedVolumeDatameshMember represents a member of the datamesh.
// +kubebuilder:object:generate=true
// +kubebuilder:validation:XValidation:rule="self.type != 'Diskful' || !has(self.typeTransition) || self.typeTransition == 'ToDiskless'",message="Diskful can only have ToDiskless typeTransition"
// +kubebuilder:validation:XValidation:rule="self.type != 'TieBreaker' || !has(self.typeTransition) || self.typeTransition == 'ToDiskful'",message="TieBreaker can only have ToDiskful typeTransition"
// +kubebuilder:validation:XValidation:rule="self.type != 'Access' || !has(self.typeTransition)",message="Access cannot have typeTransition"
// +kubebuilder:validation:XValidation:rule="self.name.lastIndexOf('-') >= 0",message="name must contain '-' separator"
// +kubebuilder:validation:XValidation:rule="int(self.name.substring(self.name.lastIndexOf('-') + 1)) <= 31",message="name numeric suffix must be between 0 and 31"
// +kubebuilder:validation:XValidation:rule="!has(self.lvmVolumeGroupName) || self.type == 'Diskful' || (has(self.typeTransition) && self.typeTransition == 'ToDiskful')",message="lvmVolumeGroupName can only be set for Diskful type or when typeTransition is ToDiskful"
// +kubebuilder:validation:XValidation:rule="!has(self.lvmVolumeGroupThinPoolName) || has(self.lvmVolumeGroupName)",message="lvmVolumeGroupThinPoolName requires lvmVolumeGroupName to be set"
type ReplicatedVolumeDatameshMember struct {
	// Name is the member name (used as list map key).
	// Must have format "prefix-N" where N is 0-31.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=140
	Name string `json:"name"`

	// Type is the member type (Diskful, Access, or TieBreaker).
	// +kubebuilder:validation:Required
	Type ReplicaType `json:"type"`

	// TypeTransition indicates the desired type transition for this member.
	// +kubebuilder:validation:Enum=ToDiskful;ToDiskless
	// +optional
	TypeTransition ReplicatedVolumeDatameshMemberTypeTransition `json:"typeTransition,omitempty"`

	// NodeName is the Kubernetes node name where the member is located.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	NodeName string `json:"nodeName"`

	// Zone is the zone where the member is located.
	// +kubebuilder:validation:MaxLength=64
	// +optional
	Zone string `json:"zone,omitempty"`

	// Addresses is the list of DRBD addresses for this member.
	// +kubebuilder:validation:XValidation:rule="self.all(x, self.exists_one(y, x.systemNetworkName == y.systemNetworkName))",message="addresses[].systemNetworkName must be unique"
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=10
	// +listType=atomic
	Addresses []DRBDResourceAddressStatus `json:"addresses"`

	// LVMVolumeGroupName is the LVMVolumeGroup resource name where this replica should be placed.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-.]{0,251}[a-z0-9])?$`
	LVMVolumeGroupName string `json:"lvmVolumeGroupName,omitempty"`
	// LVMVolumeGroupThinPoolName is the thin pool name (for LVMThin storage pools).
	// +kubebuilder:validation:MaxLength=64
	// +optional
	LVMVolumeGroupThinPoolName string `json:"lvmVolumeGroupThinPoolName,omitempty"`

	// Attached indicates whether this member should be attached (Primary in DRBD terms).
	// +kubebuilder:default=false
	Attached bool `json:"attached"`
}

// ID extracts ID from the member name (e.g., "pvc-xxx-5" → 5).
func (m ReplicatedVolumeDatameshMember) ID() uint8 {
	return idFromName(m.Name)
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

// ReplicatedVolumeEligibleNodesViolation describes a replica placed on a non-eligible node.
// +kubebuilder:object:generate=true
type ReplicatedVolumeEligibleNodesViolation struct {
	// NodeName is the node where the replica is placed.
	// +kubebuilder:validation:MaxLength=253
	NodeName string `json:"nodeName"`
	// ReplicaName is the ReplicatedVolumeReplica name.
	// +kubebuilder:validation:MaxLength=253
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
