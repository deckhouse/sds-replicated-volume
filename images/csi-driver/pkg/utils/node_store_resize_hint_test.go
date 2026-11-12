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

package utils //nolint:revive // matches the package name of the file under test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	mountutils "k8s.io/mount-utils"
	utilexec "k8s.io/utils/exec"
	testingexec "k8s.io/utils/exec/testing"

	"github.com/deckhouse/sds-common-lib/logger"
)

const (
	dumpClean              = "Filesystem state:         clean\nFS Error count:           0\n"
	dumpCleanWithErrors    = "Filesystem volume name:   <none>\nFilesystem state:         clean with errors\nErrors behavior:          Continue\n"
	dumpNotCleanWithErrors = "Filesystem state:         not clean with errors\n"
	dumpErrorCount         = "Filesystem state:         clean\nFS Error count:           3\nFirst error time:         Mon Nov  9 10:00:00 2026\n"
	dumpGarbage            = "dumpe2fs: Bad magic number in super-block while trying to open /dev/drbd1000\n"
)

func TestExtHasErrors(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want bool
	}{
		{"clean", dumpClean, false},
		{"clean with errors", dumpCleanWithErrors, true},
		{"not clean with errors", dumpNotCleanWithErrors, true},
		{"error count above zero", dumpErrorCount, true},
		{"garbage", dumpGarbage, false},
		{"empty", "", false},
		{"error count not a number", "FS Error count:           many\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, extHasErrors(tt.out))
		})
	}
}

type fakeCommand struct {
	out []byte
	err error
}

func commandExec(cmds map[string]fakeCommand, calls *[]string) *testingexec.FakeExec {
	action := func(cmd string, args ...string) utilexec.Cmd {
		*calls = append(*calls, cmd)
		c := cmds[cmd]
		fakeCmd := &testingexec.FakeCmd{
			CombinedOutputScript: []testingexec.FakeAction{
				func() ([]byte, []byte, error) { return c.out, nil, c.err },
			},
		}
		return testingexec.InitFakeCmd(fakeCmd, cmd, args...)
	}
	fake := &testingexec.FakeExec{}
	for range 8 {
		fake.CommandScript = append(fake.CommandScript, action)
	}
	return fake
}

func TestExplainResizeError(t *testing.T) {
	const devPath = "/dev/drbd1000"
	resizeErr := errors.New("resize2fs: Permission denied to resize filesystem")
	blkid := func(fs string) fakeCommand {
		return fakeCommand{out: []byte("DEVNAME=" + devPath + "\nTYPE=" + fs + "\n")}
	}

	tests := []struct {
		name         string
		cmds         map[string]fakeCommand
		wantHint     bool
		wantDumpCall bool
	}{
		{"ext4 with errors", map[string]fakeCommand{"blkid": blkid("ext4"), "dumpe2fs": {out: []byte(dumpCleanWithErrors)}}, true, true},
		{"ext3 with error count", map[string]fakeCommand{"blkid": blkid("ext3"), "dumpe2fs": {out: []byte(dumpErrorCount)}}, true, true},
		{"ext4 clean", map[string]fakeCommand{"blkid": blkid("ext4"), "dumpe2fs": {out: []byte(dumpClean)}}, false, true},
		{"ext4 garbage output", map[string]fakeCommand{"blkid": blkid("ext4"), "dumpe2fs": {out: []byte(dumpGarbage)}}, false, true},
		{"dumpe2fs fails", map[string]fakeCommand{"blkid": blkid("ext4"), "dumpe2fs": {out: []byte(dumpCleanWithErrors), err: errors.New("exit status 1")}}, false, true},
		{"xfs is not inspected", map[string]fakeCommand{"blkid": blkid("xfs"), "dumpe2fs": {out: []byte(dumpCleanWithErrors)}}, false, false},
		{"ext2 is not inspected", map[string]fakeCommand{"blkid": blkid("ext2"), "dumpe2fs": {out: []byte(dumpCleanWithErrors)}}, false, false},
		{"blkid fails", map[string]fakeCommand{"blkid": {err: errors.New("exit status 2")}, "dumpe2fs": {out: []byte(dumpCleanWithErrors)}}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			store := &Store{
				Log: logger.NewNop(),
				NodeStorage: mountutils.SafeFormatAndMount{
					Interface: &mountutils.FakeMounter{},
					Exec:      commandExec(tt.cmds, &calls),
				},
			}

			got := store.explainResizeError(devPath, resizeErr)

			assert.ErrorIs(t, got, resizeErr)
			if tt.wantHint {
				assert.Contains(t, got.Error(), "marked as having errors")
				assert.Contains(t, got.Error(), "`e2fsck -f` while it is not mounted")
				assert.True(t, strings.HasSuffix(got.Error(), "original error: "+resizeErr.Error()))
			} else {
				assert.Equal(t, resizeErr, got)
			}
			assert.Equal(t, tt.wantDumpCall, slices.Contains(calls, "dumpe2fs"))
		})
	}
}

func TestResizeFSReportsFilesystemErrors(t *testing.T) {
	const (
		devPath = "/dev/drbd1000"
		target  = "/var/lib/kubelet/pods/p/volumes/pv"
	)
	newStore := func(dump string, calls *[]string) *Store {
		cmds := map[string]fakeCommand{
			"blkid":     {out: []byte("DEVNAME=" + devPath + "\nTYPE=ext4\n")},
			"resize2fs": {out: []byte("resize2fs: Permission denied to resize filesystem\n"), err: errors.New("exit status 1")},
			"dumpe2fs":  {out: []byte(dump)},
		}
		return &Store{
			Log: logger.NewNop(),
			NodeStorage: mountutils.SafeFormatAndMount{
				Interface: &mountutils.FakeMounter{MountPoints: []mountutils.MountPoint{{Device: devPath, Path: target}}},
				Exec:      commandExec(cmds, calls),
			},
		}
	}

	t.Run("the hint precedes the resize2fs output", func(t *testing.T) {
		var calls []string
		err := newStore(dumpCleanWithErrors, &calls).ResizeFS(target)

		require.Error(t, err)
		assert.Contains(t, calls, "dumpe2fs")
		msg := err.Error()
		hint := strings.Index(msg, "the ext4 filesystem on "+devPath+" is marked as having errors")
		original := strings.Index(msg, "Permission denied to resize filesystem")
		require.NotEqual(t, -1, hint)
		require.NotEqual(t, -1, original)
		assert.Less(t, hint, original)
		assert.NotContains(t, msg, "\n:")
	})

	t.Run("a healthy filesystem keeps the plain error", func(t *testing.T) {
		var calls []string
		err := newStore(dumpClean, &calls).ResizeFS(target)

		require.Error(t, err)
		assert.Contains(t, calls, "dumpe2fs")
		assert.Contains(t, err.Error(), "Permission denied to resize filesystem")
		assert.NotContains(t, err.Error(), "e2fsck")
	})
}
