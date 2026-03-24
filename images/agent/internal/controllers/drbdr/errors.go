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

package drbdr

import "fmt"

// ConfiguredReasonSource carries a condition reason for configuration failures.
type ConfiguredReasonSource interface {
	ConfiguredReason() string
}

type configuredReasonError struct {
	error
	reason string
}

func (e configuredReasonError) ConfiguredReason() string { return e.reason }
func (e configuredReasonError) Unwrap() error            { return e.error }

// ConfiguredReasonError wraps an error with a configured reason.
// Returns nil if err is nil.
func ConfiguredReasonError(err error, reason string) error {
	if err == nil {
		return nil
	}
	return configuredReasonError{error: err, reason: reason}
}

// localPortConflictError indicates that a DRBD new-path command failed because
// the local port is already in use. Carries the conflicting IP so the
// reconciler can reset the port in status.addresses for re-allocation.
type localPortConflictError struct {
	ip  string
	err error
}

func (e *localPortConflictError) Error() string {
	return fmt.Sprintf("local port conflict on %s: %v", e.ip, e.err)
}

func (e *localPortConflictError) Unwrap() error { return e.err }
