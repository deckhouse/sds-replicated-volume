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

package drbdkmsg

import (
	"testing"

	"github.com/go-logr/logr"
)

func TestParseResourceName(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
		ok   bool
	}{
		{"resource", "6,1,2,-;drbd sdsrv-foo: role( Secondary -> Primary )", "sdsrv-foo", true},
		{"device", "6,1,2,-;drbd sdsrv-foo/0 drbd1: disk( Inconsistent -> UpToDate )", "sdsrv-foo", true},
		{"connection", "6,1,2,-;drbd sdsrv-foo peer-a: conn( Connecting -> Connected )", "sdsrv-foo", true},
		{"peer-device", "6,1,2,-;drbd sdsrv-foo/0 drbd1 peer-a: pdsk( DUnknown -> UpToDate )", "sdsrv-foo", true},
		{"unregistered", "6,1,2,-;drbd /unregistered/sdsrv-foo: role( Primary -> Secondary )", "sdsrv-foo", true},
		{"module level has no name", "6,1,2,-;drbd: module loaded", "", false},
		{"no drbd token", "6,1,2,-;kernel: hello", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseResourceName([]byte(tt.line))
			if ok != tt.ok || got != tt.want {
				t.Errorf("parseResourceName(%q) = %q, %v; want %q, %v", tt.line, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestIsStateChangeMessage(t *testing.T) {
	stateChanges := []string{
		"drbd sdsrv-foo: role( Secondary -> Primary )",
		"drbd sdsrv-foo/0 drbd1: disk( Inconsistent -> UpToDate )",
		"drbd sdsrv-foo peer-a: conn( Connecting -> Connected )",
		"drbd sdsrv-foo/0 drbd1 peer-a: pdsk( DUnknown -> UpToDate )",
		"drbd sdsrv-foo/0 drbd1 peer-a: repl( Established -> SyncTarget )",
		"drbd sdsrv-foo: susp-io( no -> quorum )",
		"drbd sdsrv-foo/0 drbd1: quorum( yes -> no )",
		"drbd sdsrv-foo peer-a: Split-Brain detected, dropping connection",
	}
	for _, line := range stateChanges {
		if !isStateChangeMessage([]byte(line)) {
			t.Errorf("isStateChangeMessage(%q) = false, want true", line)
		}
	}

	nonStateChanges := []string{
		"drbd sdsrv-foo/0 drbd1: read 12345 written 6789",
		"drbd sdsrv-foo: Meta connection shut down by peer",
		"drbd sdsrv-foo: helper command: /sbin/drbdadm before-resync-target",
	}
	for _, line := range nonStateChanges {
		if isStateChangeMessage([]byte(line)) {
			t.Errorf("isStateChangeMessage(%q) = true, want false", line)
		}
	}
}

type fakeInvalidator struct {
	invalidated []string
	allCount    int
}

func (f *fakeInvalidator) Invalidate(name string) { f.invalidated = append(f.invalidated, name) }
func (f *fakeInvalidator) InvalidateAll()         { f.allCount++ }

func TestInvalidateForKernelMessage(t *testing.T) {
	tests := []struct {
		name        string
		line        string
		wantInvalid []string
		wantAll     int
	}{
		{
			name:        "state change invalidates named resource",
			line:        "6,1,2,-;drbd sdsrv-foo/0 drbd1: disk( Inconsistent -> UpToDate )",
			wantInvalid: []string{"sdsrv-foo"},
		},
		{
			name:    "non state change is ignored",
			line:    "6,1,2,-;drbd sdsrv-foo/0 drbd1: read 12345 written 6789",
			wantAll: 0,
		},
		{
			name:    "state change without parseable name invalidates all",
			line:    "3,1,2,-;drbd: Split-Brain detected globally",
			wantAll: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeInvalidator{}
			f := NewForwarder(fake)
			f.invalidateForKernelMessage(logr.Discard(), []byte(tt.line))

			if len(fake.invalidated) != len(tt.wantInvalid) {
				t.Fatalf("invalidated = %v, want %v", fake.invalidated, tt.wantInvalid)
			}
			for i := range tt.wantInvalid {
				if fake.invalidated[i] != tt.wantInvalid[i] {
					t.Errorf("invalidated[%d] = %q, want %q", i, fake.invalidated[i], tt.wantInvalid[i])
				}
			}
			if fake.allCount != tt.wantAll {
				t.Errorf("InvalidateAll count = %d, want %d", fake.allCount, tt.wantAll)
			}
		})
	}
}
