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

package drbdsetup

import (
	"context"
	"encoding/json"
	"fmt"
)

type StatusResult []Resource

type Resource struct {
	Name             string       `json:"name"`
	NodeID           int          `json:"node-id"`
	Role             string       `json:"role"`
	Suspended        bool         `json:"suspended"`
	SuspendedUser    bool         `json:"suspended-user"`
	SuspendedNoData  bool         `json:"suspended-no-data"`
	SuspendedFencing bool         `json:"suspended-fencing"`
	SuspendedQuorum  bool         `json:"suspended-quorum"`
	ForceIOFailures  bool         `json:"force-io-failures"`
	WriteOrdering    string       `json:"write-ordering"`
	Devices          []Device     `json:"devices"`
	Connections      []Connection `json:"connections"`
}

type Device struct {
	Volume       int    `json:"volume"`
	Minor        int    `json:"minor"`
	DiskState    string `json:"disk-state"`
	Client       bool   `json:"client"`
	Open         bool   `json:"open"`
	Quorum       bool   `json:"quorum"`
	Size         int    `json:"size"`
	Read         int    `json:"read"`
	Written      int    `json:"written"`
	ALWrites     int    `json:"al-writes"`
	BMWrites     int    `json:"bm-writes"`
	UpperPending int    `json:"upper-pending"`
	LowerPending int    `json:"lower-pending"`
}

type Connection struct {
	PeerNodeID      int    `json:"peer-node-id"`
	Name            string `json:"name"`
	ConnectionState string `json:"connection-state"`
	Congested       bool   `json:"congested"`
	Peerrole        string `json:"peer-role"`
	TLS             bool   `json:"tls"`
	APInFlight      int    `json:"ap-in-flight"`
	RSInFlight      int    `json:"rs-in-flight"`

	Paths       []Path       `json:"paths"`
	PeerDevices []PeerDevice `json:"peer_devices"`
}

type Path struct {
	ThisHost    Host `json:"this_host"`
	RemoteHost  Host `json:"remote_host"`
	Established bool `json:"established"`
}

type Host struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
	Family  string `json:"family"`
}

type PeerDevice struct {
	Volume                 int     `json:"volume"`
	ReplicationState       string  `json:"replication-state"`
	PeerDiskState          string  `json:"peer-disk-state"`
	PeerClient             bool    `json:"peer-client"`
	ResyncSuspended        string  `json:"resync-suspended"`
	Received               int     `json:"received"`
	Sent                   int     `json:"sent"`
	OutOfSync              int     `json:"out-of-sync"`
	Pending                int     `json:"pending"`
	Unacked                int     `json:"unacked"`
	HasSyncDetails         bool    `json:"has-sync-details"`
	HasOnlineVerifyDetails bool    `json:"has-online-verify-details"`
	PercentInSync          float64 `json:"percent-in-sync"`
}

// ExecuteStatus runs "drbdsetup status --json <resourceName>". Pass "" to query
// all resources. Returns empty result (not error) if resource not found.
func ExecuteStatus(ctx context.Context, resourceName string) (res StatusResult, err error) {
	if resourceName == "" {
		resourceName = "all"
	}
	args := StatusArgs(resourceName)
	cmd := ExecCommandContext(ctx, Command, args...)

	defer func() {
		if err != nil {
			err = fmt.Errorf("running command %s %v: %w", Command, args, err)
		}
	}()

	jsonBytes, err := cmd.CombinedOutput()
	if err != nil {
		if errToExitCode(err) == 10 {
			return StatusResult{}, nil
		}
		return nil, withOutput(err, jsonBytes)
	}

	var result StatusResult
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		return nil, fmt.Errorf("unmarshaling command output: %w; output: %q", err, string(jsonBytes))
	}

	return result, nil
}
