package v1alpha2

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
type ReplicatedVolume struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   ReplicatedVolumeSpec    `json:"spec"`
	Status *ReplicatedVolumeStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen=true
type ReplicatedVolumeSpec struct {
	Size     int64 `json:"size"`
	Replicas int64 `json:"replicas"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	SharedSecret string `json:"sharedSecret"`

	// +kubebuilder:validation:Required
	LVM LVMSpec `json:"lvm"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=TransZonal;Zonal;Ignored
	Topology string `json:"topology"`

	AttachmentRequested []string `json:"attachmentRequested"`
}

// +k8s:deepcopy-gen=true
type LVMSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Thin;Thick
	Type string `json:"type"` // Thin/Thick
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:Required
	LVMVolumeGroups []LVGSpec `json:"volumeGroups" patchStrategy:"merge" patchMergeKey:"name"`
}

// +k8s:deepcopy-gen=true
type LVGSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name"`

	ThinPoolName string `json:"thinPoolName,omitempty"` // only for Thin

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	Zone string `json:"zone"`
}

// +k8s:deepcopy-gen=true
type ReplicatedVolumeStatus struct {
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}

// +k8s:deepcopy-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
type ReplicatedVolumeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []ReplicatedVolume `json:"items"`
}
