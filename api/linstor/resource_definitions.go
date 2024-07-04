/*
 Copyright 2024 Flant JSC
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

type ResourceDefinitions struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              struct {
		LayerStack        string `json:"layer_stack"`
		ResourceDspName   string `json:"resource_dsp_name"`
		ResourceFlags     int    `json:"resource_flags"`
		ResourceGroupName string `json:"resource_group_name"`
		ResourceName      string `json:"resource_name"`
		SnapshotDspName   string `json:"snapshot_dsp_name"`
		SnapshotName      string `json:"snapshot_name"`
		Uuid              string `json:"uuid"`
	} `json:"spec"`
}

type ResourceDefinitionsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []ResourceDefinitions `json:"items"`
}
