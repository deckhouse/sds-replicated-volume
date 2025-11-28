package v1alpha3

import (
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
)

// +k8s:deepcopy-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=rvr
// +kubebuilder:selectablefield:JSONPath=.spec.nodeName
// +kubebuilder:selectablefield:JSONPath=.spec.replicatedVolumeName
// +kubebuilder:printcolumn:name="Volume",type=string,JSONPath=".spec.replicatedVolumeName"
// +kubebuilder:printcolumn:name="Node",type=string,JSONPath=".spec.nodeName"
// +kubebuilder:printcolumn:name="Primary",type=string,JSONPath=".status.conditions[?(@.type=='Primary')].status"
// +kubebuilder:printcolumn:name="Diskless",type=string,JSONPath=".spec.volumes[0].disk==null"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="ConfigurationAdjusted",type=string,JSONPath=".status.conditions[?(@.type=='ConfigurationAdjusted')].status"
// +kubebuilder:printcolumn:name="InitialSync",type=string,JSONPath=".status.conditions[?(@.type=='InitialSync')].status"
// +kubebuilder:printcolumn:name="Quorum",type=string,JSONPath=".status.conditions[?(@.type=='Quorum')].status"
// +kubebuilder:printcolumn:name="DevicesReady",type=string,JSONPath=".status.conditions[?(@.type=='DevicesReady')].status"
// +kubebuilder:printcolumn:name="DiskIOSuspended",type=string,JSONPath=".status.conditions[?(@.type=='DiskIOSuspended')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
type ReplicatedVolumeReplica struct {
	metav1.TypeMeta `json:",inline"`

	metav1.ObjectMeta `json:"metadata"`

	Spec ReplicatedVolumeReplicaSpec `json:"spec"`

	// +patchStrategy=merge
	Status *ReplicatedVolumeReplicaStatus `json:"status,omitempty" patchStrategy:"merge"`
}

func (rvr *ReplicatedVolumeReplica) NodeNameSelector(nodeName string) fields.Selector {
	return fields.OneTermEqualSelector("spec.nodeName", nodeName)
}

// +k8s:deepcopy-gen=true
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

	// +kubebuilder:validation:Enum=Diskful;Access;TieBreaker
	Type string `json:"type,omitempty"`
}

// +k8s:deepcopy-gen=true
type Peer struct {
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=7
	NodeId uint `json:"nodeId"`

	// +kubebuilder:validation:Required
	Address Address `json:"address"`

	// +kubebuilder:default=false
	Diskless bool `json:"diskless,omitempty"`
}

// +k8s:deepcopy-gen=true
type Address struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^(?:(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)$`
	IPv4 string `json:"ipv4"`

	// +kubebuilder:validation:Minimum=1025
	// +kubebuilder:validation:Maximum=65535
	Port uint `json:"port"`
}

// +k8s:deepcopy-gen=true
type ReplicatedVolumeReplicaStatus struct {
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// +kubebuilder:validation:Enum=Diskful;Access;TieBreaker
	ActualType string `json:"actualType,omitempty"`

	// +patchStrategy=merge
	DRBD *DRBD `json:"drbd,omitempty" patchStrategy:"merge"`
}

// +k8s:deepcopy-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
type ReplicatedVolumeReplicaList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []ReplicatedVolumeReplica `json:"items"`
}

// +k8s:deepcopy-gen=true
type DRBDConfig struct {
	// TODO: forbid changing properties more then once
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=7
	// +optional
	NodeId *uint `json:"nodeId"`

	// +optional
	Address *Address `json:"address,omitempty"`

	// +optional
	Peers map[string]Peer `json:"peers,omitempty"`

	// +optional
	// +kubebuilder:validation:Pattern=`^(/[a-zA-Z0-9/.+_-]+)?$`
	// +kubebuilder:validation:MaxLength=256
	Disk string `json:"disk,omitempty"`

	// +optional
	Primary *bool `json:"primary,omitempty"`
}

func (v *DRBDConfig) SetDisk(actualVGNameOnTheNode, actualLVNameOnTheNode string) {
	v.Disk = fmt.Sprintf("/dev/%s/%s", actualVGNameOnTheNode, actualLVNameOnTheNode)
}

func (v *DRBDConfig) ParseDisk() (actualVGNameOnTheNode, actualLVNameOnTheNode string, err error) {
	parts := strings.Split(v.Disk, "/")
	if len(parts) != 4 || parts[0] != "" || parts[1] != "dev" ||
		len(parts[2]) == 0 || len(parts[3]) == 0 {
		return "", "",
			fmt.Errorf(
				"parsing Volume Disk: expected format '/dev/{actualVGNameOnTheNode}/{actualLVNameOnTheNode}', got '%s'",
				v.Disk,
			)
	}
	return parts[2], parts[3], nil
}

// +k8s:deepcopy-gen=true
type DRBD struct {
	// +patchStrategy=merge
	Config *DRBDConfig `json:"config,omitempty" patchStrategy:"merge"`
	// +patchStrategy=merge
	Actual *DRBDActual `json:"actual,omitempty" patchStrategy:"merge"`
	// +patchStrategy=merge
	Status *DRBDStatus `json:"status,omitempty" patchStrategy:"merge"`
}

// +k8s:deepcopy-gen=true
type DRBDActual struct {
	// +optional
	// +kubebuilder:validation:Pattern=`^(/[a-zA-Z0-9/.+_-]+)?$`
	// +kubebuilder:validation:MaxLength=256
	Disk string `json:"disk,omitempty"`
}

// +k8s:deepcopy-gen=true
type DRBDStatus struct {
	Name             string             `json:"name"`
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

// +k8s:deepcopy-gen=true
type DeviceStatus struct {
	Volume       int    `json:"volume"`
	Minor        int    `json:"minor"`
	DiskState    string `json:"diskState"`
	Client       bool   `json:"client"`
	Open         bool   `json:"open"`
	Quorum       bool   `json:"quorum"`
	Size         int    `json:"size"`
	Read         int    `json:"read"`
	Written      int    `json:"written"`
	ALWrites     int    `json:"alWrites"`
	BMWrites     int    `json:"bmWrites"`
	UpperPending int    `json:"upperPending"`
	LowerPending int    `json:"lowerPending"`
}

// +k8s:deepcopy-gen=true
type ConnectionStatus struct {
	PeerNodeId      int                `json:"peerNodeId"`
	Name            string             `json:"name"`
	ConnectionState string             `json:"connectionState"`
	Congested       bool               `json:"congested"`
	Peerrole        string             `json:"peerRole"`
	TLS             bool               `json:"tls"`
	APInFlight      int                `json:"apInFlight"`
	RSInFlight      int                `json:"rsInFlight"`
	Paths           []PathStatus       `json:"paths"`
	PeerDevices     []PeerDeviceStatus `json:"peerDevices"`
}

// +k8s:deepcopy-gen=true
type PathStatus struct {
	ThisHost    HostStatus `json:"thisHost"`
	RemoteHost  HostStatus `json:"remoteHost"`
	Established bool       `json:"established"`
}

// +k8s:deepcopy-gen=true
type HostStatus struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
	Family  string `json:"family"`
}

// +k8s:deepcopy-gen=true
type PeerDeviceStatus struct {
	Volume                 int    `json:"volume"`
	ReplicationState       string `json:"replicationState"`
	PeerDiskState          string `json:"peerDiskState"`
	PeerClient             bool   `json:"peerClient"`
	ResyncSuspended        string `json:"resyncSuspended"`
	OutOfSync              int    `json:"outOfSync"`
	Pending                int    `json:"pending"`
	Unacked                int    `json:"unacked"`
	HasSyncDetails         bool   `json:"hasSyncDetails"`
	HasOnlineVerifyDetails bool   `json:"hasOnlineVerifyDetails"`
	PercentInSync          string `json:"percentInSync"`
}
