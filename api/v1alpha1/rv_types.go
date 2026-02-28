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

// Auto mode requires RSC name, forbids manualConfiguration:
// +kubebuilder:validation:XValidation:rule="self.configurationMode != 'Auto' || (size(self.replicatedStorageClassName) > 0 && !has(self.manualConfiguration))",message="Auto mode requires replicatedStorageClassName and must not have manualConfiguration."
// Manual mode requires manualConfiguration, forbids RSC name:
// +kubebuilder:validation:XValidation:rule="self.configurationMode != 'Manual' || (has(self.manualConfiguration) && size(self.replicatedStorageClassName) == 0)",message="Manual mode requires manualConfiguration and must not have replicatedStorageClassName."
// +kubebuilder:object:generate=true
type ReplicatedVolumeSpec struct {
	// +kubebuilder:validation:Required
	//	+kubebuilder:validation:XValidation:rule="quantity(string(self)).isGreaterThan(quantity('0'))",message="size must be greater than 0"
	Size resource.Quantity `json:"size"`

	// ConfigurationMode selects how the volume configuration is determined.
	// Auto (default): configuration is derived from the referenced ReplicatedStorageClass.
	// Manual: configuration is specified directly in the manualConfiguration field.
	// +kubebuilder:validation:Enum=Auto;Manual
	// +kubebuilder:default=Auto
	ConfigurationMode ReplicatedVolumeConfigurationMode `json:"configurationMode,omitempty"`

	// ReplicatedStorageClassName references the RSC that provides configuration.
	// Required when configurationMode is Auto. Must not be set when configurationMode is Manual.
	// +optional
	// +kubebuilder:validation:MinLength=1
	ReplicatedStorageClassName string `json:"replicatedStorageClassName,omitempty"`

	// ManualConfiguration specifies the volume configuration directly.
	// Required when configurationMode is Manual. Must not be set when configurationMode is Auto.
	// +optional
	ManualConfiguration *ReplicatedVolumeConfiguration `json:"manualConfiguration,omitempty"`

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

// ReplicatedVolumeConfigurationMode enumerates possible values for ReplicatedVolume spec.configurationMode field.
type ReplicatedVolumeConfigurationMode string

const (
	// ReplicatedVolumeConfigurationModeAuto means configuration is derived from the referenced ReplicatedStorageClass.
	ReplicatedVolumeConfigurationModeAuto ReplicatedVolumeConfigurationMode = "Auto"
	// ReplicatedVolumeConfigurationModeManual means configuration is specified directly in the manual field.
	ReplicatedVolumeConfigurationModeManual ReplicatedVolumeConfigurationMode = "Manual"
)

func (m ReplicatedVolumeConfigurationMode) String() string { return string(m) }

// ReplicatedVolumeConfiguration represents the resolved volume configuration.
// Always contains the resolved FTT/GMDR parameters (never the legacy replication field).
// Used in rv.Status.Configuration, rv.Spec.ManualConfiguration, and rsc.Status.Configuration.
//
// Valid FTT/GMDR combinations: |FTT - GMDR| <= 1:
// +kubebuilder:validation:XValidation:rule="self.failuresToTolerate - self.guaranteedMinimumDataRedundancy <= 1 && self.guaranteedMinimumDataRedundancy - self.failuresToTolerate <= 1",message="Invalid failuresToTolerate/guaranteedMinimumDataRedundancy combination: |FTT - GMDR| must be <= 1."
//
// FTT=0, GMDR=0 requires topology Ignored:
// +kubebuilder:validation:XValidation:rule="!(self.failuresToTolerate == 0 && self.guaranteedMinimumDataRedundancy == 0) || self.topology == 'Ignored'",message="failuresToTolerate=0 with guaranteedMinimumDataRedundancy=0 requires topology Ignored."
// +kubebuilder:object:generate=true
type ReplicatedVolumeConfiguration struct {
	// ReplicatedStoragePoolName is the name of the ReplicatedStoragePool resource.
	// +kubebuilder:validation:MinLength=1
	ReplicatedStoragePoolName string `json:"replicatedStoragePoolName"`
	// Topology is the resolved topology setting.
	// +kubebuilder:validation:Enum=TransZonal;Zonal;Ignored
	Topology ReplicatedStorageClassTopology `json:"topology"`
	// FailuresToTolerate is the resolved FTT value.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=2
	FailuresToTolerate byte `json:"failuresToTolerate"`
	// GuaranteedMinimumDataRedundancy is the resolved GMDR value.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=2
	GuaranteedMinimumDataRedundancy byte `json:"guaranteedMinimumDataRedundancy"`
	// VolumeAccess is the resolved volume access mode.
	// +kubebuilder:validation:Enum=Local;EventuallyLocal;PreferablyLocal;Any
	VolumeAccess ReplicatedStorageClassVolumeAccess `json:"volumeAccess"`
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
	Configuration *ReplicatedVolumeConfiguration `json:"configuration,omitempty"`

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

	// EffectiveLayout tracks the FTT/GMDR levels that the datamesh actually
	// provides. q and qmr are computed from these values, not from Configuration.
	EffectiveLayout ReplicatedVolumeEffectiveLayout `json:"effectiveLayout"`

	// DatameshTransitions is the list of active datamesh transitions.
	// +listType=atomic
	// +optional
	DatameshTransitions []ReplicatedVolumeDatameshTransition `json:"datameshTransitions,omitempty"`

	// DatameshReplicaRequests is the list of pending membership requests from replicas.
	// +kubebuilder:validation:XValidation:rule="self.all(x, self.exists_one(y, x.name == y.name))",message="datameshReplicaRequests[].name must be unique"
	// +kubebuilder:validation:MaxItems=32
	// +listType=atomic
	// +optional
	DatameshReplicaRequests []ReplicatedVolumeDatameshReplicaRequest `json:"datameshReplicaRequests,omitempty"`
}

// ReplicatedVolumeDatameshReplicaRequest represents a pending membership request from a single replica.
// +kubebuilder:object:generate=true
type ReplicatedVolumeDatameshReplicaRequest struct {
	// Name is the replica name.
	// Must have format "prefix-N" where N is 0-31.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=123
	// +kubebuilder:validation:Pattern=`^.+-([0-9]|[12][0-9]|3[01])$`
	Name string `json:"name"`

	// Message is an optional human-readable message about the request state and progress.
	// +kubebuilder:validation:MaxLength=512
	// +optional
	Message string `json:"message,omitempty"`

	// Request is the membership change request details from the replica.
	// +kubebuilder:validation:Required
	Request DatameshMembershipRequest `json:"request"`

	// FirstObservedAt is the timestamp when this request was first observed.
	// +kubebuilder:validation:Required
	FirstObservedAt metav1.Time `json:"firstObservedAt"`
}

// ID extracts ID from the replica name (e.g., "pvc-xxx-5" → 5).
func (t ReplicatedVolumeDatameshReplicaRequest) ID() uint8 {
	return idFromName(t.Name)
}

// ReplicatedVolumeDatameshTransition represents an active datamesh transition.
// Each transition has an ordered list of steps that are pre-declared on creation
// and executed sequentially. The transition's start time is steps[0].startedAt.
// +kubebuilder:object:generate=true
//
//	+kubebuilder:validation:XValidation:rule="self.type == 'Formation' || self.type == 'EnableMultiattach' || self.type == 'DisableMultiattach' || self.type == 'ChangeQuorum' || has(self.replicaName)",message="replicaName is required for AddReplica, RemoveReplica, ChangeReplicaType, Attach, Detach, ForceRemoveReplica, ForceDetach transitions"
//	+kubebuilder:validation:XValidation:rule="!has(self.replicaName) || !(self.type == 'Formation' || self.type == 'EnableMultiattach' || self.type == 'DisableMultiattach' || self.type == 'ChangeQuorum')",message="replicaName must not be set for Formation, EnableMultiattach, DisableMultiattach, ChangeQuorum transitions"
//	+kubebuilder:validation:XValidation:rule="self.type == 'AddReplica' || self.type == 'RemoveReplica' || self.type == 'ForceRemoveReplica' || !has(self.replicaType)",message="replicaType must only be set for AddReplica, RemoveReplica, ForceRemoveReplica transitions"
//	+kubebuilder:validation:XValidation:rule="!(self.type == 'AddReplica' || self.type == 'RemoveReplica' || self.type == 'ForceRemoveReplica') || has(self.replicaType)",message="replicaType is required for AddReplica, RemoveReplica, ForceRemoveReplica transitions"
//	+kubebuilder:validation:XValidation:rule="self.type == 'ChangeReplicaType' || (!has(self.fromReplicaType) && !has(self.toReplicaType))",message="fromReplicaType and toReplicaType must only be set for ChangeReplicaType transitions"
//	+kubebuilder:validation:XValidation:rule="self.type != 'ChangeReplicaType' || (has(self.fromReplicaType) && has(self.toReplicaType))",message="fromReplicaType and toReplicaType are required for ChangeReplicaType transitions"
type ReplicatedVolumeDatameshTransition struct {
	// Type is the transition type.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Formation;AddReplica;Attach;ChangeQuorum;ChangeReplicaType;Detach;DisableMultiattach;EnableMultiattach;ForceDetach;ForceRemoveReplica;RemoveReplica
	Type ReplicatedVolumeDatameshTransitionType `json:"type"`

	// ReplicaName is the name of the replica this transition applies to.
	// Required for AddReplica, RemoveReplica, ChangeReplicaType, Attach, Detach, ForceRemoveReplica, ForceDetach.
	// Must not be set for Formation, EnableMultiattach, DisableMultiattach, ChangeQuorum.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=123
	// +kubebuilder:validation:Pattern=`^.+-([0-9]|[12][0-9]|3[01])$`
	// +optional
	ReplicaName string `json:"replicaName,omitempty"`

	// ReplicaType is the target replica type for AddReplica/RemoveReplica/ForceRemoveReplica.
	// Uses ReplicaType (Diskful, Access, TieBreaker, ShadowDiskful) — liminal states
	// (LiminalDiskful, LiminalShadowDiskful) are internal mechanics visible in step names,
	// not in transition parameters.
	// Required for AddReplica, RemoveReplica, ForceRemoveReplica. Must not be set for other types.
	// +kubebuilder:validation:Enum=Diskful;Access;TieBreaker;ShadowDiskful
	// +optional
	ReplicaType ReplicaType `json:"replicaType,omitempty"`

	// FromReplicaType is the source replica type for ChangeReplicaType transitions.
	// Required for ChangeReplicaType. Must not be set for other types.
	// +kubebuilder:validation:Enum=Diskful;Access;TieBreaker;ShadowDiskful
	// +optional
	FromReplicaType ReplicaType `json:"fromReplicaType,omitempty"`

	// ToReplicaType is the target replica type for ChangeReplicaType transitions.
	// Required for ChangeReplicaType. Must not be set for other types.
	// +kubebuilder:validation:Enum=Diskful;Access;TieBreaker;ShadowDiskful
	// +optional
	ToReplicaType ReplicaType `json:"toReplicaType,omitempty"`

	// Group is the parallelism group for this transition.
	// Set by the controller at transition creation time. Read-only for users.
	// Provides observability: explains why a transition may be waiting.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Formation;VotingMembership;NonVotingMembership;Quorum;Attachment;Multiattach;Emergency
	Group ReplicatedVolumeDatameshTransitionGroup `json:"group"`

	// Steps is the ordered list of steps for this transition.
	// All steps are written when the transition is created (with status=Pending,
	// and the first step immediately set to Active with startedAt).
	// Steps are executed sequentially — only one step is Active at a time.
	// Must have at least one element.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=8
	// +listType=atomic
	Steps []ReplicatedVolumeDatameshTransitionStep `json:"steps"`
}

// ReplicaID extracts ID from the replica name (e.g., "pvc-xxx-5" → 5).
// Panics if ReplicaName is not set (transition types without ReplicaName must not call this).
func (t ReplicatedVolumeDatameshTransition) ReplicaID() uint8 {
	if t.ReplicaName == "" {
		panic("ReplicaID called on transition without replicaName (type: " + string(t.Type) + ")")
	}
	return idFromName(t.ReplicaName)
}

// CurrentStep returns a pointer to the first non-Completed step, or nil if all steps are completed.
func (t *ReplicatedVolumeDatameshTransition) CurrentStep() *ReplicatedVolumeDatameshTransitionStep {
	for i := range t.Steps {
		if t.Steps[i].Status != ReplicatedVolumeDatameshTransitionStepStatusCompleted {
			return &t.Steps[i]
		}
	}
	return nil
}

// IsCompleted returns true if all steps are completed.
func (t *ReplicatedVolumeDatameshTransition) IsCompleted() bool {
	for i := range t.Steps {
		if t.Steps[i].Status != ReplicatedVolumeDatameshTransitionStepStatusCompleted {
			return false
		}
	}
	return true
}

// StartedAt returns the start time of the transition (= first step's StartedAt).
// Returns zero time if the first step has no StartedAt (should not happen for valid transitions).
func (t *ReplicatedVolumeDatameshTransition) StartedAt() metav1.Time {
	if len(t.Steps) > 0 && t.Steps[0].StartedAt != nil {
		return *t.Steps[0].StartedAt
	}
	return metav1.Time{}
}

// ReplicatedVolumeDatameshTransitionStep represents one step within a transition.
// +kubebuilder:object:generate=true
type ReplicatedVolumeDatameshTransitionStep struct {
	// Name is a human-readable step name (e.g., "Preconfigure", "attach", "✦ → D∅").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	Name string `json:"name"`

	// Status is the current status of this step.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Pending;Active;Completed
	// +kubebuilder:default="Pending"
	Status ReplicatedVolumeDatameshTransitionStepStatus `json:"status"`

	// DatameshRevision is the revision assigned when this step was activated.
	// 0 if the step has not been activated yet or does not bump the revision.
	// +optional
	DatameshRevision int64 `json:"datameshRevision,omitempty"`

	// StartedAt is set when the step transitions from Pending to Active.
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`

	// CompletedAt is set when the step transitions from Active to Completed.
	// +optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`

	// Message is a per-step progress or error message.
	// Updated every reconciliation while the step is Active.
	// +kubebuilder:validation:MaxLength=512
	// +optional
	Message string `json:"message,omitempty"`
}

// ReplicatedVolumeDatameshTransitionStepStatus enumerates step statuses.
type ReplicatedVolumeDatameshTransitionStepStatus string

const (
	// ReplicatedVolumeDatameshTransitionStepStatusPending indicates the step has not started yet.
	ReplicatedVolumeDatameshTransitionStepStatusPending ReplicatedVolumeDatameshTransitionStepStatus = "Pending"
	// ReplicatedVolumeDatameshTransitionStepStatusActive indicates the step is currently executing.
	ReplicatedVolumeDatameshTransitionStepStatusActive ReplicatedVolumeDatameshTransitionStepStatus = "Active"
	// ReplicatedVolumeDatameshTransitionStepStatusCompleted indicates the step has finished.
	ReplicatedVolumeDatameshTransitionStepStatusCompleted ReplicatedVolumeDatameshTransitionStepStatus = "Completed"
)

func (s ReplicatedVolumeDatameshTransitionStepStatus) String() string { return string(s) }

// ReplicatedVolumeDatameshTransitionType enumerates possible datamesh transition types.
type ReplicatedVolumeDatameshTransitionType string

const (
	// ReplicatedVolumeDatameshTransitionTypeFormation indicates initial datamesh formation.
	ReplicatedVolumeDatameshTransitionTypeFormation ReplicatedVolumeDatameshTransitionType = "Formation"

	// ReplicatedVolumeDatameshTransitionTypeAddReplica indicates adding a replica to the datamesh.
	ReplicatedVolumeDatameshTransitionTypeAddReplica ReplicatedVolumeDatameshTransitionType = "AddReplica"
	// ReplicatedVolumeDatameshTransitionTypeAttach indicates attaching a replica (promoting to Primary in DRBD terms).
	ReplicatedVolumeDatameshTransitionTypeAttach ReplicatedVolumeDatameshTransitionType = "Attach"
	// ReplicatedVolumeDatameshTransitionTypeChangeQuorum indicates a standalone quorum (q/qmr) change.
	ReplicatedVolumeDatameshTransitionTypeChangeQuorum ReplicatedVolumeDatameshTransitionType = "ChangeQuorum"
	// ReplicatedVolumeDatameshTransitionTypeChangeReplicaType indicates changing a member's type (e.g., Access to Diskful).
	ReplicatedVolumeDatameshTransitionTypeChangeReplicaType ReplicatedVolumeDatameshTransitionType = "ChangeReplicaType"
	// ReplicatedVolumeDatameshTransitionTypeDetach indicates detaching a replica (demoting from Primary in DRBD terms).
	ReplicatedVolumeDatameshTransitionTypeDetach ReplicatedVolumeDatameshTransitionType = "Detach"
	// ReplicatedVolumeDatameshTransitionTypeDisableMultiattach indicates disabling multi-attach (allowTwoPrimaries).
	ReplicatedVolumeDatameshTransitionTypeDisableMultiattach ReplicatedVolumeDatameshTransitionType = "DisableMultiattach"
	// ReplicatedVolumeDatameshTransitionTypeEnableMultiattach indicates enabling multi-attach (allowTwoPrimaries).
	ReplicatedVolumeDatameshTransitionTypeEnableMultiattach ReplicatedVolumeDatameshTransitionType = "EnableMultiattach"
	// ReplicatedVolumeDatameshTransitionTypeForceDetach indicates emergency IO detach for a dead member.
	ReplicatedVolumeDatameshTransitionTypeForceDetach ReplicatedVolumeDatameshTransitionType = "ForceDetach"
	// ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica indicates emergency removal of a dead member.
	ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica ReplicatedVolumeDatameshTransitionType = "ForceRemoveReplica"
	// ReplicatedVolumeDatameshTransitionTypeRemoveReplica indicates removing a replica from the datamesh.
	ReplicatedVolumeDatameshTransitionTypeRemoveReplica ReplicatedVolumeDatameshTransitionType = "RemoveReplica"
)

func (t ReplicatedVolumeDatameshTransitionType) String() string {
	return string(t)
}

// ReplicatedVolumeDatameshTransitionGroup enumerates parallelism groups for transitions.
// The group determines serialization and exclusivity rules (see TRANSITION_ENGINE.md §9).
type ReplicatedVolumeDatameshTransitionGroup string

const (
	// ReplicatedVolumeDatameshTransitionGroupFormation is the formation group (exclusive).
	ReplicatedVolumeDatameshTransitionGroupFormation ReplicatedVolumeDatameshTransitionGroup = "Formation"
	// ReplicatedVolumeDatameshTransitionGroupVotingMembership is the voting membership group (serialized).
	ReplicatedVolumeDatameshTransitionGroupVotingMembership ReplicatedVolumeDatameshTransitionGroup = "VotingMembership"
	// ReplicatedVolumeDatameshTransitionGroupNonVotingMembership is the non-voting membership group (parallel).
	ReplicatedVolumeDatameshTransitionGroupNonVotingMembership ReplicatedVolumeDatameshTransitionGroup = "NonVotingMembership"
	// ReplicatedVolumeDatameshTransitionGroupQuorum is the quorum group (exclusive).
	ReplicatedVolumeDatameshTransitionGroupQuorum ReplicatedVolumeDatameshTransitionGroup = "Quorum"
	// ReplicatedVolumeDatameshTransitionGroupAttachment is the attachment group (parallel).
	ReplicatedVolumeDatameshTransitionGroupAttachment ReplicatedVolumeDatameshTransitionGroup = "Attachment"
	// ReplicatedVolumeDatameshTransitionGroupMultiattach is the multiattach group (exclusive with Attachment).
	ReplicatedVolumeDatameshTransitionGroupMultiattach ReplicatedVolumeDatameshTransitionGroup = "Multiattach"
	// ReplicatedVolumeDatameshTransitionGroupEmergency is the emergency group (preemptive).
	ReplicatedVolumeDatameshTransitionGroupEmergency ReplicatedVolumeDatameshTransitionGroup = "Emergency"
)

func (g ReplicatedVolumeDatameshTransitionGroup) String() string { return string(g) }

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
	Members []DatameshMember `json:"members"`

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
func (dm *ReplicatedVolumeDatamesh) FindMemberByName(name string) *DatameshMember {
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

// DatameshMember represents a member of the datamesh.
// +kubebuilder:object:generate=true
// +kubebuilder:validation:XValidation:rule="self.name.lastIndexOf('-') >= 0",message="name must contain '-' separator"
// +kubebuilder:validation:XValidation:rule="int(self.name.substring(self.name.lastIndexOf('-') + 1)) <= 31",message="name numeric suffix must be between 0 and 31"
// +kubebuilder:validation:XValidation:rule="!has(self.lvmVolumeGroupName) || self.type == 'Diskful' || self.type == 'LiminalDiskful'",message="lvmVolumeGroupName can only be set for Diskful or LiminalDiskful type"
// +kubebuilder:validation:XValidation:rule="!has(self.lvmVolumeGroupThinPoolName) || has(self.lvmVolumeGroupName)",message="lvmVolumeGroupThinPoolName requires lvmVolumeGroupName to be set"
type DatameshMember struct {
	// Name is the member name (used as list map key).
	// Must have format "prefix-N" where N is 0-31.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=140
	Name string `json:"name"`

	// Type is the member type.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Diskful;LiminalDiskful;ShadowDiskful;LiminalShadowDiskful;TieBreaker;Access
	Type DatameshMemberType `json:"type"`

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
func (m DatameshMember) ID() uint8 {
	return idFromName(m.Name)
}

// DatameshMemberType enumerates possible types for datamesh members.
// This is separate from ReplicaType because datamesh members have an additional
// LiminalDiskful state that does not exist for RVR spec types.
type DatameshMemberType string

const (
	// DatameshMemberTypeDiskful is a fully operational diskful member.
	// DRBD is configured with the backing volume attached.
	DatameshMemberTypeDiskful DatameshMemberType = DatameshMemberType(ReplicaTypeDiskful)

	// DatameshMemberTypeLiminalDiskful is a diskful member in a preparatory
	// (threshold) stage. A liminal member is already Diskful by role (quorum voter, full peer
	// connectivity) but DRBD is configured as diskless. The backing volume is maintained but
	// not attached to DRBD. This stage is entered during PromoteToDiskful and DemoteFromDiskful
	// transitions but is skipped during Formation.
	DatameshMemberTypeLiminalDiskful DatameshMemberType = DatameshMemberType("Liminal" + ReplicaTypeDiskful)

	// DatameshMemberTypeShadowDiskful is a diskful member that pre-syncs data
	// invisibly to quorum. DRBD is configured with the backing volume attached and
	// non-voting=true. Excluded from quorum on all peers via allow-remote-read=false.
	DatameshMemberTypeShadowDiskful DatameshMemberType = DatameshMemberType(ReplicaTypeShadowDiskful)

	// DatameshMemberTypeLiminalShadowDiskful is a shadow diskful member in a preparatory
	// stage. DRBD is configured as diskless. The backing volume is maintained but not
	// attached to DRBD.
	DatameshMemberTypeLiminalShadowDiskful DatameshMemberType = DatameshMemberType("Liminal" + ReplicaTypeShadowDiskful)

	// DatameshMemberTypeTieBreaker is a diskless member that participates
	// in tiebreaker voting.
	DatameshMemberTypeTieBreaker DatameshMemberType = DatameshMemberType(ReplicaTypeTieBreaker)

	// DatameshMemberTypeAccess is a diskless member used solely for volume attachment.
	DatameshMemberTypeAccess DatameshMemberType = DatameshMemberType(ReplicaTypeAccess)
)

func (t DatameshMemberType) String() string {
	return string(t)
}

// IsVoter reports whether this member type participates in quorum voting.
func (t DatameshMemberType) IsVoter() bool {
	switch t {
	case DatameshMemberTypeDiskful, DatameshMemberTypeLiminalDiskful:
		return true
	default:
		return false
	}
}

// HasBackingVolume reports whether DRBD is configured with a backing volume
// for this member type.
func (t DatameshMemberType) HasBackingVolume() bool {
	switch t {
	case DatameshMemberTypeDiskful, DatameshMemberTypeShadowDiskful:
		return true
	default:
		return false
	}
}

// NeedsBackingVolume reports whether this member type requires a backing volume
// to be provisioned (even if DRBD is not yet configured with it).
func (t DatameshMemberType) NeedsBackingVolume() bool {
	switch t {
	case DatameshMemberTypeDiskful, DatameshMemberTypeLiminalDiskful,
		DatameshMemberTypeShadowDiskful, DatameshMemberTypeLiminalShadowDiskful:
		return true
	default:
		return false
	}
}

// ToLiminal returns the liminal variant of this member type.
// Panics if this type has no liminal variant (only types with HasBackingVolume have one).
func (t DatameshMemberType) ToLiminal() DatameshMemberType {
	switch t {
	case DatameshMemberTypeDiskful:
		return DatameshMemberTypeLiminalDiskful
	case DatameshMemberTypeShadowDiskful:
		return DatameshMemberTypeLiminalShadowDiskful
	default:
		panic("no liminal variant for " + string(t))
	}
}

// ConnectsToAllPeers reports whether this member type has full mesh peer
// connectivity (connects to every other member in the datamesh).
func (t DatameshMemberType) ConnectsToAllPeers() bool {
	switch t {
	case DatameshMemberTypeDiskful, DatameshMemberTypeLiminalDiskful,
		DatameshMemberTypeShadowDiskful, DatameshMemberTypeLiminalShadowDiskful:
		return true
	default:
		return false
	}
}

// ReplicatedVolumeEffectiveLayout tracks the FTT/GMDR protection levels
// that the datamesh actually provides right now. q and qmr are derived
// from these values, not from rv.Status.Configuration.
//
// Monotonic ratchet semantics:
//   - Values only increase (via layout transitions) until they reach the
//     levels defined in Configuration.
//   - Once reached, they are held at that level.
//   - They decrease only if Configuration is lowered below the current
//     effective values (explicit downgrade).
//
// During an upgrade transition EffectiveLayout < Configuration;
// during a downgrade transition EffectiveLayout > Configuration;
// at steady state they are equal.
// +kubebuilder:object:generate=true
type ReplicatedVolumeEffectiveLayout struct {
	// FailuresToTolerate is the effective FTT level.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=2
	// +kubebuilder:default=0
	FailuresToTolerate byte `json:"failuresToTolerate"`

	// GuaranteedMinimumDataRedundancy is the effective GMDR level.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=2
	// +kubebuilder:default=0
	GuaranteedMinimumDataRedundancy byte `json:"guaranteedMinimumDataRedundancy"`
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
