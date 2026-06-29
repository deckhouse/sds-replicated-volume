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

package drbdutils_test

import (
	"errors"
	"os"
	"testing"

	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdutils"
	fakedrbdutils "github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdutils/fake"
)

func TestExecuteDisconnectKnownError(t *testing.T) {
	fakeExec := &fakedrbdutils.Exec{}
	fakeExec.ExpectCommands(&fakedrbdutils.ExpectedCmd{
		Name:         drbdutils.DRBDSetupCommand,
		Args:         drbdutils.DisconnectArgs("res", 1),
		ResultOutput: []byte("Failure: (158) Unknown resource\n"),
		ResultErr:    fakedrbdutils.ExitErr{Code: 10},
	})
	fakeExec.Setup(t)

	err := drbdutils.ExecuteDisconnect(t.Context(), "res", 1)
	if !errors.Is(err, drbdutils.ErrDisconnectResourceNotFound) {
		t.Fatalf("ExecuteDisconnect() error = %v, want ErrDisconnectResourceNotFound", err)
	}
}

func TestExecuteStatusNotFoundHandling(t *testing.T) {
	t.Run("no such resource", func(t *testing.T) {
		fakeExec := &fakedrbdutils.Exec{}
		fakeExec.ExpectCommands(&fakedrbdutils.ExpectedCmd{
			Name:         drbdutils.DRBDSetupCommand,
			Args:         drbdutils.StatusArgs("res"),
			ResultOutput: []byte("res: No such resource\n"),
			ResultErr:    fakedrbdutils.ExitErr{Code: 10},
		})
		fakeExec.Setup(t)

		result, err := drbdutils.ExecuteStatus(t.Context(), "res")
		if err != nil {
			t.Fatalf("ExecuteStatus() unexpected error: %v", err)
		}
		if len(result) != 0 {
			t.Fatalf("ExecuteStatus() len = %d, want 0", len(result))
		}
	})

	t.Run("other exit-10 error", func(t *testing.T) {
		fakeExec := &fakedrbdutils.Exec{}
		fakeExec.ExpectCommands(&fakedrbdutils.ExpectedCmd{
			Name:         drbdutils.DRBDSetupCommand,
			Args:         drbdutils.StatusArgs("res"),
			ResultOutput: []byte("Failure: (129) Interrupted by Signal\n"),
			ResultErr:    fakedrbdutils.ExitErr{Code: 10},
		})
		fakeExec.Setup(t)

		_, err := drbdutils.ExecuteStatus(t.Context(), "res")
		if err == nil {
			t.Fatal("ExecuteStatus() error = nil, want non-nil")
		}
	})
}

func TestExecuteNewMinorKnownErrors(t *testing.T) {
	t.Run("already exists", func(t *testing.T) {
		fakeExec := &fakedrbdutils.Exec{}
		fakeExec.ExpectCommands(&fakedrbdutils.ExpectedCmd{
			Name:         drbdutils.DRBDSetupCommand,
			Args:         drbdutils.NewMinorArgs("res", 7, 0, false),
			ResultOutput: []byte("Failure: (161) Minor or volume exists already (delete it first)\n"),
			ResultErr:    fakedrbdutils.ExitErr{Code: 10},
		})
		fakeExec.Setup(t)

		err := drbdutils.ExecuteNewMinor(t.Context(), "res", 7, 0, false)
		if !errors.Is(err, drbdutils.ErrNewMinorAlreadyExists) {
			t.Fatalf("ExecuteNewMinor() error = %v, want ErrNewMinorAlreadyExists", err)
		}
	})

	t.Run("auto minor retries", func(t *testing.T) {
		sysBlock := t.TempDir()
		if err := os.Mkdir(sysBlock+"/drbd0", 0o755); err != nil {
			t.Fatal(err)
		}
		drbdutils.SysBlockPath = sysBlock

		drbdutils.ResetNextDeviceMinor()

		fakeExec := &fakedrbdutils.Exec{}
		fakeExec.ExpectCommands(
			&fakedrbdutils.ExpectedCmd{
				Name:         drbdutils.DRBDSetupCommand,
				Args:         drbdutils.NewMinorArgs("res", 0, 0, false),
				ResultOutput: []byte("Failure: (161) Minor or volume exists already (delete it first)\n"),
				ResultErr:    fakedrbdutils.ExitErr{Code: 10},
			},
			&fakedrbdutils.ExpectedCmd{
				Name: drbdutils.DRBDSetupCommand,
				Args: drbdutils.NewMinorArgs("res", 1, 0, false),
			},
		)
		fakeExec.Setup(t)

		minor, err := drbdutils.ExecuteNewAutoMinor(t.Context(), "res", 0, false)
		if err != nil {
			t.Fatalf("ExecuteNewAutoMinor() unexpected error: %v", err)
		}
		if minor != 1 {
			t.Fatalf("ExecuteNewAutoMinor() minor = %d, want 1", minor)
		}
	})
}

func TestExecuteRenameKnownErrors(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   error
	}{
		{
			name:   "unknown resource",
			output: "Failure: (158) Unknown resource\n",
			want:   drbdutils.ErrRenameUnknownResource,
		},
		{
			name:   "already exists",
			output: "Failure: (174) Already exists\n",
			want:   drbdutils.ErrRenameAlreadyExists,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeExec := &fakedrbdutils.Exec{}
			fakeExec.ExpectCommands(&fakedrbdutils.ExpectedCmd{
				Name:         drbdutils.DRBDSetupCommand,
				Args:         drbdutils.RenameArgs("old", "new"),
				ResultOutput: []byte(tt.output),
				ResultErr:    fakedrbdutils.ExitErr{Code: 10},
			})
			fakeExec.Setup(t)

			err := drbdutils.ExecuteRename(t.Context(), "old", "new")
			if !errors.Is(err, tt.want) {
				t.Fatalf("ExecuteRename() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestExecuteResizeKnownErrors(t *testing.T) {
	t.Run("backing device not grown", func(t *testing.T) {
		fakeExec := &fakedrbdutils.Exec{}
		fakeExec.ExpectCommands(&fakedrbdutils.ExpectedCmd{
			Name:         drbdutils.DRBDSetupCommand,
			Args:         drbdutils.ResizeArgs(3, 0),
			ResultOutput: []byte("Failure: (111) Low.dev. smaller than requested DRBD-dev. size.\n"),
			ResultErr:    fakedrbdutils.ExitErr{Code: 10},
		})
		fakeExec.Setup(t)

		err := drbdutils.ExecuteResize(t.Context(), 3, 0)
		if !errors.Is(err, drbdutils.ErrResizeBackingNotGrown) {
			t.Fatalf("ExecuteResize() error = %v, want ErrResizeBackingNotGrown", err)
		}
	})

	t.Run("need primary", func(t *testing.T) {
		fakeExec := &fakedrbdutils.Exec{}
		fakeExec.ExpectCommands(&fakedrbdutils.ExpectedCmd{
			Name:         drbdutils.DRBDSetupCommand,
			Args:         drbdutils.ResizeArgs(3, 0),
			ResultOutput: []byte("Failure: (131) Need one Primary node to resize.\n"),
			ResultErr:    fakedrbdutils.ExitErr{Code: 10},
		})
		fakeExec.Setup(t)

		err := drbdutils.ExecuteResize(t.Context(), 3, 0)
		if !errors.Is(err, drbdutils.ErrResizeNeedPrimary) {
			t.Fatalf("ExecuteResize() error = %v, want ErrResizeNeedPrimary", err)
		}
	})

	t.Run("other exit-10 error stays generic", func(t *testing.T) {
		fakeExec := &fakedrbdutils.Exec{}
		fakeExec.ExpectCommands(&fakedrbdutils.ExpectedCmd{
			Name:         drbdutils.DRBDSetupCommand,
			Args:         drbdutils.ResizeArgs(3, 0),
			ResultOutput: []byte("Failure: (127) Device minor not allocated\n"),
			ResultErr:    fakedrbdutils.ExitErr{Code: 10},
		})
		fakeExec.Setup(t)

		err := drbdutils.ExecuteResize(t.Context(), 3, 0)
		if err == nil {
			t.Fatal("ExecuteResize() error = nil, want non-nil")
		}
		if errors.Is(err, drbdutils.ErrResizeBackingNotGrown) || errors.Is(err, drbdutils.ErrResizeNeedPrimary) {
			t.Fatalf("ExecuteResize() incorrectly matched known error: %v", err)
		}
	})
}

func TestExecuteCheckMD(t *testing.T) {
	const (
		minor      = uint(0)
		backingDev = "/dev/vg-0/test"
	)

	tests := []struct {
		name       string
		output     string
		exitCode   int
		wantExists bool
		wantErr    bool
	}{
		// "No valid meta data found" exits 1 on drbd-utils <= 9.31.0 (main() "!!rv")
		// and 255 on >= 9.32.0 (main() "rv"). Both must read as "no metadata yet".
		{name: "no metadata, exit 1", output: "No valid meta data found\n", exitCode: 1, wantExists: false},
		{name: "no metadata, exit 255", output: "No valid meta data found\n", exitCode: 255, wantExists: false},
		// Unclean activity log counts as "metadata exists" on either exit code.
		{name: "unclean, exit 1", output: "Found meta data is \"unclean\", please apply-al first\n", exitCode: 1, wantExists: true},
		{name: "unclean, exit 255", output: "Found meta data is \"unclean\", please apply-al first\n", exitCode: 255, wantExists: true},
		// An unrecognized failure must still propagate as an error.
		{name: "unknown failure", output: "some other error\n", exitCode: 20, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeExec := &fakedrbdutils.Exec{}
			fakeExec.ExpectCommands(&fakedrbdutils.ExpectedCmd{
				Name:         drbdutils.DRBDMetaCommand,
				Args:         drbdutils.DumpMDArgs(minor, backingDev),
				ResultOutput: []byte(tt.output),
				ResultErr:    fakedrbdutils.ExitErr{Code: tt.exitCode},
			})
			fakeExec.Setup(t)

			exists, err := drbdutils.ExecuteCheckMD(t.Context(), minor, backingDev)
			if tt.wantErr {
				if err == nil {
					t.Fatal("ExecuteCheckMD() error = nil, want non-nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("ExecuteCheckMD() unexpected error: %v", err)
			}
			if exists != tt.wantExists {
				t.Fatalf("ExecuteCheckMD() exists = %v, want %v", exists, tt.wantExists)
			}
		})
	}
}
