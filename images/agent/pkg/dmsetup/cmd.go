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

package dmsetup

import (
	"fmt"
	"os/exec"
	"time"

	"github.com/deckhouse/sds-replicated-volume/lib/go/common/ctrlexec"
)

const dmsetupCommand = "dmsetup"

const (
	// ExecTimeout is the production per-invocation timeout. dmsetup
	// info/create/remove are normally sub-second; this value is the
	// worst-case bound for a kernel-side stall (e.g. backing block
	// device in D-state).
	ExecTimeout = 10 * time.Second

	// ExecWaitDelay is the production WaitDelay applied by ConfigureCmd.
	// dmsetup speaks to the device-mapper ioctl interface and can leave
	// helpers behind on error paths, so a nonzero value is required to
	// keep Cmd.Wait from blocking forever on orphaned pipes.
	ExecWaitDelay = 3 * time.Second
)

// ConfigureCmd applies the production *exec.Cmd policy for dmsetup
// invocations. Pass it as the configure argument to
// ctrlexec.ExecCommandContext when wiring a factory.
func ConfigureCmd(c *exec.Cmd) {
	c.WaitDelay = ExecWaitDelay
}

// ExecCommandContext is overridable for testing and for installing a
// wrapped factory at controller startup. The package default applies no
// policy; production code MUST override it (see ConfigureCmd, ExecTimeout).
var ExecCommandContext ctrlexec.ExecCommandContextFactory = ctrlexec.ExecCommandContext(nil)

func withOutput(err error, out []byte) error {
	if len(out) == 0 {
		return err
	}
	return fmt.Errorf("%w; output: %q", err, string(out))
}
