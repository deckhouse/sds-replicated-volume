/*
Copyright 2025 Flant JSC

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

package v1alpha3

import (
	"strconv"
	"strings"
)

// Replica type values for [ReplicatedVolumeReplica] spec.type field
const (
	// ReplicaTypeDiskful represents a diskful replica that stores data on disk
	ReplicaTypeDiskful = "Diskful"
	// ReplicaTypeAccess represents a diskless replica for data access
	ReplicaTypeAccess = "Access"
	// ReplicaTypeTieBreaker represents a diskless replica for quorum
	ReplicaTypeTieBreaker = "TieBreaker"
)

// DRBD node ID constants for ReplicatedVolumeReplica
const (
	// RVRMinNodeID is the minimum valid node ID for DRBD configuration in ReplicatedVolumeReplica
	RVRMinNodeID = uint(0)
	// RVRMaxNodeID is the maximum valid node ID for DRBD configuration in ReplicatedVolumeReplica
	RVRMaxNodeID = uint(7)
)

// IsValidNodeID checks if nodeID is within valid range [RVRMinNodeID; RVRMaxNodeID].
func IsValidNodeID(nodeID uint) bool {
	return nodeID >= RVRMinNodeID && nodeID <= RVRMaxNodeID
}

// FormatValidNodeIDRange returns a formatted string representing the valid nodeID range.
// faster than fmt.Sprintf("%d; %d", RVRMinNodeID, RVRMaxNodeID) because it avoids allocation and copying of the string.
func FormatValidNodeIDRange() string {
	var b strings.Builder
	b.Grow(10) // Pre-allocate: "[0; 7]" = 7 bytes, but allocate a bit more
	b.WriteByte('[')
	b.WriteString(strconv.FormatUint(uint64(RVRMinNodeID), 10))
	b.WriteString("; ")
	b.WriteString(strconv.FormatUint(uint64(RVRMaxNodeID), 10))
	b.WriteByte(']')
	return b.String()
}
