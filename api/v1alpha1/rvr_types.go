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
	"fmt"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ReplicatedVolumeReplica is a Kubernetes Custom Resource that represents a replica of a ReplicatedVolume.
// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=rvr
// +kubebuilder:metadata:labels=module=sds-replicated-volume
// +kubebuilder:selectablefield:JSONPath=.spec.nodeName
// +kubebuilder:selectablefield:JSONPath=.spec.replicatedVolumeName
// +kubebuilder:printcolumn:name="Volume",type=string,JSONPath=".spec.replicatedVolumeName"
// +kubebuilder:printcolumn:name="Node",type=string,JSONPath=".spec.nodeName"
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Attached",type=string,JSONPath=".status.conditions[?(@.type=='Attached')].status"
// +kubebuilder:printcolumn:name="Online",type=string,JSONPath=".status.conditions[?(@.type=='Online')].status"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Configured",type=string,JSONPath=".status.conditions[?(@.type=='Configured')].status"
// +kubebuilder:printcolumn:name="DataInitialized",type=string,JSONPath=".status.conditions[?(@.type=='DataInitialized')].status"
// +kubebuilder:printcolumn:name="InQuorum",type=string,JSONPath=".status.conditions[?(@.type=='InQuorum')].status"
// +kubebuilder:printcolumn:name="InSync",type=string,JSONPath=".status.syncProgress"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
// +kubebuilder:validation:XValidation:rule="self.metadata.name.startsWith(self.spec.replicatedVolumeName + '-')",message="metadata.name must start with spec.replicatedVolumeName + '-'"
// +kubebuilder:validation:XValidation:rule="int(self.metadata.name.substring(self.metadata.name.lastIndexOf('-') + 1)) <= 31",message="numeric suffix must be between 0 and 31"
// +kubebuilder:validation:XValidation:rule="size(self.metadata.name) <= 123",message="metadata.name must be at most 123 characters (to fit derived LLV name with prefix)"
type ReplicatedVolumeReplica struct {
	metav1.TypeMeta `json:",inline"`

	metav1.ObjectMeta `json:"metadata"`

	Spec ReplicatedVolumeReplicaSpec `json:"spec"`

	// +patchStrategy=merge
	Status ReplicatedVolumeReplicaStatus `json:"status,omitempty" patchStrategy:"merge"`
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

// NodeID extracts NodeID from the RVR name (e.g., "pvc-xxx-5" → 5).
func (rvr *ReplicatedVolumeReplica) NodeID() (uint8, bool) {
	return nodeIDFromName(rvr.Name)
}

// SetNameWithNodeID sets the RVR name using the ReplicatedVolumeName and the given NodeID.
func (rvr *ReplicatedVolumeReplica) SetNameWithNodeID(nodeID uint8) {
	rvr.Name = fmt.Sprintf("%s-%d", rvr.Spec.ReplicatedVolumeName, nodeID)
}

// ChooseNewName selects the first available NodeID and sets the RVR name.
// Returns false if all NodeIDs (0-31) are already taken by other RVRs.
func (rvr *ReplicatedVolumeReplica) ChooseNewName(otherRVRs []ReplicatedVolumeReplica) bool {
	// Bitmask for reserved NodeIDs (0-31 fit in uint32).
	var reserved uint32

	for i := range otherRVRs {
		otherRVR := &otherRVRs[i]
		if otherRVR.Spec.ReplicatedVolumeName != rvr.Spec.ReplicatedVolumeName {
			continue
		}

		id, ok := otherRVR.NodeID()
		if !ok {
			continue
		}
		reserved |= 1 << id
	}

	for i := RVRMinNodeID; i <= RVRMaxNodeID; i++ {
		if reserved&(1<<i) == 0 {
			rvr.SetNameWithNodeID(i)
			return true
		}
	}

	return false
}

// +kubebuilder:object:generate=true
// +kubebuilder:validation:XValidation:rule="size(self.lvmVolumeGroupName) == 0 || size(self.nodeName) > 0",message="lvmVolumeGroupName requires nodeName to be set"
// +kubebuilder:validation:XValidation:rule="size(self.lvmVolumeGroupName) == 0 || self.type == 'Diskful'",message="lvmVolumeGroupName can only be set for Diskful type"
// +kubebuilder:validation:XValidation:rule="size(self.lvmVolumeGroupThinPoolName) == 0 || size(self.lvmVolumeGroupName) > 0",message="lvmVolumeGroupThinPoolName requires lvmVolumeGroupName to be set"
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
	// +kubebuilder:validation:XValidation:rule="oldSelf == '' || self == oldSelf",message="nodeName is immutable once set"
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
	// +kubebuilder:validation:Enum=Diskful;Access;TieBreaker
	Type ReplicaType `json:"type"`
}

// ReplicaType enumerates possible values for ReplicatedVolumeReplica spec.type and status.effectiveType fields.
type ReplicaType string

// Replica type values for [ReplicatedVolumeReplica] spec.type field.
const (
	// ReplicaTypeDiskful represents a diskful replica that stores data on disk.
	ReplicaTypeDiskful ReplicaType = "Diskful"
	// ReplicaTypeAccess represents a diskless replica for data access.
	ReplicaTypeAccess ReplicaType = "Access"
	// ReplicaTypeTieBreaker represents a diskless replica for quorum.
	ReplicaTypeTieBreaker ReplicaType = "TieBreaker"
)

func (t ReplicaType) String() string {
	return string(t)
}

// +kubebuilder:object:generate=true
type ReplicatedVolumeReplicaStatus struct {
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// +kubebuilder:validation:Enum=Diskful;Access;TieBreaker
	EffectiveType ReplicaType `json:"effectiveType,omitempty"`

	// +patchStrategy=merge
	DRBD *DRBD `json:"drbd,omitempty" patchStrategy:"merge"`

	// +kubebuilder:validation:MaxItems=32
	// +optional
	Addresses []DRBDResourceAddressStatus `json:"addresses,omitempty"`

	// DatameshRevision is the datamesh revision this replica was configured for.
	DatameshRevision int64 `json:"datameshRevision,omitempty"`

	// BackingVolumeSize is the size of the backing LVM logical volume for this replica.
	// Only set for Diskful replicas.
	// +optional
	BackingVolumeSize resource.Quantity `json:"backingVolumeSize,omitempty"`

	// DRBDResourceGeneration is the generation of the DRBDResource that was last applied.
	// Used to skip redundant spec comparison when the generation matches.
	// +optional
	DRBDResourceGeneration int64 `json:"drbdResourceGeneration,omitempty"`
}

// +kubebuilder:object:generate=true
type DRBD struct {
	// +patchStrategy=merge
	Config *DRBDConfig `json:"config,omitempty" patchStrategy:"merge"`
	// +patchStrategy=merge
	Actual *DRBDActual `json:"actual,omitempty" patchStrategy:"merge"`
	// +patchStrategy=merge
	Status *DRBDStatus `json:"status,omitempty" patchStrategy:"merge"`
}

// +kubebuilder:object:generate=true
type DRBDConfig struct {
	// +optional
	Address *Address `json:"address,omitempty"`

	// Peers contains information about other replicas in the same ReplicatedVolume.
	// The key in this map is the node name where the peer replica is located.
	// +optional
	Peers map[string]Peer `json:"peers,omitempty"`

	// PeersInitialized indicates that Peers has been calculated.
	// This field is used to distinguish between no peers and not yet calculated.
	// +optional
	PeersInitialized bool `json:"peersInitialized,omitempty"`

	// +optional
	Primary *bool `json:"primary,omitempty"`
}

// +kubebuilder:object:generate=true
type DRBDActual struct {
	// +optional
	// +kubebuilder:validation:Pattern=`^(/[a-zA-Z0-9/.+_-]+)?$`
	// +kubebuilder:validation:MaxLength=256
	Disk string `json:"disk,omitempty"`

	// +optional
	// +kubebuilder:default=false
	AllowTwoPrimaries bool `json:"allowTwoPrimaries,omitempty"`

	// +optional
	// +kubebuilder:default=false
	InitialSyncCompleted bool `json:"initialSyncCompleted,omitempty"`
}

// +kubebuilder:object:generate=true
type DRBDStatus struct {
	Name string `json:"name"`
	//nolint:revive // var-naming: NodeId kept for API compatibility with JSON tag
	NodeId           int                `json:"nodeId"`
	Role             string             `json:"role"`
	Suspended        bool               `json:"suspended"`
	SuspendedUser    bool               `json:"suspendedUser"`
	SuspendedNoData  bool               `json:"suspendedNoData"`
	SuspendedFencing bool               `json:"suspendedFencing"`
	SuspendedQuorum  bool               `json:"suspendedQuorum"`
	ForceIOFailures  bool               `json:"forceIOFailures"`
	WriteOrdering    string             `json:"writeOrdering"`
	Devices          []DeviceStatus     `json:"devices"`
	Connections      []ConnectionStatus `json:"connections"`
}

type DiskState string

const (
	DiskStateDiskless     DiskState = "Diskless"
	DiskStateAttaching    DiskState = "Attaching"
	DiskStateDetaching    DiskState = "Detaching"
	DiskStateFailed       DiskState = "Failed"
	DiskStateNegotiating  DiskState = "Negotiating"
	DiskStateInconsistent DiskState = "Inconsistent"
	DiskStateOutdated     DiskState = "Outdated"
	DiskStateUnknown      DiskState = "DUnknown"
	DiskStateConsistent   DiskState = "Consistent"
	DiskStateUpToDate     DiskState = "UpToDate"
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

type ReplicationState string

const (
	ReplicationStateOff                ReplicationState = "Off"
	ReplicationStateEstablished        ReplicationState = "Established"
	ReplicationStateStartingSyncSource ReplicationState = "StartingSyncS"
	ReplicationStateStartingSyncTarget ReplicationState = "StartingSyncT"
	ReplicationStateWFBitMapSource     ReplicationState = "WFBitMapS"
	ReplicationStateWFBitMapTarget     ReplicationState = "WFBitMapT"
	ReplicationStateWFSyncUUID         ReplicationState = "WFSyncUUID"
	ReplicationStateSyncSource         ReplicationState = "SyncSource"
	ReplicationStateSyncTarget         ReplicationState = "SyncTarget"
	ReplicationStatePausedSyncSource   ReplicationState = "PausedSyncS"
	ReplicationStatePausedSyncTarget   ReplicationState = "PausedSyncT"
	ReplicationStateVerifySource       ReplicationState = "VerifyS"
	ReplicationStateVerifyTarget       ReplicationState = "VerifyT"
	ReplicationStateAhead              ReplicationState = "Ahead"
	ReplicationStateBehind             ReplicationState = "Behind"
	ReplicationStateUnknown            ReplicationState = "Unknown"
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

// +kubebuilder:object:generate=true
type DeviceStatus struct {
	Volume    int       `json:"volume"`
	Minor     int       `json:"minor"`
	DiskState DiskState `json:"diskState"`
	Client    bool      `json:"client"`
	Open      bool      `json:"open"`
	Quorum    bool      `json:"quorum"`
	Size      int       `json:"size"`
}

// +kubebuilder:object:generate=true
type ConnectionStatus struct {
	//nolint:revive // var-naming: PeerNodeId kept for API compatibility with JSON tag
	PeerNodeId      int                `json:"peerNodeId"`
	Name            string             `json:"name"`
	ConnectionState ConnectionState    `json:"connectionState"`
	Congested       bool               `json:"congested"`
	Peerrole        string             `json:"peerRole"`
	TLS             bool               `json:"tls"`
	Paths           []PathStatus       `json:"paths"`
	PeerDevices     []PeerDeviceStatus `json:"peerDevices"`
}

// +kubebuilder:object:generate=true
type PathStatus struct {
	ThisHost    HostStatus `json:"thisHost"`
	RemoteHost  HostStatus `json:"remoteHost"`
	Established bool       `json:"established"`
}

// +kubebuilder:object:generate=true
type HostStatus struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
	Family  string `json:"family"`
}

// +kubebuilder:object:generate=true
type PeerDeviceStatus struct {
	Volume                 int              `json:"volume"`
	ReplicationState       ReplicationState `json:"replicationState"`
	PeerDiskState          DiskState        `json:"peerDiskState"`
	PeerClient             bool             `json:"peerClient"`
	ResyncSuspended        string           `json:"resyncSuspended"`
	OutOfSync              int              `json:"outOfSync"`
	HasSyncDetails         bool             `json:"hasSyncDetails"`
	HasOnlineVerifyDetails bool             `json:"hasOnlineVerifyDetails"`
	PercentInSync          string           `json:"percentInSync"`
}

// +kubebuilder:object:generate=true
type Peer struct {
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=7
	//nolint:revive // var-naming: NodeId kept for API compatibility with JSON tag
	NodeId uint `json:"nodeId"`

	// +kubebuilder:validation:Required
	Address Address `json:"address"`

	// +kubebuilder:default=false
	Diskless bool `json:"diskless,omitempty"`
}

// +kubebuilder:object:generate=true
type Address struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^(?:(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)$`
	IPv4 string `json:"ipv4"`

	// +kubebuilder:validation:Minimum=1025
	// +kubebuilder:validation:Maximum=65535
	Port uint `json:"port"`
}

// DRBD node ID constants for ReplicatedVolumeReplica
const (
	// RVRMinNodeID is the minimum valid node ID for DRBD configuration in ReplicatedVolumeReplica
	RVRMinNodeID = uint8(0)
	// RVRMaxNodeID is the maximum valid node ID for DRBD configuration in ReplicatedVolumeReplica
	RVRMaxNodeID = uint8(31)
)

// IsValidNodeID checks if nodeID is within valid range [RVRMinNodeID; RVRMaxNodeID].
func IsValidNodeID(nodeID uint8) bool {
	return nodeID >= RVRMinNodeID && nodeID <= RVRMaxNodeID
}

// FormatValidNodeIDRange returns a formatted string representing the valid nodeID range.
// faster than fmt.Sprintf("%d; %d", RVRMinNodeID, RVRMaxNodeID) because it avoids allocation and copying of the string.
func FormatValidNodeIDRange() string {
	var b strings.Builder
	b.Grow(10) // Pre-allocate: "[0; 31]" = 8 bytes, but allocate a bit more
	b.WriteByte('[')
	b.WriteString(strconv.FormatUint(uint64(RVRMinNodeID), 10))
	b.WriteString("; ")
	b.WriteString(strconv.FormatUint(uint64(RVRMaxNodeID), 10))
	b.WriteByte(']')
	return b.String()
}

func SprintDRBDDisk(actualVGNameOnTheNode, actualLVNameOnTheNode string) string {
	return fmt.Sprintf("/dev/%s/%s", actualVGNameOnTheNode, actualLVNameOnTheNode)
}

func ParseDRBDDisk(disk string) (actualVGNameOnTheNode, actualLVNameOnTheNode string, err error) {
	parts := strings.Split(disk, "/")
	if len(parts) != 4 || parts[0] != "" || parts[1] != "dev" ||
		len(parts[2]) == 0 || len(parts[3]) == 0 {
		return "", "",
			fmt.Errorf(
				"parsing DRBD Disk: expected format '/dev/{actualVGNameOnTheNode}/{actualLVNameOnTheNode}', got '%s'",
				disk,
			)
	}
	return parts[2], parts[3], nil
}
