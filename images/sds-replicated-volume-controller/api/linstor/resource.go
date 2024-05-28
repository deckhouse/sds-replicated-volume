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

type Resources struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              struct {
		CreateTimestamp int64  `json:"create_timestamp"`
		NodeName        string `json:"node_name"`
		ResourceFlags   int    `json:"resource_flags"`
		ResourceName    string `json:"resource_name"`
		SnapshotName    string `json:"snapshot_name"`
		Uuid            string `json:"uuid"`
	} `json:"spec"`
}

type ResourcesList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []Resources `json:"items"`
}
