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

// ReplicatedStorageClass is a Kubernetes Custom Resource that defines a configuration for a Kubernetes Storage class.
// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=rsc
// +kubebuilder:metadata:labels=heritage=deckhouse
// +kubebuilder:metadata:labels=module=sds-replicated-volume
// +kubebuilder:metadata:labels=backup.deckhouse.io/cluster-config=true
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Reason",type=string,priority=1,JSONPath=`.status.reason`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`,description="The age of this resource"
type ReplicatedStorageClass struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ReplicatedStorageClassSpec   `json:"spec"`
	Status            ReplicatedStorageClassStatus `json:"status,omitempty"`
}

// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
type ReplicatedStorageClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []ReplicatedStorageClass `json:"items"`
}

// GetStatusConditions is an adapter method to satisfy objutilv1.StatusConditionObject.
// It returns the root object's `.status.conditions`.
func (o *ReplicatedStorageClass) GetStatusConditions() []metav1.Condition { return o.Status.Conditions }

// SetStatusConditions is an adapter method to satisfy objutilv1.StatusConditionObject.
// It sets the root object's `.status.conditions`.
func (o *ReplicatedStorageClass) SetStatusConditions(conditions []metav1.Condition) {
	o.Status.Conditions = conditions
}

// +kubebuilder:validation:XValidation:rule="(has(self.replication) && self.replication == \"None\") || ((!has(self.replication) || self.replication == \"Availability\" || self.replication == \"Consistency\" || self.replication == \"ConsistencyAndAvailability\") && (!has(self.zones) || size(self.zones) == 0 || size(self.zones) == 1 || size(self.zones) == 3))",message="When replication is not set or is set to Availability, Consistency, or ConsistencyAndAvailability (default value), zones must be either not specified, or must contain exactly 1 or 3 zones."
// +kubebuilder:validation:XValidation:rule="(has(self.zones) && has(oldSelf.zones)) || (!has(self.zones) && !has(oldSelf.zones))",message="zones field cannot be deleted or added"
// +kubebuilder:validation:XValidation:rule="(has(self.replication) && has(oldSelf.replication)) || (!has(self.replication) && !has(oldSelf.replication))",message="replication filed cannot be deleted or added"
// +kubebuilder:validation:XValidation:rule="(has(self.volumeAccess) && has(oldSelf.volumeAccess)) || (!has(self.volumeAccess) && !has(oldSelf.volumeAccess))",message="volumeAccess filed cannot be deleted or added"
// Defines a Kubernetes Storage class configuration.
//
// > Note that this field is in read-only mode.
// +kubebuilder:object:generate=true
type ReplicatedStorageClassSpec struct {
	// Selected ReplicatedStoragePool resource's name.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable."
	StoragePool string `json:"storagePool"`
	// The storage class's reclaim policy. Might be:
	// - Delete (If the Persistent Volume Claim is deleted, deletes the Persistent Volume and its associated storage as well)
	// - Retain (If the Persistent Volume Claim is deleted, remains the Persistent Volume and its associated storage)
	// +kubebuilder:validation:Enum=Delete;Retain
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable."
	ReclaimPolicy ReplicatedStorageClassReclaimPolicy `json:"reclaimPolicy"`
	// The Storage class's replication mode. Might be:
	// - None — In this mode the Storage class's 'placementCount' and 'AutoEvictMinReplicaCount' params equal '1'.
	// - Availability — In this mode the volume remains readable and writable even if one of the replica nodes becomes unavailable. Data is stored in two copies on different nodes. This corresponds to `placementCount = 2` and `AutoEvictMinReplicaCount = 2`. **Important:** this mode does not guarantee data consistency and may lead to split brain and data loss in case of network connectivity issues between nodes. Recommended only for non-critical data and applications that do not require high reliability and data integrity.
	// - ConsistencyAndAvailability — In this mode the volume remains readable and writable when one replica node fails. Data is stored in three copies on different nodes (`placementCount = 3`, `AutoEvictMinReplicaCount = 3`). This mode provides protection against data loss when two nodes containing volume replicas fail and guarantees data consistency. However, if two replicas are lost, the volume switches to suspend-io mode.
	//
	// > Note that default Replication mode is 'ConsistencyAndAvailability'.
	// +kubebuilder:validation:Enum=None;Availability;Consistency;ConsistencyAndAvailability
	// +kubebuilder:default:=ConsistencyAndAvailability
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable."
	Replication ReplicatedStorageClassReplication `json:"replication,omitempty"`
	// The Storage class's access mode. Might be:
	// - Local (in this mode the Storage class's 'allowRemoteVolumeAccess' param equals 'false'
	// and Volume Binding mode equals 'WaitForFirstConsumer')
	// - EventuallyLocal (in this mode the Storage class's 'allowRemoteVolumeAccess' param
	// equals '- fromSame:\n  - topology.kubernetes.io/zone', 'auto-diskful' param equals '30' minutes,
	// 'auto-diskful-allow-cleanup' param equals 'true',
	// and Volume Binding mode equals 'WaitForFirstConsumer')
	// - PreferablyLocal (in this mode the Storage class's 'allowRemoteVolumeAccess' param
	// equals '- fromSame:\n  - topology.kubernetes.io/zone',
	// and Volume Binding mode equals 'WaitForFirstConsumer')
	// - Any (in this mode the Storage class's 'allowRemoteVolumeAccess' param
	// equals '- fromSame:\n  - topology.kubernetes.io/zone',
	// and Volume Binding mode equals 'Immediate')
	//
	// > Note that the default Volume Access mode is 'PreferablyLocal'.
	// +kubebuilder:validation:Enum=Local;EventuallyLocal;PreferablyLocal;Any
	// +kubebuilder:default:=PreferablyLocal
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable."
	VolumeAccess ReplicatedStorageClassVolumeAccess `json:"volumeAccess,omitempty"`
	// The topology settings for the volumes in the created Storage class. Might be:
	// - TransZonal - replicas of the volumes will be created in different zones (one replica per zone).
	// To use this topology, the available zones must be specified in the 'zones' param, and the cluster nodes must have the topology.kubernetes.io/zone=<zone name> label.
	// - Zonal - all replicas of the volumes are created in the same zone that the scheduler selected to place the pod using this volume.
	// - Ignored - the topology information will not be used to place replicas of the volumes.
	// The replicas can be placed on any available nodes, with the restriction: no more than one replica of a given volume on one node.
	//
	// > Note that the 'Ignored' value can be used only if there are no zones in the cluster (there are no nodes with the topology.kubernetes.io/zone label).
	//
	// > For the system to operate correctly, either every cluster node must be labeled with 'topology.kubernetes.io/zone', or none of them should have this label.
	// +kubebuilder:validation:Enum=TransZonal;Zonal;Ignored
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable."
	Topology ReplicatedStorageClassTopology `json:"topology"`
	// Array of zones the Storage class's volumes should be replicated in. The controller will put a label with
	// the Storage class's name on the nodes which be actual used by the Storage class.
	//
	// > Note that for Replication mode 'Availability' and 'ConsistencyAndAvailability' you have to select
	// exactly 1 or 3 zones.
	// +kubebuilder:validation:MaxItems=10
	// +kubebuilder:validation:items:MaxLength=63
	// +listType=set
	// +optional
	Zones []string `json:"zones,omitempty"`
	// NodeLabelSelector filters nodes eligible for DRBD participation.
	// Only nodes matching this selector can store data, provide access, or host tiebreaker.
	// If not specified, all nodes are candidates.
	// +optional
	NodeLabelSelector *metav1.LabelSelector `json:"nodeLabelSelector,omitempty"`
	// SystemNetworkNames specifies network names used for DRBD replication traffic.
	// At least one network name must be specified. Each name is limited to 64 characters.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:Items={type=string,maxLength=64}
	// +kubebuilder:default:={"Internal"}
	SystemNetworkNames []string `json:"systemNetworkNames"`
	// ConfigurationRolloutStrategy defines how configuration changes are applied to existing volumes.
	// Always present with defaults.
	ConfigurationRolloutStrategy ReplicatedStorageClassConfigurationRolloutStrategy `json:"configurationRolloutStrategy"`
	// EligibleNodesConflictResolutionStrategy defines how the controller handles volumes with eligible nodes conflicts.
	// Always present with defaults.
	EligibleNodesConflictResolutionStrategy ReplicatedStorageClassEligibleNodesConflictResolutionStrategy `json:"eligibleNodesConflictResolutionStrategy"`
	// EligibleNodesPolicy defines policies for managing eligible nodes.
	// Always present with defaults.
	EligibleNodesPolicy ReplicatedStorageClassEligibleNodesPolicy `json:"eligibleNodesPolicy"`
}

// ReplicatedStorageClassReclaimPolicy enumerates possible values for ReplicatedStorageClass spec.reclaimPolicy field.
type ReplicatedStorageClassReclaimPolicy string

// ReclaimPolicy values for [ReplicatedStorageClass] spec.reclaimPolicy field.
const (
	// RSCReclaimPolicyDelete means the PV is deleted when the PVC is deleted.
	RSCReclaimPolicyDelete ReplicatedStorageClassReclaimPolicy = "Delete"
	// RSCReclaimPolicyRetain means the PV is retained when the PVC is deleted.
	RSCReclaimPolicyRetain ReplicatedStorageClassReclaimPolicy = "Retain"
)

func (p ReplicatedStorageClassReclaimPolicy) String() string {
	return string(p)
}

// ReplicatedStorageClassReplication enumerates possible values for ReplicatedStorageClass spec.replication field.
type ReplicatedStorageClassReplication string

// Replication values for [ReplicatedStorageClass] spec.replication field.
const (
	// ReplicationNone means no replication (single replica).
	ReplicationNone ReplicatedStorageClassReplication = "None"
	// ReplicationAvailability means 2 replicas; can lose 1 node, but may lose consistency in network partitions.
	ReplicationAvailability ReplicatedStorageClassReplication = "Availability"
	// ReplicationConsistency means 2 replicas with consistency guarantees; requires quorum for writes.
	ReplicationConsistency ReplicatedStorageClassReplication = "Consistency"
	// ReplicationConsistencyAndAvailability means 3 replicas; can lose 1 node and keeps consistency.
	ReplicationConsistencyAndAvailability ReplicatedStorageClassReplication = "ConsistencyAndAvailability"
)

func (r ReplicatedStorageClassReplication) String() string {
	return string(r)
}

// ReplicatedStorageClassVolumeAccess enumerates possible values for ReplicatedStorageClass spec.volumeAccess field.
type ReplicatedStorageClassVolumeAccess string

// VolumeAccess values for [ReplicatedStorageClass] spec.volumeAccess field.
const (
	// VolumeAccessLocal requires data to be accessed only from nodes with Diskful replicas
	VolumeAccessLocal ReplicatedStorageClassVolumeAccess = "Local"
	// VolumeAccessPreferablyLocal prefers local access but allows remote if needed
	VolumeAccessPreferablyLocal ReplicatedStorageClassVolumeAccess = "PreferablyLocal"
	// VolumeAccessEventuallyLocal will eventually migrate to local access
	VolumeAccessEventuallyLocal ReplicatedStorageClassVolumeAccess = "EventuallyLocal"
	// VolumeAccessAny allows access from any node
	VolumeAccessAny ReplicatedStorageClassVolumeAccess = "Any"
)

func (a ReplicatedStorageClassVolumeAccess) String() string {
	return string(a)
}

// ReplicatedStorageClassTopology enumerates possible values for ReplicatedStorageClass spec.topology field.
type ReplicatedStorageClassTopology string

// Topology values for [ReplicatedStorageClass] spec.topology field.
const (
	// RSCTopologyTransZonal means replicas should be placed across zones.
	RSCTopologyTransZonal ReplicatedStorageClassTopology = "TransZonal"
	// RSCTopologyZonal means replicas should be placed in a single zone.
	RSCTopologyZonal ReplicatedStorageClassTopology = "Zonal"
	// RSCTopologyIgnored means topology information is not used for placement.
	RSCTopologyIgnored ReplicatedStorageClassTopology = "Ignored"
)

func (t ReplicatedStorageClassTopology) String() string {
	return string(t)
}

// ReplicatedStorageClassConfigurationRolloutStrategy defines how configuration changes are rolled out to existing volumes.
// +kubebuilder:validation:XValidation:rule="self.type != 'RollingUpdate' || has(self.rollingUpdate)",message="rollingUpdate is required when type is RollingUpdate"
// +kubebuilder:validation:XValidation:rule="self.type == 'RollingUpdate' || !has(self.rollingUpdate)",message="rollingUpdate must not be set when type is not RollingUpdate"
// +kubebuilder:object:generate=true
type ReplicatedStorageClassConfigurationRolloutStrategy struct {
	// Type specifies the rollout strategy type.
	// +kubebuilder:validation:Enum=RollingUpdate;NewVolumesOnly
	// +kubebuilder:default:=RollingUpdate
	Type ReplicatedStorageClassConfigurationRolloutStrategyType `json:"type,omitempty"`
	// RollingUpdate configures parameters for RollingUpdate strategy.
	// Required when type is RollingUpdate.
	// +optional
	RollingUpdate *ReplicatedStorageClassConfigurationRollingUpdateStrategy `json:"rollingUpdate,omitempty"`
}

// ReplicatedStorageClassConfigurationRolloutStrategyType enumerates possible values for configuration rollout strategy type.
type ReplicatedStorageClassConfigurationRolloutStrategyType string

const (
	// ReplicatedStorageClassConfigurationRolloutStrategyTypeRollingUpdate means configuration changes are rolled out to existing volumes.
	ReplicatedStorageClassConfigurationRolloutStrategyTypeRollingUpdate ReplicatedStorageClassConfigurationRolloutStrategyType = "RollingUpdate"
	// ReplicatedStorageClassConfigurationRolloutStrategyTypeNewVolumesOnly means configuration changes only apply to newly created volumes.
	ReplicatedStorageClassConfigurationRolloutStrategyTypeNewVolumesOnly ReplicatedStorageClassConfigurationRolloutStrategyType = "NewVolumesOnly"
)

func (t ReplicatedStorageClassConfigurationRolloutStrategyType) String() string { return string(t) }

// ReplicatedStorageClassConfigurationRollingUpdateStrategy configures parameters for rolling update configuration rollout strategy.
// +kubebuilder:object:generate=true
type ReplicatedStorageClassConfigurationRollingUpdateStrategy struct {
	// MaxParallel is the maximum number of volumes being rolled out simultaneously.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=200
	// +kubebuilder:default:=5
	MaxParallel int32 `json:"maxParallel"`
}

// ReplicatedStorageClassEligibleNodesConflictResolutionStrategy defines how the controller resolves volumes with eligible nodes conflicts.
// +kubebuilder:validation:XValidation:rule="self.type != 'RollingRepair' || has(self.rollingRepair)",message="rollingRepair is required when type is RollingRepair"
// +kubebuilder:validation:XValidation:rule="self.type == 'RollingRepair' || !has(self.rollingRepair)",message="rollingRepair must not be set when type is not RollingRepair"
// +kubebuilder:object:generate=true
type ReplicatedStorageClassEligibleNodesConflictResolutionStrategy struct {
	// Type specifies the conflict resolution strategy type.
	// +kubebuilder:validation:Enum=Manual;RollingRepair
	// +kubebuilder:default:=RollingRepair
	Type ReplicatedStorageClassEligibleNodesConflictResolutionStrategyType `json:"type,omitempty"`
	// RollingRepair configures parameters for RollingRepair conflict resolution strategy.
	// Required when type is RollingRepair.
	// +optional
	RollingRepair *ReplicatedStorageClassEligibleNodesConflictResolutionRollingRepair `json:"rollingRepair,omitempty"`
}

// ReplicatedStorageClassEligibleNodesConflictResolutionStrategyType enumerates possible values for eligible nodes conflict resolution strategy type.
type ReplicatedStorageClassEligibleNodesConflictResolutionStrategyType string

const (
	// ReplicatedStorageClassEligibleNodesConflictResolutionStrategyTypeManual means conflicts are resolved manually.
	ReplicatedStorageClassEligibleNodesConflictResolutionStrategyTypeManual ReplicatedStorageClassEligibleNodesConflictResolutionStrategyType = "Manual"
	// ReplicatedStorageClassEligibleNodesConflictResolutionStrategyTypeRollingRepair means replicas are moved automatically when eligible nodes change.
	ReplicatedStorageClassEligibleNodesConflictResolutionStrategyTypeRollingRepair ReplicatedStorageClassEligibleNodesConflictResolutionStrategyType = "RollingRepair"
)

func (t ReplicatedStorageClassEligibleNodesConflictResolutionStrategyType) String() string {
	return string(t)
}

// ReplicatedStorageClassEligibleNodesConflictResolutionRollingRepair configures parameters for rolling repair conflict resolution strategy.
// +kubebuilder:object:generate=true
type ReplicatedStorageClassEligibleNodesConflictResolutionRollingRepair struct {
	// MaxParallel is the maximum number of volumes being repaired simultaneously.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=200
	// +kubebuilder:default:=5
	MaxParallel int32 `json:"maxParallel"`
}

// ReplicatedStorageClassEligibleNodesPolicy defines policies for managing eligible nodes.
// +kubebuilder:object:generate=true
type ReplicatedStorageClassEligibleNodesPolicy struct {
	// NotReadyGracePeriod specifies how long to wait before removing
	// a not-ready node from the eligible nodes list.
	// +kubebuilder:validation:Required
	NotReadyGracePeriod metav1.Duration `json:"notReadyGracePeriod"`
}

// Displays current information about the Storage Class.
// +kubebuilder:object:generate=true
type ReplicatedStorageClassStatus struct {
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// The Storage class current state. Might be:
	// - Failed (if the controller received incorrect resource configuration or some errors occurred during the operation)
	// - Create (if everything went fine)
	// +kubebuilder:validation:Enum=Failed;Created
	Phase ReplicatedStorageClassPhase `json:"phase,omitempty"`
	// Additional information about the current state of the Storage Class.
	Reason string `json:"reason,omitempty"`
	// ConfigurationGeneration is the RSC generation when configuration was accepted.
	// +optional
	ConfigurationGeneration int64 `json:"configurationGeneration,omitempty"`
	// Configuration is the resolved configuration that volumes should align to.
	// +optional
	Configuration *ReplicatedStorageClassConfiguration `json:"configuration,omitempty"`
	// EligibleNodesRevision is incremented when eligible nodes change.
	// +optional
	EligibleNodesRevision int64 `json:"eligibleNodesRevision,omitempty"`
	// EligibleNodesWorldState tracks external state (RSP, LVGs, Nodes) that affects eligible nodes calculation.
	// +optional
	EligibleNodesWorldState *ReplicatedStorageClassEligibleNodesWorldState `json:"eligibleNodesWorldState,omitempty"`
	// EligibleNodes lists nodes eligible for this storage class.
	// +optional
	EligibleNodes []ReplicatedStorageClassEligibleNode `json:"eligibleNodes,omitempty"`
	// Volumes provides aggregated volume statistics.
	// Always present (may have total=0).
	Volumes ReplicatedStorageClassVolumesSummary `json:"volumes"`
}

// ReplicatedStorageClassEligibleNodesWorldState tracks external state that affects eligible nodes.
// +kubebuilder:object:generate=true
type ReplicatedStorageClassEligibleNodesWorldState struct {
	// Checksum is a hash of external state (RSP generation, LVG generations/annotations, Node labels/conditions).
	Checksum string `json:"checksum"`
	// ExpiresAt is the time when this state should be recalculated regardless of checksum match.
	ExpiresAt metav1.Time `json:"expiresAt"`
}

// ReplicatedStorageClassPhase enumerates possible values for ReplicatedStorageClass status.phase field.
type ReplicatedStorageClassPhase string

// Phase values for [ReplicatedStorageClass] status.phase field.
const (
	// RSCPhaseFailed means the controller detected an invalid configuration or an operation error.
	RSCPhaseFailed ReplicatedStorageClassPhase = "Failed"
	// RSCPhaseCreated means the replicated storage class has been reconciled successfully.
	RSCPhaseCreated ReplicatedStorageClassPhase = "Created"
)

func (p ReplicatedStorageClassPhase) String() string {
	return string(p)
}

// ReplicatedStorageClassConfiguration represents the resolved configuration that volumes should align to.
// +kubebuilder:object:generate=true
type ReplicatedStorageClassConfiguration struct {
	// Topology is the resolved topology setting.
	Topology ReplicatedStorageClassTopology `json:"topology"`
	// Replication is the resolved replication mode.
	Replication ReplicatedStorageClassReplication `json:"replication"`
	// VolumeAccess is the resolved volume access mode.
	VolumeAccess ReplicatedStorageClassVolumeAccess `json:"volumeAccess"`
	// Zones is the resolved list of zones.
	// +optional
	Zones []string `json:"zones,omitempty"`
	// SystemNetworkNames is the resolved list of system network names.
	SystemNetworkNames []string `json:"systemNetworkNames"`
	// EligibleNodesPolicy is the resolved eligible nodes policy.
	EligibleNodesPolicy ReplicatedStorageClassEligibleNodesPolicy `json:"eligibleNodesPolicy"`
	// NodeLabelSelector filters nodes eligible for DRBD participation.
	// +optional
	NodeLabelSelector *metav1.LabelSelector `json:"nodeLabelSelector,omitempty"`
}

// ReplicatedStorageClassEligibleNode represents a node eligible for placing volumes of this storage class.
// +kubebuilder:object:generate=true
type ReplicatedStorageClassEligibleNode struct {
	// NodeName is the Kubernetes node name.
	NodeName string `json:"nodeName"`
	// ZoneName is the zone this node belongs to.
	// +optional
	ZoneName string `json:"zoneName,omitempty"`
	// LVMVolumeGroups lists LVM volume groups available on this node.
	// +optional
	LVMVolumeGroups []ReplicatedStorageClassEligibleNodeLVMVolumeGroup `json:"lvmVolumeGroups,omitempty"`
	// Unschedulable indicates whether new volumes should not be scheduled to this node.
	// +optional
	Unschedulable bool `json:"unschedulable,omitempty"`
	// Ready indicates whether the node is ready to serve volumes.
	// +optional
	Ready bool `json:"ready,omitempty"`
}

// ReplicatedStorageClassEligibleNodeLVMVolumeGroup represents an LVM volume group on an eligible node.
// +kubebuilder:object:generate=true
type ReplicatedStorageClassEligibleNodeLVMVolumeGroup struct {
	// Name is the LVMVolumeGroup resource name.
	Name string `json:"name"`
	// ThinPoolName is the thin pool name (for LVMThin storage pools).
	// +optional
	ThinPoolName string `json:"thinPoolName,omitempty"`
	// Unschedulable indicates whether new volumes should not use this volume group.
	// +optional
	Unschedulable bool `json:"unschedulable,omitempty"`
}

// ReplicatedStorageClassVolumesSummary provides aggregated information about volumes in this storage class.
// +kubebuilder:object:generate=true
type ReplicatedStorageClassVolumesSummary struct {
	// Total is the total number of volumes.
	// +optional
	Total *int32 `json:"total,omitempty"`
	// PendingObservation is the number of volumes that haven't observed current RSC configuration or eligible nodes.
	// +optional
	PendingObservation *int32 `json:"pendingObservation,omitempty"`
	// Aligned is the number of volumes whose configuration matches the storage class.
	// +optional
	Aligned *int32 `json:"aligned,omitempty"`
	// InConflictWithEligibleNodes is the number of volumes with replicas on non-eligible nodes.
	// +optional
	InConflictWithEligibleNodes *int32 `json:"inConflictWithEligibleNodes,omitempty"`
	// StaleConfiguration is the number of volumes with outdated configuration.
	// +optional
	StaleConfiguration *int32 `json:"staleConfiguration,omitempty"`
}
