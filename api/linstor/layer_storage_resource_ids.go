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
