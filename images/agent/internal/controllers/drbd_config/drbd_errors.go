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
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/drbdapierrors"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdadm"
)

// Error types specific to drbd_config controller

type configurationCommandError struct{ drbdadm.CommandError }

type fileSystemOperationError struct{ error }

type sharedSecretAlgUnsupportedError struct {
	error
	unsupportedAlg string
}

// allDRBDConfigErrors lists all error types managed by the drbd_config controller.
// Used to reset only this controller's errors without affecting other controllers' errors.
var allDRBDConfigErrors = []drbdapierrors.DRBDAPIError{
	configurationCommandError{},
	fileSystemOperationError{},
	sharedSecretAlgUnsupportedError{},
}

// resetDRBDConfigErrors resets only the errors managed by the drbd_config controller.
func resetDRBDConfigErrors(apiErrors *v1alpha1.DRBDErrors) {
	for _, e := range allDRBDConfigErrors {
		e.ResetDRBDError(apiErrors)
	}
}

// WriteDRBDError implementations

func (c configurationCommandError) WriteDRBDError(apiErrors *v1alpha1.DRBDErrors) {
	apiErrors.ConfigurationCommandError = &v1alpha1.CmdError{
		Command:  drbdapierrors.TrimLen(strings.Join(c.CommandWithArgs(), " "), drbdapierrors.MaxErrLen),
		Output:   drbdapierrors.TrimLen(c.Output(), drbdapierrors.MaxErrLen),
		ExitCode: c.ExitCode(),
	}
}

func (f fileSystemOperationError) WriteDRBDError(apiErrors *v1alpha1.DRBDErrors) {
	apiErrors.FileSystemOperationError = &v1alpha1.MessageError{
		Message: drbdapierrors.TrimLen(f.Error(), drbdapierrors.MaxErrLen),
	}
}

func (s sharedSecretAlgUnsupportedError) WriteDRBDError(apiErrors *v1alpha1.DRBDErrors) {
	apiErrors.SharedSecretAlgSelectionError = &v1alpha1.SharedSecretUnsupportedAlgError{
		UnsupportedAlg: s.unsupportedAlg,
	}
}

// ResetDRBDError implementations

func (configurationCommandError) ResetDRBDError(apiErrors *v1alpha1.DRBDErrors) {
	apiErrors.ConfigurationCommandError = nil
}

func (fileSystemOperationError) ResetDRBDError(apiErrors *v1alpha1.DRBDErrors) {
	apiErrors.FileSystemOperationError = nil
}

func (sharedSecretAlgUnsupportedError) ResetDRBDError(apiErrors *v1alpha1.DRBDErrors) {
	apiErrors.SharedSecretAlgSelectionError = nil
}
