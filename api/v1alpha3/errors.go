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

package v1alpha3

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
