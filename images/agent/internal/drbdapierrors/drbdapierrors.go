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

// Package drbdapierrors provides common utilities for writing DRBD errors to RVR status.
// Each controller should define its own error types and reset functions to avoid
// accidentally resetting errors managed by other controllers.
package drbdapierrors

import (
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// MaxErrLen is the maximum length of error message/output fields in the API.
const MaxErrLen = 1024

// DRBDAPIError is an error that can be written to and reset from DRBDErrors in the RVR status.
// Each controller should define its own error types implementing this interface.
// The ResetDRBDError method should be callable with a zero receiver.
type DRBDAPIError interface {
	error
	WriteDRBDError(apiErrors *v1alpha1.DRBDErrors)
	ResetDRBDError(apiErrors *v1alpha1.DRBDErrors)
}

// TrimLen trims a string to the specified maximum length.
func TrimLen(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[0:maxLen]
	}
	return s
}

// EnsureErrorsField ensures that rvr.Status.DRBD.Errors is initialized.
// Returns the Errors field for convenience.
func EnsureErrorsField(rvr *v1alpha1.ReplicatedVolumeReplica) *v1alpha1.DRBDErrors {
	if rvr.Status == nil {
		rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{}
	}
	if rvr.Status.DRBD == nil {
		rvr.Status.DRBD = &v1alpha1.DRBD{}
	}
	if rvr.Status.DRBD.Errors == nil {
		rvr.Status.DRBD.Errors = &v1alpha1.DRBDErrors{}
	}
	return rvr.Status.DRBD.Errors
}
