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

// Package rvrschedulingcontroller implements the rvr-scheduling-controller, which
// assigns nodes to ReplicatedVolumeReplicas based on topology, storage capacity,
// and placement requirements.
//
// # Controller Responsibilities
//
// The controller performs intelligent replica placement by:
//   - Assigning unique nodes to each replica of a ReplicatedVolume
//   - Respecting topology constraints (Zonal, TransZonal, Ignored)
//   - Checking storage capacity via scheduler-extender API
//   - Preferring nodes in rv.spec.attachTo when possible
//   - Handling different scheduling requirements for Diskful, Access, and TieBreaker replicas
//
// # Watched Resources
//
// The controller watches:
//   - ReplicatedVolumeReplica: To detect replicas needing node assignment
//   - ReplicatedVolume: To get placement hints (attachTo)
//   - ReplicatedStorageClass: To get topology and zone constraints
//   - ReplicatedStoragePool: To determine available nodes with storage
//   - Node: To get zone information
//
// # Node Selection Criteria
//
// Eligible nodes are determined by intersection of:
//   - Nodes in zones specified by rsc.spec.zones (or all zones if not specified)
//   - Exception: For Access replicas, all nodes are eligible regardless of zones
//   - Nodes with LVG from rsp.spec.lvmVolumeGroups (only for Diskful replicas)
//   - Access and TieBreaker replicas can be scheduled on any node
//
// # Scheduling Phases
//
// The controller schedules replicas in three sequential phases:
//
// Phase 1: Diskful Replicas
//   - Exclude nodes already hosting any replica of this RV
//   - Apply topology constraints:
//   - Zonal: All replicas in one zone
//   - If Diskful replicas exist, use their zone
//   - Else if rv.spec.attachTo specified, choose best zone from those nodes
//   - Else choose best zone from allowed zones
//   - TransZonal: Distribute replicas evenly across zones
//   - Place each replica in zone with fewest Diskful replicas
//   - Fail if even distribution is impossible
//   - Ignored: No zone constraints
//   - Check storage capacity via scheduler-extender API
//   - Prefer nodes in rv.spec.attachTo (increase priority)
//
// Phase 2: Access Replicas
//   - Only when rv.spec.attachTo is set AND rsc.spec.volumeAccess != Local
//   - Exclude nodes already hosting any replica of this RV
//   - Target nodes in rv.spec.attachTo without replicas
//   - No topology or storage capacity constraints
//   - OK if some attachTo nodes cannot get replicas (already have other replica types)
//   - OK if some Access replicas cannot be scheduled (all attachTo nodes have replicas)
//
// Phase 3: TieBreaker Replicas
//   - Exclude nodes already hosting any replica of this RV
//   - Apply topology constraints:
//   - Zonal: Place in same zone as Diskful replicas
//   - Fail if no Diskful replicas exist
//   - Fail if insufficient free nodes in zone
//   - TransZonal: Place in zone with fewest replicas (any type)
//   - If multiple zones tied, choose any
//   - Fail if no free nodes in least-populated zones (cannot guarantee balance)
//   - Ignored: No zone constraints
//   - Fail if insufficient free nodes
//
// # Reconciliation Flow
//
//  1. Check prerequisites:
//     - RV must have the controller finalizer
//  2. Get ReplicatedStorageClass and determine topology mode
//  3. List all RVRs for this RV to see existing placements
//  4. Schedule Diskful replicas:
//     a. Collect eligible nodes based on zones and storage pools
//     b. Apply topology rules
//     c. Call scheduler-extender to verify storage capacity
//     d. Assign rvr.spec.nodeName
//  5. Schedule Access replicas (if applicable):
//     a. Identify nodes in attachTo without replicas
//     b. Assign rvr.spec.nodeName
//  6. Schedule TieBreaker replicas:
//     a. Apply topology rules
//     b. Assign rvr.spec.nodeName
//  7. Update rvr.status.conditions[type=Scheduled]:
//     - status=True, reason=ReplicaScheduled when successful
//     - status=False with appropriate reason when scheduling fails:
//     * InsufficientNodes, NoEligibleNodes, TopologyConstraintViolation, etc.
//     - For unscheduled replicas: reason=WaitingForAnotherReplica
//
// # Status Updates
//
// The controller maintains:
//   - rvr.spec.nodeName - Assigned node for the replica
//   - rvr.status.conditions[type=Scheduled] - Scheduling success/failure status
//
// # Scheduler-Extender Integration
//
// For Diskful replicas, the controller calls the scheduler-extender API to:
//   - Filter nodes with sufficient storage capacity
//   - Consider LVM volume group availability
//   - Ensure the volume can actually be created on selected nodes
//
// # Special Notes
//
// Best Zone Selection:
//   - Chooses the zone with most available capacity and nodes
//   - Considers storage pool availability
//
// Topology Guarantees:
//   - Zonal: Failure locality within one availability zone
//   - TransZonal: Replicas survive zone failures, even distribution required
//   - Ignored: No zone awareness, simplest scheduling
//
// The scheduling algorithm ensures that replica placement supports the high
// availability and data consistency guarantees of the storage system.
package rvrschedulingcontroller
