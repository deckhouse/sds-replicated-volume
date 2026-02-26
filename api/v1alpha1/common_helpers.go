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

// idFromName extracts ID from a name with format "prefix-N" (e.g., "pvc-xxx-5" → 5).
// Used by ReplicatedVolumeReplica.ID(), ReplicatedVolumeDatameshMember.ID(),
// and ReplicatedVolumeDatameshPendingReplicaTransition.ID().
//
// IMPORTANT: This function assumes the name is already validated by kubebuilder markers.
// Only use with validated names (ending with -0 to -31).
func idFromName(name string) uint8 {
	l := len(name)
	last := name[l-1] - '0'
	prev := name[l-2] - '0'
	// isDigit: 1 if prev ∈ [0,9], else 0
	// Trick: prev + 246 overflows 8 bits (>=256) when prev >= 10
	isDigit := uint8(1) ^ uint8((uint16(prev)+246)>>8)
	return last + prev*10*isDigit
}
