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

package drbdprimary

import (
	"strings"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/drbdapierrors"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdadm"
)

// Error types specific to drbd_primary controller

type primaryCommandError struct{ drbdadm.CommandError }

type secondaryCommandError struct{ drbdadm.CommandError }

// allDRBDPrimaryErrors lists all error types managed by the drbd_primary controller.
// Used to reset only this controller's errors without affecting other controllers' errors.
var allDRBDPrimaryErrors = []drbdapierrors.DRBDAPIError{
	primaryCommandError{},
	secondaryCommandError{},
}

// resetDRBDPrimaryErrors resets only the errors managed by the drbd_primary controller.
func resetDRBDPrimaryErrors(apiErrors *v1alpha1.DRBDErrors) {
	for _, e := range allDRBDPrimaryErrors {
		e.ResetDRBDError(apiErrors)
	}
}

// WriteDRBDError implementations

func (p primaryCommandError) WriteDRBDError(apiErrors *v1alpha1.DRBDErrors) {
	apiErrors.LastPrimaryError = &v1alpha1.CmdError{
		Command:  drbdapierrors.TrimLen(strings.Join(p.CommandWithArgs(), " "), drbdapierrors.MaxErrLen),
		Output:   drbdapierrors.TrimLen(p.Output(), drbdapierrors.MaxErrLen),
		ExitCode: p.ExitCode(),
	}
}

func (s secondaryCommandError) WriteDRBDError(apiErrors *v1alpha1.DRBDErrors) {
	apiErrors.LastSecondaryError = &v1alpha1.CmdError{
		Command:  drbdapierrors.TrimLen(strings.Join(s.CommandWithArgs(), " "), drbdapierrors.MaxErrLen),
		Output:   drbdapierrors.TrimLen(s.Output(), drbdapierrors.MaxErrLen),
		ExitCode: s.ExitCode(),
	}
}

// ResetDRBDError implementations

func (primaryCommandError) ResetDRBDError(apiErrors *v1alpha1.DRBDErrors) {
	apiErrors.LastPrimaryError = nil
}

func (secondaryCommandError) ResetDRBDError(apiErrors *v1alpha1.DRBDErrors) {
	apiErrors.LastSecondaryError = nil
}

