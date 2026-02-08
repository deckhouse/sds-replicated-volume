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

// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=drbdrlop
// +kubebuilder:metadata:labels=module=sds-replicated-volume
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
type DRBDResourceListOperation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec DRBDResourceListOperationSpec `json:"spec"`
	// +patchStrategy=merge
	Status *DRBDResourceListOperationStatus `json:"status,omitempty" patchStrategy:"merge"`
}

// +kubebuilder:object:generate=true
type DRBDResourceListOperationSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=2
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:UniqueItems=true
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=253
	// +kubebuilder:validation:items:Pattern=`^[0-9A-Za-z.+_-]*$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="drbdResourceNames is immutable"
	// +listType=set
	DRBDResourceNames []string `json:"drbdResourceNames"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=ConfigurePeers
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="type is immutable"
	Type DRBDResourceListOperationType `json:"type"`
}

// +kubebuilder:object:generate=true
type DRBDResourceListOperationStatus struct {
	// Results holds per-resource results. Each agent SSA-applies its entry keyed by drbdResourceName.
	// +patchMergeKey=drbdResourceName
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=drbdResourceName
	// +optional
	Results []DRBDResourceListOperationResult `json:"results,omitempty" patchStrategy:"merge" patchMergeKey:"drbdResourceName"`
}

// +kubebuilder:object:generate=true
type DRBDResourceListOperationResult struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	DRBDResourceName string `json:"drbdResourceName"`

	// +optional
	Phase DRBDOperationPhase `json:"phase,omitempty"`

	// +kubebuilder:validation:MaxLength=1024
	// +optional
	Message string `json:"message,omitempty"`

	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`

	// +optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`
}

// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
type DRBDResourceListOperationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []DRBDResourceListOperation `json:"items"`
}

// DRBDResourceListOperationType represents the type of operation to perform on a list of DRBD resources.
type DRBDResourceListOperationType string

const (
	// DRBDResourceListOperationConfigurePeers configures spec.peers for each DRBDResource in the list using status.addresses from the others.
	DRBDResourceListOperationConfigurePeers DRBDResourceListOperationType = "ConfigurePeers"
)
