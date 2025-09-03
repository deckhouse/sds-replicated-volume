package v1alpha2

import (
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
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
// +kubebuilder:printcolumn:name="Primary",type=boolean,JSONPath=".spec.primary"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
type ReplicatedVolumeReplica struct {
	metav1.TypeMeta `json:",inline"`

	metav1.ObjectMeta `json:"metadata"`

	Spec   ReplicatedVolumeReplicaSpec    `json:"spec"`
	Status *ReplicatedVolumeReplicaStatus `json:"status,omitempty"`
}

func (rvr *ReplicatedVolumeReplica) NodeNameSelector(nodeName string) fields.Selector {
	return fields.OneTermEqualSelector("spec.nodeName", nodeName)
}

func (rvr *ReplicatedVolumeReplica) Diskless() (bool, error) {
	if len(rvr.Spec.Volumes) == 0 {
		return true, nil
	}
	diskless := rvr.Spec.Volumes[0].Disk == ""
	for _, v := range rvr.Spec.Volumes[1:] {
		if diskless != (v.Disk == "") {
			return false, fmt.Errorf("diskful volumes should not be mixed with diskless volumes")
		}
	}
	return diskless, nil
}

func (rvr *ReplicatedVolumeReplica) StatusConditionsInitialized() bool {
	if rvr.Status == nil {
		return false
	}

	if rvr.Status.Conditions == nil {
		return false
	}

	for t := range ReplicatedVolumeReplicaConditions {
		if meta.FindStatusCondition(rvr.Status.Conditions, t) == nil {
			return false
		}
	}
	return true
}

func (rvr *ReplicatedVolumeReplica) InitializeStatusConditions() {
	if rvr.Status == nil {
		rvr.Status = &ReplicatedVolumeReplicaStatus{}
	}

	if rvr.Status.Conditions == nil {
		rvr.Status.Conditions = []metav1.Condition{}
	}

	for t, opts := range ReplicatedVolumeReplicaConditions {
		if meta.FindStatusCondition(rvr.Status.Conditions, t) != nil {
			continue
		}
		cond := metav1.Condition{
			Type:               t,
			Status:             metav1.ConditionUnknown,
			Reason:             "Initializing",
			Message:            "",
			LastTransitionTime: metav1.NewTime(time.Now()),
		}
		if opts.UseObservedGeneration {
			cond.ObservedGeneration = rvr.Generation
		}
		rvr.Status.Conditions = append(rvr.Status.Conditions, cond)
	}
}

func (rvr *ReplicatedVolumeReplica) RecalculateStatusConditionReady() {
	if rvr.Status == nil || rvr.Status.Conditions == nil {
		return
	}

	if !meta.IsStatusConditionTrue(rvr.Status.Conditions, ConditionTypeDevicesReady) {
		meta.SetStatusCondition(
			&rvr.Status.Conditions,
			metav1.Condition{
				Type:               ConditionTypeReady,
				Status:             metav1.ConditionFalse,
				Reason:             ReasonDevicesAreNotReady,
				Message:            "Devices are not ready",
				ObservedGeneration: rvr.Generation,
			},
		)
	} else if !meta.IsStatusConditionTrue(rvr.Status.Conditions, ConditionTypeConfigurationAdjusted) {
		meta.SetStatusCondition(
			&rvr.Status.Conditions,
			metav1.Condition{
				Type:               ConditionTypeReady,
				Status:             metav1.ConditionFalse,
				Reason:             ReasonAdjustmentFailed,
				Message:            "Resource adjustment failed",
				ObservedGeneration: rvr.Generation,
			},
		)
	} else {
		meta.SetStatusCondition(
			&rvr.Status.Conditions,
			metav1.Condition{
				Type:               ConditionTypeReady,
				Status:             metav1.ConditionTrue,
				Reason:             ReasonReady,
				Message:            "Replica is configured and operational",
				ObservedGeneration: rvr.Generation,
			},
		)
	}
}

// +k8s:deepcopy-gen=true
type ReplicatedVolumeReplicaSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=127
	// +kubebuilder:validation:Pattern=`^[0-9A-Za-z.+_-]*$`
	ReplicatedVolumeName string `json:"replicatedVolumeName"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	NodeName string `json:"nodeName"`

	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=7
	NodeId uint `json:"nodeId"`

	// +kubebuilder:validation:Required
	NodeAddress Address `json:"nodeAddress"`

	Peers map[string]Peer `json:"peers,omitempty"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=100
	Volumes []Volume `json:"volumes"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	SharedSecret string `json:"sharedSecret"`

	// +kubebuilder:default=false
	Primary bool `json:"primary,omitempty"`

	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=7
	Quorum byte `json:"quorum,omitempty"`

	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=7
	QuorumMinimumRedundancy byte `json:"quorumMinimumRedundancy,omitempty"`

	// +kubebuilder:default=false
	AllowTwoPrimaries bool `json:"allowTwoPrimaries,omitempty"`
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

	SharedSecret string `json:"sharedSecret,omitempty"`
}

// +k8s:deepcopy-gen=true
type Volume struct {
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=255
	Number uint `json:"number"`

	// +kubebuilder:validation:Pattern=`^(/[a-zA-Z0-9/.+_-]+)?$`
	Disk string `json:"disk,omitempty"`

	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1048575
	Device uint `json:"device"`
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
