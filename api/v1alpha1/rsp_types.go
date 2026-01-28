/*
Copyright 2023 Flant JSC

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

// ReplicatedStoragePool is a Kubernetes Custom Resource that defines a configuration for Linstor Storage-pools.
// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=rsp
// +kubebuilder:metadata:labels=heritage=deckhouse
// +kubebuilder:metadata:labels=module=sds-replicated-volume
// +kubebuilder:metadata:labels=backup.deckhouse.io/cluster-config=true
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`,description="The age of this resource"
type ReplicatedStoragePool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ReplicatedStoragePoolSpec   `json:"spec"`
	Status            ReplicatedStoragePoolStatus `json:"status,omitempty"`
}

// ReplicatedStoragePoolList contains a list of ReplicatedStoragePool
// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
type ReplicatedStoragePoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []ReplicatedStoragePool `json:"items"`
}

// GetStatusConditions is an adapter method to satisfy objutilv1.StatusConditionObject.
// It returns the root object's `.status.conditions`.
func (rsp *ReplicatedStoragePool) GetStatusConditions() []metav1.Condition {
	return rsp.Status.Conditions
}

// SetStatusConditions is an adapter method to satisfy objutilv1.StatusConditionObject.
// It sets the root object's `.status.conditions`.
func (rsp *ReplicatedStoragePool) SetStatusConditions(conditions []metav1.Condition) {
	rsp.Status.Conditions = conditions
}

// Defines desired rules for Linstor's Storage-pools.
// +kubebuilder:object:generate=true
// +kubebuilder:validation:XValidation:rule="self.type != 'LVMThin' || self.lvmVolumeGroups.all(g, g.thinPoolName != ”)",message="thinPoolName is required for each lvmVolumeGroups entry when type is LVMThin"
// +kubebuilder:validation:XValidation:rule="self.type != 'LVM' || self.lvmVolumeGroups.all(g, !has(g.thinPoolName) || g.thinPoolName == ”)",message="thinPoolName must not be specified when type is LVM"
type ReplicatedStoragePoolSpec struct {
	// Defines the volumes type. Might be:
	// - LVM (for Thick)
	// - LVMThin (for Thin)
	// +kubebuilder:validation:Enum=LVM;LVMThin
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable."
	Type ReplicatedStoragePoolType `json:"type"`
	// An array of names of LVMVolumeGroup resources, whose Volume Groups/Thin-pools will be used to allocate
	// the required space.
	//
	// > Note that every LVMVolumeGroup resource has to have the same type Thin/Thick
	// as it is in current resource's 'Spec.Type' field.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable."
	// +kubebuilder:validation:MinItems=1
	LVMVolumeGroups []ReplicatedStoragePoolLVMVolumeGroups `json:"lvmVolumeGroups"`
	// Array of zones the Storage pool's volumes should be replicated in.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable."
	// +kubebuilder:validation:MaxItems=10
	// +kubebuilder:validation:items:MaxLength=63
	// +listType=set
	// +optional
	Zones []string `json:"zones,omitempty"`
	// NodeLabelSelector filters nodes eligible for storage pool participation.
	// Only nodes matching this selector can store data.
	// If not specified, all nodes with matching LVMVolumeGroups are candidates.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable."
	// +kubebuilder:validation:XValidation:rule="!has(self.matchExpressions) || self.matchExpressions.all(e, e.operator in ['In', 'NotIn', 'Exists', 'DoesNotExist'])",message="matchExpressions[].operator must be one of: In, NotIn, Exists, DoesNotExist"
	// +kubebuilder:validation:XValidation:rule="!has(self.matchExpressions) || self.matchExpressions.all(e, (e.operator in ['Exists', 'DoesNotExist']) ? (!has(e.values) || size(e.values) == 0) : (has(e.values) && size(e.values) > 0))",message="matchExpressions[].values must be empty for Exists/DoesNotExist operators, non-empty for In/NotIn"
	// +optional
	NodeLabelSelector *metav1.LabelSelector `json:"nodeLabelSelector,omitempty"`
	// SystemNetworkNames specifies network names used for DRBD replication traffic.
	// At least one network name must be specified. Each name is limited to 64 characters.
	//
	// TODO(systemnetwork): Currently only "Internal" (default node network) is supported.
	// Custom network support requires NetworkNode watch implementation in the controller.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=1
	// +kubebuilder:validation:Items={type=string,maxLength=64}
	// +kubebuilder:validation:XValidation:rule="self.all(n, n == 'Internal')",message="Only 'Internal' network is currently supported"
	// +kubebuilder:default:={"Internal"}
	SystemNetworkNames []string `json:"systemNetworkNames"`
	// EligibleNodesPolicy defines policies for managing eligible nodes.
	// Always present with defaults.
	EligibleNodesPolicy ReplicatedStoragePoolEligibleNodesPolicy `json:"eligibleNodesPolicy"`
}

// EligibleNodesPolicy defines policies for managing eligible nodes.
// +kubebuilder:object:generate=true
type ReplicatedStoragePoolEligibleNodesPolicy struct {
	// NotReadyGracePeriod specifies how long to wait before removing
	// a not-ready node from the eligible nodes list.
	// +kubebuilder:default="10m"
	NotReadyGracePeriod metav1.Duration `json:"notReadyGracePeriod"`
}

// ReplicatedStoragePoolType enumerates possible values for ReplicatedStoragePool spec.type field.
type ReplicatedStoragePoolType string

// ReplicatedStoragePool spec.type possible values.
// Keep these in sync with `ReplicatedStoragePoolSpec.Type` validation enum.
const (
	// ReplicatedStoragePoolTypeLVM means Thick volumes backed by LVM.
	ReplicatedStoragePoolTypeLVM ReplicatedStoragePoolType = "LVM"
	// ReplicatedStoragePoolTypeLVMThin means Thin volumes backed by LVM Thin pools.
	ReplicatedStoragePoolTypeLVMThin ReplicatedStoragePoolType = "LVMThin"
)

func (t ReplicatedStoragePoolType) String() string {
	return string(t)
}

type ReplicatedStoragePoolLVMVolumeGroups struct {
	// Selected LVMVolumeGroup resource's name.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-.]{0,251}[a-z0-9])?$`
	Name string `json:"name"`
	// Selected Thin-pool name.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9][a-zA-Z0-9_.+-]*$`
	// +optional
	ThinPoolName string `json:"thinPoolName,omitempty"`
}

// Displays current information about the state of the LINSTOR storage pool.
// +kubebuilder:object:generate=true
type ReplicatedStoragePoolStatus struct {
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// TODO: Remove Phase once the old controller (sds-replicated-volume-controller) is retired.
	// Phase is used only by the old controller and will be removed in a future version.
	// +optional
	Phase ReplicatedStoragePoolPhase `json:"phase,omitempty"`
	// TODO: Remove Reason once the old controller (sds-replicated-volume-controller) is retired.
	// Reason is used only by the old controller and will be removed in a future version.
	// +optional
	Reason string `json:"reason,omitempty"`

	// EligibleNodesRevision is incremented when eligible nodes change.
	// +optional
	EligibleNodesRevision int64 `json:"eligibleNodesRevision,omitempty"`
	// EligibleNodes lists nodes eligible for this storage pool.
	// +optional
	EligibleNodes []ReplicatedStoragePoolEligibleNode `json:"eligibleNodes,omitempty"`

	// UsedBy tracks which resources are using this storage pool.
	// +optional
	UsedBy ReplicatedStoragePoolUsedBy `json:"usedBy,omitempty"`
}

// ReplicatedStoragePoolUsedBy tracks resources using this storage pool.
// +kubebuilder:object:generate=true
type ReplicatedStoragePoolUsedBy struct {
	// ReplicatedStorageClassNames lists RSC names using this storage pool.
	// +listType=set
	// +optional
	ReplicatedStorageClassNames []string `json:"replicatedStorageClassNames,omitempty"`
}

// TODO: Remove ReplicatedStoragePoolPhase once the old controller (sds-replicated-volume-controller) is retired.
// ReplicatedStoragePoolPhase represents the phase of the ReplicatedStoragePool.
// Deprecated: Used only by the old controller.
type ReplicatedStoragePoolPhase string

// TODO: Remove phase constants once the old controller (sds-replicated-volume-controller) is retired.
// Deprecated: Used only by the old controller.
//
// ReplicatedStoragePool phase values.
const (
	RSPPhaseCompleted ReplicatedStoragePoolPhase = "Completed"
	RSPPhaseFailed    ReplicatedStoragePoolPhase = "Failed"
)

func (p ReplicatedStoragePoolPhase) String() string {
	return string(p)
}

// ReplicatedStoragePoolEligibleNode represents a node eligible for placing volumes of this storage pool.
// +kubebuilder:object:generate=true
type ReplicatedStoragePoolEligibleNode struct {
	// NodeName is the Kubernetes node name.
	NodeName string `json:"nodeName"`
	// ZoneName is the zone this node belongs to.
	// +optional
	ZoneName string `json:"zoneName,omitempty"`
	// LVMVolumeGroups lists LVM volume groups available on this node.
	// +optional
	LVMVolumeGroups []ReplicatedStoragePoolEligibleNodeLVMVolumeGroup `json:"lvmVolumeGroups,omitempty"`
	// Unschedulable indicates whether new volumes should not be scheduled to this node.
	Unschedulable bool `json:"unschedulable"`
	// NodeReady indicates whether the Kubernetes node is ready.
	NodeReady bool `json:"nodeReady"`
	// AgentReady indicates whether the sds-replicated-volume agent on this node is ready.
	AgentReady bool `json:"agentReady"`
}

// ReplicatedStoragePoolEligibleNodeLVMVolumeGroup represents an LVM volume group on an eligible node.
// +kubebuilder:object:generate=true
type ReplicatedStoragePoolEligibleNodeLVMVolumeGroup struct {
	// Name is the LVMVolumeGroup resource name.
	Name string `json:"name"`
	// ThinPoolName is the thin pool name (for LVMThin storage pools).
	// +optional
	ThinPoolName string `json:"thinPoolName,omitempty"`
	// Unschedulable indicates whether new volumes should not use this volume group.
	Unschedulable bool `json:"unschedulable"`
	// Ready indicates whether the LVMVolumeGroup (and its thin pool, if applicable) is ready.
	Ready bool `json:"ready"`
}
