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

package drbd

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
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

func newTestAddr(snn string, ip string, port uint) v1alpha1.DRBDResourceAddressStatus {
	return v1alpha1.DRBDResourceAddressStatus{
		SystemNetworkName: snn,
		Address: v1alpha1.DRBDAddress{
			IPv4: ip,
			Port: port,
		},
	}
}

func TestComputeIntendedIPs(t *testing.T) {
	tests := []struct {
		name           string
		systemNetworks []string
		nodeAddresses  []corev1.NodeAddress
		expected       []IntendedIP
	}{
		{
			name:           "empty networks and node addresses",
			systemNetworks: nil,
			nodeAddresses:  nil,
			expected:       []IntendedIP{},
		},
		{
			name:           "single internal network",
			systemNetworks: []string{"Internal"},
			nodeAddresses: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: testIPv4_1},
			},
			expected: []IntendedIP{
				{SystemNetworkName: "Internal", IPv4: testIPv4_1},
			},
		},
		{
			name:           "internal and external networks",
			systemNetworks: []string{"Internal", "External"},
			nodeAddresses: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: testIPv4_1},
				{Type: corev1.NodeExternalIP, Address: testIPv4_2},
			},
			expected: []IntendedIP{
				{SystemNetworkName: "Internal", IPv4: testIPv4_1},
				{SystemNetworkName: "External", IPv4: testIPv4_2},
			},
		},
		{
			name:           "missing node address type",
			systemNetworks: []string{"Internal", "External"},
			nodeAddresses: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: testIPv4_1},
				// External IP missing
			},
			expected: []IntendedIP{
				{SystemNetworkName: "Internal", IPv4: testIPv4_1},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			drbdr := &v1alpha1.DRBDResource{
				Spec: v1alpha1.DRBDResourceSpec{
					SystemNetworks: tc.systemNetworks,
				},
			}
			node := &corev1.Node{
				Status: corev1.NodeStatus{
					Addresses: tc.nodeAddresses,
				},
			}

			result := computeIntendedIPs(drbdr, node)

			if diff := cmp.Diff(tc.expected, result); diff != "" {
				t.Errorf("unexpected intended IPs (-want, +got):\n%s", diff)
			}
		})
	}
}

func TestApplyIPs(t *testing.T) {
	tests := []struct {
		name        string
		intendedIPs []IntendedIP
		existing    []v1alpha1.DRBDResourceAddressStatus
		expected    []v1alpha1.DRBDResourceAddressStatus
	}{
		{
			name:        "empty intended IPs, nil existing",
			intendedIPs: nil,
			existing:    nil,
			expected:    nil,
		},
		{
			name: "add new IP, preserve port=0",
			intendedIPs: []IntendedIP{
				{SystemNetworkName: testNetworkName1, IPv4: testIPv4_1},
			},
			existing: nil,
			expected: []v1alpha1.DRBDResourceAddressStatus{
				newTestAddr(testNetworkName1, testIPv4_1, 0),
			},
		},
		{
			name: "existing IP with port preserved",
			intendedIPs: []IntendedIP{
				{SystemNetworkName: testNetworkName1, IPv4: testIPv4_1},
			},
			existing: []v1alpha1.DRBDResourceAddressStatus{
				newTestAddr(testNetworkName1, testIPv4_1, testPort1),
			},
			expected: []v1alpha1.DRBDResourceAddressStatus{
				newTestAddr(testNetworkName1, testIPv4_1, testPort1),
			},
		},
		{
			name: "IP changed, port reset to 0",
			intendedIPs: []IntendedIP{
				{SystemNetworkName: testNetworkName1, IPv4: testIPv4_2},
			},
			existing: []v1alpha1.DRBDResourceAddressStatus{
				newTestAddr(testNetworkName1, testIPv4_1, testPort1),
			},
			expected: []v1alpha1.DRBDResourceAddressStatus{
				newTestAddr(testNetworkName1, testIPv4_2, 0),
			},
		},
		{
			name:        "remove addresses",
			intendedIPs: []IntendedIP{},
			existing: []v1alpha1.DRBDResourceAddressStatus{
				newTestAddr(testNetworkName1, testIPv4_1, testPort1),
			},
			expected: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			drbdr := &v1alpha1.DRBDResource{
				Status: v1alpha1.DRBDResourceStatus{
					Addresses: tc.existing,
				},
			}

			applyIPs(drbdr, tc.intendedIPs)

			if diff := cmp.Diff(tc.expected, drbdr.Status.Addresses); diff != "" {
				t.Errorf("unexpected addresses (-want, +got):\n%s", diff)
			}
		})
	}
}

func TestApplyPorts(t *testing.T) {
	tests := []struct {
		name          string
		intendedPorts []IntendedPort
		existing      []v1alpha1.DRBDResourceAddressStatus
		expected      []v1alpha1.DRBDResourceAddressStatus
	}{
		{
			name:          "empty intended ports removes all addresses",
			intendedPorts: nil,
			existing:      []v1alpha1.DRBDResourceAddressStatus{newTestAddr(testNetworkName1, testIPv4_1, 0)},
			expected:      nil, // Addresses not in intendedPorts are removed
		},
		{
			name: "set port on address",
			intendedPorts: []IntendedPort{
				{SystemNetworkName: testNetworkName1, IPv4: testIPv4_1, Port: testPort1},
			},
			existing: []v1alpha1.DRBDResourceAddressStatus{
				newTestAddr(testNetworkName1, testIPv4_1, 0),
			},
			expected: []v1alpha1.DRBDResourceAddressStatus{
				newTestAddr(testNetworkName1, testIPv4_1, testPort1),
			},
		},
		{
			name: "port already set correctly",
			intendedPorts: []IntendedPort{
				{SystemNetworkName: testNetworkName1, IPv4: testIPv4_1, Port: testPort1},
			},
			existing: []v1alpha1.DRBDResourceAddressStatus{
				newTestAddr(testNetworkName1, testIPv4_1, testPort1),
			},
			expected: []v1alpha1.DRBDResourceAddressStatus{
				newTestAddr(testNetworkName1, testIPv4_1, testPort1),
			},
		},
		{
			name: "address not in intended ports is removed",
			intendedPorts: []IntendedPort{
				{SystemNetworkName: testNetworkName1, IPv4: testIPv4_1, Port: testPort1},
			},
			existing: []v1alpha1.DRBDResourceAddressStatus{
				newTestAddr(testNetworkName1, testIPv4_1, 0),
				newTestAddr(testNetworkName2, testIPv4_2, testPort2), // Not in intended
			},
			expected: []v1alpha1.DRBDResourceAddressStatus{
				newTestAddr(testNetworkName1, testIPv4_1, testPort1),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			drbdr := &v1alpha1.DRBDResource{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Status: v1alpha1.DRBDResourceStatus{
					Addresses: tc.existing,
				},
			}

			applyPorts(drbdr, tc.intendedPorts)

			if diff := cmp.Diff(tc.expected, drbdr.Status.Addresses); diff != "" {
				t.Errorf("unexpected addresses (-want, +got):\n%s", diff)
			}
		})
	}
}
