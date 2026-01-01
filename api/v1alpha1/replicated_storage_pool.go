/*
Copyright 2023 Flant JSC

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

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// ReplicatedStoragePool is a Kubernetes Custom Resource that defines a configuration for Linstor Storage-pools.
// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=rsp
// +kubebuilder:metadata:labels=heritage=deckhouse
// +kubebuilder:metadata:labels=module=sds-replicated-volume
// +kubebuilder:metadata:labels=backup.deckhouse.io/cluster-config=true
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Reason",type=string,priority=1,JSONPath=`.status.reason`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`,description="The age of this resource"
type ReplicatedStoragePool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ReplicatedStoragePoolSpec   `json:"spec"`
	Status            ReplicatedStoragePoolStatus `json:"status,omitempty"`
}

// Defines desired rules for Linstor's Storage-pools.
// +kubebuilder:object:generate=true
type ReplicatedStoragePoolSpec struct {
	// Defines the volumes type. Might be:
	// - LVM (for Thick)
	// - LVMThin (for Thin)
	// +kubebuilder:validation:Enum=LVM;LVMThin
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable."
	Type string `json:"type"`
	// An array of names of LVMVolumeGroup resources, whose Volume Groups/Thin-pools will be used to allocate
	// the required space.
	//
	// > Note that every LVMVolumeGroup resource has to have the same type Thin/Thick
	// as it is in current resource's 'Spec.Type' field.
	LVMVolumeGroups []ReplicatedStoragePoolLVMVolumeGroups `json:"lvmVolumeGroups"`
}

type ReplicatedStoragePoolLVMVolumeGroups struct {
	// Selected LVMVolumeGroup resource's name.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-.]{0,251}[a-z0-9])?$`
	Name string `json:"name"`
	// Selected Thin-pool name.
	ThinPoolName string `json:"thinPoolName,omitempty"`
}

// Displays current information about the state of the LINSTOR storage pool.
// +kubebuilder:object:generate=true
type ReplicatedStoragePoolStatus struct {
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// The actual ReplicatedStoragePool resource's state. Might be:
	// - Completed (if the controller received correct resource configuration and Linstor Storage-pools configuration is up-to-date)
	// - Updating (if the controller received correct resource configuration and Linstor Storage-pools configuration needs to be updated)
	// - Failed (if the controller received incorrect resource configuration or an error occurs during the operation)
	// +kubebuilder:validation:Enum=Updating;Failed;Completed
	Phase string `json:"phase,omitempty"`
	// The additional information about the resource's current state.
	Reason string `json:"reason,omitempty"`
}

// ReplicatedStoragePoolList contains a list of ReplicatedStoragePool
// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
type ReplicatedStoragePoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []ReplicatedStoragePool `json:"items"`
}
