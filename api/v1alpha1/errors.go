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

// +k8s:deepcopy-gen=true
type MessageError struct {
	// +kubebuilder:validation:MaxLength=1024
	Message string `json:"message,omitempty"`
}

// +k8s:deepcopy-gen=true
type CmdError struct {
	// +kubebuilder:validation:MaxLength=1024
	Command string `json:"command,omitempty"`
	// +kubebuilder:validation:MaxLength=1024
	Output   string `json:"output,omitempty"`
	ExitCode int    `json:"exitCode,omitempty"`
}

// +k8s:deepcopy-gen=true
type SharedSecretUnsupportedAlgError struct {
	// +kubebuilder:validation:MaxLength=1024
	UnsupportedAlg string `json:"unsupportedAlg,omitempty"`
}

// +k8s:deepcopy-gen=true
type DeviceUUIDMismatchError struct {
	// Expected device-uuid stored in RVR status.
	// +kubebuilder:validation:MaxLength=32
	Expected string `json:"expected,omitempty"`
	// Actual device-uuid read from DRBD metadata.
	// +kubebuilder:validation:MaxLength=32
	Actual string `json:"actual,omitempty"`
}

// +k8s:deepcopy-gen=true
type NodeIDMismatchError struct {
	// Expected node-id from RVR name.
	Expected uint `json:"expected,omitempty"`
	// Actual node-id read from DRBD metadata.
	// Can be -1 for uninitialized metadata (resource was never up'd).
	Actual int `json:"actual,omitempty"`
}
