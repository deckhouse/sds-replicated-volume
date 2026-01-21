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

package drbdsetup

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// ShowArgs returns the arguments for drbdsetup show command.
var ShowArgs = func(resource string) []string {
	return []string{"show", "--json", "--show-defaults", resource}
}

// ShowResource represents the parsed output of drbdsetup show --json.
type ShowResource struct {
	Resource    string           `json:"resource"`
	Options     ShowOptions      `json:"options"`
	ThisHost    ShowThisHost     `json:"_this_host"`
	Connections []ShowConnection `json:"connections"`
}

type ShowOptions struct {
	CPUMask                    string `json:"cpu-mask"`
	OnNoDataAccessible         string `json:"on-no-data-accessible"`
	AutoPromote                bool   `json:"auto-promote"`
	PeerAckWindow              string `json:"peer-ack-window"`
	PeerAckDelay               string `json:"peer-ack-delay"`
	TwopcTimeout               string `json:"twopc-timeout"`
	TwopcRetryTimeout          string `json:"twopc-retry-timeout"`
	AutoPromoteTimeout         string `json:"auto-promote-timeout"`
	MaxIODepth                 string `json:"max-io-depth"`
	Quorum                     string `json:"quorum"`
	OnNoQuorum                 string `json:"on-no-quorum"`
	QuorumMinimumRedundancy    string `json:"quorum-minimum-redundancy"`
	OnSuspendedPrimaryOutdated string `json:"on-suspended-primary-outdated"`
}

type ShowThisHost struct {
	NodeID  int          `json:"node-id"`
	Volumes []ShowVolume `json:"volumes"`
}

type ShowVolume struct {
	VolumeNr    int            `json:"volume_nr"`
	DeviceMinor int            `json:"device_minor"`
	BackingDisk string         `json:"backing-disk"`
	MetaDisk    string         `json:"meta-disk"`
	Disk        ShowVolumeDisk `json:"disk"`
}

type ShowVolumeDisk struct {
	Size                   string `json:"size"`
	OnIOError              string `json:"on-io-error"`
	DiskBarrier            bool   `json:"disk-barrier"`
	DiskFlushes            bool   `json:"disk-flushes"`
	DiskDrain              bool   `json:"disk-drain"`
	MDFlushes              bool   `json:"md-flushes"`
	ResyncAfter            string `json:"resync-after"`
	ALExtents              string `json:"al-extents"`
	ALUpdates              bool   `json:"al-updates"`
	DiscardZeroesIfAligned bool   `json:"discard-zeroes-if-aligned"`
	DisableWriteSame       bool   `json:"disable-write-same"`
	DiskTimeout            string `json:"disk-timeout"`
	ReadBalancing          string `json:"read-balancing"`
	RSDiscardGranularity   string `json:"rs-discard-granularity"`
}

type ShowConnection struct {
	Path       ShowPath               `json:"path"`
	Net        ShowNet                `json:"net"`
	Volumes    []ShowConnectionVolume `json:"volumes"`
	PeerNodeID int                    `json:"_peer_node_id"`
}

type ShowPath struct {
	ThisHost   string `json:"_this_host"`
	RemoteHost string `json:"_remote_host"`
}

type ShowNet struct {
	Transport           string `json:"transport"`
	LoadBalancePaths    bool   `json:"load-balance-paths"`
	Protocol            string `json:"protocol"`
	Timeout             string `json:"timeout"`
	MaxEpochSize        string `json:"max-epoch-size"`
	ConnectInt          string `json:"connect-int"`
	PingInt             string `json:"ping-int"`
	SndbufSize          string `json:"sndbuf-size"`
	RcvbufSize          string `json:"rcvbuf-size"`
	KoCount             string `json:"ko-count"`
	AllowTwoPrimaries   bool   `json:"allow-two-primaries"`
	CRAMHMACAlg         string `json:"cram-hmac-alg"`
	SharedSecret        string `json:"shared-secret"`
	AfterSB0Pri         string `json:"after-sb-0pri"`
	AfterSB1Pri         string `json:"after-sb-1pri"`
	AfterSB2Pri         string `json:"after-sb-2pri"`
	AlwaysASBP          bool   `json:"always-asbp"`
	RRConflict          string `json:"rr-conflict"`
	PingTimeout         string `json:"ping-timeout"`
	DataIntegrityAlg    string `json:"data-integrity-alg"`
	TCPCork             bool   `json:"tcp-cork"`
	OnCongestion        string `json:"on-congestion"`
	CongestionFill      string `json:"congestion-fill"`
	CongestionExtents   string `json:"congestion-extents"`
	CsumsAlg            string `json:"csums-alg"`
	CsumsAfterCrashOnly bool   `json:"csums-after-crash-only"`
	VerifyAlg           string `json:"verify-alg"`
	UseRLE              bool   `json:"use-rle"`
	SocketCheckTimeout  string `json:"socket-check-timeout"`
	Fencing             string `json:"fencing"`
	MaxBuffers          string `json:"max-buffers"`
	AllowRemoteRead     bool   `json:"allow-remote-read"`
	TLS                 bool   `json:"tls"`
	TLSKeyring          string `json:"tls-keyring"`
	TLSPrivkey          string `json:"tls-privkey"`
	TLSCertificate      string `json:"tls-certificate"`
	RDMACtrlRcvbufSize  string `json:"rdma-ctrl-rcvbuf-size"`
	RDMACtrlSndbufSize  string `json:"rdma-ctrl-sndbuf-size"`
	Name                string `json:"_name"`
}

type ShowConnectionVolume struct {
	VolumeNr int                      `json:"volume_nr"`
	Disk     ShowConnectionVolumeDisk `json:"disk"`
}

type ShowConnectionVolumeDisk struct {
	ResyncRate   string `json:"resync-rate"`
	CPlanAhead   string `json:"c-plan-ahead"`
	CDelayTarget string `json:"c-delay-target"`
	CFillTarget  string `json:"c-fill-target"`
	CMaxRate     string `json:"c-max-rate"`
	CMinRate     string `json:"c-min-rate"`
	Bitmap       bool   `json:"bitmap"`
}

// ExecuteShow executes drbdsetup show --json --show-defaults and parses the output.
func ExecuteShow(ctx context.Context, resourceName string) (*ShowResource, error) {
	cmd := exec.CommandContext(ctx, Command, ShowArgs(resourceName)...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("running command: %w; output: %q", err, string(output))
	}

	var results []ShowResource
	if err := json.Unmarshal(output, &results); err != nil {
		return nil, fmt.Errorf("parsing JSON output: %w", err)
	}

	// Empty array means resource doesn't exist
	if len(results) == 0 {
		return nil, nil
	}

	for i := range results {
		if results[i].Resource == resourceName {
			return &results[i], nil
		}
	}

	return nil, nil
}
