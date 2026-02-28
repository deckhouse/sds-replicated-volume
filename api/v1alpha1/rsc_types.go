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
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=rsc
// +kubebuilder:metadata:labels=heritage=deckhouse
// +kubebuilder:metadata:labels=module=sds-replicated-volume
// +kubebuilder:metadata:labels=backup.deckhouse.io/cluster-config=true
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].status`
// +kubebuilder:printcolumn:name="Replication",type=string,JSONPath=`.spec.replication`
// +kubebuilder:printcolumn:name="Topology",type=string,JSONPath=`.spec.topology`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:printcolumn:name="StoragePool",type=string,priority=1,JSONPath=`.status.storagePoolName`
// +kubebuilder:printcolumn:name="StoragePoolReady",type=string,priority=1,JSONPath=`.status.conditions[?(@.type=='StoragePoolReady')].status`
// +kubebuilder:printcolumn:name="ConfigRolledOut",type=string,priority=1,JSONPath=`.status.conditions[?(@.type=='ConfigurationRolledOut')].status`
// +kubebuilder:printcolumn:name="VolsSatisfyEN",type=string,priority=1,JSONPath=`.status.conditions[?(@.type=='VolumesSatisfyEligibleNodes')].status`
// +kubebuilder:printcolumn:name="VolumeAccess",type=string,priority=1,JSONPath=`.spec.volumeAccess`
// +kubebuilder:printcolumn:name="Volumes",type=integer,priority=1,JSONPath=`.status.volumes.total`
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
func (rsc *ReplicatedStorageClass) GetStatusConditions() []metav1.Condition {
	return rsc.Status.Conditions
}

// SetStatusConditions is an adapter method to satisfy objutilv1.StatusConditionObject.
// It sets the root object's `.status.conditions`.
func (rsc *ReplicatedStorageClass) SetStatusConditions(conditions []metav1.Condition) {
	rsc.Status.Conditions = conditions
}

// Legacy replication validations (only fire when replication is set):
// +kubebuilder:validation:XValidation:rule="!has(self.replication) || self.replication != 'None' || self.topology == 'Ignored'",message="Replication None requires topology Ignored (no replicas to distribute)."
// +kubebuilder:validation:XValidation:rule="self.topology != 'TransZonal' || !has(self.replication) || self.replication != 'Availability' || !has(self.zones) || size(self.zones) == 0 || size(self.zones) >= 3",message="TransZonal topology with Availability replication requires at least 3 zones (if specified)."
// +kubebuilder:validation:XValidation:rule="self.topology != 'TransZonal' || !has(self.replication) || self.replication != 'Consistency' || !has(self.zones) || size(self.zones) == 0 || size(self.zones) >= 2",message="TransZonal topology with Consistency replication requires at least 2 zones (if specified)."
// +kubebuilder:validation:XValidation:rule="self.topology != 'TransZonal' || (has(self.replication) && self.replication != 'ConsistencyAndAvailability') || !has(self.zones) || size(self.zones) == 0 || size(self.zones) >= 3",message="TransZonal topology with ConsistencyAndAvailability replication (default) requires at least 3 zones (if specified)."
//
// FTT/GMDR mutual exclusivity with replication:
// +kubebuilder:validation:XValidation:rule="!(has(self.failuresToTolerate) || has(self.guaranteedMinimumDataRedundancy)) || !has(self.replication)",message="Cannot specify both replication and failuresToTolerate/guaranteedMinimumDataRedundancy."
//
// FTT and GMDR must be specified together:
// +kubebuilder:validation:XValidation:rule="has(self.failuresToTolerate) == has(self.guaranteedMinimumDataRedundancy)",message="failuresToTolerate and guaranteedMinimumDataRedundancy must be specified together."
//
// Valid FTT/GMDR combinations: |FTT - GMDR| <= 1:
// +kubebuilder:validation:XValidation:rule="!has(self.failuresToTolerate) || (self.failuresToTolerate - self.guaranteedMinimumDataRedundancy <= 1 && self.guaranteedMinimumDataRedundancy - self.failuresToTolerate <= 1)",message="Invalid failuresToTolerate/guaranteedMinimumDataRedundancy combination: |FTT - GMDR| must be <= 1."
//
// FTT=0, GMDR=0 requires topology Ignored:
// +kubebuilder:validation:XValidation:rule="!(has(self.failuresToTolerate) && self.failuresToTolerate == 0 && self.guaranteedMinimumDataRedundancy == 0) || self.topology == 'Ignored'",message="failuresToTolerate=0 with guaranteedMinimumDataRedundancy=0 requires topology Ignored."
//
// TransZonal zone count validations for FTT/GMDR:
//
//	+kubebuilder:validation:XValidation:rule="self.topology != 'TransZonal' || !has(self.failuresToTolerate) || !has(self.zones) || size(self.zones) == 0 || self.failuresToTolerate != 0 || self.guaranteedMinimumDataRedundancy != 1 || size(self.zones) == 2",message="TransZonal with FTT=0, GMDR=1 requires exactly 2 zones."
//	+kubebuilder:validation:XValidation:rule="self.topology != 'TransZonal' || !has(self.failuresToTolerate) || !has(self.zones) || size(self.zones) == 0 || self.failuresToTolerate != 1 || self.guaranteedMinimumDataRedundancy != 0 || size(self.zones) == 3",message="TransZonal with FTT=1, GMDR=0 requires exactly 3 zones."
//	+kubebuilder:validation:XValidation:rule="self.topology != 'TransZonal' || !has(self.failuresToTolerate) || !has(self.zones) || size(self.zones) == 0 || self.failuresToTolerate != 1 || self.guaranteedMinimumDataRedundancy != 1 || size(self.zones) == 3",message="TransZonal with FTT=1, GMDR=1 requires exactly 3 zones."
//	+kubebuilder:validation:XValidation:rule="self.topology != 'TransZonal' || !has(self.failuresToTolerate) || !has(self.zones) || size(self.zones) == 0 || self.failuresToTolerate != 1 || self.guaranteedMinimumDataRedundancy != 2 || size(self.zones) == 3 || size(self.zones) == 5",message="TransZonal with FTT=1, GMDR=2 requires exactly 3 or 5 zones."
//	+kubebuilder:validation:XValidation:rule="self.topology != 'TransZonal' || !has(self.failuresToTolerate) || !has(self.zones) || size(self.zones) == 0 || self.failuresToTolerate != 2 || self.guaranteedMinimumDataRedundancy != 1 || size(self.zones) == 4",message="TransZonal with FTT=2, GMDR=1 requires exactly 4 zones."
//	+kubebuilder:validation:XValidation:rule="self.topology != 'TransZonal' || !has(self.failuresToTolerate) || !has(self.zones) || size(self.zones) == 0 || self.failuresToTolerate != 2 || self.guaranteedMinimumDataRedundancy != 2 || size(self.zones) == 3 || size(self.zones) == 5",message="TransZonal with FTT=2, GMDR=2 requires exactly 3 or 5 zones."
//
// Defines a Kubernetes Storage class configuration.
//
// > Note that this field is in read-only mode.
// +kubebuilder:object:generate=true
type ReplicatedStorageClassSpec struct {
	// StoragePool is the name of a ReplicatedStoragePool resource.
	// This field cannot be added or changed, only removed.
	//
	// Deprecated: Use Storage instead.
	// +kubebuilder:validation:XValidation:rule="size(self) == 0 || self == oldSelf",message="StoragePool cannot be added or changed, only removed"
	// +optional
	StoragePool string `json:"storagePool,omitempty"`
	// Storage defines the storage backend configuration for this storage class.
	// Specifies the type of volumes (LVM or LVMThin) and which LVMVolumeGroups
	// will be used to allocate space for volumes.
	Storage ReplicatedStorageClassStorage `json:"storage"`
	// The storage class's reclaim policy. Might be:
	// - Delete (If the Persistent Volume Claim is deleted, deletes the Persistent Volume and its associated storage as well)
	// - Retain (If the Persistent Volume Claim is deleted, remains the Persistent Volume and its associated storage)
	// +kubebuilder:validation:Enum=Delete;Retain
	ReclaimPolicy ReplicatedStorageClassReclaimPolicy `json:"reclaimPolicy"`
	// Deprecated: Use FailuresToTolerate and GuaranteedMinimumDataRedundancy instead.
	// Mutually exclusive with FailuresToTolerate/GuaranteedMinimumDataRedundancy.
	//
	// The Storage class's replication mode. Might be:
	// - None — single replica, no replication. Requires topology to be 'Ignored'.
	// - Availability — 2 replicas; can lose 1 node but may lose data redundancy in degraded mode.
	// - Consistency — 2 replicas with consistency guarantees; requires both replicas for IO.
	// - ConsistencyAndAvailability — 3 replicas; can lose 1 node and keeps consistency.
	//
	// Mapping to failuresToTolerate/guaranteedMinimumDataRedundancy:
	//   None                       → failuresToTolerate=0, guaranteedMinimumDataRedundancy=0
	//   Availability               → failuresToTolerate=1, guaranteedMinimumDataRedundancy=0
	//   Consistency                → failuresToTolerate=0, guaranteedMinimumDataRedundancy=1
	//   ConsistencyAndAvailability → failuresToTolerate=1, guaranteedMinimumDataRedundancy=1
	//
	// +kubebuilder:validation:Enum=None;Availability;Consistency;ConsistencyAndAvailability
	// +kubebuilder:default:=ConsistencyAndAvailability
	Replication ReplicatedStorageClassReplication `json:"replication,omitempty"`
	// FailuresToTolerate (FTT) specifies how many arbitrary node failures the volume
	// can tolerate while remaining available for IO.
	// Mutually exclusive with Replication.
	// Must be specified together with GuaranteedMinimumDataRedundancy.
	// Valid range: 0-2. Valid combinations: |FTT - GMDR| <= 1.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=2
	// +optional
	FailuresToTolerate *byte `json:"failuresToTolerate,omitempty"`
	// GuaranteedMinimumDataRedundancy (GMDR) specifies the minimum number of additional
	// data copies maintained while the volume is serving IO. GMDR=0 means the system may
	// operate with a single data copy in degraded mode (like a RAID-1 mirror after one
	// disk failure). GMDR=1 means at least 2 copies are always maintained during IO.
	// Mutually exclusive with Replication.
	// Must be specified together with FailuresToTolerate.
	// Valid range: 0-2. Valid combinations: |FTT - GMDR| <= 1.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=2
	// +optional
	GuaranteedMinimumDataRedundancy *byte `json:"guaranteedMinimumDataRedundancy,omitempty"`
	// The Storage class's volume access mode. Defines how pods access the volume. Might be:
	// - Local — volume is accessed only from the node where a replica resides. Pod scheduling waits for consumer.
	// - EventuallyLocal — volume can be accessed remotely, but a local replica will be created on the accessing node
	//   after some time. Pod scheduling waits for consumer.
	// - PreferablyLocal — volume prefers local access but allows remote access if no local replica is available.
	//   Scheduler tries to place pods on nodes with replicas. Pod scheduling waits for consumer.
	// - Any — volume can be accessed from any node. Most flexible mode with immediate volume binding.
	//
	// > Note that the default Volume Access mode is 'PreferablyLocal'.
	// +kubebuilder:validation:Enum=Local;EventuallyLocal;PreferablyLocal;Any
	// +kubebuilder:default:=PreferablyLocal
	VolumeAccess ReplicatedStorageClassVolumeAccess `json:"volumeAccess,omitempty"`
	// The topology settings for the volumes in the created Storage class. Might be:
	// - TransZonal — replicas of the volumes will be created in different zones (one replica per zone).
	//   To use this topology, the available zones must be specified in the 'zones' param, and the cluster nodes must have the topology.kubernetes.io/zone=<zone name> label.
	// - Zonal — all replicas of the volumes are created in the same zone that the scheduler selected to place the pod using this volume.
	// - Ignored — the topology information will not be used to place replicas of the volumes.
	//   The replicas can be placed on any available nodes, with the restriction: no more than one replica of a given volume on one node.
	//   Required when replication is 'None'.
	//
	// > Note that the 'Ignored' value can be used only if there are no zones in the cluster (there are no nodes with the topology.kubernetes.io/zone label).
	//
	// > For the system to operate correctly, either every cluster node must be labeled with 'topology.kubernetes.io/zone', or none of them should have this label.
	// +kubebuilder:validation:Enum=TransZonal;Zonal;Ignored
	Topology ReplicatedStorageClassTopology `json:"topology"`
	// Array of zones the Storage class's volumes should be replicated in. The controller will put a label with
	// the Storage class's name on the nodes which be actual used by the Storage class.
	//
	// For TransZonal topology, the number of zones depends on replication mode:
	// - Availability, ConsistencyAndAvailability: at least 3 zones required
	// - Consistency: at least 2 zones required
	//
	// When replication is 'None', zones act as a node constraint
	// limiting where the single replica can be placed.
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
	//
	// TODO(systemnetwork): Currently only "Internal" (default node network) is supported.
	// Custom network support requires NetworkNode watch implementation in the controller.
	// When multi-network support is implemented, raise MaxItems to 10.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=1
	// +kubebuilder:validation:Items={type=string,maxLength=64}
	// +kubebuilder:validation:XValidation:rule="self.all(n, n == 'Internal')",message="Only 'Internal' network is currently supported"
	// +kubebuilder:default:={"Internal"}
	// +listType=set
	SystemNetworkNames []string `json:"systemNetworkNames"`
	// ConfigurationRolloutStrategy defines how configuration changes are applied to existing volumes.
	// Always present with defaults.
	// +kubebuilder:default={type: "RollingUpdate", rollingUpdate: {maxParallel: 5}}
	ConfigurationRolloutStrategy ReplicatedStorageClassConfigurationRolloutStrategy `json:"configurationRolloutStrategy"`
	// EligibleNodesConflictResolutionStrategy defines how the controller handles volumes with eligible nodes conflicts.
	// Always present with defaults.
	// +kubebuilder:default={type: "RollingRepair", rollingRepair: {maxParallel: 5}}
	EligibleNodesConflictResolutionStrategy ReplicatedStorageClassEligibleNodesConflictResolutionStrategy `json:"eligibleNodesConflictResolutionStrategy"`
	// EligibleNodesPolicy defines policies for managing eligible nodes.
	// Always present with defaults.
	// +kubebuilder:default={notReadyGracePeriod: "10m"}
	EligibleNodesPolicy ReplicatedStoragePoolEligibleNodesPolicy `json:"eligibleNodesPolicy"`
}

// ReplicatedStorageClassStorage defines the storage backend configuration for RSC.
// +kubebuilder:validation:XValidation:rule="self.type != 'LVMThin' || self.lvmVolumeGroups.all(g, has(g.thinPoolName) && size(g.thinPoolName) > 0)",message="thinPoolName is required for each lvmVolumeGroups entry when type is LVMThin"
// +kubebuilder:validation:XValidation:rule="self.type != 'LVM' || self.lvmVolumeGroups.all(g, !has(g.thinPoolName) || size(g.thinPoolName) == 0)",message="thinPoolName must not be specified when type is LVM"
// +kubebuilder:object:generate=true
type ReplicatedStorageClassStorage struct {
	// Type defines the volumes type. Might be:
	// - LVM (for Thick)
	// - LVMThin (for Thin)
	// +kubebuilder:validation:Enum=LVM;LVMThin
	Type ReplicatedStoragePoolType `json:"type"`
	// LVMVolumeGroups is an array of LVMVolumeGroup resource names whose Volume Groups/Thin-pools
	// will be used to allocate the required space.
	//
	// > Note that every LVMVolumeGroup resource must have the same type (Thin/Thick)
	// as specified in the Type field.
	// TODO(thinpool-object): When ThinPool becomes a separate API object and LVMVolumeGroups
	// is replaced with selectors, revisit the key. Currently using name only because
	// thinPoolName is optional and cannot be a listMapKey without a default value.
	// +kubebuilder:validation:MinItems=1
	// +listType=map
	// +listMapKey=name
	LVMVolumeGroups []ReplicatedStoragePoolLVMVolumeGroups `json:"lvmVolumeGroups"`
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

// GetFTT returns the resolved FailuresToTolerate value.
// If FailuresToTolerate is set, returns it directly.
// Otherwise, computes it from the legacy Replication field.
func (s *ReplicatedStorageClassSpec) GetFTT() byte {
	if s.FailuresToTolerate != nil {
		return *s.FailuresToTolerate
	}
	ftt, _ := replicationToFTTGMDR(s.Replication)
	return ftt
}

// GetGMDR returns the resolved GuaranteedMinimumDataRedundancy value.
// If GuaranteedMinimumDataRedundancy is set, returns it directly.
// Otherwise, computes it from the legacy Replication field.
func (s *ReplicatedStorageClassSpec) GetGMDR() byte {
	if s.GuaranteedMinimumDataRedundancy != nil {
		return *s.GuaranteedMinimumDataRedundancy
	}
	_, gmdr := replicationToFTTGMDR(s.Replication)
	return gmdr
}

// replicationToFTTGMDR maps the legacy Replication enum to FTT/GMDR values.
//
//	None                       → FTT=0, GMDR=0
//	Availability               → FTT=1, GMDR=0
//	Consistency                → FTT=0, GMDR=1
//	ConsistencyAndAvailability → FTT=1, GMDR=1
func replicationToFTTGMDR(replication ReplicatedStorageClassReplication) (ftt, gmdr byte) {
	switch replication {
	case ReplicationNone:
		return 0, 0
	case ReplicationAvailability:
		return 1, 0
	case ReplicationConsistency:
		return 0, 1
	case ReplicationConsistencyAndAvailability:
		return 1, 1
	default:
		return 1, 1
	}
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
	// TopologyTransZonal means replicas should be placed across zones.
	TopologyTransZonal ReplicatedStorageClassTopology = "TransZonal"
	// TopologyZonal means replicas should be placed in a single zone.
	TopologyZonal ReplicatedStorageClassTopology = "Zonal"
	// TopologyIgnored means topology information is not used for placement.
	TopologyIgnored ReplicatedStorageClassTopology = "Ignored"
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
	// ConfigurationRolloutRollingUpdate means configuration changes are rolled out to existing volumes.
	ConfigurationRolloutRollingUpdate ReplicatedStorageClassConfigurationRolloutStrategyType = "RollingUpdate"
	// ConfigurationRolloutNewVolumesOnly means configuration changes only apply to newly created volumes.
	ConfigurationRolloutNewVolumesOnly ReplicatedStorageClassConfigurationRolloutStrategyType = "NewVolumesOnly"
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
	// EligibleNodesConflictResolutionManual means conflicts are resolved manually.
	EligibleNodesConflictResolutionManual ReplicatedStorageClassEligibleNodesConflictResolutionStrategyType = "Manual"
	// EligibleNodesConflictResolutionRollingRepair means replicas are moved automatically when eligible nodes change.
	EligibleNodesConflictResolutionRollingRepair ReplicatedStorageClassEligibleNodesConflictResolutionStrategyType = "RollingRepair"
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

// Displays current information about the Storage Class.
// +kubebuilder:object:generate=true
type ReplicatedStorageClassStatus struct {
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

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
	Configuration *ReplicatedVolumeConfiguration `json:"configuration,omitempty"`
	// StoragePoolEligibleNodesRevision tracks RSP's eligibleNodesRevision for change detection.
	// +optional
	StoragePoolEligibleNodesRevision int64 `json:"storagePoolEligibleNodesRevision,omitempty"`
	// StoragePoolBasedOnGeneration is the RSC generation when storagePoolName was computed.
	// +optional
	StoragePoolBasedOnGeneration int64 `json:"storagePoolBasedOnGeneration,omitempty"`
	// StoragePoolName is the computed name of the ReplicatedStoragePool for this RSC.
	// Format: auto-rsp-<fnv128-hex>. Multiple RSCs with identical storage parameters
	// will share the same StoragePoolName.
	// +optional
	StoragePoolName string `json:"storagePoolName,omitempty"`
	// Volumes provides aggregated volume statistics.
	// Always present (may have total=0).
	// +kubebuilder:default={}
	Volumes ReplicatedStorageClassVolumesSummary `json:"volumes"`
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
	// UsedStoragePoolNames is a sorted list of storage pool names currently used by volumes.
	// +listType=atomic
	// +optional
	UsedStoragePoolNames []string `json:"usedStoragePoolNames,omitempty"`
}
