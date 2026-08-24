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
	"slices"
	"strconv"
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

	// setupSysfs points minor discovery at temp dirs standing in for
	// /sys/block (holding the given drbd<minor> entries) and
	// /sys/devices/virtual/bdi (empty), and drops any previously seeded
	// allocation state. It returns both dirs so a test can add entries mid-run.
	setupSysfs := func(t *testing.T, blockMinors ...string) (sysBlock, sysBDI string) {
		t.Helper()
		sysBlock, sysBDI = t.TempDir(), t.TempDir()
		for _, m := range blockMinors {
			if err := os.Mkdir(sysBlock+"/drbd"+m, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		drbdutils.SysBlockPath = sysBlock
		drbdutils.SysBDIPath = sysBDI
		drbdutils.ResetNextDeviceMinor()
		return sysBlock, sysBDI
	}

	newMinorOK := func(resource string, minor, volume uint) *fakedrbdutils.ExpectedCmd {
		return &fakedrbdutils.ExpectedCmd{
			Name: drbdutils.DRBDSetupCommand,
			Args: drbdutils.NewMinorArgs(resource, minor, volume, false),
		}
	}

	newMinorVolumeExists := func(resource string, minor, volume uint) *fakedrbdutils.ExpectedCmd {
		return &fakedrbdutils.ExpectedCmd{
			Name:         drbdutils.DRBDSetupCommand,
			Args:         drbdutils.NewMinorArgs(resource, minor, volume, false),
			ResultOutput: []byte("res: Failure: (161) Minor or volume exists already (delete it first)\n"),
			ResultErr:    fakedrbdutils.ExitErr{Code: 10},
		}
	}

	// The counter is seeded past the highest minor in sysfs and then handed out
	// without re-reading it: the drbd100 added mid-run must not be picked up
	// while allocations keep succeeding.
	t.Run("auto minor allocates past the highest used", func(t *testing.T) {
		sysBlock, _ := setupSysfs(t, "0", "5")

		fakeExec := &fakedrbdutils.Exec{}
		fakeExec.ExpectCommands(newMinorOK("res", 6, 0), newMinorOK("res", 7, 0))
		fakeExec.Setup(t)

		minor, err := drbdutils.ExecuteNewAutoMinor(t.Context(), "res", 0, false)
		if err != nil || minor != 6 {
			t.Fatalf("ExecuteNewAutoMinor() = (%d, %v), want (6, nil)", minor, err)
		}

		if err := os.Mkdir(sysBlock+"/drbd100", 0o755); err != nil {
			t.Fatal(err)
		}

		minor, err = drbdutils.ExecuteNewAutoMinor(t.Context(), "res", 0, false)
		if err != nil || minor != 7 {
			t.Fatalf("ExecuteNewAutoMinor() = (%d, %v), want (7, nil)", minor, err)
		}
	})

	t.Run("auto minor starts at zero when nothing is used", func(t *testing.T) {
		setupSysfs(t)

		fakeExec := &fakedrbdutils.Exec{}
		fakeExec.ExpectCommands(newMinorOK("res", 0, 0))
		fakeExec.Setup(t)

		minor, err := drbdutils.ExecuteNewAutoMinor(t.Context(), "res", 0, false)
		if err != nil || minor != 0 {
			t.Fatalf("ExecuteNewAutoMinor() = (%d, %v), want (0, nil)", minor, err)
		}
	})

	// 161 out of new-minor means the volume already exists in the resource, so
	// no other minor can satisfy the request. It must surface to the caller
	// after a single attempt instead of being retried; the fake asserts the
	// exact command count. This is the case that used to loop forever.
	t.Run("auto minor does not retry a failure", func(t *testing.T) {
		setupSysfs(t, "0")

		fakeExec := &fakedrbdutils.Exec{}
		fakeExec.ExpectCommands(newMinorVolumeExists("res", 1, 0))
		fakeExec.Setup(t)

		_, err := drbdutils.ExecuteNewAutoMinor(t.Context(), "res", 0, false)
		if !errors.Is(err, drbdutils.ErrNewMinorAlreadyExists) {
			t.Fatalf("ExecuteNewAutoMinor() error = %v, want ErrNewMinorAlreadyExists", err)
		}
	})

	// A failure invalidates the seeded counter, so the next allocation re-reads
	// sysfs and skips minors that appeared in the meantime.
	t.Run("auto minor re-seeds after a failure", func(t *testing.T) {
		sysBlock, _ := setupSysfs(t, "0")

		fakeExec := &fakedrbdutils.Exec{}
		fakeExec.ExpectCommands(newMinorVolumeExists("res", 1, 0), newMinorOK("res", 8, 0))
		fakeExec.Setup(t)

		if _, err := drbdutils.ExecuteNewAutoMinor(t.Context(), "res", 0, false); err == nil {
			t.Fatal("ExecuteNewAutoMinor() error = nil, want error")
		}

		if err := os.Mkdir(sysBlock+"/drbd7", 0o755); err != nil {
			t.Fatal(err)
		}

		minor, err := drbdutils.ExecuteNewAutoMinor(t.Context(), "res", 0, false)
		if err != nil || minor != 8 {
			t.Fatalf("ExecuteNewAutoMinor() = (%d, %v), want (8, nil)", minor, err)
		}
	})

	// A torn-down device leaves its /sys/devices/virtual/bdi entry behind for a
	// short while after /sys/block is clean. drbdsetup new-minor refuses such a
	// minor with (161), so allocation must skip it — otherwise every re-seed
	// lands on the same blocked minor until the leftover is reaped.
	t.Run("auto minor skips a leftover bdi node", func(t *testing.T) {
		_, sysBDI := setupSysfs(t, "0", "1")
		if err := os.Mkdir(sysBDI+"/147:2", 0o755); err != nil {
			t.Fatal(err)
		}
		// An unrelated major must not be mistaken for a DRBD minor.
		if err := os.Mkdir(sysBDI+"/252:3", 0o755); err != nil {
			t.Fatal(err)
		}

		fakeExec := &fakedrbdutils.Exec{}
		fakeExec.ExpectCommands(newMinorOK("res", 3, 0))
		fakeExec.Setup(t)

		minor, err := drbdutils.ExecuteNewAutoMinor(t.Context(), "res", 0, false)
		if err != nil || minor != 3 {
			t.Fatalf("ExecuteNewAutoMinor() = (%d, %v), want (3, nil)", minor, err)
		}
	})

	// When the highest minor in use sits at the top of the minor space, seeding
	// one past it would be out of range, so allocation restarts from the lowest
	// free minor instead.
	t.Run("auto minor wraps to the lowest free minor", func(t *testing.T) {
		setupSysfs(t, "0", "2", strconv.Itoa(drbdutils.MaxDeviceMinor))

		fakeExec := &fakedrbdutils.Exec{}
		fakeExec.ExpectCommands(newMinorOK("res", 1, 0))
		fakeExec.Setup(t)

		minor, err := drbdutils.ExecuteNewAutoMinor(t.Context(), "res", 0, false)
		if err != nil || minor != 1 {
			t.Fatalf("ExecuteNewAutoMinor() = (%d, %v), want (1, nil)", minor, err)
		}
	})
}

func TestDetachArgs(t *testing.T) {
	tests := []struct {
		name     string
		diskless bool
		want     []string
	}{
		{
			name:     "plain detach",
			diskless: false,
			want:     []string{"detach", "7"},
		},
		{
			name:     "intentional diskless detach",
			diskless: true,
			want:     []string{"detach", "7", "--diskless"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := drbdutils.DetachArgs(7, tt.diskless)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("DetachArgs(7, %t) = %v, want %v", tt.diskless, got, tt.want)
			}
		})
	}
}

// TestExecuteDetachPassesDisklessFlag pins the whole detach chain down to the
// executed command line: dropping --diskless here is what makes the kernel mark
// the device as unintentionally diskless.
func TestExecuteDetachPassesDisklessFlag(t *testing.T) {
	fakeExec := &fakedrbdutils.Exec{}
	fakeExec.ExpectCommands(&fakedrbdutils.ExpectedCmd{
		Name: drbdutils.DRBDSetupCommand,
		Args: []string{"detach", "7", "--diskless"},
	})
	fakeExec.Setup(t)

	if err := drbdutils.ExecuteDetach(t.Context(), 7, true); err != nil {
		t.Fatalf("ExecuteDetach() unexpected error: %v", err)
	}
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
