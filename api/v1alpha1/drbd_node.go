package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DRBDNodeSpec defines the specification for DRBDNode.
// +k8s:deepcopy-gen=true
type DRBDNodeSpec struct {
	NetworkPools map[string]NetworkPool `json:"networkPools"`
}

// NetworkPool defines the structure for network pools.
// +k8s:deepcopy-gen=true
type NetworkPool struct {
	Address Address `json:"address"`
}

// Address defines the structure for addresses.
// +k8s:deepcopy-gen=true
type Address struct {
	IPv4 string `json:"ipv4"`
}

// DRBDNodeStatus defines the status for DRBDNode.
// +k8s:deepcopy-gen=true
type DRBDNodeStatus struct {
	Conditions []Condition `json:"conditions"`
}

// Condition describes the state of the object.
// +k8s:deepcopy-gen=true
type Condition struct {
	LastTransitionTime metav1.Time `json:"lastTransitionTime"`
	Message            string      `json:"message"`
	Reason             string      `json:"reason"`
	Status             string      `json:"status"`
	Type               string      `json:"type"`
}

// DRBDNode represents an object for managing DRBD nodes.
// +k8s:deepcopy-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type DRBDNode struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DRBDNodeSpec   `json:"spec,omitempty"`
	Status            DRBDNodeStatus `json:"status,omitempty"`
}

// DRBDNodeList is the list of DRBDNodes
// +k8s:deepcopy-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type DRBDNodeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []DRBDNode `json:"items"`
}
