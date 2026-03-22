/*
Copyright 2026 Flant JSC

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
// +kubebuilder:resource:scope=Cluster,shortName=drbdm
// +kubebuilder:metadata:labels=module=sds-replicated-volume
// +kubebuilder:printcolumn:name="Node",type=string,JSONPath=".spec.nodeName"
// +kubebuilder:printcolumn:name="LowerDevice",type=string,JSONPath=".spec.lowerDevicePath"
// +kubebuilder:printcolumn:name="UpperDevice",type=string,JSONPath=".status.upperDevicePath"
// +kubebuilder:printcolumn:name="OpenCount",type=integer,JSONPath=".status.openCount"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
type DRBDMapper struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec DRBDMapperSpec `json:"spec"`

	// +optional
	Status DRBDMapperStatus `json:"status,omitempty"`
}

// GetStatusConditions is an adapter method to satisfy objutilv1.StatusConditionObject.
func (d *DRBDMapper) GetStatusConditions() []metav1.Condition {
	return d.Status.Conditions
}

// SetStatusConditions is an adapter method to satisfy objutilv1.StatusConditionObject.
func (d *DRBDMapper) SetStatusConditions(conditions []metav1.Condition) {
	d.Status.Conditions = conditions
}

// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
type DRBDMapperList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []DRBDMapper `json:"items"`
}

// +kubebuilder:object:generate=true
type DRBDMapperSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="nodeName is immutable"
	NodeName string `json:"nodeName"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=4096
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="lowerDevicePath is immutable"
	LowerDevicePath string `json:"lowerDevicePath"`
}

// +kubebuilder:object:generate=true
type DRBDMapperStatus struct {
	// +kubebuilder:validation:MaxLength=4096
	// +optional
	UpperDevicePath string `json:"upperDevicePath,omitempty"`

	// +optional
	OpenCount int32 `json:"openCount,omitempty"`

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}
