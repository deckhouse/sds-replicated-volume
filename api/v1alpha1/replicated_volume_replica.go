/*
Copyright 2025 Flant JSC

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
	"slices"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

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
// +kubebuilder:printcolumn:name="Published",type=string,JSONPath=".status.conditions[?(@.type=='Published')].status"
// +kubebuilder:printcolumn:name="Online",type=string,JSONPath=".status.conditions[?(@.type=='Online')].status"
// +kubebuilder:printcolumn:name="IOReady",type=string,JSONPath=".status.conditions[?(@.type=='IOReady')].status"
// +kubebuilder:printcolumn:name="Configured",type=string,JSONPath=".status.conditions[?(@.type=='Configured')].status"
// +kubebuilder:printcolumn:name="DataInitialized",type=string,JSONPath=".status.conditions[?(@.type=='DataInitialized')].status"
// +kubebuilder:printcolumn:name="InQuorum",type=string,JSONPath=".status.conditions[?(@.type=='InQuorum')].status"
// +kubebuilder:printcolumn:name="InSync",type=string,JSONPath=".status.syncProgress"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
// +kubebuilder:validation:XValidation:rule="self.metadata.name.startsWith(self.spec.replicatedVolumeName + '-')",message="metadata.name must start with spec.replicatedVolumeName + '-'"
// +kubebuilder:validation:XValidation:rule="int(self.metadata.name.substring(self.metadata.name.lastIndexOf('-') + 1)) <= 31",message="numeric suffix must be between 0 and 31"
type ReplicatedVolumeReplica struct {
	metav1.TypeMeta `json:",inline"`

	metav1.ObjectMeta `json:"metadata"`

	Spec ReplicatedVolumeReplicaSpec `json:"spec"`

	// +patchStrategy=merge
	Status *ReplicatedVolumeReplicaStatus `json:"status,omitempty" patchStrategy:"merge"`
}

func (rvr *ReplicatedVolumeReplica) NodeID() (uint, bool) {
	idx := strings.LastIndex(rvr.Name, "-")
	if idx < 0 {
		return 0, false
	}

	id, err := strconv.ParseUint(rvr.Name[idx+1:], 10, 0)
	if err != nil {
		return 0, false
	}
	return uint(id), true
}

func (rvr *ReplicatedVolumeReplica) SetNameWithNodeID(nodeID uint) {
	rvr.Name = fmt.Sprintf("%s-%d", rvr.Spec.ReplicatedVolumeName, nodeID)
}

func (rvr *ReplicatedVolumeReplica) ChooseNewName(otherRVRs []ReplicatedVolumeReplica) bool {
	reservedNodeIDs := make([]uint, 0, RVRMaxNodeID)

	for i := range otherRVRs {
		otherRVR := &otherRVRs[i]
		if otherRVR.Spec.ReplicatedVolumeName != rvr.Spec.ReplicatedVolumeName {
			continue
		}

		id, ok := otherRVR.NodeID()
		if !ok {
			continue
		}
		reservedNodeIDs = append(reservedNodeIDs, id)
	}

	for i := RVRMinNodeID; i <= RVRMaxNodeID; i++ {
		if !slices.Contains(reservedNodeIDs, i) {
			rvr.SetNameWithNodeID(i)
			return true
		}
	}

	return false
}

// SetReplicatedVolume sets the ReplicatedVolumeName in Spec and ControllerReference for the RVR.
func (rvr *ReplicatedVolumeReplica) SetReplicatedVolume(rv *ReplicatedVolume, scheme *runtime.Scheme) error {
	rvr.Spec.ReplicatedVolumeName = rv.Name
	return controllerutil.SetControllerReference(rv, rvr, scheme)
}

// +kubebuilder:object:generate=true
type ReplicatedVolumeReplicaSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=127
	// +kubebuilder:validation:Pattern=`^[0-9A-Za-z.+_-]*$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="replicatedVolumeName is immutable"
	ReplicatedVolumeName string `json:"replicatedVolumeName"`

	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	NodeName string `json:"nodeName,omitempty"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Diskful;Access;TieBreaker
	Type ReplicaType `json:"type"`
}

func (s *ReplicatedVolumeReplicaSpec) IsDiskless() bool {
	return s.Type != ReplicaTypeDiskful
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

// +kubebuilder:object:generate=true
type ReplicatedVolumeReplicaStatus struct {
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// +kubebuilder:validation:Enum=Diskful;Access;TieBreaker
	ActualType ReplicaType `json:"actualType,omitempty"`

	// +optional
	// +kubebuilder:validation:MaxLength=256
	LVMLogicalVolumeName string `json:"lvmLogicalVolumeName,omitempty"`

	// +patchStrategy=merge
	DRBD *DRBD `json:"drbd,omitempty" patchStrategy:"merge"`

	// SyncProgress shows sync status for kubectl output:
	// - "True" when fully synced (InSync condition is True)
	// - "XX.XX%" during active synchronization (SyncTarget)
	// - DiskState (e.g. "Outdated", "Inconsistent") when not syncing but not in sync
	// +optional
	SyncProgress string `json:"syncProgress,omitempty"`
}

// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
type ReplicatedVolumeReplicaList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []ReplicatedVolumeReplica `json:"items"`
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
type DRBD struct {
	// +patchStrategy=merge
	Config *DRBDConfig `json:"config,omitempty" patchStrategy:"merge"`
	// +patchStrategy=merge
	Actual *DRBDActual `json:"actual,omitempty" patchStrategy:"merge"`
	// +patchStrategy=merge
	Status *DRBDStatus `json:"status,omitempty" patchStrategy:"merge"`
	// +patchStrategy=merge
	Errors *DRBDErrors `json:"errors,omitempty" patchStrategy:"merge"`
}

// +kubebuilder:object:generate=true
type DRBDErrors struct {
	// +patchStrategy=merge
	FileSystemOperationError *MessageError `json:"fileSystemOperationError,omitempty" patchStrategy:"merge"`
	// +patchStrategy=merge
	ConfigurationCommandError *CmdError `json:"configurationCommandError,omitempty" patchStrategy:"merge"`
	// +patchStrategy=merge
	SharedSecretAlgSelectionError *SharedSecretUnsupportedAlgError `json:"sharedSecretAlgSelectionError,omitempty" patchStrategy:"merge"`
	// +patchStrategy=merge
	LastPrimaryError *CmdError `json:"lastPrimaryError,omitempty" patchStrategy:"merge"`
	// +patchStrategy=merge
	LastSecondaryError *CmdError `json:"lastSecondaryError,omitempty" patchStrategy:"merge"`
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

// +kubebuilder:object:generate=true
type DeviceStatus struct {
	Volume       int       `json:"volume"`
	Minor        int       `json:"minor"`
	DiskState    DiskState `json:"diskState"`
	Client       bool      `json:"client"`
	Open         bool      `json:"open"`
	Quorum       bool      `json:"quorum"`
	Size         int       `json:"size"`
	Read         int       `json:"read"`
	Written      int       `json:"written"`
	ALWrites     int       `json:"alWrites"`
	BMWrites     int       `json:"bmWrites"`
	UpperPending int       `json:"upperPending"`
	LowerPending int       `json:"lowerPending"`
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
	APInFlight      int                `json:"apInFlight"`
	RSInFlight      int                `json:"rsInFlight"`
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
	Pending                int              `json:"pending"`
	Unacked                int              `json:"unacked"`
	HasSyncDetails         bool             `json:"hasSyncDetails"`
	HasOnlineVerifyDetails bool             `json:"hasOnlineVerifyDetails"`
	PercentInSync          string           `json:"percentInSync"`
}
