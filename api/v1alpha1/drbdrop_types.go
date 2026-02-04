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
// +kubebuilder:resource:scope=Cluster,shortName=drbdrop
// +kubebuilder:metadata:labels=module=sds-replicated-volume
// +kubebuilder:printcolumn:name="Resource",type=string,JSONPath=".spec.drbdResourceName"
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
type DRBDResourceOperation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec DRBDResourceOperationSpec `json:"spec"`
	// +patchStrategy=merge
	Status *DRBDResourceOperationStatus `json:"status,omitempty" patchStrategy:"merge"`
}

// +kubebuilder:object:generate=true
type DRBDResourceOperationSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[0-9A-Za-z.+_-]*$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="drbdResourceName is immutable"
	DRBDResourceName string `json:"drbdResourceName"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=CreateNewUUID;ForcePrimary;Invalidate;Outdate;Verify;CreateSnapshot
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="type is immutable"
	Type DRBDResourceOperationType `json:"type"`

	// Parameters for CreateNewUUID operation. Immutable once set.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="createNewUUID is immutable"
	// +optional
	CreateNewUUID *CreateNewUUIDParams `json:"createNewUUID,omitempty"`
}

// +kubebuilder:object:generate=true
//
//	+kubebuilder:validation:XValidation:rule="!(self.clearBitmap && self.forceResync)",message="clearBitmap and forceResync are mutually exclusive"
type CreateNewUUIDParams struct {
	// +kubebuilder:default=false
	// +optional
	ClearBitmap bool `json:"clearBitmap,omitempty"`
	// +kubebuilder:default=false
	// +optional
	ForceResync bool `json:"forceResync,omitempty"`
}

// +kubebuilder:object:generate=true
type DRBDResourceOperationStatus struct {
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

// DRBDOperationPhase represents the phase of a DRBD operation.
type DRBDOperationPhase string

const (
	// DRBDOperationPhasePending indicates the operation is pending.
	DRBDOperationPhasePending DRBDOperationPhase = "Pending"
	// DRBDOperationPhaseRunning indicates the operation is running.
	DRBDOperationPhaseRunning DRBDOperationPhase = "Running"
	// DRBDOperationPhaseSucceeded indicates the operation completed successfully.
	DRBDOperationPhaseSucceeded DRBDOperationPhase = "Succeeded"
	// DRBDOperationPhaseFailed indicates the operation failed.
	DRBDOperationPhaseFailed DRBDOperationPhase = "Failed"
)

// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
type DRBDResourceOperationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []DRBDResourceOperation `json:"items"`
}

// DRBDResourceOperationType represents the type of operation to perform on a DRBD resource.
type DRBDResourceOperationType string

const (
	// DRBDResourceOperationCreateNewUUID creates a new UUID for the resource.
	DRBDResourceOperationCreateNewUUID DRBDResourceOperationType = "CreateNewUUID"
	// DRBDResourceOperationForcePrimary forces the resource to become primary.
	DRBDResourceOperationForcePrimary DRBDResourceOperationType = "ForcePrimary"
	// DRBDResourceOperationInvalidate invalidates the resource data.
	DRBDResourceOperationInvalidate DRBDResourceOperationType = "Invalidate"
	// DRBDResourceOperationOutdate marks the resource as outdated.
	DRBDResourceOperationOutdate DRBDResourceOperationType = "Outdate"
	// DRBDResourceOperationVerify verifies data consistency with peers.
	DRBDResourceOperationVerify DRBDResourceOperationType = "Verify"
	// DRBDResourceOperationCreateSnapshot creates a snapshot of the resource.
	DRBDResourceOperationCreateSnapshot DRBDResourceOperationType = "CreateSnapshot"
)
