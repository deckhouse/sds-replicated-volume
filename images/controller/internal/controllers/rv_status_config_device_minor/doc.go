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

// Package rvstatusconfigdeviceminor implements the rv-status-config-device-minor-controller,
// which assigns a unique DRBD device minor number to each ReplicatedVolume.
//
// # Controller Responsibilities
//
// The controller ensures unique device identification by:
//   - Allocating the smallest available device minor number
//   - Ensuring uniqueness across all ReplicatedVolumes in the cluster
//   - Persisting the assignment in rv.status.drbd.config.deviceMinor
//
// # Watched Resources
//
// The controller watches:
//   - ReplicatedVolume: To detect volumes needing device minor assignment
//
// # Triggers
//
// The controller reconciles when:
//   - CREATE/UPDATE(RV) where rv.status.drbd.config.deviceMinor is not set
//
// # Device Minor Allocation
//
// The controller:
//  1. Lists all ReplicatedVolumes in the cluster
//  2. Collects all currently assigned device minor numbers
//  3. Finds the smallest available (unused) minor number
//  4. Assigns it to rv.status.drbd.config.deviceMinor
//
// # Reconciliation Flow
//
//  1. Check if rv.status.drbd.config.deviceMinor is already set
//  2. If not set:
//     a. List all ReplicatedVolumes
//     b. Build a set of used device minor numbers
//     c. Find the smallest available number (starting from 0)
//     d. Update rv.status.drbd.config.deviceMinor
//
// # Status Updates
//
// The controller maintains:
//   - rv.status.drbd.config.deviceMinor - Unique DRBD device minor number
//
// # Special Notes
//
// Device minor numbers are permanent once assigned and remain unchanged for the
// lifetime of the ReplicatedVolume. This ensures consistent DRBD device paths
// (/dev/drbdX) on all nodes.
package rvstatusconfigdeviceminor
