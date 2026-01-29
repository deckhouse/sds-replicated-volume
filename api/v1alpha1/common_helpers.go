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

package v1alpha1

import (
	"strconv"
	"strings"
)

// Place shared helpers here that do not belong to any single API object,
// but you are sure they must live in the API package.

// nodeIDFromName extracts NodeID from a name with format "prefix-N" (e.g., "pvc-xxx-5" → 5).
// Used by ReplicatedVolumeReplica.NodeID() and ReplicatedVolumeDatameshMember.NodeID().
func nodeIDFromName(name string) (uint8, bool) {
	idx := strings.LastIndex(name, "-")
	if idx < 0 {
		return 0, false
	}
	id, err := strconv.ParseUint(name[idx+1:], 10, 8)
	if err != nil {
		return 0, false
	}
	return uint8(id), true
}
