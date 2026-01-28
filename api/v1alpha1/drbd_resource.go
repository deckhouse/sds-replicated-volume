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
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=drbdr
// +kubebuilder:metadata:labels=module=sds-replicated-volume
// +kubebuilder:printcolumn:name="Node",type=string,JSONPath=".spec.nodeName"
// +kubebuilder:printcolumn:name="State",type=string,JSONPath=".spec.state"
// +kubebuilder:printcolumn:name="Role",type=string,JSONPath=".status.role"
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="DiskState",type=string,JSONPath=".status.diskState"
// +kubebuilder:printcolumn:name="Quorum",type=boolean,JSONPath=".status.quorum"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
// +kubebuilder:validation:XValidation:rule="self.spec.type == 'Diskful' ? has(self.spec.lvmLogicalVolumeName) && size(self.spec.lvmLogicalVolumeName) > 0 : !has(self.spec.lvmLogicalVolumeName) || size(self.spec.lvmLogicalVolumeName) == 0",message="lvmLogicalVolumeName is required when type is Diskful and must be empty when type is Diskless"
// +kubebuilder:validation:XValidation:rule="self.spec.type == 'Diskful' ? has(self.spec.size) : !has(self.spec.size)",message="size is required when type is Diskful and must be empty when type is Diskless"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.spec.size) || !has(self.spec.size) || self.spec.size >= oldSelf.spec.size",message="spec.size cannot be decreased"
type DRBDResource struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec DRBDResourceSpec `json:"spec"`
	// +patchStrategy=merge
	// +optional
	Status DRBDResourceStatus `json:"status,omitempty" patchStrategy:"merge"`
}

// GetStatusConditions is an adapter method to satisfy objutilv1.StatusConditionObject.
// It returns the root object's `.status.conditions`.
func (d *DRBDResource) GetStatusConditions() []metav1.Condition {
	return d.Status.Conditions
}

// SetStatusConditions is an adapter method to satisfy objutilv1.StatusConditionObject.
// It sets the root object's `.status.conditions`.
func (d *DRBDResource) SetStatusConditions(conditions []metav1.Condition) {
	d.Status.Conditions = conditions
}

func (d *DRBDResource) DRBDResourceNameOnTheNode() string {
	if d.Spec.ActualNameOnTheNode != "" {
		return d.Spec.ActualNameOnTheNode
	}
	return fmt.Sprintf("sdsrv-%s", d.Name)
}

func ParseDRBDResourceNameOnTheNode(s string) (string, bool) {
	return strings.CutPrefix(s, "sdsrv-")
}

// +kubebuilder:object:generate=true
type DRBDResourceSpec struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[0-9A-Za-z.+_-]*$`
	// +optional
	ActualNameOnTheNode string `json:"actualNameOnTheNode,omitempty"`

	// +kubebuilder:validation:Enum=Up;Down
	// +kubebuilder:default=Up
	// +optional
	State DRBDResourceState `json:"state,omitempty"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="nodeName is immutable"
	NodeName string `json:"nodeName"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=64
	SystemNetworks []string `json:"systemNetworks"`

	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=32
	// +optional
	Quorum byte `json:"quorum,omitempty"`

	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=32
	// +optional
	QuorumMinimumRedundancy byte `json:"quorumMinimumRedundancy,omitempty"`

	// Required when type is Diskful, must be empty when type is Diskless.
	// +optional
	Size *resource.Quantity `json:"size,omitempty"`

	// +kubebuilder:validation:Enum=Primary;Secondary
	// +optional
	// +kubebuilder:default=Secondary
	Role DRBDRole `json:"role,omitempty"`

	// +kubebuilder:default=false
	// +optional
	AllowTwoPrimaries bool `json:"allowTwoPrimaries,omitempty"`

	// +kubebuilder:validation:Enum=Diskful;Diskless
	// +kubebuilder:default=Diskful
	// +optional
	Type DRBDResourceType `json:"type,omitempty"`

	// Required when type is Diskful, must be empty when type is Diskless.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +optional
	LVMLogicalVolumeName string `json:"lvmLogicalVolumeName,omitempty"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=31
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="nodeID is immutable"
	NodeID uint `json:"nodeID"`

	// +patchMergeKey=name
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=31
	// +optional
	Peers []DRBDResourcePeer `json:"peers,omitempty" patchStrategy:"merge" patchMergeKey:"name"`

	// Maintenance mode - when set, reconciliation is paused but status is still updated
	// +kubebuilder:validation:Enum=NoResourceReconciliation
	// +optional
	Maintenance MaintenanceMode `json:"maintenance,omitempty"`
}

// MaintenanceMode represents the maintenance mode of a DRBD resource.
type MaintenanceMode string

const (
	// MaintenanceModeNoResourceReconciliation pauses reconciliation but status is still updated.
	MaintenanceModeNoResourceReconciliation MaintenanceMode = "NoResourceReconciliation"
)

// +kubebuilder:object:generate=true
type DRBDResourcePeer struct {
	// Peer node name. Immutable, used as list map key.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[0-9A-Za-z.+_-]*$`
	Name string `json:"name"`

	// +kubebuilder:validation:Enum=Diskful;Diskless
	// +kubebuilder:default=Diskful
	// +optional
	Type DRBDResourceType `json:"type,omitempty"`

	// +kubebuilder:default=true
	// +optional
	AllowRemoteRead bool `json:"allowRemoteRead,omitempty"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=31
	NodeID uint `json:"nodeID"`

	// +kubebuilder:validation:Enum=A;B;C
	// +kubebuilder:default=C
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="protocol is immutable"
	// +optional
	Protocol DRBDProtocol `json:"protocol,omitempty"`

	// +kubebuilder:validation:MaxLength=256
	// +optional
	SharedSecret string `json:"sharedSecret,omitempty"`

	// +kubebuilder:validation:Enum=SHA256;SHA1;DummyForTest
	// +optional
	SharedSecretAlg SharedSecretAlg `json:"sharedSecretAlg,omitempty"`

	// +kubebuilder:default=false
	// +optional
	PauseSync bool `json:"pauseSync,omitempty"`

	// +patchMergeKey=systemNetworkName
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=systemNetworkName
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	Paths []DRBDResourcePath `json:"paths" patchStrategy:"merge" patchMergeKey:"systemNetworkName"`
}

// +kubebuilder:object:generate=true
type DRBDResourcePath struct {
	// System network name. Immutable, used as list map key.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	SystemNetworkName string `json:"systemNetworkName"`

	// +kubebuilder:validation:Required
	Address DRBDAddress `json:"address"`
}

// +kubebuilder:object:generate=true
type DRBDAddress struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^(?:(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)$`
	IPv4 string `json:"ipv4"`

	// +kubebuilder:validation:Minimum=1025
	// +kubebuilder:validation:Maximum=65535
	Port uint `json:"port"`
}

// +kubebuilder:object:generate=true
type DRBDResourceStatus struct {
	// Device path, e.g. /dev/drbd10012 or /dev/sds-replicated/<rvrName>
	// Only present on primary
	// +kubebuilder:validation:MaxLength=256
	// +optional
	Device string `json:"device,omitempty"`

	// +kubebuilder:validation:MaxItems=32
	// +optional
	Addresses []DRBDResourceAddressStatus `json:"addresses,omitempty"`

	// +patchStrategy=merge
	// +optional
	ActiveConfiguration *DRBDResourceActiveConfiguration `json:"activeConfiguration,omitempty" patchStrategy:"merge"`

	// +patchMergeKey=name
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=31
	// +optional
	Peers []DRBDResourcePeerStatus `json:"peers,omitempty" patchStrategy:"merge" patchMergeKey:"name"`

	// +optional
	DiskState DiskState `json:"diskState,omitempty"`

	// +optional
	Quorum *bool `json:"quorum,omitempty"`

	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}

// +kubebuilder:object:generate=true
type DRBDResourceAddressStatus struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=64
	SystemNetworkName string `json:"systemNetworkName"`

	// +kubebuilder:validation:Required
	Address DRBDAddress `json:"address"`
}

// +kubebuilder:object:generate=true
type DRBDResourceActiveConfiguration struct {
	// +optional
	Quorum *byte `json:"quorum,omitempty"`

	// +optional
	QuorumMinimumRedundancy *byte `json:"quorumMinimumRedundancy,omitempty"`

	// +kubebuilder:validation:Enum=Up;Down
	// +optional
	State DRBDResourceState `json:"state,omitempty"`

	// +optional
	Size *resource.Quantity `json:"size,omitempty"`

	// +kubebuilder:validation:Enum=Primary;Secondary
	// +optional
	Role DRBDRole `json:"role,omitempty"`

	// +optional
	AllowTwoPrimaries *bool `json:"allowTwoPrimaries,omitempty"`

	// +kubebuilder:validation:Enum=Diskful;Diskless
	// +optional
	Type DRBDResourceType `json:"type,omitempty"`

	// Disk path, e.g. /dev/...
	// +kubebuilder:validation:MaxLength=256
	// +optional
	Disk string `json:"disk,omitempty"`
}

// +kubebuilder:object:generate=true
type DRBDResourcePeerStatus struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// +kubebuilder:validation:Enum=Diskful;Diskless
	// +optional
	Type DRBDResourceType `json:"type,omitempty"`

	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=31
	// +optional
	NodeID *uint `json:"nodeID,omitempty"`

	// +patchMergeKey=systemNetworkName
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=systemNetworkName
	// +kubebuilder:validation:MaxItems=16
	// +optional
	Paths []DRBDResourcePathStatus `json:"paths,omitempty" patchStrategy:"merge" patchMergeKey:"systemNetworkName"`

	// +optional
	ConnectionState ConnectionState `json:"connectionState,omitempty"`

	// +optional
	DiskState DiskState `json:"diskState,omitempty"`
}

// +kubebuilder:object:generate=true
type DRBDResourcePathStatus struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	SystemNetworkName string `json:"systemNetworkName"`

	// +kubebuilder:validation:Required
	Address DRBDAddress `json:"address"`

	// +optional
	Established bool `json:"established,omitempty"`
}

// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
type DRBDResourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []DRBDResource `json:"items"`
}
