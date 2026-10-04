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

// ReplicatedStorageClass is the interface for managing StorageClasses of the replicated.csi.storage.deckhouse.io
// CSI driver. Creating a StorageClass for this driver manually is prohibited.
// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=rsc
// +kubebuilder:metadata:labels=heritage=deckhouse
// +kubebuilder:metadata:labels=module=sds-replicated-volume
// +kubebuilder:metadata:labels=backup.deckhouse.io/cluster-config=true
// +kubebuilder:selectablefield:JSONPath=.status.phase
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="FTT",type=integer,JSONPath=`.status.configuration.failuresToTolerate`
// +kubebuilder:printcolumn:name="GMDR",type=integer,JSONPath=`.status.configuration.guaranteedMinimumDataRedundancy`
// +kubebuilder:printcolumn:name="Volumes",type=integer,JSONPath=`.status.volumes.total`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:printcolumn:name="Topology",type=string,priority=1,JSONPath=`.spec.topology`
// +kubebuilder:printcolumn:name="VolumeAccess",type=string,priority=1,JSONPath=`.spec.volumeAccess`
// +kubebuilder:printcolumn:name="StoragePool",type=string,priority=1,JSONPath=`.status.storagePoolName`
// +kubebuilder:printcolumn:name="Message",type=string,priority=1,JSONPath=`.status.message`
type ReplicatedStorageClass struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// The reference text of spec lives here, not on ReplicatedStorageClassSpec:
	// the comment on that type is interleaved with the CEL markers and their
	// rationale, and controller-gen renders all of its prose.

	// Defines a Kubernetes Storage class configuration.
	//
	// > In the LINSTOR control-plane the spec cannot be changed after creation: changes to
	// > most fields are rejected, and changes to replication or reclaimPolicy switch the
	// > resource to the InvalidConfiguration phase and leave its StorageClass unchanged.
	// > In the new control-plane most fields are immutable after creation as well, but the
	// > replication settings (replication, failuresToTolerate, guaranteedMinimumDataRedundancy),
	// > configurationRolloutStrategy, eligibleNodesConflictResolutionStrategy and
	// > reclaimPolicy (the StorageClass is recreated with the new policy) can be changed on an
	// > existing resource.
	Spec   ReplicatedStorageClassSpec   `json:"spec"`
	Status ReplicatedStorageClassStatus `json:"status,omitempty"`
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

// GetStatusPhase returns .status.phase as a string.
func (rsc *ReplicatedStorageClass) GetStatusPhase() string { return string(rsc.Status.Phase) }

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
// Immutable spec fields (transition rules; evaluated on update only):
// the conservative r3->r2 migration matrix permits changing only replication,
// failuresToTolerate, guaranteedMinimumDataRedundancy, configurationRolloutStrategy and
// eligibleNodesConflictResolutionStrategy; every other spec field is immutable after
// creation. These CEL rules guard the scalar/enum and bounded-list fields. The unbounded
// structured fields (storage, nodeLabelSelector) are guarded by the update webhook
// (images/webhooks/handlers/rscValidator.go) instead of CEL, because a CEL equality
// comparison over their unbounded list/map would risk the per-expression CEL cost budget
// and cause the apiserver to reject the whole CRD at install time.
//
// topology, volumeAccess and zones are strictly immutable (never touched by the
// controller). reclaimPolicy is deliberately MUTABLE: changing it is a supported operation —
// rsc_controller reacts by recreating the Kubernetes StorageClass with the new policy (a
// StorageClass itself is immutable, so recreate is the mechanism; covered by the SC
// lifecycle e2e). systemNetworkNames and eligibleNodesPolicy are immutable *once set*: the
// controller fills them from nil to their default (rsc_controller applySpecDefaults), so the
// rules must permit that initial nil->value transition while still rejecting later changes.
// +kubebuilder:validation:XValidation:rule="self.topology == oldSelf.topology",message="spec.topology is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.volumeAccess) == has(oldSelf.volumeAccess) && (!has(self.volumeAccess) || self.volumeAccess == oldSelf.volumeAccess)",message="spec.volumeAccess is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.zones) == has(oldSelf.zones) && (!has(self.zones) || self.zones == oldSelf.zones)",message="spec.zones is immutable"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.systemNetworkNames) || (has(self.systemNetworkNames) && self.systemNetworkNames == oldSelf.systemNetworkNames)",message="spec.systemNetworkNames is immutable once set"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.eligibleNodesPolicy) || (has(self.eligibleNodesPolicy) && self.eligibleNodesPolicy == oldSelf.eligibleNodesPolicy)",message="spec.eligibleNodesPolicy is immutable once set"
// +kubebuilder:object:generate=true
type ReplicatedStorageClassSpec struct {
	// Deprecated: in the new control-plane, use storage instead.
	//
	// The name of the ReplicatedStoragePool resource, which is the Storage Pool in the LINSTOR
	// backend the volumes are created in. Required in the LINSTOR control-plane.
	//
	// > The field cannot be added or changed after creation; removing it is possible only in
	// > the new control-plane.
	// +kubebuilder:validation:XValidation:rule="size(self) == 0 || self == oldSelf",message="StoragePool cannot be added or changed, only removed"
	// +optional
	StoragePool string `json:"storagePool,omitempty"`
	// Only meaningful with the new control-plane.
	//
	// Storage defines the storage backend configuration for this storage class.
	// Specifies the type of volumes (LVM or LVMThin) and which LVMVolumeGroups
	// will be used to allocate space for volumes.
	// +optional
	Storage *ReplicatedStorageClassStorage `json:"storage,omitempty"`
	// The reclaim policy of the StorageClass. Might be:
	// - Delete (if the PersistentVolumeClaim is deleted, the PersistentVolume is deleted with it)
	// - Retain (if the PersistentVolumeClaim is deleted, the PersistentVolume stays intact in the Released phase)
	//
	// > Changing the field is not supported in the LINSTOR control-plane: the resource switches
	// > to the InvalidConfiguration phase. In the new control-plane the StorageClass is recreated
	// > with the new policy.
	// +kubebuilder:validation:Enum=Delete;Retain
	ReclaimPolicy ReplicatedStorageClassReclaimPolicy `json:"reclaimPolicy"`
	// Deprecated: in the new control-plane, use failuresToTolerate and
	// guaranteedMinimumDataRedundancy instead; the field is mutually exclusive with them.
	//
	// The Storage class's replication mode. Might be:
	// - None — replication is disabled; the data exists in a single copy.
	// - Availability — the volume stays readable and writable even if one of its replicas becomes unavailable. The data is stored in two copies on different nodes. **Important:** this mode does not guarantee data consistency and may lead to split brain and data loss if the network connectivity between nodes is disrupted. Recommended only for non-critical data and applications that do not require high reliability and data integrity.
	// - Consistency — 2 replicas with consistency guarantees; requires both replicas for IO. Supported only in the new control-plane.
	// - ConsistencyAndAvailability — the volume stays readable and writable even if one of its replicas becomes unavailable. The data is stored in three copies on different nodes. This mode protects against data loss when two nodes holding volume replicas fail and guarantees data consistency. However, if two replicas are lost, the volume switches to suspend-io mode.
	//
	// Mapping to failuresToTolerate/guaranteedMinimumDataRedundancy (in the new control-plane):
	//   None                       → failuresToTolerate=0, guaranteedMinimumDataRedundancy=0
	//   Availability               → failuresToTolerate=1, guaranteedMinimumDataRedundancy=0
	//   Consistency                → failuresToTolerate=0, guaranteedMinimumDataRedundancy=1
	//   ConsistencyAndAvailability → failuresToTolerate=1, guaranteedMinimumDataRedundancy=1
	//
	// > Both in the new control-plane and in the LINSTOR control-plane an unset value is treated
	// > as 'ConsistencyAndAvailability'; setting the field explicitly is recommended.
	// +kubebuilder:validation:Enum=None;Availability;Consistency;ConsistencyAndAvailability
	Replication ReplicatedStorageClassReplication `json:"replication,omitempty"`
	// Only meaningful with the new control-plane.
	//
	// FailuresToTolerate (FTT) specifies how many arbitrary node failures the volume
	// can tolerate while remaining available for IO.
	// Mutually exclusive with Replication.
	// Must be specified together with GuaranteedMinimumDataRedundancy.
	// Valid range: 0-2. Valid combinations: |FTT - GMDR| <= 1.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=2
	// +optional
	FailuresToTolerate *byte `json:"failuresToTolerate,omitempty"`
	// Only meaningful with the new control-plane.
	//
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
	// The volume access mode. Might be:
	// - Local — pods cannot be created on nodes that have no local replica of the volume used by the pod.
	// - EventuallyLocal — the volume is created right after the pod appears and is assigned a node. The pod's topology settings are taken into account on initial creation, and a local replica is created on the pod's node where possible. If the pod is created on a node without a local replica, it is attached to the existing replicas over the network, and after 30 minutes a local replica starts being created on that node, which copies all volume data over the network from the other replicas.
	// - PreferablyLocal — the same as EventuallyLocal, except that a pod created on a node without a local replica stays attached to the replicas over the network permanently (no local replica is created on that node).
	// - Any — the volume is created before the pod appears and can be placed on any available node regardless of the topology settings of the pod that will use it. A pod created on a node without a local replica stays attached to the replicas over the network permanently.
	//
	// > Note that the default Volume Access mode is 'PreferablyLocal'.
	//
	// > With EventuallyLocal, PreferablyLocal and Any, access to the data over the network is allowed only within one zone: a pod cannot be created in a zone that has no replica of the volume.
	//
	// > The field is immutable after creation.
	// +kubebuilder:validation:Enum=Local;EventuallyLocal;PreferablyLocal;Any
	// +kubebuilder:default:=PreferablyLocal
	VolumeAccess ReplicatedStorageClassVolumeAccess `json:"volumeAccess,omitempty"`
	// The topology settings for the volumes in the created Storage class. Might be:
	// - TransZonal — replicas of the volumes will be created in different zones (one replica per zone).
	//   To use this topology, the available zones must be specified in the 'zones' param, and the cluster nodes must have the topology.kubernetes.io/zone=<zone name> label.
	// - Zonal — all replicas of the volumes are created in the same zone that the scheduler selected to place the pod using this volume.
	//   Recommended when the latency between zones is high and/or the bandwidth is low.
	// - Ignored — the topology information will not be used to place replicas of the volumes.
	//   The replicas can be placed on any available nodes, with the restriction: no more than one replica of a given volume on one node.
	//   Required when replication is 'None'.
	//
	// > Note that the 'Ignored' value can be used only if there are no zones in the cluster (there are no nodes with the topology.kubernetes.io/zone label).
	// > In the LINSTOR control-plane 'Zonal' and 'TransZonal' can be used only if there are zones in the cluster.
	//
	// > For the system to operate correctly, either every cluster node must be labeled with 'topology.kubernetes.io/zone', or none of them should have this label.
	//
	// > The field is immutable after creation.
	// +kubebuilder:validation:Enum=TransZonal;Zonal;Ignored
	Topology ReplicatedStorageClassTopology `json:"topology"`
	// The list of zones the StorageClass's volumes are replicated in. The controller puts a label
	// with the StorageClass's name on the nodes where replicas of the volumes can be created.
	//
	// > Required with the TransZonal topology; with 'Availability' and 'ConsistencyAndAvailability'
	// > replication exactly 3 zones must be specified.
	// > In the LINSTOR control-plane the field can be set only with the TransZonal topology.
	// > In the new control-plane, 'Consistency' replication requires at least 2 zones, and with
	// > 'None' replication the zones limit where the single replica can be placed.
	// > The field is immutable after creation.
	// +kubebuilder:validation:MaxItems=10
	// +kubebuilder:validation:items:MaxLength=63
	// +listType=set
	// +optional
	Zones []string `json:"zones,omitempty"`
	// Only meaningful with the new control-plane.
	//
	// NodeLabelSelector filters nodes eligible for DRBD participation.
	// Only nodes matching this selector can store data, provide access, or host tiebreaker.
	// If not specified, all nodes are candidates.
	// +optional
	NodeLabelSelector *metav1.LabelSelector `json:"nodeLabelSelector,omitempty"`
	// The four optional fields below (systemNetworkNames, configurationRolloutStrategy,
	// eligibleNodesConflictResolutionStrategy, eligibleNodesPolicy) are defaulted by the controller
	// (rsc_controller applySpecDefaults), NOT by +kubebuilder:default. A schema default is applied
	// before validating admission, and the legacy control-plane webhook (validateLegacySpecFields)
	// rejects an RSC that has any of these set — so a schema default would break RSC on every legacy
	// installation. Revisit once the legacy control plane is removed.
	//
	// Kept detached from the doc comment below by a blank line on purpose: a
	// comment touching the field is its doc comment, which controller-gen renders
	// into the CRD description and the documentation site. This note is for
	// whoever edits these fields, not for a user reading the API reference.

	// TODO(systemnetwork): Currently only "Internal" (default node network) is supported.
	// Custom network support requires NetworkNode watch implementation in the controller.
	// When multi-network support is implemented, raise MaxItems to 10.

	// Only meaningful with the new control-plane.
	//
	// SystemNetworkNames specifies network names used for DRBD replication traffic.
	// At least one network name must be specified. Each name is limited to 64 characters.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=1
	// +kubebuilder:validation:Items={type=string,maxLength=64}
	// +kubebuilder:validation:XValidation:rule="self.all(n, n == 'Internal')",message="Only 'Internal' network is currently supported"
	// +listType=set
	// +optional
	SystemNetworkNames []string `json:"systemNetworkNames,omitempty"`
	// Only meaningful with the new control-plane.
	//
	// ConfigurationRolloutStrategy defines how configuration changes are applied to existing volumes.
	// When not specified, the controller fills the default (RollingUpdate, maxParallel=5).
	// +optional
	ConfigurationRolloutStrategy *ReplicatedStorageClassConfigurationRolloutStrategy `json:"configurationRolloutStrategy,omitempty"`
	// Only meaningful with the new control-plane.
	//
	// EligibleNodesConflictResolutionStrategy defines how the controller handles volumes with eligible nodes conflicts.
	// When not specified, the controller fills the default (RollingRepair, maxParallel=5).
	// +optional
	EligibleNodesConflictResolutionStrategy *ReplicatedStorageClassEligibleNodesConflictResolutionStrategy `json:"eligibleNodesConflictResolutionStrategy,omitempty"`
	// Only meaningful with the new control-plane.
	//
	// EligibleNodesPolicy defines policies for managing eligible nodes.
	// When not specified, the controller fills the default (notReadyGracePeriod=10m).
	// +optional
	EligibleNodesPolicy *ReplicatedStoragePoolEligibleNodesPolicy `json:"eligibleNodesPolicy,omitempty"`
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

	// TODO(thinpool-object): When ThinPool becomes a separate API object and LVMVolumeGroups
	// is replaced with selectors, revisit the key. Currently using name only because
	// thinPoolName is optional and cannot be a listMapKey without a default value.

	// LVMVolumeGroups is an array of LVMVolumeGroup resource names whose Volume Groups/Thin-pools
	// will be used to allocate the required space.
	//
	// > Note that every LVMVolumeGroup resource must have the same type (Thin/Thick)
	// as specified in the Type field.
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
	Type ReplicatedStorageClassConfigurationRolloutStrategyType `json:"type"`
	// RollingUpdate configures parameters for RollingUpdate strategy.
	// Required when type is RollingUpdate.
	// +optional
	RollingUpdate *ReplicatedStorageClassConfigurationRollingUpdateStrategy `json:"rollingUpdate,omitempty"`
}

// GetType returns the effective configuration rollout strategy type.
//
// The field is optional: the controller fills the default on the first reconcile, so a nil
// strategy only means "not defaulted yet". Consumers MUST treat that state as RollingUpdate,
// which is the default this controller writes; assuming NewVolumesOnly instead would freeze
// configuration rollout during the window before defaults are persisted.
func (s *ReplicatedStorageClassConfigurationRolloutStrategy) GetType() ReplicatedStorageClassConfigurationRolloutStrategyType {
	if s == nil {
		return ConfigurationRolloutRollingUpdate
	}
	return s.Type
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
	Type ReplicatedStorageClassEligibleNodesConflictResolutionStrategyType `json:"type"`
	// RollingRepair configures parameters for RollingRepair conflict resolution strategy.
	// Required when type is RollingRepair.
	// +optional
	RollingRepair *ReplicatedStorageClassEligibleNodesConflictResolutionRollingRepair `json:"rollingRepair,omitempty"`
}

// GetType returns the effective eligible nodes conflict resolution strategy type.
//
// The field is optional: the controller fills the default on the first reconcile, so a nil
// strategy only means "not defaulted yet". Consumers MUST treat that state as RollingRepair,
// which is the default this controller writes; assuming Manual instead would report conflict
// resolution as disabled during the window before defaults are persisted.
func (s *ReplicatedStorageClassEligibleNodesConflictResolutionStrategy) GetType() ReplicatedStorageClassEligibleNodesConflictResolutionStrategyType {
	if s == nil {
		return EligibleNodesConflictResolutionRollingRepair
	}
	return s.Type
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

	// Phase is the operational state summary of the storage class.
	// +kubebuilder:validation:Enum=Ready;WaitingForStoragePool;InsufficientNodes;InvalidConfiguration;RollingOut;PartiallyAligned;Terminating
	// +optional
	Phase ReplicatedStorageClassPhase `json:"phase,omitempty"`
	// Message is a human-readable description of the current phase.
	// +optional
	Message string `json:"message,omitempty"`
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

// ReplicatedStorageClassPhase represents the operational state of the ReplicatedStorageClass.
type ReplicatedStorageClassPhase string

// ReplicatedStorageClass phase values.
const (
	// ReplicatedStorageClassPhaseReady means configuration is accepted and all volumes are aligned.
	ReplicatedStorageClassPhaseReady ReplicatedStorageClassPhase = "Ready"
	// ReplicatedStorageClassPhaseWaitingForStoragePool means the referenced storage pool is not ready.
	ReplicatedStorageClassPhaseWaitingForStoragePool ReplicatedStorageClassPhase = "WaitingForStoragePool"
	// ReplicatedStorageClassPhaseInsufficientNodes means eligible nodes do not meet FTT/GMDR/topology requirements.
	ReplicatedStorageClassPhaseInsufficientNodes ReplicatedStorageClassPhase = "InsufficientNodes"
	// ReplicatedStorageClassPhaseInvalidConfiguration means spec validation failed.
	ReplicatedStorageClassPhaseInvalidConfiguration ReplicatedStorageClassPhase = "InvalidConfiguration"
	// ReplicatedStorageClassPhaseRollingOut means configuration is accepted and active rollout/repair is in progress.
	ReplicatedStorageClassPhaseRollingOut ReplicatedStorageClassPhase = "RollingOut"
	// ReplicatedStorageClassPhasePartiallyAligned means divergence exists but all auto-fixes are disabled.
	ReplicatedStorageClassPhasePartiallyAligned ReplicatedStorageClassPhase = "PartiallyAligned"
	// ReplicatedStorageClassPhaseTerminating means the storage class is terminating.
	ReplicatedStorageClassPhaseTerminating ReplicatedStorageClassPhase = "Terminating"
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
