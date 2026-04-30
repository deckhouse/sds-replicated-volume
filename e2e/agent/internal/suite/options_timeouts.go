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

package suite

import (
	"time"
	"unsafe"
)

// Timeout wraps time.Duration with JSON/text unmarshalling support for
// Go duration strings (e.g. "10s", "2m").
type Timeout struct{ time.Duration }

// UnmarshalText implements encoding.TextUnmarshaler.
func (t *Timeout) UnmarshalText(b []byte) (err error) {
	t.Duration, err = time.ParseDuration(unsafe.String(unsafe.SliceData(b), len(b)))
	return
}

// DRBDRConfiguredTimeout is the timeout for waiting for DRBDResource to become configured.
type DRBDRConfiguredTimeout struct{ Timeout }

// LLVCreatedTimeout is the timeout for waiting for LVMLogicalVolume to be created.
type LLVCreatedTimeout struct{ Timeout }

// PeerConnectedTimeout is the timeout for waiting for peer connection to be established.
type PeerConnectedTimeout struct{ Timeout }

// DRBDROpTimeout is the timeout for waiting for a DRBDResourceOperation to complete.
type DRBDROpTimeout struct{ Timeout }

// DiskUpToDateTimeout is the timeout for waiting for DiskState to become UpToDate.
type DiskUpToDateTimeout struct{ Timeout }
