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

// ReplicatedStorageClass is a Kubernetes Custom Resource that defines a configuration for a Kubernetes Storage class.
// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=rsc
// +kubebuilder:metadata:labels=heritage=deckhouse
// +kubebuilder:metadata:labels=module=sds-replicated-volume
// +kubebuilder:metadata:labels=backup.deckhouse.io/cluster-config=true
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Reason",type=string,priority=1,JSONPath=`.status.reason`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`,description="The age of this resource"
type ReplicatedStorageClass struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ReplicatedStorageClassSpec   `json:"spec"`
	Status            ReplicatedStorageClassStatus `json:"status,omitempty"`
}

// +kubebuilder:object:generate=true
// +kubebuilder:object:root=true
type ReplicatedStorageClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []ReplicatedStorageClass `json:"items"`
}

// +kubebuilder:validation:XValidation:rule="(has(self.replication) && self.replication == \"None\") || ((!has(self.replication) || self.replication == \"Availability\" || self.replication == \"ConsistencyAndAvailability\") && (!has(self.zones) || size(self.zones) == 0 || size(self.zones) == 1 || size(self.zones) == 3))",message="When replication is not set or is set to Availability or ConsistencyAndAvailability (default value), zones must be either not specified, or must contain exactly three zones."
// +kubebuilder:validation:XValidation:rule="(has(self.zones) && has(oldSelf.zones)) || (!has(self.zones) && !has(oldSelf.zones))",message="zones field cannot be deleted or added"
// +kubebuilder:validation:XValidation:rule="(has(self.replication) && has(oldSelf.replication)) || (!has(self.replication) && !has(oldSelf.replication))",message="replication filed cannot be deleted or added"
// +kubebuilder:validation:XValidation:rule="(has(self.volumeAccess) && has(oldSelf.volumeAccess)) || (!has(self.volumeAccess) && !has(oldSelf.volumeAccess))",message="volumeAccess filed cannot be deleted or added"
// Defines a Kubernetes Storage class configuration.
//
// > Note that this field is in read-only mode.
// +kubebuilder:object:generate=true
type ReplicatedStorageClassSpec struct {
	// Selected ReplicatedStoragePool resource's name.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable."
	StoragePool string `json:"storagePool"`
	// The storage class's reclaim policy. Might be:
	// - Delete (If the Persistent Volume Claim is deleted, deletes the Persistent Volume and its associated storage as well)
	// - Retain (If the Persistent Volume Claim is deleted, remains the Persistent Volume and its associated storage)
	// +kubebuilder:validation:Enum=Delete;Retain
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable."
	ReclaimPolicy string `json:"reclaimPolicy"`
	// The Storage class's replication mode. Might be:
	// - None — In this mode the Storage class's 'placementCount' and 'AutoEvictMinReplicaCount' params equal '1'.
	// - Availability — In this mode the volume remains readable and writable even if one of the replica nodes becomes unavailable. Data is stored in two copies on different nodes. This corresponds to `placementCount = 2` and `AutoEvictMinReplicaCount = 2`. **Important:** this mode does not guarantee data consistency and may lead to split brain and data loss in case of network connectivity issues between nodes. Recommended only for non-critical data and applications that do not require high reliability and data integrity.
	// - ConsistencyAndAvailability — In this mode the volume remains readable and writable when one replica node fails. Data is stored in three copies on different nodes (`placementCount = 3`, `AutoEvictMinReplicaCount = 3`). This mode provides protection against data loss when two nodes containing volume replicas fail and guarantees data consistency. However, if two replicas are lost, the volume switches to suspend-io mode.
	//
	// > Note that default Replication mode is 'ConsistencyAndAvailability'.
	// +kubebuilder:validation:Enum=None;Availability;ConsistencyAndAvailability
	// +kubebuilder:default:=ConsistencyAndAvailability
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable."
	Replication string `json:"replication,omitempty"`
	// The Storage class's access mode. Might be:
	// - Local (in this mode the Storage class's 'allowRemoteVolumeAccess' param equals 'false'
	// and Volume Binding mode equals 'WaitForFirstConsumer')
	// - EventuallyLocal (in this mode the Storage class's 'allowRemoteVolumeAccess' param
	// equals '- fromSame:\n  - topology.kubernetes.io/zone', 'auto-diskful' param equals '30' minutes,
	// 'auto-diskful-allow-cleanup' param equals 'true',
	// and Volume Binding mode equals 'WaitForFirstConsumer')
	// - PreferablyLocal (in this mode the Storage class's 'allowRemoteVolumeAccess' param
	// equals '- fromSame:\n  - topology.kubernetes.io/zone',
	// and Volume Binding mode equals 'WaitForFirstConsumer')
	// - Any (in this mode the Storage class's 'allowRemoteVolumeAccess' param
	// equals '- fromSame:\n  - topology.kubernetes.io/zone',
	// and Volume Binding mode equals 'Immediate')
	//
	// > Note that the default Volume Access mode is 'PreferablyLocal'.
	// +kubebuilder:validation:Enum=Local;EventuallyLocal;PreferablyLocal;Any
	// +kubebuilder:default:=PreferablyLocal
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable."
	VolumeAccess string `json:"volumeAccess,omitempty"`
	// The topology settings for the volumes in the created Storage class. Might be:
	// - TransZonal - replicas of the volumes will be created in different zones (one replica per zone).
	// To use this topology, the available zones must be specified in the 'zones' param, and the cluster nodes must have the topology.kubernetes.io/zone=<zone name> label.
	// - Zonal - all replicas of the volumes are created in the same zone that the scheduler selected to place the pod using this volume.
	// - Ignored - the topology information will not be used to place replicas of the volumes.
	// The replicas can be placed on any available nodes, with the restriction: no more than one replica of a given volume on one node.
	//
	// > Note that the 'Ignored' value can be used only if there are no zones in the cluster (there are no nodes with the topology.kubernetes.io/zone label).
	//
	// > For the system to operate correctly, either every cluster node must be labeled with 'topology.kubernetes.io/zone', or none of them should have this label.
	// +kubebuilder:validation:Enum=TransZonal;Zonal;Ignored
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable."
	Topology string `json:"topology"`
	// Array of zones the Storage class's volumes should be replicated in. The controller will put a label with
	// the Storage class's name on the nodes which be actual used by the Storage class.
	//
	// > Note that for Replication mode 'Availability' and 'ConsistencyAndAvailability' you have to select
	// exactly 1 or 3 zones.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable."
	Zones []string `json:"zones,omitempty"`
}

// Displays current information about the Storage Class.
// +kubebuilder:object:generate=true
type ReplicatedStorageClassStatus struct {
	// The Storage class current state. Might be:
	// - Failed (if the controller received incorrect resource configuration or some errors occurred during the operation)
	// - Create (if everything went fine)
	// +kubebuilder:validation:Enum=Failed;Created
	Phase string `json:"phase,omitempty"`
	// Additional information about the current state of the Storage Class.
	Reason string `json:"reason,omitempty"`
}
