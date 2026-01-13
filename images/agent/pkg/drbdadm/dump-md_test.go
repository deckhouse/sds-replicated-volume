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

package drbdadm

import (
	"testing"
)

func TestParseMetadata(t *testing.T) {
	tests := []struct {
		name           string
		output         string
		wantDeviceUUID string
		wantNodeID     int
		wantErr        bool
	}{
		{
			name: "valid metadata output",
			output: `# DRBD meta data dump
# 2025-01-12 10:00:00
version "v09";

device-uuid 0xABCDEF1234567890;

node-id 0;

current-uuid 0x1234567890ABCDEF;
bitmap[0] {
}
`,
			wantDeviceUUID: "0xABCDEF1234567890",
			wantNodeID:     0,
			wantErr:        false,
		},
		{
			name: "valid metadata with node-id 5",
			output: `# DRBD meta data dump
device-uuid 0x1111222233334444;
node-id 5;
`,
			wantDeviceUUID: "0x1111222233334444",
			wantNodeID:     5,
			wantErr:        false,
		},
		{
			name: "lowercase hex uuid",
			output: `device-uuid 0xabcdef1234567890;
node-id 3;
`,
			wantDeviceUUID: "0xabcdef1234567890",
			wantNodeID:     3,
			wantErr:        false,
		},
		{
			name:    "missing device-uuid",
			output:  `node-id 0;`,
			wantErr: true,
		},
		{
			name:    "empty output",
			output:  ``,
			wantErr: true,
		},
		{
			name: "device-uuid without semicolon",
			output: `device-uuid 0xABCDEF1234567890
node-id 0;
`,
			wantDeviceUUID: "0xABCDEF1234567890",
			wantNodeID:     0,
			wantErr:        false,
		},
		{
			name: "mixed case hex",
			output: `device-uuid 0xAbCdEf1234567890;
node-id 2;
`,
			wantDeviceUUID: "0xAbCdEf1234567890",
			wantNodeID:     2,
			wantErr:        false,
		},
		{
			name: "node-id -1 (uninitialized, freshly created metadata)",
			output: `# DRBD meta data dump
version "v09";
node-id -1;
device-uuid 0xB1991A4410AD6E1C;
`,
			wantDeviceUUID: "0xB1991A4410AD6E1C",
			wantNodeID:     -1,
			wantErr:        false,
		},
		{
			name: "missing node-id defaults to -1",
			output: `device-uuid 0xABCDEF1234567890;
`,
			wantDeviceUUID: "0xABCDEF1234567890",
			wantNodeID:     -1, // NodeIDUninitialized
			wantErr:        false,
		},
		{
			name: "real dump format from drbdmeta before up",
			output: `# DRBD meta data dump
# 2026-01-13 04:32:18 +0300 [1768267938]
version "v09";

max-peers 7;
node-id -1;
current-uuid 0x0000000000000004;
flags 0x00000080;
members 0x0000000000000000;
peer[0] {
    bitmap-index -1;
}
la-size-sect 0;
bm-byte-per-bit 4096;
device-uuid 0xB1991A4410AD6E1C;
la-peer-max-bio-size 0;
bitmap[0] {
}
`,
			wantDeviceUUID: "0xB1991A4410AD6E1C",
			wantNodeID:     -1,
			wantErr:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			md, err := parseMetadata(tt.output)

			if tt.wantErr {
				if err == nil {
					t.Errorf("parseMetadata() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("parseMetadata() unexpected error: %v", err)
				return
			}

			if md.DeviceUUID != tt.wantDeviceUUID {
				t.Errorf("parseMetadata() DeviceUUID = %q, want %q", md.DeviceUUID, tt.wantDeviceUUID)
			}

			if md.NodeID != tt.wantNodeID {
				t.Errorf("parseMetadata() NodeID = %d, want %d", md.NodeID, tt.wantNodeID)
			}
		})
	}
}
