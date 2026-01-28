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

package drbd_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/drbd"
)

// Test constants for network names
const (
	testNetworkName1 = "network-1"
	testNetworkName2 = "network-2"
)

// Test constants for IP addresses
const (
	testIPv4_1 = "10.0.0.1"
	testIPv4_2 = "10.0.0.2"
)

// Test constants for ports
const (
	testPort1 = uint(7000)
	testPort2 = uint(7001)
)

func TestSetAddressesAction_ApplyStatusPatch(t *testing.T) {
	type targetAddrs = []v1alpha1.DRBDResourceAddressStatus

	newTargetAddr := func(snn string, ip string, port uint) v1alpha1.DRBDResourceAddressStatus {
		return v1alpha1.DRBDResourceAddressStatus{
			SystemNetworkName: snn,
			Address: v1alpha1.DRBDAddress{
				IPv4: ip,
				Port: port,
			},
		}
	}

	tests := []struct {
		name        string
		newAddrs    targetAddrs
		existing    targetAddrs
		wantChanged bool
	}{
		{
			name:        "nil new, nil existing - no change",
			newAddrs:    nil,
			existing:    nil,
			wantChanged: false,
		},
		{
			name:        "empty new, nil existing - no change",
			newAddrs:    targetAddrs{},
			existing:    nil,
			wantChanged: false,
		},
		{
			name:        "nil new, empty existing - no change",
			newAddrs:    nil,
			existing:    targetAddrs{},
			wantChanged: false,
		},
		{
			name: "same addresses - no change",
			newAddrs: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
			},
			existing: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
			},
			wantChanged: false,
		},
		{
			name: "different IP - changes",
			newAddrs: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_2, testPort1),
			},
			existing: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
			},
			wantChanged: true,
		},
		{
			name: "different port - changes",
			newAddrs: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort2),
			},
			existing: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
			},
			wantChanged: true,
		},
		{
			name: "add address - changes",
			newAddrs: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
				newTargetAddr(testNetworkName2, testIPv4_2, testPort2),
			},
			existing: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
			},
			wantChanged: true,
		},
		{
			name: "remove address - changes",
			newAddrs: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
			},
			existing: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
				newTargetAddr(testNetworkName2, testIPv4_2, testPort2),
			},
			wantChanged: true,
		},
		{
			name:        "set addresses from empty - changes",
			newAddrs:    targetAddrs{newTargetAddr(testNetworkName1, testIPv4_1, testPort1)},
			existing:    nil,
			wantChanged: true,
		},
		{
			name:        "clear addresses - changes",
			newAddrs:    targetAddrs{},
			existing:    targetAddrs{newTargetAddr(testNetworkName1, testIPv4_1, testPort1)},
			wantChanged: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			action := drbd.SetAddressesAction{Addresses: tc.newAddrs}
			drbdr := &v1alpha1.DRBDResource{
				Status: v1alpha1.DRBDResourceStatus{
					Addresses: tc.existing,
				},
			}

			gotChanged := action.ApplyStatusPatch(drbdr)

			if gotChanged != tc.wantChanged {
				t.Errorf("gotChanged=%v, wantChanged=%v", gotChanged, tc.wantChanged)
			}

			if gotChanged {
				if diff := cmp.Diff(tc.newAddrs, drbdr.Status.Addresses); diff != "" {
					t.Errorf("unexpected addresses after change (-want, +got):\n%s", diff)
				}
			}
		})
	}
}
