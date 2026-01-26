package drbd_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/drbd"
)

// Test constants for network names
const (
	testNetworkName1 = "network-1"
	testNetworkName2 = "network-2"
	testNetworkName3 = "network-3"
)

// Test constants for IP addresses
const (
	testIPv4_1    = "10.0.0.1"
	testIPv4_2    = "10.0.0.2"
	testIPv4_3    = "10.0.0.3"
	testIPv4_1New = "192.168.1.1"
	testIPv4_2New = "192.168.1.2"
)

// Test constants for ports
const (
	testPort1 = uint(7000)
	testPort2 = uint(7001)
	testPort3 = uint(7002)
)

func TestConfigureIPAddressAction_ApplyStatusPatch(t *testing.T) {
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

	type testCase struct {
		name        string
		source      map[string]string
		target      targetAddrs
		wantChanged bool
		wantTarget  *targetAddrs // ignored if wantChanged==false
	}
	tests := []testCase{
		// Empty/nil cases
		{
			name:        "nil source, nil target - no change",
			source:      nil,
			target:      nil,
			wantChanged: false,
		},
		{
			name:   "empty source, non-empty target - removes all",
			source: map[string]string{},
			target: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
				newTargetAddr(testNetworkName2, testIPv4_2, testPort2),
			},
			wantChanged: true,
			wantTarget:  &targetAddrs{},
		},
		{
			name: "non-empty source, empty target - adds all",
			source: map[string]string{
				testNetworkName1: testIPv4_1,
				testNetworkName2: testIPv4_2,
			},
			target:      targetAddrs{},
			wantChanged: true,
			wantTarget: &targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, 0),
				newTargetAddr(testNetworkName2, testIPv4_2, 0),
			},
		},

		// No change - matching cases
		{
			name: "matching single address - no change",
			source: map[string]string{
				testNetworkName1: testIPv4_1,
			},
			target: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
			},
			wantChanged: false,
		},
		{
			name: "matching multiple addresses - no change",
			source: map[string]string{
				testNetworkName1: testIPv4_1,
				testNetworkName2: testIPv4_2,
				testNetworkName3: testIPv4_3,
			},
			target: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
				newTargetAddr(testNetworkName2, testIPv4_2, testPort2),
				newTargetAddr(testNetworkName3, testIPv4_3, testPort3),
			},
			wantChanged: false,
		},
		{
			name: "matching addresses, different order in target - no change",
			source: map[string]string{
				testNetworkName1: testIPv4_1,
				testNetworkName2: testIPv4_2,
			},
			target: targetAddrs{
				newTargetAddr(testNetworkName2, testIPv4_2, testPort2),
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
			},
			wantChanged: false,
		},

		// Update cases
		{
			name: "update single IP address - changes",
			source: map[string]string{
				testNetworkName1: testIPv4_1New,
			},
			target: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
			},
			wantChanged: true,
			wantTarget: &targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1New, testPort1),
			},
		},
		{
			name: "update one IP, keep another unchanged - changes",
			source: map[string]string{
				testNetworkName1: testIPv4_1New,
				testNetworkName2: testIPv4_2,
			},
			target: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
				newTargetAddr(testNetworkName2, testIPv4_2, testPort2),
			},
			wantChanged: true,
			wantTarget: &targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1New, testPort1),
				newTargetAddr(testNetworkName2, testIPv4_2, testPort2),
			},
		},
		{
			name: "update multiple IPs - changes",
			source: map[string]string{
				testNetworkName1: testIPv4_1New,
				testNetworkName2: testIPv4_2New,
			},
			target: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
				newTargetAddr(testNetworkName2, testIPv4_2, testPort2),
			},
			wantChanged: true,
			wantTarget: &targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1New, testPort1),
				newTargetAddr(testNetworkName2, testIPv4_2New, testPort2),
			},
		},
		{
			name: "update preserves existing port - changes",
			source: map[string]string{
				testNetworkName1: testIPv4_1New,
			},
			target: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, 9999),
			},
			wantChanged: true,
			wantTarget: &targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1New, 9999),
			},
		},

		// Add cases
		{
			name: "add one address to existing - changes",
			source: map[string]string{
				testNetworkName1: testIPv4_1,
				testNetworkName2: testIPv4_2,
			},
			target: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
			},
			wantChanged: true,
			wantTarget: &targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
				newTargetAddr(testNetworkName2, testIPv4_2, 0),
			},
		},

		// Remove cases
		{
			name: "remove one address from multiple - changes",
			source: map[string]string{
				testNetworkName1: testIPv4_1,
			},
			target: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
				newTargetAddr(testNetworkName2, testIPv4_2, testPort2),
			},
			wantChanged: true,
			wantTarget: &targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
			},
		},
		{
			name: "remove first address, keep rest - changes",
			source: map[string]string{
				testNetworkName2: testIPv4_2,
				testNetworkName3: testIPv4_3,
			},
			target: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
				newTargetAddr(testNetworkName2, testIPv4_2, testPort2),
				newTargetAddr(testNetworkName3, testIPv4_3, testPort3),
			},
			wantChanged: true,
			wantTarget: &targetAddrs{
				newTargetAddr(testNetworkName2, testIPv4_2, testPort2),
				newTargetAddr(testNetworkName3, testIPv4_3, testPort3),
			},
		},
		{
			name: "remove middle address - changes",
			source: map[string]string{
				testNetworkName1: testIPv4_1,
				testNetworkName3: testIPv4_3,
			},
			target: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
				newTargetAddr(testNetworkName2, testIPv4_2, testPort2),
				newTargetAddr(testNetworkName3, testIPv4_3, testPort3),
			},
			wantChanged: true,
			wantTarget: &targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
				newTargetAddr(testNetworkName3, testIPv4_3, testPort3),
			},
		},

		// Mixed operations
		{
			name: "swap - add and remove simultaneously - changes",
			source: map[string]string{
				testNetworkName2: testIPv4_2,
			},
			target: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
			},
			wantChanged: true,
			wantTarget: &targetAddrs{
				newTargetAddr(testNetworkName2, testIPv4_2, 0),
			},
		},
		{
			name: "update and add simultaneously - changes",
			source: map[string]string{
				testNetworkName1: testIPv4_1New,
				testNetworkName2: testIPv4_2,
			},
			target: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
			},
			wantChanged: true,
			wantTarget: &targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1New, testPort1),
				newTargetAddr(testNetworkName2, testIPv4_2, 0),
			},
		},
		{
			name: "update and remove simultaneously - changes",
			source: map[string]string{
				testNetworkName1: testIPv4_1New,
			},
			target: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
				newTargetAddr(testNetworkName2, testIPv4_2, testPort2),
			},
			wantChanged: true,
			wantTarget: &targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1New, testPort1),
			},
		},
		{
			name: "update, add, and remove simultaneously - changes",
			source: map[string]string{
				testNetworkName1: testIPv4_1New,
				testNetworkName3: testIPv4_3,
			},
			target: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
				newTargetAddr(testNetworkName2, testIPv4_2, testPort2),
			},
			wantChanged: true,
			wantTarget: &targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1New, testPort1),
				newTargetAddr(testNetworkName3, testIPv4_3, 0),
			},
		},

		// Complete replacement
		{
			name: "complete replacement - all networks change - changes",
			source: map[string]string{
				testNetworkName3: testIPv4_3,
			},
			target: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
				newTargetAddr(testNetworkName2, testIPv4_2, testPort2),
			},
			wantChanged: true,
			wantTarget: &targetAddrs{
				newTargetAddr(testNetworkName3, testIPv4_3, 0),
			},
		},

		// Edge cases with duplicate network names in target
		{
			name: "target has duplicate network names - only first is matched, second removed",
			source: map[string]string{
				testNetworkName1: testIPv4_1,
			},
			target: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
				newTargetAddr(testNetworkName1, testIPv4_2, testPort2), // duplicate name
			},
			wantChanged: true,
			wantTarget: &targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
			},
		},
		{
			name: "target has duplicate network names - update first, remove second",
			source: map[string]string{
				testNetworkName1: testIPv4_1New,
			},
			target: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
				newTargetAddr(testNetworkName1, testIPv4_2, testPort2), // duplicate name
			},
			wantChanged: true,
			wantTarget: &targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1New, testPort1),
			},
		},

		// Edge cases with empty string network name
		{
			name: "empty string network name is valid - no change",
			source: map[string]string{
				"": testIPv4_1,
			},
			target: targetAddrs{
				newTargetAddr("", testIPv4_1, testPort1),
			},
			wantChanged: false,
		},
		{
			name: "empty string network name mixed with others - no change",
			source: map[string]string{
				"":               testIPv4_1,
				testNetworkName1: testIPv4_2,
			},
			target: targetAddrs{
				newTargetAddr("", testIPv4_1, testPort1),
				newTargetAddr(testNetworkName1, testIPv4_2, testPort2),
			},
			wantChanged: false,
		},

		// Same IP for different networks
		{
			name: "same IP for different networks - no change",
			source: map[string]string{
				testNetworkName1: testIPv4_1,
				testNetworkName2: testIPv4_1, // same IP, different network
			},
			target: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
				newTargetAddr(testNetworkName2, testIPv4_1, testPort2),
			},
			wantChanged: false,
		},
		{
			name: "same IP for different networks - add third with same IP",
			source: map[string]string{
				testNetworkName1: testIPv4_1,
				testNetworkName2: testIPv4_1,
				testNetworkName3: testIPv4_1, // new network with same IP
			},
			target: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
				newTargetAddr(testNetworkName2, testIPv4_1, testPort2),
			},
			wantChanged: true,
			wantTarget: &targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
				newTargetAddr(testNetworkName2, testIPv4_1, testPort2),
				newTargetAddr(testNetworkName3, testIPv4_1, 0),
			},
		},

		// Multiple removes from end
		{
			name: "remove multiple addresses from end - changes",
			source: map[string]string{
				testNetworkName1: testIPv4_1,
			},
			target: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
				newTargetAddr(testNetworkName2, testIPv4_2, testPort2),
				newTargetAddr(testNetworkName3, testIPv4_3, testPort3),
			},
			wantChanged: true,
			wantTarget: &targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
			},
		},

		// IP changes to same value as another network
		{
			name: "IP changes to value already used by another network - changes",
			source: map[string]string{
				testNetworkName1: testIPv4_2, // change to same IP as network-2
				testNetworkName2: testIPv4_2,
			},
			target: targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_1, testPort1),
				newTargetAddr(testNetworkName2, testIPv4_2, testPort2),
			},
			wantChanged: true,
			wantTarget: &targetAddrs{
				newTargetAddr(testNetworkName1, testIPv4_2, testPort1),
				newTargetAddr(testNetworkName2, testIPv4_2, testPort2),
			},
		},
	}
	for _, tc := range tests {
		t.Run(
			tc.name,
			func(t *testing.T) {
				action := drbd.ConfigureIPAddressAction{IPv4BySystemNetworkNames: tc.source}
				drbdr := &v1alpha1.DRBDResource{
					Status: v1alpha1.DRBDResourceStatus{
						Addresses: tc.target,
					},
				}
				var wantTarget targetAddrs
				if tc.wantChanged {
					wantTarget = *tc.wantTarget
				} else {
					wantTarget = slices.Clone(tc.target)
				}

				gotChanged := action.ApplyStatusPatch(drbdr)

				if gotChanged != tc.wantChanged {
					t.Errorf("gotChanged=%v, wantChanged=%v", gotChanged, tc.wantChanged)
				}

				// Sort both slices by SystemNetworkName for comparison (since map iteration order is undefined)
				sortByNetworkName := func(addrs []v1alpha1.DRBDResourceAddressStatus) {
					slices.SortFunc(addrs, func(a, b v1alpha1.DRBDResourceAddressStatus) int {
						return strings.Compare(a.SystemNetworkName, b.SystemNetworkName)
					})
				}
				sortByNetworkName(wantTarget)
				sortByNetworkName(drbdr.Status.Addresses)

				if diff := cmp.Diff(wantTarget, drbdr.Status.Addresses); diff != "" {
					t.Errorf("unexpected target addresses (-want, +got):\n%s", diff)
				}
			},
		)
	}
}
