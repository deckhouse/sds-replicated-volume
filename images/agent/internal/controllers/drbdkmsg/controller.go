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

// Package drbdkmsg surfaces DRBD-related kernel messages from /dev/kmsg
// in the agent pod's stdout/stderr, so on-call SREs can diagnose
// split-brain, IO error, and sync-failure events from kubectl logs
// instead of having to SSH into each node and read dmesg.
package drbdkmsg

import (
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/drbdr"
)

// ControllerName is the stable name used by the controller registry to
// gate this runnable on the ENABLED_CONTROLLERS env var.
const ControllerName = "drbdkmsg-controller"

// BuildController constructs the DRBD-kmsg forwarder runnable and adds
// it to the manager. It performs no I/O at build time; the kernel log
// is opened lazily inside Start so a missing or inaccessible /dev/kmsg
// does not abort agent startup.
func BuildController(mgr manager.Manager) error {
	fwd := NewForwarder(drbdr.GetCaches())
	if err := mgr.Add(fwd); err != nil {
		return fmt.Errorf("adding drbdkmsg forwarder runnable: %w", err)
	}
	return nil
}
