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

// ReplicatedVolumeOperation represents an operation to update volume configuration
// or resolve eligible nodes conflicts for a ReplicatedVolume.
//
// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=rvo
// +kubebuilder:metadata:labels=module=sds-replicated-volume
// +kubebuilder:printcolumn:name="Volume",type=string,JSONPath=".spec.replicatedVolumeName"
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Completed",type=string,JSONPath=".status.conditions[?(@.type=='Completed')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
type ReplicatedVolumeOperation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec ReplicatedVolumeOperationSpec `json:"spec"`
	// +patchStrategy=merge
	Status ReplicatedVolumeOperationStatus `json:"status,omitempty" patchStrategy:"merge"`
}

// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
type ReplicatedVolumeOperationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []ReplicatedVolumeOperation `json:"items"`
}

// GetStatusConditions is an adapter method to satisfy objutilv1.StatusConditionObject.
// It returns the root object's `.status.conditions`.
func (o *ReplicatedVolumeOperation) GetStatusConditions() []metav1.Condition {
	return o.Status.Conditions
}

// SetStatusConditions is an adapter method to satisfy objutilv1.StatusConditionObject.
// It sets the root object's `.status.conditions`.
func (o *ReplicatedVolumeOperation) SetStatusConditions(conditions []metav1.Condition) {
	o.Status.Conditions = conditions
}

// ReplicatedVolumeOperationSpec defines the desired state of a ReplicatedVolumeOperation.
// All fields are immutable after creation.
//
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable"
// +kubebuilder:object:generate=true
type ReplicatedVolumeOperationSpec struct {
	// ReplicatedVolumeName is the name of the ReplicatedVolume this operation targets.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=120
	ReplicatedVolumeName string `json:"replicatedVolumeName"`

	// Type specifies the operation type.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=UpdateConfiguration;ResolveEligibleNodesConflict
	Type ReplicatedVolumeOperationType `json:"type"`

	// ResolveEligibleNodesConflict indicates whether this operation should also resolve
	// eligible nodes conflicts when updating configuration. Only meaningful when Type is
	// UpdateConfiguration. Determined by EligibleNodesConflictResolutionStrategy.
	// +kubebuilder:default=false
	// +optional
	ResolveEligibleNodesConflict bool `json:"resolveEligibleNodesConflict,omitempty"`
}

// ReplicatedVolumeOperationType enumerates possible operation types.
type ReplicatedVolumeOperationType string

const (
	// ReplicatedVolumeOperationTypeUpdateConfiguration updates the volume configuration
	// to match the current storage class configuration.
	ReplicatedVolumeOperationTypeUpdateConfiguration ReplicatedVolumeOperationType = "UpdateConfiguration"
	// ReplicatedVolumeOperationTypeResolveEligibleNodesConflict moves replicas
	// from non-eligible nodes to eligible nodes.
	ReplicatedVolumeOperationTypeResolveEligibleNodesConflict ReplicatedVolumeOperationType = "ResolveEligibleNodesConflict"
)

func (t ReplicatedVolumeOperationType) String() string { return string(t) }

// Short name suffixes for operation resource naming.
// Format: <rv-name>-<suffix>-<cursor>
const (
	// ReplicatedVolumeOperationNameSuffixUpdateConfig is the short suffix for UpdateConfiguration operations.
	ReplicatedVolumeOperationNameSuffixUpdateConfig = "updateConfig"
	// ReplicatedVolumeOperationNameSuffixResolveNodes is the short suffix for ResolveEligibleNodesConflict operations.
	ReplicatedVolumeOperationNameSuffixResolveNodes = "resolveNodes"
)

// NameSuffix returns the short suffix for generating operation resource names.
func (t ReplicatedVolumeOperationType) NameSuffix() string {
	switch t {
	case ReplicatedVolumeOperationTypeUpdateConfiguration:
		return ReplicatedVolumeOperationNameSuffixUpdateConfig
	case ReplicatedVolumeOperationTypeResolveEligibleNodesConflict:
		return ReplicatedVolumeOperationNameSuffixResolveNodes
	default:
		return string(t)
	}
}

// ReplicatedVolumeOperationStatus represents the current state of a ReplicatedVolumeOperation.
// +kubebuilder:object:generate=true
type ReplicatedVolumeOperationStatus struct {
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}
