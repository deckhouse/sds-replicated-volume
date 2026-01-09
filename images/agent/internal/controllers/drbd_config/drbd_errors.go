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

package drbdconfig

import (
	"strings"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdadm"
)

type drbdAPIError interface {
	error
	WriteDRBDError(apiErrors *v1alpha1.DRBDErrors)
	// should be callable with zero receiver
	ResetDRBDError(apiErrors *v1alpha1.DRBDErrors)
}

// all errors

type configurationCommandError struct{ drbdadm.CommandError }

type fileSystemOperationError struct{ error }

type sharedSecretAlgUnsupportedError struct {
	error
	unsupportedAlg string
}

// [drbdAPIError]

var allDRBDAPIErrors = []drbdAPIError{
	configurationCommandError{},
	fileSystemOperationError{},
	sharedSecretAlgUnsupportedError{},
}

func resetAllDRBDAPIErrors(apiErrors *v1alpha1.DRBDErrors) {
	for _, e := range allDRBDAPIErrors {
		e.ResetDRBDError(apiErrors)
	}
}

// [drbdAPIError.WriteDRBDError]

func (c configurationCommandError) WriteDRBDError(apiErrors *v1alpha1.DRBDErrors) {
	apiErrors.ConfigurationCommandError = &v1alpha1.DRBDCmdError{
		Command:  trimLen(strings.Join(c.CommandWithArgs(), " "), maxErrLen),
		Output:   trimLen(c.Output(), maxErrLen),
		ExitCode: c.ExitCode(),
	}
}

func (f fileSystemOperationError) WriteDRBDError(apiErrors *v1alpha1.DRBDErrors) {
	apiErrors.FileSystemOperationError = &v1alpha1.DRBDMessageError{
		Message: trimLen(f.Error(), maxErrLen),
	}
}

func (s sharedSecretAlgUnsupportedError) WriteDRBDError(apiErrors *v1alpha1.DRBDErrors) {
	apiErrors.SharedSecretAlgSelectionError = &v1alpha1.SharedSecretUnsupportedAlgError{
		UnsupportedAlg: s.unsupportedAlg,
	}
}

// [drbdAPIError.ResetDRBDError]

func (configurationCommandError) ResetDRBDError(apiErrors *v1alpha1.DRBDErrors) {
	apiErrors.ConfigurationCommandError = nil
}

func (fileSystemOperationError) ResetDRBDError(apiErrors *v1alpha1.DRBDErrors) {
	apiErrors.FileSystemOperationError = nil
}

func (sharedSecretAlgUnsupportedError) ResetDRBDError(apiErrors *v1alpha1.DRBDErrors) {
	apiErrors.SharedSecretAlgSelectionError = nil
}

// utils

const maxErrLen = 1024

func trimLen(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[0:maxLen]
	}
	return s
}
