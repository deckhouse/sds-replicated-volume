package v1alpha3

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=rv
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Size",type=string,JSONPath=".spec.size"
// +kubebuilder:printcolumn:name="ActualSize",type=string,JSONPath=".status.actualSize"
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=".spec.replicas"
// +kubebuilder:printcolumn:name="Topology",type=string,JSONPath=".spec.topology"
type ReplicatedVolume struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec ReplicatedVolumeSpec `json:"spec"`
	// +patchStrategy=merge
	Status *ReplicatedVolumeStatus `json:"status,omitempty" patchStrategy:"merge"`
}

// +k8s:deepcopy-gen=true
type ReplicatedVolumeSpec struct {
	// +kubebuilder:validation:Required
	Size resource.Quantity `json:"size"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ReplicatedStorageClassName string `json:"replicatedStorageClassName"`

	// +kubebuilder:validation:MaxItems=2
	// +kubebuilder:validation:Items={type=string,minLength=1,maxLength=253}
	PublishOn []string `json:"publishOn"`
}

// +k8s:deepcopy-gen=true
type ReplicatedVolumeStatus struct {
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// +patchStrategy=merge
	// +optional
	Config *DRBDResourceConfig `json:"config,omitempty" patchStrategy:"merge"`

	// +kubebuilder:validation:MaxItems=2
	// +kubebuilder:validation:Items={type=string,minLength=1,maxLength=253}
	// +optional
	PublishedOn []string `json:"publishedOn,omitempty"`

	// +optional
	ActualSize *resource.Quantity `json:"actualSize,omitempty"`

	// +optional
	Phase string `json:"phase,omitempty"`
}

func (s *ReplicatedVolumeStatus) GetConditions() []metav1.Condition {
	return s.Conditions
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

// +k8s:deepcopy-gen=true
type DRBDResourceConfig struct {
	// +optional
	// +kubebuilder:validation:MinLength=1
	SharedSecret string `json:"sharedSecret,omitempty"`

	// +optional
	// +kubebuilder:validation:MinLength=1
	SharedSecretAlg string `json:"sharedSecretAlg,omitempty"`

	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=7
	Quorum byte `json:"quorum"`

	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=7
	QuorumMinimumRedundancy byte `json:"quorumMinimumRedundancy"`

	// +kubebuilder:default=false
	AllowTwoPrimaries bool `json:"allowTwoPrimaries,omitempty"`

	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1048575
	DeviceMinor uint `json:"deviceMinor,omitempty"`
}
