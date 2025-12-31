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

package v1alpha1

import (
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const AgentAppFinalizer = "sds-replicated-volume.deckhouse.io/agent"

const ControllerAppFinalizer = "sds-replicated-volume.deckhouse.io/controller"

func isExternalFinalizer(f string) bool {
	return f != ControllerAppFinalizer && f != AgentAppFinalizer
}

func HasExternalFinalizers(obj metav1.Object) bool {
	return slices.ContainsFunc(obj.GetFinalizers(), isExternalFinalizer)
}

func HasControllerFinalizer(obj metav1.Object) bool {
	return slices.Contains(obj.GetFinalizers(), ControllerAppFinalizer)
}

func HasAgentFinalizer(obj metav1.Object) bool {
	return slices.Contains(obj.GetFinalizers(), AgentAppFinalizer)
}
