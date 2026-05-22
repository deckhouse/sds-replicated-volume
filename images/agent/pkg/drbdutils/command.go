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

package drbdutils

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/deckhouse/sds-replicated-volume/lib/go/common/ctrlexec"
)

const (
	// ExecTimeout is the production per-invocation timeout for one-shot
	// drbdsetup/drbdmeta commands. Sized for happy-path metadata
	// operations (create-md, attach, resize, write-dev-uuid) on typical
	// volumes while still being small enough to cap goroutine pile-up
	// when a command gets stuck behind a kernel-side stall.
	ExecTimeout = 10 * time.Second

	// ExecWaitDelay is the production WaitDelay applied by ConfigureCmd.
	// drbdsetup and drbdmeta wrap kernel ioctls and can leave helpers
	// behind on error paths, so a nonzero value is required to keep
	// Cmd.Wait from blocking forever on orphaned pipes.
	ExecWaitDelay = 3 * time.Second
)

// ConfigureCmd applies the production *exec.Cmd policy for
// drbdsetup/drbdmeta invocations. Pass it as the configure argument to
// ctrlexec.ExecCommandContext when wiring a factory.
func ConfigureCmd(c *exec.Cmd) {
	c.WaitDelay = ExecWaitDelay
}

// ExecCommandContext is the factory used by every one-shot Execute*
// helper (status, attach, options, peer mgmt, etc.). It is overridable at
// controller startup; the package default applies no policy, so
// production code MUST override it (see ConfigureCmd, ExecTimeout).
var ExecCommandContext ctrlexec.ExecCommandContextFactory = ctrlexec.ExecCommandContext(nil)

// Events2ExecCommandContext is the factory used exclusively by
// ExecuteEvents2. It is a separate variable because events2 streams via
// StdoutPipe+Start+Wait — incompatible with ctrlexec.WithCache's
// CombinedOutput path — and is expected to run for the entire manager
// lifetime, ruling out a per-call timeout.
var Events2ExecCommandContext ctrlexec.ExecCommandContextFactory = ctrlexec.ExecCommandContext(nil)

var DRBDSetupCommand = "drbdsetup"

var DRBDMetaCommand = "drbdmeta"

type KnownError struct {
	ExitCode        int
	OutputSubstring string
	JoinErr         error
}

func errToExitCode(err error) int {
	type exitCode interface{ ExitCode() int }

	if errWithExitCode, ok := err.(exitCode); ok {
		return errWithExitCode.ExitCode()
	}

	return 0
}

func withOutput(err error, out []byte) error {
	if len(out) == 0 {
		return err
	}
	return fmt.Errorf("%w; output: %q", err, string(out))
}

func executeCommand(
	cmd ctrlexec.CombinedOutputRunner,
	knownErrors []KnownError,
) ([]byte, error) {
	out, err := cmd.CombinedOutput()
	if err != nil {
		exitCode := errToExitCode(err)
		outStr := string(out)

		for _, ke := range knownErrors {
			if ke.ExitCode == exitCode && strings.Contains(outStr, ke.OutputSubstring) {
				err = errors.Join(ke.JoinErr, err)
				break
			}
		}

		return nil, fmt.Errorf("running command %s: %w", cmd, withOutput(err, out))
	}

	return out, nil
}
