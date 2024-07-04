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

type VolumeDefinitions struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              struct {
		ResourceName string `json:"resource_name"`
		SnapshotName string `json:"snapshot_name"`
		Uuid         string `json:"uuid"`
		VlmFlags     int    `json:"vlm_flags"`
		VlmNr        int    `json:"vlm_nr"`
		VlmSize      int    `json:"vlm_size"`
	} `json:"spec"`
}

type VolumeDefinitionsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []VolumeDefinitions `json:"items"`
}
