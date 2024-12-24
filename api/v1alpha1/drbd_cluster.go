/*
Copyright 2025 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DRBDClusterSpec defines the desired state of DRBDCluster
// +k8s:deepcopy-gen=true
type DRBDClusterSpec struct {
	Replicas                  int32                      `json:"replicas"`
	QuorumPolicy              string                     `json:"quorumPolicy"`
	NetworkPoolName           string                     `json:"networkPoolName"`
	SharedSecret              string                     `json:"sharedSecret"`
	Size                      int64                      `json:"size"`
	DrbdCurrentGi             string                     `json:"drbdCurrentGi"`
	Port                      int32                      `json:"port"`
	Minor                     int                        `json:"minor"`
	AttachmentRequested       []string                   `json:"attachmentRequested"`
	TopologySpreadConstraints []TopologySpreadConstraint `json:"topologySpreadConstraints,omitempty"`
	Affinity                  Affinity                   `json:"affinity,omitempty"`
	AutoDiskful               AutoDiskful                `json:"autoDiskful,omitempty"`
	AutoRecovery              AutoRecovery               `json:"autoRecovery,omitempty"`
	StoragePoolSelector       []metav1.LabelSelector     `json:"storagePoolSelector,omitempty"`
}

// TopologySpreadConstraint specifies topology constraints
// +k8s:deepcopy-gen=true
type TopologySpreadConstraint struct {
	MaxSkew           int    `json:"maxSkew"`
	TopologyKey       string `json:"topologyKey"`
	WhenUnsatisfiable string `json:"whenUnsatisfiable"`
}

// Affinity defines node affinity scheduling rules
// +k8s:deepcopy-gen=true
type Affinity struct {
	NodeAffinity NodeAffinity `json:"nodeAffinity,omitempty"`
}

// NodeAffinity specifies node selection criteria
// +k8s:deepcopy-gen=true
type NodeAffinity struct {
	RequiredDuringSchedulingIgnoredDuringExecution NodeSelector `json:"requiredDuringSchedulingIgnoredDuringExecution,omitempty"`
}

// NodeSelector represents constraints to match nodes
// +k8s:deepcopy-gen=true
type NodeSelector struct {
	NodeSelectorTerms []NodeSelectorTerm `json:"nodeSelectorTerms"`
}

// NodeSelectorTerm defines node selection conditions
// +k8s:deepcopy-gen=true
type NodeSelectorTerm struct {
	MatchExpressions []metav1.LabelSelectorRequirement `json:"matchExpressions"`
}

// AutoDiskful represents auto-diskful settings
// +k8s:deepcopy-gen=true
type AutoDiskful struct {
	DelaySeconds int `json:"delaySeconds"`
}

// AutoRecovery represents auto-recovery settings
// +k8s:deepcopy-gen=true
type AutoRecovery struct {
	DelaySeconds int `json:"delaySeconds"`
}

// DRBDClusterStatus defines the observed state of DRBDCluster
// +k8s:deepcopy-gen=true
type DRBDClusterStatus struct {
	Size                int64              `json:"size"`
	AttachmentCompleted []string           `json:"attachmentCompleted"`
	Conditions          []metav1.Condition `json:"conditions"`
}

// DRBDCluster is the Schema for the drbdclusters API
// +k8s:deepcopy-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type DRBDCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DRBDClusterSpec   `json:"spec"`
	Status DRBDClusterStatus `json:"status,omitempty"`
}
