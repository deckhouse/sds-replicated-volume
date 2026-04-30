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

// Package rvr_llv_name computes deterministic LVMLogicalVolume names for
// ReplicatedVolumeReplica objects. This logic is shared between the rvr
// controller (which creates LLVs) and the rvr-scheduling controller (which
// uses the future LLV name as a scheduler-extender reservation ID).
package rvr_llv_name

import (
	"encoding/hex"
	"hash/fnv"
)

// ComputeLLVName computes the LVMLogicalVolume name for a given RVR.
// Format: rvrName + "-" + fnv128(lvgName + "\x00" + thinPoolName)
func ComputeLLVName(rvrName, lvgName, thinPoolName string) string {
	h := fnv.New128a()
	h.Write([]byte(lvgName))
	h.Write([]byte{0}) // separator
	h.Write([]byte(thinPoolName))
	checksum := hex.EncodeToString(h.Sum(nil))
	return rvrName + "-" + checksum
}
