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

import (
	"errors"
	"fmt"
	"testing"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdutils"
)

// While a snapshot holds the cluster-wide admin lock, the kernel rejects every
// administrative command on that resource with ERR_LOCK_HELD. That is a wait, not
// a failure, and the condition must say so instead of blaming the command that
// happened to run into the gate.
func TestGetConfiguredReasonAdminLockedWinsOverCommandReason(t *testing.T) {
	// Shape of what executeCommand produces for exit 10 + "(176)".
	gated := fmt.Errorf("running command drbdsetup resize: %w",
		errors.Join(drbdutils.ErrLockHeld, errors.New(`exit status 10; output: "(176)"`)))

	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "bare gate error",
			err:  gated,
			want: v1alpha1.DRBDResourceCondConfiguredReasonAdminLocked,
		},
		{
			// Call sites wrap command errors with a per-step reason. The gate must
			// still win, otherwise the status blames the wrong thing and sends an
			// operator chasing a problem that does not exist.
			name: "wrapped in a per-command reason",
			err:  ConfiguredReasonError(gated, v1alpha1.DRBDResourceCondConfiguredReasonAttachFailed),
			want: v1alpha1.DRBDResourceCondConfiguredReasonAdminLocked,
		},
		{
			name: "unrelated command failure keeps its own reason",
			err:  ConfiguredReasonError(errors.New("boom"), v1alpha1.DRBDResourceCondConfiguredReasonAttachFailed),
			want: v1alpha1.DRBDResourceCondConfiguredReasonAttachFailed,
		},
		{
			name: "unclassified error falls back to Failed",
			err:  errors.New("boom"),
			want: v1alpha1.DRBDResourceCondConfiguredReasonFailed,
		},
		{
			name: "nil error is not reasoned about",
			err:  nil,
			want: v1alpha1.DRBDResourceCondConfiguredReasonFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getConfiguredReason(tt.err); got != tt.want {
				t.Errorf("getConfiguredReason() = %q, want %q", got, tt.want)
			}
		})
	}
}
