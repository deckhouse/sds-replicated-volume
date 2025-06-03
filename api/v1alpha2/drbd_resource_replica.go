package v1alpha2

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type DRBDResourceReplica struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   DRBDResourceReplicaSpec    `json:"spec"`
	Status *DRBDResourceReplicaStatus `json:"status,omitempty"`
}

func (rr *DRBDResourceReplica) ResourceName() string {
	var resourceName string
	for _, ownerRef := range rr.OwnerReferences {
		if ownerRef.APIVersion == APIVersion &&
			ownerRef.Kind == "DRBDResource" {
			resourceName = ownerRef.Name
			// last owner wins
		}
	}
	return resourceName
}

func (rr *DRBDResourceReplica) NodeName() string {
	return rr.Labels[NodeNameLabelKey]
}

func (rr *DRBDResourceReplica) UniqueIndexName() string {
	return "uniqueIndex"
}

func (rr *DRBDResourceReplica) UniqueIndexKey() string {
	rn := rr.ResourceName()
	nn := rr.NodeName()
	if rn == "" || nn == "" {
		return ""
	}
	return rr.ResourceName() + "@" + rr.NodeName()
}

// +k8s:deepcopy-gen=true
type DRBDResourceReplicaSpec struct {
	Peers map[string]Peer `json:"peers,omitempty"`

	Diskless bool `json:"diskless,omitempty"`
}

// +k8s:deepcopy-gen=true
type Peer struct {
	Address Address `json:"address"`
}

// +k8s:deepcopy-gen=true
type Address struct {
	IPv4 string `json:"ipv4"`
}

// +k8s:deepcopy-gen=true
type DRBDResourceReplicaStatus struct {
	Conditions []metav1.Condition `json:"conditions"`
}

// +k8s:deepcopy-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type DRBDResourceReplicaList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []DRBDResourceReplica `json:"items"`
}
