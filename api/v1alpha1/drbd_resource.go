package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DRBDResource is the list of DRBDResources
// +k8s:deepcopy-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type DRBDResource struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DRBDResourceSpec   `json:"spec"`
	Status            DRBDResourceStatus `json:"status,omitempty"`
}

// DRBDResourceList is the list of DRBDResources
// +k8s:deepcopy-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type DRBDResourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []DRBDResource `json:"items"`
}

// DRBDResourceSpec defines the desired state of DRBDResource
// +k8s:deepcopy-gen=true
type DRBDResourceSpec struct {
	Inactive        bool               `json:"inactive"`
	NetworkPoolName string             `json:"networkPoolName"`
	Size            int64              `json:"size"`
	Peers           map[string]Peer    `json:"peers"`
	ResourceName    string             `json:"resourceName"`
	NodeName        string             `json:"nodeName"`
	StoragePoolName string             `json:"storagePoolName"`
	NodeID          int                `json:"nodeId"`
	DRBDCurrentGi   string             `json:"drbdCurrentGi"`
	Port            int                `json:"port"`
	Minor           int                `json:"minor"`
	Device          string             `json:"device,omitempty"`
	DRBDResource    DRBDResourceConfig `json:"drbdResource"`
}

// Peer defines the peer information
// +k8s:deepcopy-gen=true
type Peer struct {
	NodeID   int     `json:"nodeID"`
	NodeName string  `json:"nodeName"`
	Diskless bool    `json:"diskless"`
	Address  Address `json:"address"`
}

// DRBDResourceConfig defines the resource config
// +k8s:deepcopy-gen=true
type DRBDResourceConfig struct {
	Options map[string]string `json:"options"`
	Net     DRBDNetConfig     `json:"net"`
}

// DRBDNetConfig defines net config
// +k8s:deepcopy-gen=true
type DRBDNetConfig struct {
	CramHmacAlg       string `json:"cram-hmac-alg"`
	SharedSecret      string `json:"shared-secret"`
	RrConflict        string `json:"rr-conflict"`
	VerifyAlg         string `json:"verify-alg"`
	AllowTwoPrimaries string `json:"allow-two-primaries"`
}

// DRBDResourceStatus defines the observed state of DRBDResource
// +k8s:deepcopy-gen=true
type DRBDResourceStatus struct {
	BackingDisk   string             `json:"backingDisk"`
	Size          int64              `json:"size"`
	AllocatedSize int64              `json:"allocatedSize"`
	Peers         map[string]Peer    `json:"peers"`
	DRBDResource  DRBDResourceConfig `json:"drbdResource"`
	Conditions    []Condition        `json:"conditions"`
}
