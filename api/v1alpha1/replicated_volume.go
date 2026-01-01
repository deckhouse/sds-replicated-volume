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
	"fmt"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=rv
// +kubebuilder:metadata:labels=module=sds-replicated-volume
// +kubebuilder:validation:XValidation:rule="size(self.metadata.name) <= 120",message="metadata.name must be at most 120 characters (to fit derived RVR/LLV names)"
// +kubebuilder:printcolumn:name="IOReady",type=string,JSONPath=".status.conditions[?(@.type=='IOReady')].status"
// +kubebuilder:printcolumn:name="Size",type=string,JSONPath=".spec.size"
// +kubebuilder:printcolumn:name="ActualSize",type=string,JSONPath=".status.actualSize"
// +kubebuilder:printcolumn:name="DiskfulReplicas",type=string,JSONPath=".status.diskfulReplicaCount"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
type ReplicatedVolume struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec ReplicatedVolumeSpec `json:"spec"`
	// +patchStrategy=merge
	Status ReplicatedVolumeStatus `json:"status,omitempty" patchStrategy:"merge"`
}

// +kubebuilder:object:generate=true
type ReplicatedVolumeSpec struct {
	// +kubebuilder:validation:Required
	Size resource.Quantity `json:"size"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ReplicatedStorageClassName string `json:"replicatedStorageClassName"`
}

// +kubebuilder:object:generate=true
type ReplicatedVolumeStatus struct {
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// +patchStrategy=merge
	// +optional
	DRBD *DRBDResource `json:"drbd,omitempty" patchStrategy:"merge"`

	// DeviceMinor is a unique DRBD device minor number assigned to this ReplicatedVolume.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1048575
	// +optional
	DeviceMinor *uint32 `json:"deviceMinor,omitempty"`

	// +kubebuilder:validation:MaxItems=2
	// +kubebuilder:validation:Items={type=string,minLength=1,maxLength=253}
	// +optional
	ActuallyAttachedTo []string `json:"actuallyAttachedTo,omitempty"`

	// DesiredAttachTo is the desired set of nodes where the volume should be attached (up to 2 nodes).
	// It is computed by controllers from ReplicatedVolumeAttachment (RVA) objects.
	// +kubebuilder:validation:MaxItems=2
	// +kubebuilder:validation:Items={type=string,minLength=1,maxLength=253}
	// +optional
	DesiredAttachTo []string `json:"desiredAttachTo,omitempty"`

	// +optional
	ActualSize *resource.Quantity `json:"actualSize,omitempty"`

	// +optional
	Phase string `json:"phase,omitempty"`

	// DiskfulReplicaCount represents the current and desired number of diskful replicas in format "current/desired"
	// Example: "2/3" means 2 current diskful replicas out of 3 desired
	// +optional
	DiskfulReplicaCount string `json:"diskfulReplicaCount,omitempty"`

	// DiskfulReplicasInSync represents the number of diskful replicas that are in sync in format "inSync/total"
	// Example: "2/3" means 2 diskful replicas are in sync out of 3 total diskful replicas
	// +optional
	DiskfulReplicasInSync string `json:"diskfulReplicasInSync,omitempty"`

	// AttachedAndIOReadyCount represents the number of attached replicas that are IOReady in format "ready/attached"
	// Example: "1/2" means 1 replica is IOReady out of 2 attached
	// +optional
	AttachedAndIOReadyCount string `json:"attachedAndIOReadyCount,omitempty"`
}

func (s *ReplicatedVolumeStatus) HasDeviceMinor() bool {
	return s != nil && s.DeviceMinor != nil
}

func (s *ReplicatedVolumeStatus) GetDeviceMinor() (uint32, bool) {
	if s == nil || s.DeviceMinor == nil {
		return 0, false
	}
	return *s.DeviceMinor, true
}

func (s *ReplicatedVolumeStatus) SetDeviceMinor(v uint32) (changed bool) {
	// Keep validation in sync with kubebuilder tags on the field:
	// Minimum=0, Maximum=1048575.
	if v < RVMinDeviceMinor || v > RVMaxDeviceMinor {
		panic(fmt.Sprintf("ReplicatedVolumeStatus.DeviceMinor=%d is out of allowed range [%d..%d]", v, RVMinDeviceMinor, RVMaxDeviceMinor))
	}

	if s.DeviceMinor != nil && *s.DeviceMinor == v {
		return false
	}
	s.DeviceMinor = &v
	return true
}

func (s *ReplicatedVolumeStatus) SetDeviceMinorPtr(deviceMinor *uint32) (changed bool) {
	if deviceMinor == nil {
		return s.ClearDeviceMinor()
	}
	return s.SetDeviceMinor(*deviceMinor)
}

func (s *ReplicatedVolumeStatus) DeviceMinorEquals(deviceMinor *uint32) bool {
	current, ok := s.GetDeviceMinor()
	return deviceMinor == nil && !ok || deviceMinor != nil && ok && current == *deviceMinor
}

func (s *ReplicatedVolumeStatus) ClearDeviceMinor() (changed bool) {
	if s == nil || s.DeviceMinor == nil {
		return false
	}
	s.DeviceMinor = nil
	return true
}

// GetConditions/SetConditions are kept for compatibility with upstream helper interfaces
// (e.g. sigs.k8s.io/cluster-api/util/conditions.Getter/Setter).
func (s *ReplicatedVolumeStatus) GetConditions() []metav1.Condition {
	return s.Conditions
}

func (s *ReplicatedVolumeStatus) SetConditions(conditions []metav1.Condition) {
	s.Conditions = conditions
}

// +kubebuilder:object:generate=true
type DRBDResource struct {
	// +patchStrategy=merge
	// +optional
	Config *DRBDResourceConfig `json:"config,omitempty" patchStrategy:"merge"`
}

// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
type ReplicatedVolumeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []ReplicatedVolume `json:"items"`
}

// +kubebuilder:object:generate=true
type DRBDResourceConfig struct {
	// +optional
	// +kubebuilder:validation:MinLength=1
	SharedSecret string `json:"sharedSecret,omitempty"`

	// +optional
	// +kubebuilder:validation:Enum=SHA256;SHA1;DummyForTest
	SharedSecretAlg SharedSecretAlg `json:"sharedSecretAlg,omitempty"`

	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=8
	Quorum byte `json:"quorum,omitempty"`

	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=8
	QuorumMinimumRedundancy byte `json:"quorumMinimumRedundancy,omitempty"`

	// +kubebuilder:default=false
	AllowTwoPrimaries bool `json:"allowTwoPrimaries,omitempty"`
}
