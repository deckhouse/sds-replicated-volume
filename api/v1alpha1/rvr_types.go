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
	"math/bits"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// ReplicatedVolumeReplica is a Kubernetes Custom Resource that represents a replica of a ReplicatedVolume.
// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=rvr
// +kubebuilder:metadata:labels=module=sds-replicated-volume
// +kubebuilder:selectablefield:JSONPath=.spec.nodeName
// +kubebuilder:selectablefield:JSONPath=.spec.replicatedVolumeName
// +kubebuilder:printcolumn:name="Node",type=string,JSONPath=".spec.nodeName"
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Configured",type=string,JSONPath=".status.conditions[?(@.type=='Configured')].status"
// +kubebuilder:printcolumn:name="DRBDConfigured",type=string,JSONPath=".status.conditions[?(@.type=='DRBDConfigured')].status"
// +kubebuilder:printcolumn:name="DataUpToDate",type=string,JSONPath=".status.conditions[?(@.type=='BackingVolumeUpToDate')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Scheduled",type=string,priority=1,JSONPath=".status.conditions[?(@.type=='Scheduled')].status"
// +kubebuilder:printcolumn:name="Attached",type=string,priority=1,JSONPath=".status.conditions[?(@.type=='Attached')].status"
// +kubebuilder:printcolumn:name="FullyConnected",type=string,priority=1,JSONPath=".status.conditions[?(@.type=='FullyConnected')].status"
// +kubebuilder:printcolumn:name="BVReady",type=string,priority=1,JSONPath=".status.conditions[?(@.type=='BackingVolumeReady')].status"
// +kubebuilder:printcolumn:name="SatisfyEligibleNodes",type=string,priority=1,JSONPath=".status.conditions[?(@.type=='SatisfyEligibleNodes')].status"
// +kubebuilder:validation:XValidation:rule="self.metadata.name.startsWith(self.spec.replicatedVolumeName + '-')",message="metadata.name must start with spec.replicatedVolumeName + '-'"
// +kubebuilder:validation:XValidation:rule="self.metadata.name.substring(size(self.spec.replicatedVolumeName) + 1).matches('^[0-9][0-9]?$') && int(self.metadata.name.substring(size(self.spec.replicatedVolumeName) + 1)) <= 31",message="metadata.name must be exactly spec.replicatedVolumeName + '-' + id where id is 0..31"
// +kubebuilder:validation:XValidation:rule="size(self.metadata.name) <= 123",message="metadata.name must be at most 123 characters (to fit derived LLV name with prefix)"
type ReplicatedVolumeReplica struct {
	metav1.TypeMeta `json:",inline"`

	metav1.ObjectMeta `json:"metadata"`

	Spec ReplicatedVolumeReplicaSpec `json:"spec"`

	Status ReplicatedVolumeReplicaStatus `json:"status,omitempty"`
}

// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
type ReplicatedVolumeReplicaList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []ReplicatedVolumeReplica `json:"items"`
}

// GetStatusConditions is an adapter method to satisfy objutilv1.StatusConditionObject.
// It returns the root object's `.status.conditions`.
func (rvr *ReplicatedVolumeReplica) GetStatusConditions() []metav1.Condition {
	return rvr.Status.Conditions
}

// SetStatusConditions is an adapter method to satisfy objutilv1.StatusConditionObject.
// It sets the root object's `.status.conditions`.
func (rvr *ReplicatedVolumeReplica) SetStatusConditions(conditions []metav1.Condition) {
	rvr.Status.Conditions = conditions
}

// ID extracts ID from the RVR name (e.g., "pvc-xxx-5" → 5).
// Result is cached after first successful call
func (rvr *ReplicatedVolumeReplica) ID() uint8 {
	return idFromName(rvr.Name)
}

// idSuffixes is a lookup table for ID (0-31) to "-N" suffix conversion.
var idSuffixes = [32]string{
	"-0", "-1", "-2", "-3", "-4", "-5", "-6", "-7",
	"-8", "-9", "-10", "-11", "-12", "-13", "-14", "-15",
	"-16", "-17", "-18", "-19", "-20", "-21", "-22", "-23",
	"-24", "-25", "-26", "-27", "-28", "-29", "-30", "-31",
}

// FormatReplicatedVolumeReplicaName returns the RVR name for the given RV name and ID.
// Panics if id > 31.
func FormatReplicatedVolumeReplicaName(rvName string, id uint8) string {
	if id > 31 {
		panic("id must be in range 0-31")
	}
	return rvName + idSuffixes[id]
}

// SetNameWithID sets the RVR name using the ReplicatedVolumeName and the given ID.
func (rvr *ReplicatedVolumeReplica) SetNameWithID(id uint8) {
	rvr.Name = FormatReplicatedVolumeReplicaName(rvr.Spec.ReplicatedVolumeName, id)
}

// ChooseNewName selects the first available ID and sets the RVR name.
// Returns false if all IDs (0-31) are already taken by other RVRs.
func (rvr *ReplicatedVolumeReplica) ChooseNewName(otherRVRs []*ReplicatedVolumeReplica) bool {
	// Bitmask for reserved IDs (0-31 fit in uint32).
	var reserved uint32

	for _, otherRVR := range otherRVRs {
		if otherRVR.Spec.ReplicatedVolumeName != rvr.Spec.ReplicatedVolumeName {
			continue
		}
		reserved |= 1 << otherRVR.ID()
	}

	free := ^reserved
	if free == 0 {
		return false
	}
	rvr.SetNameWithID(uint8(bits.TrailingZeros32(free)))
	return true
}

// +kubebuilder:object:generate=true
// +kubebuilder:validation:XValidation:rule="self.type != 'Access' || has(self.nodeName)",message="nodeName is required for Access type"
// +kubebuilder:validation:XValidation:rule="!has(self.lvmVolumeGroupName) || has(self.nodeName)",message="lvmVolumeGroupName requires nodeName to be set"
// +kubebuilder:validation:XValidation:rule="!has(self.lvmVolumeGroupName) || self.type == 'Diskful'",message="lvmVolumeGroupName can only be set for Diskful type"
// +kubebuilder:validation:XValidation:rule="!has(self.lvmVolumeGroupThinPoolName) || has(self.lvmVolumeGroupName)",message="lvmVolumeGroupThinPoolName requires lvmVolumeGroupName to be set"
type ReplicatedVolumeReplicaSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=120
	// +kubebuilder:validation:Pattern=`^[0-9A-Za-z.+_-]*$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="replicatedVolumeName is immutable"
	ReplicatedVolumeName string `json:"replicatedVolumeName"`

	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="size(oldSelf) == 0 || self == oldSelf",message="nodeName is immutable once set"
	NodeName string `json:"nodeName,omitempty"`

	// LVMVolumeGroupName is the LVMVolumeGroup resource name where this replica should be placed.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-.]{0,251}[a-z0-9])?$`
	LVMVolumeGroupName string `json:"lvmVolumeGroupName,omitempty"`

	// LVMVolumeGroupThinPoolName is the thin pool name (for LVMThin storage pools).
	// +optional
	LVMVolumeGroupThinPoolName string `json:"lvmVolumeGroupThinPoolName,omitempty"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Diskful;ShadowDiskful;Access;TieBreaker
	Type ReplicaType `json:"type"`
}

// ReplicaType enumerates possible values for ReplicatedVolumeReplica spec.type and status.effectiveType fields.
type ReplicaType string

// Replica type values for [ReplicatedVolumeReplica] spec.type field.
const (
	// ReplicaTypeDiskful represents a diskful replica that stores data on disk.
	// Diskful replicas are the primary quorum voters. They fully participate in
	// quorum when their backing volume is attached and up-to-date; otherwise
	// (disk detached or not yet up-to-date) they still count toward quorum
	// reachability but do not contribute to the data redundancy guarantee.
	ReplicaTypeDiskful ReplicaType = "Diskful"

	// ReplicaTypeTieBreaker represents a diskless replica that can provide a tie-breaking
	// vote to maintain quorum. TieBreakers do not participate in quorum under normal
	// conditions. They contribute a vote only when the total number of diskful nodes
	// participating in quorum is even and exactly one vote is needed to maintain (not
	// obtain) quorum; in that case, the majority of tiebreakers can provide the required
	// vote. TieBreakers can also be used for volume access if the StorageClass permits
	// it via volumeAccess settings.
	ReplicaTypeTieBreaker ReplicaType = "TieBreaker"

	// ReplicaTypeShadowDiskful represents a diskful replica that stores and
	// replicates data but is invisible to quorum. Typical uses include
	// pre-synchronizing data on a new node before promoting it to a full
	// Diskful voter (so the promotion requires only a fast configuration
	// change rather than a lengthy data resync), providing a local data
	// copy for read-heavy workloads, and maintaining a backup-ready
	// replica without affecting quorum or availability guarantees.
	ReplicaTypeShadowDiskful ReplicaType = "ShadowDiskful"

	// ReplicaTypeAccess represents a diskless replica used solely for volume attachment.
	// Access replicas do not store data and do not participate in quorum in any way.
	// Their only purpose is to allow attaching the volume on additional nodes when
	// the StorageClass permits it via volumeAccess settings.
	ReplicaTypeAccess ReplicaType = "Access"
)

func (t ReplicaType) String() string {
	return string(t)
}

// +kubebuilder:object:generate=true
type ReplicatedVolumeReplicaStatus struct {
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +kubebuilder:validation:MaxItems=10
	// +listType=atomic
	// +optional
	Addresses []DRBDResourceAddressStatus `json:"addresses,omitempty"`

	// Type reflects the currently observed DRBD configuration type
	// from drbdr.Status.ActiveConfiguration.Type.
	//
	// This is the observed DRBD type (Diskful or Diskless), not the intended spec.type
	// which can be Diskful/Diskless/Access/TieBreaker.
	//
	// Note: Access and TieBreaker both appear as Diskless here because they cannot be
	// distinguished from the replica's own status. The difference is only visible
	// in how other replicas treat this replica's vote in quorum calculation.
	// +kubebuilder:validation:Enum=Diskful;Diskless
	// +optional
	Type DRBDResourceType `json:"type,omitempty"`

	// BackingVolume contains information about the backing LVM logical volume.
	// Only set for Diskful replicas.
	// +optional
	BackingVolume *ReplicatedVolumeReplicaStatusBackingVolume `json:"backingVolume,omitempty"`

	// DatameshPending describes the pending datamesh membership state for this replica.
	//
	// This field contains changes that have been validated and are ready from the RVR's
	// perspective, waiting to be applied to the datamesh. The controller sets this field
	// only after all prerequisites are satisfied.
	//
	// For example, member=true will not be set until scheduling completes: the spec.nodeName,
	// spec.lvmVolumeGroupName, and spec.lvmVolumeGroupThinPoolName fields must be populated
	// and validated before the replica can be marked as a pending datamesh member.
	//
	// +optional
	DatameshPendingTransition *ReplicatedVolumeReplicaStatusDatameshPendingTransition `json:"datameshPendingTransition,omitempty"`

	// DatameshRevision is the datamesh revision for which the replica was fully configured.
	//
	// "Configured" means:
	//   - DRBD was configured to match the intended state derived from this datamesh revision.
	//   - Backing volume (if Diskful) was configured: exists and matches intended LVG/ThinPool/Size.
	//   - Backing volume (if Diskful) is ready: reported ready and actual size >= intended size.
	//   - Agent confirmed successful configuration.
	//
	// Note: "configured" does NOT mean:
	//   - DRBD connections are established (happens asynchronously after configuration).
	//   - Backing volume is synchronized (resync happens asynchronously if the volume was newly added).
	//
	// +optional
	DatameshRevision int64 `json:"datameshRevision,omitempty"`

	// Attachment contains information about the device attachment state.
	// Only set when the replica is attached on the node.
	// +optional
	Attachment *ReplicatedVolumeReplicaStatusAttachment `json:"attachment,omitempty"`

	// Quorum indicates whether this replica has quorum.
	// +optional
	Quorum *bool `json:"quorum,omitempty"`

	// QuorumSummary provides detailed quorum information.
	// +optional
	QuorumSummary *ReplicatedVolumeReplicaStatusQuorumSummary `json:"quorumSummary,omitempty"`

	// Peers contains the status of connections to peer replicas.
	// +kubebuilder:validation:XValidation:rule="self.all(x, self.exists_one(y, x.name == y.name))",message="peers[].name must be unique"
	// +kubebuilder:validation:MaxItems=31
	// +listType=atomic
	// +optional
	Peers []ReplicatedVolumeReplicaStatusPeerStatus `json:"peers,omitempty"`

	// DRBDRReconciliationCache holds cached values for DRBDResource reconciliation optimization.
	// +optional
	DRBDRReconciliationCache ReplicatedVolumeReplicaStatusDRBDRReconciliationCache `json:"drbdrReconciliationCache,omitempty"`
}

// ReplicatedVolumeReplicaStatusAttachment contains information about the device attachment state.
// +kubebuilder:object:generate=true
type ReplicatedVolumeReplicaStatusAttachment struct {
	// DevicePath is the block device path when the replica is attached.
	// Example: /dev/drbd10012.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	DevicePath string `json:"devicePath"`

	// IOSuspended indicates whether I/O is suspended on the device.
	IOSuspended bool `json:"ioSuspended"`

	// InUse indicates whether the replica's block device is currently in use by a process.
	InUse bool `json:"inUse"`
}

// ReplicatedVolumeReplicaStatusBackingVolume contains information about the backing LVM logical volume.
// +kubebuilder:object:generate=true
type ReplicatedVolumeReplicaStatusBackingVolume struct {
	// Size is the size of the backing LVM logical volume.
	// +optional
	Size *resource.Quantity `json:"size,omitempty"`

	// State is the local backing volume state.
	// +optional
	State DiskState `json:"state,omitempty"`

	// LVMVolumeGroupName is the name of the LVM volume group.
	// +kubebuilder:validation:MaxLength=253
	// +optional
	LVMVolumeGroupName string `json:"lvmVolumeGroupName,omitempty"`

	// LVMVolumeGroupThinPoolName is the name of the thin pool within the LVM volume group.
	// Empty if the backing volume is not on a thin pool.
	// +kubebuilder:validation:MaxLength=253
	// +optional
	LVMVolumeGroupThinPoolName string `json:"lvmVolumeGroupThinPoolName,omitempty"`
}

// ReplicatedVolumeReplicaStatusQuorumSummary provides detailed quorum information for a replica.
// +kubebuilder:object:generate=true
type ReplicatedVolumeReplicaStatusQuorumSummary struct {
	// ConnectedDiskfulPeers is the number of Diskful peers with established connection.
	// +kubebuilder:default=0
	ConnectedDiskfulPeers int `json:"connectedDiskfulPeers"`

	// ConnectedTieBreakerPeers is the number of TieBreaker peers with established connection.
	// TieBreakers only contribute to quorum under specific edge conditions.
	// +kubebuilder:default=0
	ConnectedTieBreakerPeers int `json:"connectedTieBreakerPeers"`

	// ConnectedUpToDatePeers is the number of peers with established connection and UpToDate disk.
	// +kubebuilder:default=0
	ConnectedUpToDatePeers int `json:"connectedUpToDatePeers"`

	// Quorum is the required quorum threshold.
	// +optional
	Quorum *int `json:"quorum,omitempty"`

	// QuorumMinimumRedundancy is the number of diskful UpToDate peers (including self) required for quorum.
	// +optional
	QuorumMinimumRedundancy *int `json:"quorumMinimumRedundancy,omitempty"`
}

// ReplicatedVolumeReplicaStatusPeerStatus represents the status of a connection to a peer replica.
// +kubebuilder:object:generate=true
type ReplicatedVolumeReplicaStatusPeerStatus struct {
	// Name is the name of the peer ReplicatedVolumeReplica.
	// Must have format "prefix-N" where N is 0-31.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^.+-([0-9]|[12][0-9]|3[01])$`
	Name string `json:"name"`

	// Type is the replica type (Diskful/TieBreaker/Access).
	// +kubebuilder:validation:Enum=Diskful;Access;TieBreaker
	// +optional
	Type ReplicaType `json:"type,omitempty"`

	// Attached indicates whether this peer is attached on its node (and has a block device).
	// +optional
	Attached bool `json:"attached,omitempty"`

	// ConnectionEstablishedOn lists system network names where connection to this peer is established.
	// +kubebuilder:validation:XValidation:rule="self.all(x, self.exists_one(y, x == y))",message="connectionEstablishedOn must be unique"
	// +kubebuilder:validation:MaxItems=10
	// +kubebuilder:validation:items:MaxLength=64
	// +listType=atomic
	// +optional
	ConnectionEstablishedOn []string `json:"connectionEstablishedOn,omitempty"`

	// ConnectionState is the DRBD connection state to this peer.
	// +optional
	ConnectionState ConnectionState `json:"connectionState,omitempty"`

	// BackingVolumeState is the peer's backing volume state.
	// +optional
	BackingVolumeState DiskState `json:"backingVolumeState,omitempty"`

	// ReplicationState is the DRBD replication state with this peer.
	// +optional
	ReplicationState ReplicationState `json:"replicationState,omitempty"`
}

// ID extracts ID from the peer name (e.g., "pvc-xxx-5" → 5).
func (p ReplicatedVolumeReplicaStatusPeerStatus) ID() uint8 {
	return idFromName(p.Name)
}

// ReplicatedVolumeReplicaStatusDRBDRReconciliationCache holds cached values used to optimize DRBDResource reconciliation.
// ReplicatedVolumeReplicaStatusDRBDRReconciliationCache is a composite cache key
// used to skip redundant DRBDResource spec recomputations. The fields together
// capture the inputs that determine the DRBDR spec:
//   - DatameshRevision — peer topology (membership, connectivity);
//   - DRBDRGeneration  — external modifications to DRBDR;
//   - TargetType       — local resource configuration (disk mode, spec.type changes).
//
// When any field changes, the controller recomputes and potentially patches the
// DRBDR spec. Note: the cache is populated even before the replica becomes a
// datamesh member; in that case the DRBDR is only partially configured (no peer
// connections), and DatameshRevision / TargetType reflect the partial inputs.
// +kubebuilder:object:generate=true
type ReplicatedVolumeReplicaStatusDRBDRReconciliationCache struct {
	// DatameshRevision is the datamesh revision for which DRBDResource spec was last computed.
	DatameshRevision int64 `json:"datameshRevision,omitempty"`

	// DRBDRGeneration is the DRBDResource generation at the time DRBDResource spec was last computed.
	DRBDRGeneration int64 `json:"drbdrGeneration,omitempty"`

	// TargetType is the target type for which DRBDResource spec was last computed.
	// +kubebuilder:validation:Enum=Diskful;LiminalDiskful;ShadowDiskful;LiminalShadowDiskful;TieBreaker;Access
	TargetType DatameshMemberType `json:"targetType,omitempty"`
}

// ReplicatedVolumeReplicaStatusDatameshPendingTransition describes pending datamesh changes for this replica.
//
// This field contains changes that have been validated and are ready from the RVR's
// perspective, waiting to be applied to the datamesh. The controller sets this field
// only after all prerequisites are satisfied (e.g., scheduling completed, spec fields
// populated and validated).
//
// # Supported Operations
//
// 1. Datamesh Join (member=true): Add this replica to the datamesh with specified type.
//   - Type is required when joining.
//   - For Diskful type: lvmVolumeGroupName is required, thinPoolName is optional.
//   - For TieBreaker/Access types: no backing volume fields allowed.
//
// 2. Datamesh Leave (member=false): Remove this replica from the datamesh.
//   - No other fields allowed when leaving.
//
// 3. Type Change (member=nil, type set): Change the type of an existing datamesh member.
//   - For change to Diskful: lvmVolumeGroupName required, thinPoolName optional.
//   - For change to TieBreaker/Access: no backing volume fields allowed.
//
// 4. Backing Volume Change (member=nil, type=nil): Replace backing volume for existing Diskful.
//   - Used when migrating storage (e.g., between LVGs or between thick/thin).
//   - Only lvmVolumeGroupName (and optionally thinPoolName) is set, no type.
//   - Implies the replica is already Diskful and remains Diskful.
//
// # Field Combinations and Their Meanings
//
// Join as Diskful on thin pool:
//
//	member: true, type: Diskful, lvmVolumeGroupName: "vg-1", thinPoolName: "tp-1"
//
// Join as Diskful on thick LVM:
//
//	member: true, type: Diskful, lvmVolumeGroupName: "vg-1"
//
// Join as TieBreaker (diskless quorum voter):
//
//	member: true, type: TieBreaker
//
// Join as Access (diskless data accessor):
//
//	member: true, type: Access
//
// Leave datamesh:
//
//	member: false
//
// Change type to Diskful:
//
//	type: Diskful, lvmVolumeGroupName: "vg-2", thinPoolName: "tp-2"
//
// Change type to TieBreaker:
//
//	type: TieBreaker
//
// Change type to Access:
//
//	type: Access
//
// Replace backing volume for existing Diskful (migrate from thin to thick):
//
//	lvmVolumeGroupName: "vg-thick"
//
// Replace backing volume for existing Diskful (migrate from thick to thin):
//
//	lvmVolumeGroupName: "vg-thin", thinPoolName: "tp-1"
//
// Replace backing volume for existing Diskful (migrate between LVGs):
//
//	lvmVolumeGroupName: "vg-new", thinPoolName: "tp-1"
//
// No pending changes (field is nil or absent):
//
//	datameshPending: nil
//
// +kubebuilder:object:generate=true
// +kubebuilder:validation:XValidation:rule="!has(self.member) || self.member == true || (!has(self.type) && !has(self.lvmVolumeGroupName) && !has(self.thinPoolName))",message="when member is false, type/lvmVolumeGroupName/thinPoolName must not be set"
// +kubebuilder:validation:XValidation:rule="!has(self.member) || self.member == false || has(self.type)",message="when member is true, type is required"
// +kubebuilder:validation:XValidation:rule="!has(self.type) || self.type != 'Diskful' || has(self.lvmVolumeGroupName)",message="lvmVolumeGroupName is required when type is Diskful"
// +kubebuilder:validation:XValidation:rule="!has(self.type) || self.type == 'Diskful' || !has(self.lvmVolumeGroupName)",message="lvmVolumeGroupName must not be set when type is not Diskful"
// +kubebuilder:validation:XValidation:rule="!has(self.thinPoolName) || has(self.lvmVolumeGroupName)",message="thinPoolName requires lvmVolumeGroupName to be set"
type ReplicatedVolumeReplicaStatusDatameshPendingTransition struct {
	// Member indicates whether this replica should be a datamesh member.
	// +optional
	Member *bool `json:"member,omitempty"`

	// Type is the intended type when Member is true.
	// +kubebuilder:validation:Enum=Diskful;Access;TieBreaker
	// +optional
	Type ReplicaType `json:"type,omitempty"`

	// LVMVolumeGroupName is the LVMVolumeGroup resource name for Diskful replicas.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-.]{0,251}[a-z0-9])?$`
	// +optional
	LVMVolumeGroupName string `json:"lvmVolumeGroupName,omitempty"`

	// ThinPoolName is the thin pool name (optional, for LVMThin storage pools).
	// +optional
	ThinPoolName string `json:"thinPoolName,omitempty"`
}

// Equals returns true if all fields match.
func (t *ReplicatedVolumeReplicaStatusDatameshPendingTransition) Equals(other *ReplicatedVolumeReplicaStatusDatameshPendingTransition) bool {
	if t == nil || other == nil {
		return t == other
	}
	return ptr.Equal(t.Member, other.Member) &&
		t.Type == other.Type &&
		t.LVMVolumeGroupName == other.LVMVolumeGroupName &&
		t.ThinPoolName == other.ThinPoolName
}

// DiskState represents the state of a DRBD backing disk.
// It reflects the disk's synchronization status and determines whether
// application I/O can be served locally or requires peer involvement.
type DiskState string

const (
	// DiskStateDiskless indicates no local disk is attached.
	// The node operates without local storage (diskless client configuration),
	// receiving and sending data only via network from peer nodes.
	// Application I/O: only through peers.
	DiskStateDiskless DiskState = "Diskless"

	// DiskStateAttaching is a transient state when attaching a disk.
	// DRBD is reading metadata from the local device (triggered by `drbdadm attach`).
	// Application I/O: not allowed.
	DiskStateAttaching DiskState = "Attaching"

	// DiskStateDetaching is a transient state when detaching a disk.
	// DRBD is completing pending operations and preparing to transition to Diskless.
	// Application I/O: not allowed.
	DiskStateDetaching DiskState = "Detaching"

	// DiskStateFailed indicates the disk failed due to I/O errors.
	// This is a transient state before transitioning to Diskless.
	// After notifying peers about the failure, the node transitions to Diskless.
	// Application I/O: not allowed.
	DiskStateFailed DiskState = "Failed"

	// DiskStateNegotiating is a late attach state where DRBD negotiates with peers
	// to determine: who has current data, whether synchronization is needed, and
	// in which direction. After negotiation completes, transitions to one of:
	// Inconsistent, Outdated, Consistent, or UpToDate.
	// Application I/O: not allowed.
	DiskStateNegotiating DiskState = "Negotiating"

	// DiskStateInconsistent indicates that local data is not guaranteed to be
	// consistent. This state occurs when:
	//   - Freshly created disk (before initial sync)
	//   - Node is a sync target receiving data
	//   - Manual invalidation for full resync
	//   - Synchronization failed with errors on target
	//   - Local I/O error occurred
	//
	// When acting as sync target: reading synchronized blocks is done locally;
	// reading unsynchronized blocks is forwarded to a peer with UpToDate data.
	// Writing requires a peer with UpToDate data.
	// Application I/O: allowed (with peer assistance).
	DiskStateInconsistent DiskState = "Inconsistent"

	// DiskStateOutdated indicates consistent but stale data.
	// The node was disconnected from the cluster while another node performed writes.
	// Data is consistent (not corrupted) but does not contain the latest changes.
	// Resynchronization from an up-to-date node is required.
	// Application I/O: only through peers (direct I/O not allowed).
	DiskStateOutdated DiskState = "Outdated"

	// DiskStateUnknown indicates unknown disk state.
	// Used only to describe the state of a peer node when connection to it
	// is absent or lost. Never used to describe the node's own disk.
	// Application I/O: N/A (peer state only).
	DiskStateUnknown DiskState = "DUnknown"

	// DiskStateConsistent indicates consistent data with undetermined currency.
	// The node knows its data was consistent (was up-to-date) but at the moment
	// cannot determine whether it is current or outdated without peer connection.
	// Occurs when:
	//   - Starting without connected peers.
	//   - Cannot unambiguously determine who has current data.
	//
	// Upon establishing connection, transitions to UpToDate or Outdated
	// depending on negotiation results.
	// Application I/O: only through peers (direct I/O not allowed).
	DiskStateConsistent DiskState = "Consistent"

	// DiskStateUpToDate indicates fully up-to-date data.
	// This is the only state allowing full application I/O without peer involvement.
	// The disk contains the most recent consistent data and is ready for operation.
	// Application I/O: allowed locally (full read/write without peers).
	DiskStateUpToDate DiskState = "UpToDate"
)

func (s DiskState) String() string {
	return string(s)
}

func ParseDiskState(s string) DiskState {
	switch DiskState(s) {
	case DiskStateDiskless,
		DiskStateAttaching,
		DiskStateDetaching,
		DiskStateFailed,
		DiskStateNegotiating,
		DiskStateInconsistent,
		DiskStateOutdated,
		DiskStateUnknown,
		DiskStateConsistent,
		DiskStateUpToDate:
		return DiskState(s)
	default:
		return ""
	}
}

// ReplicationState represents DRBD's replication state (repl_state) for a peer connection.
// The state describes OUR role in the replication relationship with a specific peer:
//   - "Source" states mean we send data TO the peer
//   - "Target" states mean we receive data FROM the peer
type ReplicationState string

const (
	// --- Basic connection states ---

	// ReplicationStateOff indicates no active connection with the peer.
	ReplicationStateOff ReplicationState = "Off"
	// ReplicationStateEstablished indicates a normal, fully synchronized connection.
	ReplicationStateEstablished ReplicationState = "Established"

	// --- Synchronization preparation (handshake before data transfer) ---

	// ReplicationStateStartingSyncSource: initiating full resync as source (admin-triggered).
	ReplicationStateStartingSyncSource ReplicationState = "StartingSyncS"
	// ReplicationStateStartingSyncTarget: initiating full resync as target (admin-triggered).
	ReplicationStateStartingSyncTarget ReplicationState = "StartingSyncT"
	// ReplicationStateWFBitMapSource: waiting for bitmap from peer; we will be sync source.
	ReplicationStateWFBitMapSource ReplicationState = "WFBitMapS"
	// ReplicationStateWFBitMapTarget: waiting for bitmap from peer; we will be sync target.
	ReplicationStateWFBitMapTarget ReplicationState = "WFBitMapT"
	// ReplicationStateWFSyncUUID: waiting for sync UUID from peer before proceeding.
	ReplicationStateWFSyncUUID ReplicationState = "WFSyncUUID"

	// --- Active synchronization (data transfer in progress) ---

	// ReplicationStateSyncSource: we are sending data TO this peer.
	ReplicationStateSyncSource ReplicationState = "SyncSource"
	// ReplicationStateSyncTarget: we are receiving data FROM this peer.
	ReplicationStateSyncTarget ReplicationState = "SyncTarget"

	// --- Paused synchronization (suspended by user, peer, or dependency) ---

	// ReplicationStatePausedSyncSource: sync paused; we would be source when resumed.
	ReplicationStatePausedSyncSource ReplicationState = "PausedSyncS"
	// ReplicationStatePausedSyncTarget: sync paused; we would be target when resumed.
	ReplicationStatePausedSyncTarget ReplicationState = "PausedSyncT"

	// --- Online verify (hash-based consistency check) ---

	// ReplicationStateVerifySource: we are the verify initiator, requesting and comparing hashes.
	ReplicationStateVerifySource ReplicationState = "VerifyS"
	// ReplicationStateVerifyTarget: we are the verify responder, sending hashes on request.
	ReplicationStateVerifyTarget ReplicationState = "VerifyT"

	// --- Congestion / DRBD Proxy states ---

	// ReplicationStateAhead: we are ahead of the peer (peer is behind due to congestion).
	ReplicationStateAhead ReplicationState = "Ahead"
	// ReplicationStateBehind: we are behind the peer (we are congested).
	ReplicationStateBehind ReplicationState = "Behind"

	// --- Fallback ---

	// ReplicationStateUnknown: unrecognized or unavailable replication state.
	ReplicationStateUnknown ReplicationState = "Unknown"
)

func (s ReplicationState) String() string {
	return string(s)
}

func ParseReplicationState(s string) ReplicationState {
	switch ReplicationState(s) {
	case ReplicationStateOff,
		ReplicationStateEstablished,
		ReplicationStateStartingSyncSource,
		ReplicationStateStartingSyncTarget,
		ReplicationStateWFBitMapSource,
		ReplicationStateWFBitMapTarget,
		ReplicationStateWFSyncUUID,
		ReplicationStateSyncSource,
		ReplicationStateSyncTarget,
		ReplicationStatePausedSyncSource,
		ReplicationStatePausedSyncTarget,
		ReplicationStateVerifySource,
		ReplicationStateVerifyTarget,
		ReplicationStateAhead,
		ReplicationStateBehind,
		ReplicationStateUnknown:
		return ReplicationState(s)
	default:
		return ""
	}
}

// IsSyncingState returns true if the replication state indicates active synchronization.
func (s ReplicationState) IsSyncingState() bool {
	switch s {
	case ReplicationStateSyncSource,
		ReplicationStateSyncTarget,
		ReplicationStateStartingSyncSource,
		ReplicationStateStartingSyncTarget,
		ReplicationStatePausedSyncSource,
		ReplicationStatePausedSyncTarget,
		ReplicationStateWFBitMapSource,
		ReplicationStateWFBitMapTarget,
		ReplicationStateWFSyncUUID:
		return true
	default:
		return false
	}
}

type ConnectionState string

const (
	ConnectionStateStandAlone     ConnectionState = "StandAlone"
	ConnectionStateDisconnecting  ConnectionState = "Disconnecting"
	ConnectionStateUnconnected    ConnectionState = "Unconnected"
	ConnectionStateTimeout        ConnectionState = "Timeout"
	ConnectionStateBrokenPipe     ConnectionState = "BrokenPipe"
	ConnectionStateNetworkFailure ConnectionState = "NetworkFailure"
	ConnectionStateProtocolError  ConnectionState = "ProtocolError"
	ConnectionStateConnecting     ConnectionState = "Connecting"
	ConnectionStateTearDown       ConnectionState = "TearDown"
	ConnectionStateConnected      ConnectionState = "Connected"
	ConnectionStateUnknown        ConnectionState = "Unknown"
)

func (s ConnectionState) String() string {
	return string(s)
}

func ParseConnectionState(s string) ConnectionState {
	switch ConnectionState(s) {
	case ConnectionStateStandAlone,
		ConnectionStateDisconnecting,
		ConnectionStateUnconnected,
		ConnectionStateTimeout,
		ConnectionStateBrokenPipe,
		ConnectionStateNetworkFailure,
		ConnectionStateProtocolError,
		ConnectionStateConnecting,
		ConnectionStateTearDown,
		ConnectionStateConnected,
		ConnectionStateUnknown:
		return ConnectionState(s)
	default:
		return ""
	}
}

// ID range constants for ReplicatedVolumeReplica
const (
	// RVRMinID is the minimum valid ID for ReplicatedVolumeReplica
	RVRMinID = uint8(0)
	// RVRMaxID is the maximum valid ID for ReplicatedVolumeReplica
	RVRMaxID = uint8(31)
)

// IsValidID checks if id is within valid range [RVRMinID; RVRMaxID].
func IsValidID(id uint8) bool {
	return id >= RVRMinID && id <= RVRMaxID
}
