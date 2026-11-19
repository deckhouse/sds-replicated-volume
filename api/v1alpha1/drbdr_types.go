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

// Low-level representation of a DRBD resource on a particular cluster node. The agent manages it;
// it describes the desired and the actual configuration of the DRBD device, its peers and its
// replication. It is not meant to be created or edited by hand.
//
// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=drbdr
// +kubebuilder:metadata:labels=module=sds-replicated-volume
// +kubebuilder:selectablefield:JSONPath=.spec.nodeName
// +kubebuilder:printcolumn:name="Node",type=string,JSONPath=".spec.nodeName"
// +kubebuilder:printcolumn:name="State",type=string,JSONPath=".status.activeConfiguration.state"
// +kubebuilder:printcolumn:name="Configured",type=string,JSONPath=".status.conditions[?(@.type=='Configured')].status"
// +kubebuilder:printcolumn:name="Role",type=string,JSONPath=".status.activeConfiguration.role"
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=".status.activeConfiguration.type"
// +kubebuilder:printcolumn:name="DiskState",type=string,JSONPath=".status.diskState"
// +kubebuilder:printcolumn:name="Quorum",type=boolean,JSONPath=".status.quorum"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Message",type=string,priority=1,JSONPath=".status.conditions[?(@.type=='Configured')].message"
// +kubebuilder:validation:XValidation:rule="self.spec.type == 'Diskful' ? has(self.spec.lvmLogicalVolumeName) && size(self.spec.lvmLogicalVolumeName) > 0 : !has(self.spec.lvmLogicalVolumeName) || size(self.spec.lvmLogicalVolumeName) == 0",message="lvmLogicalVolumeName is required when type is Diskful and must be empty when type is Diskless"
// +kubebuilder:validation:XValidation:rule="self.spec.type == 'Diskful' ? has(self.spec.size) : !has(self.spec.size)",message="size is required when type is Diskful and must be empty when type is Diskless"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.spec.size) || !has(self.spec.size) || !quantity(string(self.spec.size)).isLessThan(quantity(string(oldSelf.spec.size)))",message="spec.size cannot be decreased"
// +kubebuilder:validation:XValidation:rule="self.spec.type == 'Diskless' ? !has(self.spec.nonVoting) || self.spec.nonVoting == false : true",message="nonVoting must be false (or unset) when type is Diskless"
// +kubebuilder:validation:XValidation:rule="!has(self.spec.size) || (quantity(string(self.spec.size)).isInteger() && quantity(string(self.spec.size)).asInteger() % 4096 == 0)",message="spec.size must be a multiple of 4Ki (4096 bytes)"
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

// IsUnderMaintenance reports whether `.spec.maintenance` holds a mode. Any mode pauses
// reconciliation; the value only says who paused it, which is why every consumer of the pause
// MUST ask this question rather than compare against one mode of its own choosing.
func (d *DRBDResource) IsUnderMaintenance() bool { return d.Spec.Maintenance != "" }

// +kubebuilder:object:generate=true
type DRBDResourceSpec struct {
	// Actual name of the DRBD resource on the node.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[0-9A-Za-z.+_-]*$`
	// +optional
	ActualNameOnTheNode string `json:"actualNameOnTheNode,omitempty"`

	// +kubebuilder:validation:Enum=Up;Down
	// +kubebuilder:default=Up
	// +optional
	State DRBDResourceState `json:"state,omitempty"`

	// Name of the node the DRBD resource is on.
	//
	// > Important: this field is immutable and cannot be changed after the resource is created.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="nodeName is immutable"
	NodeName string `json:"nodeName"`

	// Names of the system networks used for DRBD connections.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=10
	// +kubebuilder:validation:items:MaxLength=253
	// +listType=set
	SystemNetworks []string `json:"systemNetworks"`

	// Number of votes required for quorum.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=32
	// +optional
	Quorum byte `json:"quorum,omitempty"`

	// Minimum redundancy (number of up-to-date replicas) required to keep quorum.
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

	// Allow two Primary roles at the same time (dual-primary mode).
	// +kubebuilder:default=false
	// +optional
	AllowTwoPrimaries bool `json:"allowTwoPrimaries,omitempty"`

	// When enabled, the disk does not participate in quorum voting while
	// still replicating data normally. Only valid for Diskful resources.
	// +kubebuilder:default=false
	// +optional
	NonVoting bool `json:"nonVoting,omitempty"`

	// +kubebuilder:validation:Enum=Diskful;Diskless
	// +kubebuilder:default=Diskful
	// +optional
	Type DRBDResourceType `json:"type,omitempty"`

	// Required when type is Diskful, must be empty when type is Diskless.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +optional
	LVMLogicalVolumeName string `json:"lvmLogicalVolumeName,omitempty"`

	// Numeric identifier of the node in DRBD (0 to 31).
	//
	// > Important: this field is immutable and cannot be changed after the resource is created.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=31
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="nodeID is immutable"
	NodeID uint8 `json:"nodeID"`

	// Peers (neighbouring nodes) the DRBD resource replicates with.
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=31
	// +optional
	Peers []DRBDResourcePeer `json:"peers,omitempty"`

	// Maintenance mode - when set, reconciliation is paused but status is still updated.
	//
	// The field has two writers, told apart by the value. NoResourceReconciliation is an
	// operator's own pause and is never written or cleared by a controller.
	// NoResourceReconciliationByVolume is written by the replica controller while the owning
	// ReplicatedVolume is paused through its own spec.maintenance, and cleared by the same
	// controller when the volume resumes; it MUST NOT be set by hand.
	// +kubebuilder:validation:Enum=NoResourceReconciliation;NoResourceReconciliationByVolume
	// +optional
	Maintenance MaintenanceMode `json:"maintenance,omitempty"`
}

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

	// Allow reading data from this peer.
	// +kubebuilder:default=true
	// +optional
	AllowRemoteRead bool `json:"allowRemoteRead"` // No omitempty: preserve false against the CRD default of true.

	// Numeric identifier of the peer in DRBD (0 to 31).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=31
	NodeID uint8 `json:"nodeID"`

	// +kubebuilder:validation:Enum=A;B;C
	// +kubebuilder:default=C
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="protocol is immutable"
	// +optional
	Protocol DRBDProtocol `json:"protocol,omitempty"`

	// Shared secret that authenticates the connection with the peer.
	//
	// If the caller is not allowed to read the `drbdresources/sensitive` subresource, the API returns `<omitted>` instead of the value.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	// +crd-enricher:deckhouse:sensitive-data
	SharedSecret string `json:"sharedSecret,omitempty"`

	// +kubebuilder:validation:Enum=SHA256;SHA1;DummyForTest
	// +optional
	SharedSecretAlg SharedSecretAlg `json:"sharedSecretAlg,omitempty"`

	// Pause synchronization with this peer.
	// +kubebuilder:default=false
	// +optional
	PauseSync bool `json:"pauseSync,omitempty"`

	// Network paths to the peer.
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
	// +kubebuilder:validation:MaxLength=253
	SystemNetworkName string `json:"systemNetworkName"`

	// Network address (IPv4 and port) to connect to the peer at.
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
	// Device is the stable symlink path to the DRBD block device,
	// e.g. /dev/sdsrv/<resourceName>. The symlink target is /dev/drbd<minor>.
	// Only present when the resource is Up and has an allocated minor.
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

	// Size is the usable capacity of the DRBD upper device in bytes.
	// Smaller than spec.size because DRBD internal metadata is stored on
	// the backing device. Nil when the resource is diskless or down.
	// +optional
	Size *resource.Quantity `json:"size,omitempty"`

	// Network addresses the DRBD resource actually uses.
	// +kubebuilder:validation:XValidation:rule="self.all(x, self.exists_one(y, x.systemNetworkName == y.systemNetworkName))",message="addresses[].systemNetworkName must be unique"
	// +kubebuilder:validation:MaxItems=10
	// +listType=atomic
	// +optional
	Addresses []DRBDResourceAddressStatus `json:"addresses,omitempty"`

	// Configuration of the DRBD resource currently in effect (actually applied).
	// +optional
	ActiveConfiguration *DRBDResourceActiveConfiguration `json:"activeConfiguration,omitempty"`

	// Actual state of the peers of the DRBD resource.
	// +kubebuilder:validation:XValidation:rule="self.all(x, self.exists_one(y, x.nodeID == y.nodeID))",message="peers[].nodeID must be unique"
	// +kubebuilder:validation:MaxItems=31
	// +listType=atomic
	// +optional
	Peers []DRBDResourcePeerStatus `json:"peers,omitempty"`

	// +optional
	DiskState DiskState `json:"diskState,omitempty"`

	// Whether the DRBD resource has quorum.
	// +optional
	Quorum *bool `json:"quorum,omitempty"`

	// DeviceUUID is a 16-digit uppercase hexadecimal identifier that binds this
	// DRBDResource to specific physical DRBD metadata on the backing device.
	// Format matches drbdmeta read-dev-uuid output. Set once after first
	// metadata creation or adoption; immutable thereafter.
	// +kubebuilder:validation:MaxLength=16
	// +kubebuilder:validation:Pattern=`^([0-9A-F]{16})?$`
	// +kubebuilder:validation:XValidation:rule="size(oldSelf) == 0 || oldSelf == self",message="deviceUUID is immutable once set"
	// +optional
	DeviceUUID string `json:"deviceUUID,omitempty"`

	// Conditions reflecting the current state of the DRBD resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:generate=true
type DRBDResourceAddressStatus struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=253
	SystemNetworkName string `json:"systemNetworkName"`

	// +kubebuilder:validation:Required
	Address DRBDAddress `json:"address"`
}

// +kubebuilder:object:generate=true
type DRBDResourceActiveConfiguration struct {
	// Number of votes required for quorum in the active configuration.
	// +optional
	Quorum *byte `json:"quorum,omitempty"`

	// Minimum redundancy in the active configuration.
	// +optional
	QuorumMinimumRedundancy *byte `json:"quorumMinimumRedundancy,omitempty"`

	// +kubebuilder:validation:Enum=Up;Down
	// +optional
	State DRBDResourceState `json:"state,omitempty"`

	// +kubebuilder:validation:Enum=Primary;Secondary
	// +optional
	Role DRBDRole `json:"role,omitempty"`

	// Whether dual-primary mode is allowed in the active configuration.
	// +optional
	AllowTwoPrimaries *bool `json:"allowTwoPrimaries,omitempty"`

	// Whether the disk is non-voting, that is, does not take part in the quorum vote, in the
	// active configuration.
	// +optional
	NonVoting *bool `json:"nonVoting,omitempty"`

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

	// LLVFinalizersToRelease lists LVMLogicalVolume names whose agent finalizer
	// must be released. Populated before adding a finalizer to an LLV, cleared
	// after the finalizer is successfully released. This ensures the agent never
	// loses track of which LLVs it needs to clean up.
	// +listType=set
	// +optional
	LLVFinalizersToRelease []string `json:"llvFinalizersToRelease,omitempty"`
}

// +kubebuilder:object:generate=true
type DRBDResourcePeerStatus struct {
	// Name of the peer node.
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

	// Numeric identifier of the peer in DRBD.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=31
	NodeID uint `json:"nodeID"`

	// Actual network paths to the peer.
	// +kubebuilder:validation:XValidation:rule="self.all(x, self.exists_one(y, x.systemNetworkName == y.systemNetworkName))",message="paths[].systemNetworkName must be unique"
	// +kubebuilder:validation:MaxItems=10
	// +listType=atomic
	// +optional
	Paths []DRBDResourcePathStatus `json:"paths,omitempty"`

	// State of the connection with the peer.
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
	// Name of the system network of this path.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	SystemNetworkName string `json:"systemNetworkName"`

	// Network address (IPv4 and port) of the connection with the peer.
	// +kubebuilder:validation:Required
	Address DRBDAddress `json:"address"`

	// Whether the connection over this path is established.
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
