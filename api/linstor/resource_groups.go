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

type ResourceGroups struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              struct {
		AllowedProviderList   string `json:"allowed_provider_list"`
		Description           string `json:"description"`
		DoNotPlaceWithRscList string `json:"do_not_place_with_rsc_list"`
		LayerStack            string `json:"layer_stack"`
		NodeNameList          string `json:"node_name_list"`
		PoolName              string `json:"pool_name"`
		PoolNameDiskless      string `json:"pool_name_diskless"`
		ReplicaCount          int    `json:"replica_count"`
		ReplicasOnDifferent   string `json:"replicas_on_different"`
		ReplicasOnSame        string `json:"replicas_on_same"`
		ResourceGroupDspName  string `json:"resource_group_dsp_name"`
		ResourceGroupName     string `json:"resource_group_name"`
		UUID                  string `json:"uuid"`
	} `json:"spec"`
}

type ResourceGroupsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []ResourceGroups `json:"items"`
}
