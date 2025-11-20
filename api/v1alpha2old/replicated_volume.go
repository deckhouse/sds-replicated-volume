package v1alpha2

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

	Spec   ReplicatedVolumeSpec    `json:"spec"`
	Status *ReplicatedVolumeStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen=true
type ReplicatedVolumeSpec struct {
	// +kubebuilder:validation:Required
	Size resource.Quantity `json:"size"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=8
	Replicas byte `json:"replicas"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	SharedSecret string `json:"sharedSecret"`

	// +kubebuilder:validation:Required
	LVM LVMSpec `json:"lvm"`

	// +kubebuilder:validation:MaxItems=1024
	// +kubebuilder:validation:Items={type=string,minLength=1,maxLength=253}
	Zones []string `json:"zones,omitempty"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=TransZonal;Zonal;Ignored
	Topology string `json:"topology"`

	// +kubebuilder:validation:MaxItems=2
	// +kubebuilder:validation:Items={type=string,minLength=1,maxLength=253}
	PublishRequested []string `json:"publishRequested"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Local;PreferablyLocal;EventuallyLocal;Any
	VolumeAccess string `json:"volumeAccess"`
}

// +k8s:deepcopy-gen=true
type LVMSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Thin;Thick
	Type string `json:"type"`

	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:Required
	LVMVolumeGroups []LVGRef `json:"volumeGroups" patchStrategy:"merge" patchMergeKey:"name"`
}

// +k8s:deepcopy-gen=true
type LVGRef struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name"`

	// +kubebuilder:validation:MaxLength=255
	ThinPoolName string `json:"thinPoolName,omitempty"` // only for Thin
}

// +k8s:deepcopy-gen=true
type ReplicatedVolumeStatus struct {
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// +kubebuilder:validation:MaxItems=2
	// +kubebuilder:validation:Items={type=string,minLength=1,maxLength=253}
	// +optional
	PublishProvided []string `json:"publishProvided,omitempty"`

	// +optional
	ActualSize resource.Quantity `json:"actualSize,omitempty"`
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
