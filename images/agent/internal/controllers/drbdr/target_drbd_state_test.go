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

package drbdr

import (
	"testing"
)

type stubMetadata struct {
	hasMetadata bool
	diskUUID    string
}

func (s stubMetadata) HasMetadata() bool      { return s.hasMetadata }
func (s stubMetadata) DiskDeviceUUID() string { return s.diskUUID }

func uintPtr(v uint) *uint { return &v }

func TestComputeAttachActions_PreserveMetadata(t *testing.T) {
	minor := uintPtr(0)
	dev := "/dev/vg/snap"

	tests := []struct {
		name             string
		metadata         stubMetadata
		statusUUID       string
		preserveMetadata bool
		wantFirst        string
	}{
		{
			name:             "preserve=true, metadata exists -> ApplyAL only",
			metadata:         stubMetadata{hasMetadata: true, diskUUID: "ABCD1234ABCD1234"},
			statusUUID:       "",
			preserveMetadata: true,
			wantFirst:        "ApplyALAction",
		},
		{
			name:             "preserve=true, no metadata -> CreateMetadata",
			metadata:         stubMetadata{hasMetadata: false},
			statusUUID:       "",
			preserveMetadata: true,
			wantFirst:        "CreateMetadataAction",
		},
		{
			name:             "preserve=false, metadata exists, no statusUUID -> CreateMetadata",
			metadata:         stubMetadata{hasMetadata: true, diskUUID: "ABCD1234ABCD1234"},
			statusUUID:       "",
			preserveMetadata: false,
			wantFirst:        "CreateMetadataAction",
		},
		{
			name:             "preserve=false, metadata exists, matching statusUUID -> ApplyAL",
			metadata:         stubMetadata{hasMetadata: true, diskUUID: "ABCD1234ABCD1234"},
			statusUUID:       "ABCD1234ABCD1234",
			preserveMetadata: false,
			wantFirst:        "ApplyALAction",
		},
		{
			name:             "preserve=false, no metadata, no statusUUID -> CreateMetadata",
			metadata:         stubMetadata{hasMetadata: false},
			statusUUID:       "",
			preserveMetadata: false,
			wantFirst:        "CreateMetadataAction",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actions := computeAttachActions(tc.metadata, tc.statusUUID, minor, dev, tc.preserveMetadata)

			if len(actions) == 0 {
				t.Fatal("got 0 actions, want at least 1")
			}

			var firstType string
			switch actions[0].(type) {
			case ApplyALAction:
				firstType = "ApplyALAction"
			case CreateMetadataAction:
				firstType = "CreateMetadataAction"
			default:
				firstType = "unknown"
			}

			if firstType != tc.wantFirst {
				t.Errorf("first action = %s, want %s", firstType, tc.wantFirst)
			}

			last := actions[len(actions)-1]
			if _, ok := last.(AttachAction); !ok {
				t.Errorf("last action = %T, want AttachAction", last)
			}
		})
	}
}

func TestComputeAttachActions_PreserveMetadata_NoCreateMdNoWriteUUID(t *testing.T) {
	minor := uintPtr(0)
	dev := "/dev/vg/snap"
	meta := stubMetadata{hasMetadata: true, diskUUID: "ABCD1234ABCD1234"}

	actions := computeAttachActions(meta, "", minor, dev, true)

	for _, a := range actions {
		switch a.(type) {
		case CreateMetadataAction:
			t.Error("preserve=true with metadata must not produce CreateMetadataAction")
		case WriteDeviceUUIDAction:
			t.Error("preserve=true with metadata must not produce WriteDeviceUUIDAction")
		}
	}

	if len(actions) != 2 {
		t.Errorf("len(actions) = %d, want 2 (ApplyAL + Attach)", len(actions))
	}
}
