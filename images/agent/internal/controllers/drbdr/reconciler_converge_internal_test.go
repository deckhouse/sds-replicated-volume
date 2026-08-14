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
	"context"
	"errors"
	"testing"
)

type stubAction struct {
	name string
	err  error
	ran  *bool
}

func (a stubAction) Execute(_ context.Context) error {
	if a.ran != nil {
		*a.ran = true
	}
	return a.err
}

func (a stubAction) String() string { return a.name }

func TestConvergeDRBDState_RefreshNeeded(t *testing.T) {
	boom := errors.New("boom")

	tests := []struct {
		name            string
		actions         DRBDActions
		maintenanceMode bool
		wantRefresh     bool
		wantErr         error
	}{
		{
			name:        "no actions",
			actions:     nil,
			wantRefresh: false,
		},
		{
			name:        "action succeeds",
			actions:     DRBDActions{stubAction{name: "ok"}},
			wantRefresh: true,
		},
		{
			// A failed action may still have changed kernel state, so the
			// observed state must be re-read. Reporting no refresh here is what
			// used to keep a stale snapshot cached, making the next reconcile
			// recompute the same already-applied action.
			name:        "first action fails",
			actions:     DRBDActions{stubAction{name: "fail", err: boom}, stubAction{name: "unreached"}},
			wantRefresh: true,
			wantErr:     boom,
		},
		{
			name:            "maintenance mode executes nothing",
			actions:         DRBDActions{stubAction{name: "skipped", err: boom}},
			maintenanceMode: true,
			wantRefresh:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			refreshNeeded, err := convergeDRBDState(t.Context(), tt.actions, tt.maintenanceMode)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("convergeDRBDState() error = %v, want %v", err, tt.wantErr)
			}
			if refreshNeeded != tt.wantRefresh {
				t.Errorf("convergeDRBDState() refreshNeeded = %t, want %t", refreshNeeded, tt.wantRefresh)
			}
		})
	}
}

func TestConvergeDRBDState_StopsAtFirstFailure(t *testing.T) {
	var secondRan bool
	_, err := convergeDRBDState(t.Context(), DRBDActions{
		stubAction{name: "fail", err: errors.New("boom")},
		stubAction{name: "second", ran: &secondRan},
	}, false)

	if err == nil {
		t.Fatal("convergeDRBDState() error = nil, want error")
	}
	if secondRan {
		t.Error("action after a failure was executed, want the sequence aborted")
	}
}
