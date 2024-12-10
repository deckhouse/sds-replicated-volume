package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DRBDNodeSpec defines the specification for DRBDNode.
type DRBDNodeSpec struct {
	NetworkPools map[string]NetworkPool `json:"networkPools"`
}

// NetworkPool defines the structure for network pools.
type NetworkPool struct {
	Address Address `json:"address"`
}

// Address defines the structure for addresses.
type Address struct {
	IPv4 string `json:"ipv4"`
}

// DRBDNodeStatus defines the status for DRBDNode.
type DRBDNodeStatus struct {
	Conditions []Condition `json:"conditions"`
}

// Condition describes the state of the object.
type Condition struct {
	LastTransitionTime metav1.Time `json:"lastTransitionTime"`
	Message            string      `json:"message"`
	Reason             string      `json:"reason"`
	Status             string      `json:"status"`
	Type               string      `json:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// DRBDNode represents an object for managing DRBD nodes.
// +kubebuilder:resource:path=drbdnodes,scope=Namespaced
type DRBDNode struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DRBDNodeSpec   `json:"spec,omitempty"`
	Status            DRBDNodeStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DRBDNodeList contains a list of DRBDNode.
type DRBDNodeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DRBDNode `json:"items"`
}
