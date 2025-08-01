package v1alpha2

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
)

// name: my-gitlab # TODO validate length

//

// # Some important non-typed and embededd properties
//
//	metadata:
//	  labels:
//	    storage.deckhouse.io/node-name: my-hostname
//	  name: my-gitlab-?????
//	  ownerReferences:
//	  - apiVersion: storage.deckhouse.io/v1alpha2
//	    blockOwnerDeletion: true
//	    controller: true
//	    kind: DistributedBlockDevice
//	    name: my-gitlab
//	    uid: 7697dab1-2382-4901-87bb-249f3562a5b4
//	  generation: 89
//	  finalizers:
//	  - storage.deckhouse.io/sds-replicated-volume
//	status:
//	  conditions:
//	  - message: resource metadata creation successful
//	    reason: ReconcileOnCreate
//	    status: "True"
//	    type: DeviceMetadataCreated
//	  - message: resource activation successful
//	    reason: ReconcileOnCreate
//	    status: "True"
//	    type: DeviceIsActive
//
// +k8s:deepcopy-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:selectablefield:JSONPath=.spec.nodeName
// +kubebuilder:selectablefield:JSONPath=.spec.replicatedVolumeName
type ReplicatedVolumeReplica struct {
	metav1.TypeMeta `json:",inline"`

	metav1.ObjectMeta `json:"metadata"`

	Spec   ReplicatedVolumeReplicaSpec    `json:"spec"`
	Status *ReplicatedVolumeReplicaStatus `json:"status,omitempty"`
}

func (rvr *ReplicatedVolumeReplica) NodeNameSelector(nodeName string) fields.Selector {
	return fields.OneTermEqualSelector("spec.nodeName", nodeName)
}

// +k8s:deepcopy-gen=true
type ReplicatedVolumeReplicaSpec struct {
	ReplicatedVolumeName string          `json:"replicatedVolumeName"`
	NodeName             string          `json:"nodeName"`
	NodeId               uint            `json:"nodeId"`
	NodeAddress          Address         `json:"nodeAddress"`
	Peers                map[string]Peer `json:"peers"`
	Volumes              []Volume        `json:"volumes"`
	SharedSecret         string          `json:"sharedSecret"`
}

// +k8s:deepcopy-gen=true
type Peer struct {
	NodeId       uint    `json:"nodeId"`
	Address      Address `json:"address"`
	Diskless     bool    `json:"diskless,omitempty"`
	SharedSecret string  `json:"sharedSecret,omitempty"`
}

// +k8s:deepcopy-gen=true
type Volume struct {
	Number uint   `json:"number"`
	Disk   string `json:"disk"`
	Device uint   `json:"device"`
}

// +k8s:deepcopy-gen=true
type Address struct {
	IPv4 string `json:"ipv4"`
	Port uint   `json:"port"`
}

// +k8s:deepcopy-gen=true
type ReplicatedVolumeReplicaStatus struct {
	Conditions []metav1.Condition `json:"conditions"`
	DRBD       *DRBDStatus        `json:"drbd,omitempty"`
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
type DRBDStatus struct {
	Name             string             `json:"name"`
	NodeId           int                `json:"node-id"`
	Role             string             `json:"role"`
	Suspended        bool               `json:"suspended"`
	SuspendedUser    bool               `json:"suspended-user"`
	SuspendedNoData  bool               `json:"suspended-no-data"`
	SuspendedFencing bool               `json:"suspended-fencing"`
	SuspendedQuorum  bool               `json:"suspended-quorum"`
	ForceIOFailures  bool               `json:"force-io-failures"`
	WriteOrdering    string             `json:"write-ordering"`
	Devices          []DeviceStatus     `json:"devices"`
	Connections      []ConnectionStatus `json:"connections"`
}

// +k8s:deepcopy-gen=true
type DeviceStatus struct {
	Volume       int    `json:"volume"`
	Minor        int    `json:"minor"`
	DiskState    string `json:"disk-state"`
	Client       bool   `json:"client"`
	Open         bool   `json:"open"`
	Quorum       bool   `json:"quorum"`
	Size         int    `json:"size"`
	Read         int    `json:"read"`
	Written      int    `json:"written"`
	ALWrites     int    `json:"al-writes"`
	BMWrites     int    `json:"bm-writes"`
	UpperPending int    `json:"upper-pending"`
	LowerPending int    `json:"lower-pending"`
}

// +k8s:deepcopy-gen=true
type ConnectionStatus struct {
	PeerNodeId      int    `json:"peer-node-id"`
	Name            string `json:"name"`
	ConnectionState string `json:"connection-state"`
	Congested       bool   `json:"congested"`
	Peerrole        string `json:"peer-role"`
	TLS             bool   `json:"tls"`
	APInFlight      int    `json:"ap-in-flight"`
	RSInFlight      int    `json:"rs-in-flight"`

	Paths       []PathStatus       `json:"paths"`
	PeerDevices []PeerDeviceStatus `json:"peer_devices"`
}

// +k8s:deepcopy-gen=true
type PathStatus struct {
	ThisHost    HostStatus `json:"this_host"`
	RemoteHost  HostStatus `json:"remote_host"`
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
	Volume           int    `json:"volume"`
	ReplicationState string `json:"replication-state"`
	PeerDiskState    string `json:"peer-disk-state"`
	PeerClient       bool   `json:"peer-client"`
	ResyncSuspended  string `json:"resync-suspended"`
	// Received               int     `json:"received"`
	// Sent                   int     `json:"sent"`
	OutOfSync              int    `json:"out-of-sync"`
	Pending                int    `json:"pending"`
	Unacked                int    `json:"unacked"`
	HasSyncDetails         bool   `json:"has-sync-details"`
	HasOnlineVerifyDetails bool   `json:"has-online-verify-details"`
	PercentInSync          string `json:"percent-in-sync"`
}
