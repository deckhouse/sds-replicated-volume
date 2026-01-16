/*
Copyright 2026 Flant JSC

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

package rvrstatusconditions

import "os"

const (
	// podNamespaceEnvVar is expected to be provided via Downward API in the controller Deployment.
	podNamespaceEnvVar = "POD_NAMESPACE"

	// agentNamespaceDefault matches the Helm namespace template: `d8-{{ .Chart.Name }}`.
	agentNamespaceDefault = "d8-sds-replicated-volume"
)

func agentNamespace() string {
	if ns := os.Getenv(podNamespaceEnvVar); ns != "" {
		return ns
	}
	return agentNamespaceDefault
}
