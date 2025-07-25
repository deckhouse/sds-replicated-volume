package linstor

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// LayerResourceIdsSpec defines the desired state of LayerResourceIds
type LayerResourceIdsSpec struct {
	LayerResourceID        int    `json:"layer_resource_id"`
	LayerResourceKind      string `json:"layer_resource_kind"`
	LayerResourceSuffix    string `json:"layer_resource_suffix"`
	LayerResourceSuspended bool   `json:"layer_resource_suspended"`
	NodeName               string `json:"node_name"`
	ResourceName           string `json:"resource_name"`
	SnapshotName           string `json:"snapshot_name"`
}

// LayerResourceIds is the Schema for the layerresourceids API
type LayerResourceIds struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec LayerResourceIdsSpec `json:"spec,omitempty"`
}

// LayerResourceIdsList contains a list of LayerResourceIds
type LayerResourceIdsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LayerResourceIds `json:"items"`
}
