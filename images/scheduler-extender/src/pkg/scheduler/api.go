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

package scheduler

import (
	apiv1 "k8s.io/api/core/v1"
)

// ExtenderArgs is copied from https://godoc.org/k8s.io/kubernetes/pkg/scheduler/api/v1#ExtenderArgs
type ExtenderArgs struct {
	// Pod being scheduled
	Pod *apiv1.Pod `json:"pod"`
	// List of candidate nodes where the pod can be scheduled; to be populated
	// only if ExtenderConfig.NodeCacheCapable == false
	Nodes *apiv1.NodeList `json:"nodes,omitempty"`
	// List of candidate node names where the pod can be scheduled; to be
	// populated only if ExtenderConfig.NodeCacheCapable == true
	NodeNames *[]string `json:"nodenames,omitempty"`
}
