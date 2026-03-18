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
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// ExistingPorts maps local IP addresses to ports currently configured in the
// DRBD kernel. Used as a fallback source-of-truth when status.addresses has
// no port yet (migration / adoption scenario).
type ExistingPorts map[string]uint

// existingPortsFromState extracts local IP → port mapping from an observed
// DRBD state. Returns nil if the state has no connections with paths.
func existingPortsFromState(aState *actualState) ExistingPorts {
	if aState == nil || aState.status == nil {
		return nil
	}
	ports := make(ExistingPorts)
	for i := range aState.status.Connections {
		for j := range aState.status.Connections[i].Paths {
			h := &aState.status.Connections[i].Paths[j].ThisHost
			if h.Address != "" && h.Port > 0 {
				ports[h.Address] = uint(h.Port)
			}
		}
	}
	if len(ports) == 0 {
		return nil
	}
	return ports
}

// IntendedIP represents an intended IP address for a system network.
type IntendedIP struct {
	SystemNetworkName string
	IPv4              string
}

// IntendedPort represents an intended port for a system network address.
type IntendedPort struct {
	SystemNetworkName string
	IPv4              string
	Port              uint
}

// computeIntendedIPs computes the intended IP addresses from the DRBDResource spec
// and the Node's addresses. It returns the networks and IPs that should be used,
// ignoring ports.
func computeIntendedIPs(drbdr *v1alpha1.DRBDResource, node *corev1.Node) []IntendedIP {
	// No addresses for down resources
	if drbdr.Spec.State == v1alpha1.DRBDResourceStateDown {
		return nil
	}

	// Build address map from Node.status.addresses
	nodeAddressesByType := make(map[corev1.NodeAddressType]string)
	for _, addr := range node.Status.Addresses {
		nodeAddressesByType[addr.Type] = addr.Address
	}

	result := make([]IntendedIP, 0, len(drbdr.Spec.SystemNetworks))
	for _, snn := range drbdr.Spec.SystemNetworks {
		addrType := systemNetworkToNodeAddressType(snn)
		ip, ok := nodeAddressesByType[addrType]
		if !ok {
			continue
		}
		result = append(result, IntendedIP{
			SystemNetworkName: snn,
			IPv4:              ip,
		})
	}
	return result
}

// applyIPs updates drbdr.status.addresses to match the intended IPs.
// For existing valid addresses (matching network+IP), it keeps their ports.
// For new addresses, it sets port to 0.
// Addresses with unknown networks or invalid IPs are removed.
func applyIPs(drbdr *v1alpha1.DRBDResource, intendedIPs []IntendedIP) {
	// Build map of existing addresses for quick lookup
	type addrKey struct {
		snn string
		ip  string
	}
	existingPorts := make(map[addrKey]uint)
	for _, addr := range drbdr.Status.Addresses {
		existingPorts[addrKey{snn: addr.SystemNetworkName, ip: addr.Address.IPv4}] = addr.Address.Port
	}

	// Build new addresses list
	newAddresses := make([]v1alpha1.DRBDResourceAddressStatus, 0, len(intendedIPs))
	for _, ip := range intendedIPs {
		key := addrKey{snn: ip.SystemNetworkName, ip: ip.IPv4}
		port := existingPorts[key] // 0 if not found
		newAddresses = append(newAddresses, v1alpha1.DRBDResourceAddressStatus{
			SystemNetworkName: ip.SystemNetworkName,
			Address: v1alpha1.DRBDAddress{
				IPv4: ip.IPv4,
				Port: port,
			},
		})
	}

	if len(newAddresses) == 0 {
		drbdr.Status.Addresses = nil
	} else {
		drbdr.Status.Addresses = newAddresses
	}
}

// computeIntendedPorts computes ports for each IP in drbdr.status.addresses.
//
// Port source-of-truth priority:
//  1. status.addresses port (primary SoT) — non-zero port already persisted.
//  2. DRBD kernel port (existingPorts) — fallback for migration/adoption when
//     the DRBD resource already exists on the node but the K8S object is new.
//  3. portAllocator (last resort) — allocates a fresh port.
//
// If port allocation fails (returns 0), the address is skipped and an error is returned.
//
// Note: This helper mutates reconciler-owned deterministic state (PortCache).
// The PortCache maintains port allocation across reconciliations to ensure
// stable port assignments. This is acceptable because the cache is deterministic
// relative to its state and produces stable outputs for the same inputs.
func computeIntendedPorts(drbdr *v1alpha1.DRBDResource, existingPorts ExistingPorts, portAllocator PortAllocator) ([]IntendedPort, error) {
	result := make([]IntendedPort, 0, len(drbdr.Status.Addresses))
	var allocErr error
	for _, addr := range drbdr.Status.Addresses {
		port := addr.Address.Port
		if port == 0 {
			port = existingPorts[addr.Address.IPv4]
		}
		if port == 0 {
			port = portAllocator(addr.Address.IPv4)
			if port == 0 {
				allocErr = fmt.Errorf("failed to allocate port for IP %s (network %s)", addr.Address.IPv4, addr.SystemNetworkName)
				continue
			}
		}
		result = append(result, IntendedPort{
			SystemNetworkName: addr.SystemNetworkName,
			IPv4:              addr.Address.IPv4,
			Port:              port,
		})
	}
	return result, allocErr
}

// applyPorts updates the ports in drbdr.status.addresses to match the intended ports.
// Addresses not present in intendedPorts (e.g., due to failed port allocation) are removed.
func applyPorts(drbdr *v1alpha1.DRBDResource, intendedPorts []IntendedPort) {
	// Build map for quick lookup
	type addrKey struct {
		snn string
		ip  string
	}
	portMap := make(map[addrKey]uint, len(intendedPorts))
	for _, p := range intendedPorts {
		portMap[addrKey{snn: p.SystemNetworkName, ip: p.IPv4}] = p.Port
	}

	// Filter and update addresses - only keep those with valid ports
	newAddresses := make([]v1alpha1.DRBDResourceAddressStatus, 0, len(drbdr.Status.Addresses))
	for _, addr := range drbdr.Status.Addresses {
		key := addrKey{snn: addr.SystemNetworkName, ip: addr.Address.IPv4}
		if newPort, ok := portMap[key]; ok {
			addr.Address.Port = newPort
			newAddresses = append(newAddresses, addr)
		}
	}

	if len(newAddresses) == 0 {
		drbdr.Status.Addresses = nil
	} else {
		drbdr.Status.Addresses = newAddresses
	}
}

// ensureAddresses ensures that drbdr.status.addresses contains the correct
// IP addresses and ports based on the DRBDResource spec and Node addresses.
// This mutates drbdr.Status in-memory; patching is done by the caller based
// on semantic equality comparison with the base object.
//
// existingPorts provides ports currently configured in the DRBD kernel. When
// status.addresses has no port for an IP, the DRBD kernel port is adopted
// before falling back to portAllocator. Pass nil when no DRBD resource exists.
//
// If port allocation fails for some addresses, those addresses are excluded
// from status and an error is returned. The error is non-critical and should
// not cause early exit from reconciliation.
func ensureAddresses(
	ctx context.Context,
	drbdr *v1alpha1.DRBDResource,
	node *corev1.Node,
	existingPorts ExistingPorts,
	portAllocator PortAllocator,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "ensure-addresses")
	defer ef.OnEnd(&outcome)

	// Compute and apply IPs
	intendedIPs := computeIntendedIPs(drbdr, node)
	applyIPs(drbdr, intendedIPs)

	// Compute and apply ports
	intendedPorts, portErr := computeIntendedPorts(drbdr, existingPorts, portAllocator)
	applyPorts(drbdr, intendedPorts)

	if portErr != nil {
		return ef.Err(portErr)
	}
	return ef.Ok()
}
