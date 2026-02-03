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

// Place shared helpers here that do not belong to any single API object,
// but you are sure they must live in the API package.

// nodeIDFromName extracts NodeID from a name with format "prefix-N" (e.g., "pvc-xxx-5" → 5).
// Used by ReplicatedVolumeReplica.NodeID(), ReplicatedVolumeDatameshMember.NodeID(),
// and ReplicatedVolumeDatameshPendingReplicaTransition.NodeID().
//
// IMPORTANT: This function assumes the name is already validated by kubebuilder markers.
// Only use with validated names (ending with -0 to -31).
func nodeIDFromName(name string) uint8 {
	l := len(name)
	if l < 3 { // minimum "a-0"
		panic("nodeIDFromName: name too short: " + name)
	}

	last := name[l-1]
	if last < '0' || last > '9' {
		panic("nodeIDFromName: no digit suffix: " + name)
	}
	v := last - '0'

	if c := name[l-2]; c >= '0' && c <= '9' {
		v = (c-'0')*10 + v
	}

	if v > 31 {
		panic("nodeIDFromName: node ID out of range: " + name)
	}

	return v
}
