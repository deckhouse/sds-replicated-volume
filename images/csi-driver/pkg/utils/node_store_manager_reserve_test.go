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
	"testing"

	"github.com/stretchr/testify/assert"
	mountutils "k8s.io/mount-utils"
	utilexec "k8s.io/utils/exec"
	testingexec "k8s.io/utils/exec/testing"

	"github.com/deckhouse/sds-replicated-volume/lib/go/common/logger"
)

// recordingExec captures the commands setReservedPercent runs, so the test can
// assert on the tune2fs invocation itself rather than on its effect.
func recordingExec(runErr error) (*testingexec.FakeExec, *[][]string) {
	var calls [][]string
	fake := &testingexec.FakeExec{ExactOrder: true}
	fake.CommandScript = append(fake.CommandScript,
		func(cmd string, args ...string) utilexec.Cmd {
			calls = append(calls, append([]string{cmd}, args...))
			fakeCmd := &testingexec.FakeCmd{
				CombinedOutputScript: []testingexec.FakeAction{
					func() ([]byte, []byte, error) { return []byte("tune2fs output"), nil, runErr },
				},
			}
			return testingexec.InitFakeCmd(fakeCmd, cmd, args...)
		})
	return fake, &calls
}

func newReserveStore(fake *testingexec.FakeExec) *Store {
	return &Store{
		Log: &logger.Logger{},
		NodeStorage: mountutils.SafeFormatAndMount{
			Interface: &mountutils.FakeMounter{},
			Exec:      fake,
		},
	}
}

func TestSetReservedPercent(t *testing.T) {
	const devPath = "/dev/drbd1000"

	t.Run("zero percent runs nothing", func(t *testing.T) {
		fake, calls := recordingExec(nil)
		store := newReserveStore(fake)

		assert.NoError(t, store.setReservedPercent(devPath, "ext4", 0))
		assert.Empty(t, *calls)
	})

	t.Run("a percentage reaches tune2fs", func(t *testing.T) {
		fake, calls := recordingExec(nil)
		store := newReserveStore(fake)

		assert.NoError(t, store.setReservedPercent(devPath, "ext4", 5))
		assert.Equal(t, [][]string{{"tune2fs", "-m", "5", devPath}}, *calls)
	})

	t.Run("xfs keeps no blocks for the superuser, so nothing runs", func(t *testing.T) {
		fake, calls := recordingExec(nil)
		store := newReserveStore(fake)

		// -m on mkfs.xfs means metadata options, so a reserve request has to be
		// dropped rather than translated.
		assert.NoError(t, store.setReservedPercent(devPath, "xfs", 5))
		assert.Empty(t, *calls)
	})

	t.Run("a failing tune2fs fails staging and reports its output", func(t *testing.T) {
		fake, _ := recordingExec(errors.New("exit status 1"))
		store := newReserveStore(fake)

		err := store.setReservedPercent(devPath, "ext4", 5)
		assert.ErrorContains(t, err, "failed to reserve 5% of /dev/drbd1000")
		assert.ErrorContains(t, err, "tune2fs output")
	})
}
