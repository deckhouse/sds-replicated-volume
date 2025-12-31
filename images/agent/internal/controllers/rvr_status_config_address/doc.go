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

// Package rvrstatusconfigaddress implements the rvr-status-config-address-controller,
// which configures the network address and port for DRBD communication on each replica.
//
// # Controller Responsibilities
//
// The controller assigns network configuration for DRBD by:
//   - Extracting the node's internal IPv4 address from Kubernetes Node status
//   - Allocating a free port within the configured DRBD port range (7000-7999)
//   - Setting rvr.status.drbd.config.address with IPv4 and port information
//   - Tracking configuration status in RVR conditions
//
// # Watched Resources
//
// The controller watches:
//   - ReplicatedVolumeReplica: To detect replicas needing address configuration
//   - Node: To obtain the node's internal IP address
//
// Only replicas where rvr.spec.nodeName matches the controller's NODE_NAME are processed.
//
// # Triggers
//
// The controller reconciles when:
//   - CREATE/UPDATE(RVR) where rvr.spec.nodeName is set but rvr.status.drbd.config.address is not
//
// # Address Configuration
//
// IPv4 Address:
//   - Extracted from node.status.addresses[type=InternalIP]
//
// Port Selection:
//   - Range: 7000-7999 (drbdMinPort to drbdMaxPort)
//   - Algorithm: Find the smallest available port not used by other RVRs on this node
//
// If no IP address or free port is available, the reconciliation will fail and retry.
//
// # Reconciliation Flow
//
//  1. Verify that rvr.status.drbd.config.address is not already set
//  2. Fetch the Node resource matching rvr.spec.nodeName
//  3. Extract InternalIP from node.status.addresses
//  4. Scan all RVRs on this node to determine used ports
//  5. Find the smallest available port in the DRBD port range
//  6. Update rvr.status.drbd.config.address with IPv4 and port
//  7. Set rvr.status.conditions[type=AddressConfigured].status=True
//
// # Status Updates
//
// The controller maintains:
//   - rvr.status.drbd.config.address - Network address configuration (IPv4 and port)
//   - rvr.status.conditions[type=AddressConfigured] - Configuration success/failure status
//
// # Special Notes
//
// The controller only processes resources when the RV has the controller finalizer
// (sds-replicated-volume.deckhouse.io/controller) set.
//
// Resources marked for deletion (metadata.deletionTimestamp set) are only considered
// deleted if they don't have non-module finalizers (those not starting with
// sds-replicated-volume.deckhouse.io/).
package rvrstatusconfigaddress
