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

type PropsContainersSpec struct {
	PropKey       string `json:"prop_key"`
	PropValue     string `json:"prop_value"`
	PropsInstance string `json:"props_instance"`
}

type PropsContainers struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              PropsContainersSpec `json:"spec"`
}

type PropsContainersList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []PropsContainers `json:"items"`
}
