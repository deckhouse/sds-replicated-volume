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

// Package rvrdiskfulcount implements the rvr-diskful-count-controller, which manages
// the creation of Diskful replicas to meet replication requirements.
//
// # Controller Responsibilities
//
// The controller manages Diskful replicas by:
//   - Creating Diskful replicas up to the target count specified in ReplicatedStorageClass
//   - Ensuring the first replica is fully ready before creating additional replicas
//   - Allowing parallel creation of second and subsequent replicas
//   - Setting ownerReferences to link replicas to their ReplicatedVolume
//
// # Watched Resources
//
// The controller watches:
//   - ReplicatedVolume: To determine target replica count from storage class
//   - ReplicatedVolumeReplica: To track existing replicas and their readiness
//   - ReplicatedStorageClass: To get replication settings
//
// # Triggers
//
// The controller reconciles when:
//   - CREATE(RV) - New volume needs initial replicas
//   - UPDATE(RVR[metadata.deletionTimestamp -> !null]) - Replica being deleted
//   - UPDATE(RVR[status.conditions[type=Ready].status == True]) - First replica becomes ready
//
// # Target Replica Count
//
// The target count is determined by rsc.spec.replication:
//   - None: 1 Diskful replica
//   - Availability: 2 Diskful replicas
//   - ConsistencyAndAvailability: 3 Diskful replicas
//
// # Reconciliation Flow
//
//  1. Check prerequisites:
//     - RV must have the controller finalizer
//  2. If RV is being deleted (only module finalizers remain):
//     - Do not create new replicas
//  3. Get the ReplicatedStorageClass via rv.spec.replicatedStorageClassName
//  4. Determine target Diskful replica count from rsc.spec.replication
//  5. Count existing Diskful replicas (excluding those being deleted)
//  6. If current count < target count:
//     a. For the first replica (count == 0):
//        - Create one replica and wait for it to be Ready
//     b. For subsequent replicas (count >= 1):
//        - Create remaining replicas (can be created in parallel)
//  7. For each new replica:
//     - Set spec.type=Diskful
//     - Set spec.replicatedVolumeName to RV name
//     - Set metadata.ownerReferences pointing to the RV
//  8. Update rv.status.conditions[type=DiskfulReplicaCountReached]:
//     - status=True when current count == target count
//     - status=False when current count < target count
//
// # Status Updates
//
// The controller maintains:
//   - rv.status.conditions[type=DiskfulReplicaCountReached] - Replica count status
//
// Creates:
//   - ReplicatedVolumeReplica resources with spec.type=Diskful
//
// # Special Notes
//
// Sequential First Replica:
//   - The first Diskful replica must complete initial synchronization before others are created
//   - This ensures a valid data source exists for subsequent replicas
//
// Parallel Subsequent Replicas:
//   - Once the first replica is Ready, remaining replicas can be created simultaneously
//   - This speeds up the volume initialization process
//
// Owner References:
//   - Replicas have ownerReferences pointing to their ReplicatedVolume
//   - This enables automatic cleanup when the volume is deleted
package rvrdiskfulcount
