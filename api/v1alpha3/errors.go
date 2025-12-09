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
