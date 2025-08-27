package v1alpha2

import (
	// TODO: topologySpreadConstraints+affinity
	// corev1 "k8s.io/api/core/v1"
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
	// TODO: topologySpreadConstraints+affinity
	// TopologySpreadConstraints []corev1.TopologySpreadConstraint `json:"topologySpreadConstraints,omitempty"`
	// Affinity                  *corev1.Affinity                  `json:"affinity,omitempty"`
}

// +k8s:deepcopy-gen=true
type ReplicatedVolumeStatus struct {
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
