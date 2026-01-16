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
// +kubebuilder:resource:scope=Cluster,shortName=dno
// +kubebuilder:metadata:labels=module=sds-replicated-volume
// +kubebuilder:printcolumn:name="Node",type=string,JSONPath=".spec.nodeName"
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
type DRBDNodeOperation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec DRBDNodeOperationSpec `json:"spec"`
	// +patchStrategy=merge
	Status *DRBDNodeOperationStatus `json:"status,omitempty" patchStrategy:"merge"`
}

// +kubebuilder:object:generate=true
type DRBDNodeOperationSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="nodeName is immutable"
	NodeName string `json:"nodeName"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=UpdateDRBD
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="type is immutable"
	Type DRBDNodeOperationType `json:"type"`
}

// +kubebuilder:object:generate=true
type DRBDNodeOperationStatus struct {
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
type DRBDNodeOperationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []DRBDNodeOperation `json:"items"`
}
