package v1alpha2

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type DistributedBlockDevice struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   DistributedBlockDeviceSpec    `json:"spec"`
	Status *DistributedBlockDeviceStatus `json:"status,omitempty"`
}

type DBD = DistributedBlockDevice

// +k8s:deepcopy-gen=true
type DistributedBlockDeviceSpec struct {
	Size  int64 `json:"size"`
	Nodes DistributedBlockDeviceNode
}

// +k8s:deepcopy-gen=true
type DistributedBlockDeviceNode struct {
}

type DBDSpec = DistributedBlockDeviceSpec

// +k8s:deepcopy-gen=true
type DistributedBlockDeviceStatus struct {
}

type DBDStatus = DistributedBlockDeviceStatus

// +k8s:deepcopy-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type DistributedBlockDeviceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []DistributedBlockDevice `json:"items"`
}

type DBDList = DistributedBlockDeviceList
