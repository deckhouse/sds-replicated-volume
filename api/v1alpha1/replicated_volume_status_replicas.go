package v1alpha1

import (
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:generate=true
type RVReplicaInfo struct {
	// +optional
	Name string `json:"name,omitempty"`

	// +optional
	NodeName string `json:"nodeName,omitempty"`

	// +optional
	Type ReplicaType `json:"type,omitempty"`

	// +optional
	DeletionTimestamp *v1.Time `json:"deletionTimestamp,omitempty" protobuf:"bytes,9,opt,name=deletionTimestamp"`
}
