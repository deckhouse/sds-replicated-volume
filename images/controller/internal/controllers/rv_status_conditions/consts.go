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

package rvstatusconditions

const (
	RVStatusConditionsControllerName = "rv_status_conditions"

	// reasonUnknown is used when RVR condition is not found
	reasonUnknown = "Unknown"

	// conditionNotFoundSuffix is appended to condition type when condition is missing
	// Full message format: "<conditionType> condition not found"
	conditionNotFoundSuffix = " condition not found"

	// Status messages for empty replica cases
	messageNoReplicasFound        = "No replicas found"
	messageNoDiskfulReplicasFound = "No diskful replicas found"
	messageNoIOReadyReplicas      = "No replicas are IOReady"
)
