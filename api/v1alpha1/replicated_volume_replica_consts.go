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

package v1alpha1

import (
	"strconv"
	"strings"
)

// ReplicaType enumerates possible values for ReplicatedVolumeReplica spec.type and status.actualType fields.
type ReplicaType string

// Replica type values for [ReplicatedVolumeReplica] spec.type field.
const (
	// ReplicaTypeDiskful represents a diskful replica that stores data on disk.
	ReplicaTypeDiskful ReplicaType = "Diskful"
	// ReplicaTypeAccess represents a diskless replica for data access.
	ReplicaTypeAccess ReplicaType = "Access"
	// ReplicaTypeTieBreaker represents a diskless replica for quorum.
	ReplicaTypeTieBreaker ReplicaType = "TieBreaker"
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

type DiskState string

const (
	DiskStateDiskless     DiskState = "Diskless"
	DiskStateAttaching    DiskState = "Attaching"
	DiskStateDetaching    DiskState = "Detaching"
	DiskStateFailed       DiskState = "Failed"
	DiskStateNegotiating  DiskState = "Negotiating"
	DiskStateInconsistent DiskState = "Inconsistent"
	DiskStateOutdated     DiskState = "Outdated"
	DiskStateUnknown      DiskState = "DUnknown"
	DiskStateConsistent   DiskState = "Consistent"
	DiskStateUpToDate     DiskState = "UpToDate"
)

type ReplicationState string

const (
	ReplicationStateOff                ReplicationState = "Off"
	ReplicationStateEstablished        ReplicationState = "Established"
	ReplicationStateStartingSyncSource ReplicationState = "StartingSyncS"
	ReplicationStateStartingSyncTarget ReplicationState = "StartingSyncT"
	ReplicationStateWFBitMapSource     ReplicationState = "WFBitMapS"
	ReplicationStateWFBitMapTarget     ReplicationState = "WFBitMapT"
	ReplicationStateWFSyncUUID         ReplicationState = "WFSyncUUID"
	ReplicationStateSyncSource         ReplicationState = "SyncSource"
	ReplicationStateSyncTarget         ReplicationState = "SyncTarget"
	ReplicationStatePausedSyncSource   ReplicationState = "PausedSyncS"
	ReplicationStatePausedSyncTarget   ReplicationState = "PausedSyncT"
	ReplicationStateVerifySource       ReplicationState = "VerifyS"
	ReplicationStateVerifyTarget       ReplicationState = "VerifyT"
	ReplicationStateAhead              ReplicationState = "Ahead"
	ReplicationStateBehind             ReplicationState = "Behind"
	ReplicationStateUnknown            ReplicationState = "Unknown"
)

type ConnectionState string

const (
	ConnectionStateStandAlone     ConnectionState = "StandAlone"
	ConnectionStateDisconnecting  ConnectionState = "Disconnecting"
	ConnectionStateUnconnected    ConnectionState = "Unconnected"
	ConnectionStateTimeout        ConnectionState = "Timeout"
	ConnectionStateBrokenPipe     ConnectionState = "BrokenPipe"
	ConnectionStateNetworkFailure ConnectionState = "NetworkFailure"
	ConnectionStateProtocolError  ConnectionState = "ProtocolError"
	ConnectionStateConnecting     ConnectionState = "Connecting"
	ConnectionStateTearDown       ConnectionState = "TearDown"
	ConnectionStateConnected      ConnectionState = "Connected"
	ConnectionStateUnknown        ConnectionState = "Unknown"
)

func ParseDiskState(s string) DiskState {
	switch DiskState(s) {
	case DiskStateDiskless,
		DiskStateAttaching,
		DiskStateDetaching,
		DiskStateFailed,
		DiskStateNegotiating,
		DiskStateInconsistent,
		DiskStateOutdated,
		DiskStateUnknown,
		DiskStateConsistent,
		DiskStateUpToDate:
		return DiskState(s)
	default:
		return ""
	}
}

func ParseReplicationState(s string) ReplicationState {
	switch ReplicationState(s) {
	case ReplicationStateOff,
		ReplicationStateEstablished,
		ReplicationStateStartingSyncSource,
		ReplicationStateStartingSyncTarget,
		ReplicationStateWFBitMapSource,
		ReplicationStateWFBitMapTarget,
		ReplicationStateWFSyncUUID,
		ReplicationStateSyncSource,
		ReplicationStateSyncTarget,
		ReplicationStatePausedSyncSource,
		ReplicationStatePausedSyncTarget,
		ReplicationStateVerifySource,
		ReplicationStateVerifyTarget,
		ReplicationStateAhead,
		ReplicationStateBehind,
		ReplicationStateUnknown:
		return ReplicationState(s)
	default:
		return ""
	}
}

func ParseConnectionState(s string) ConnectionState {
	switch ConnectionState(s) {
	case ConnectionStateStandAlone,
		ConnectionStateDisconnecting,
		ConnectionStateUnconnected,
		ConnectionStateTimeout,
		ConnectionStateBrokenPipe,
		ConnectionStateNetworkFailure,
		ConnectionStateProtocolError,
		ConnectionStateConnecting,
		ConnectionStateTearDown,
		ConnectionStateConnected,
		ConnectionStateUnknown:
		return ConnectionState(s)
	default:
		return ""
	}
}
