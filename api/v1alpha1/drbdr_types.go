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
// +kubebuilder:printcolumn:name="Role",type=string,JSONPath=".status.activeConfiguration.role"
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=".status.activeConfiguration.type"
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

	// +optional
	Status DRBDResourceStatus `json:"status,omitempty"`
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
	// +kubebuilder:validation:MaxItems=10
	// +kubebuilder:validation:items:MaxLength=64
	// +listType=set
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
	NodeID uint8 `json:"nodeID"`

	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=31
	// +optional
	Peers []DRBDResourcePeer `json:"peers,omitempty"`

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
	NodeID uint8 `json:"nodeID"`

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

	// +kubebuilder:validation:XValidation:rule="self.all(x, self.exists_one(y, x.systemNetworkName == y.systemNetworkName))",message="paths[].systemNetworkName must be unique"
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=10
	// +listType=atomic
	Paths []DRBDResourcePath `json:"paths"`
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
	// +kubebuilder:validation:MaxLength=15
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

	// DeviceIOSuspended indicates whether I/O is suspended on the device.
	// Only present on primary
	// +optional
	DeviceIOSuspended *bool `json:"deviceIOSuspended,omitempty"`

	// DeviceOpen indicates whether the block device is currently open by a process
	// (bd_openers > 0). Reflects DRBD's "open: yes/no" from drbdsetup status.
	// When true, demotion to Secondary will fail.
	// Only present on primary
	// +optional
	DeviceOpen *bool `json:"deviceOpen,omitempty"`

	// +kubebuilder:validation:XValidation:rule="self.all(x, self.exists_one(y, x.systemNetworkName == y.systemNetworkName))",message="addresses[].systemNetworkName must be unique"
	// +kubebuilder:validation:MaxItems=10
	// +listType=atomic
	// +optional
	Addresses []DRBDResourceAddressStatus `json:"addresses,omitempty"`

	// +optional
	ActiveConfiguration *DRBDResourceActiveConfiguration `json:"activeConfiguration,omitempty"`

	// +kubebuilder:validation:XValidation:rule="self.all(x, self.exists_one(y, x.nodeID == y.nodeID))",message="peers[].nodeID must be unique"
	// +kubebuilder:validation:MaxItems=31
	// +listType=atomic
	// +optional
	Peers []DRBDResourcePeerStatus `json:"peers,omitempty"`

	// +optional
	DiskState DiskState `json:"diskState,omitempty"`

	// +optional
	Quorum *bool `json:"quorum,omitempty"`

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
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

	// Type is the current effective DRBD configuration (diskful or diskless).
	// This reflects how DRBD is configured, not the transient disk state
	// (which could be DISKLESS, ATTACHING, DETACHING, etc.).
	// +kubebuilder:validation:Enum=Diskful;Diskless
	// +optional
	Type DRBDResourceType `json:"type,omitempty"`

	// LVMLogicalVolumeName is the LVM logical volume name currently configured in DRBD.
	// This reflects the actual backing volume that DRBD is using, not the desired spec value.
	// DRBD itself stores the block device path (e.g. /dev/vg/lv), and this field is
	// reverse-computed from that path to the LVMLogicalVolume name.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	LVMLogicalVolumeName string `json:"lvmLogicalVolumeName,omitempty"`
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

	// AllowRemoteRead indicates whether reads are allowed from this peer.
	// +optional
	AllowRemoteRead bool `json:"allowRemoteRead,omitempty"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=31
	NodeID uint `json:"nodeID"`

	// +kubebuilder:validation:XValidation:rule="self.all(x, self.exists_one(y, x.systemNetworkName == y.systemNetworkName))",message="paths[].systemNetworkName must be unique"
	// +kubebuilder:validation:MaxItems=10
	// +listType=atomic
	// +optional
	Paths []DRBDResourcePathStatus `json:"paths,omitempty"`

	// +optional
	ConnectionState ConnectionState `json:"connectionState,omitempty"`

	// +optional
	DiskState DiskState `json:"diskState,omitempty"`

	// ReplicationState is the DRBD replication state with this peer.
	// +optional
	ReplicationState ReplicationState `json:"replicationState,omitempty"`

	// Role is the DRBD role of this peer (Primary or Secondary).
	// +kubebuilder:validation:Enum=Primary;Secondary
	// +optional
	Role DRBDRole `json:"role,omitempty"`
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

// DRBDResourceState represents the desired state of a DRBD resource.
type DRBDResourceState string

const (
	// DRBDResourceStateUp indicates the resource should be up.
	DRBDResourceStateUp DRBDResourceState = "Up"
	// DRBDResourceStateDown indicates the resource should be down.
	DRBDResourceStateDown DRBDResourceState = "Down"
)

// DRBDRole represents the role of a DRBD resource.
type DRBDRole string

const (
	// DRBDRolePrimary indicates the resource is primary.
	DRBDRolePrimary DRBDRole = "Primary"
	// DRBDRoleSecondary indicates the resource is secondary.
	DRBDRoleSecondary DRBDRole = "Secondary"
)

// DRBDResourceType represents the type of a DRBD resource.
type DRBDResourceType string

const (
	// DRBDResourceTypeDiskful indicates a diskful resource that stores data.
	DRBDResourceTypeDiskful DRBDResourceType = "Diskful"
	// DRBDResourceTypeDiskless indicates a diskless resource.
	DRBDResourceTypeDiskless DRBDResourceType = "Diskless"
)

// DRBDProtocol represents the DRBD replication protocol.
type DRBDProtocol string

const (
	// DRBDProtocolA is asynchronous replication protocol.
	DRBDProtocolA DRBDProtocol = "A"
	// DRBDProtocolB is memory synchronous (semi-synchronous) replication protocol.
	DRBDProtocolB DRBDProtocol = "B"
	// DRBDProtocolC is synchronous replication protocol.
	DRBDProtocolC DRBDProtocol = "C"
)
