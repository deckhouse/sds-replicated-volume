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

package v1alpha1

const (
	// ReplicatedVolumeSnapshotCondAdminLockedType reports whether the
	// cluster-wide DRBD admin lock is currently held on behalf of this RVS.
	//
	// True: the agent has acquired the kernel lock and the snapshot orchestration
	// can issue admin operations on the underlying DRBDResource.
	// False: the lock is not (yet) held — the controller is either waiting for
	// preconditions, retrying after a transient failure, or has released it
	// because the snapshot has reached a terminal phase.
	//
	// Reasons describe the concrete sub-state.
	ReplicatedVolumeSnapshotCondAdminLockedType                    = "AdminLocked"
	ReplicatedVolumeSnapshotCondAdminLockedReasonAcquired          = "Acquired"          // Agent has taken the kernel-level admin lock.
	ReplicatedVolumeSnapshotCondAdminLockedReasonAcquiring         = "Acquiring"         // LockAdmin DRBDOp created; waiting for the agent to take the lock.
	ReplicatedVolumeSnapshotCondAdminLockedReasonClusterNotReady   = "ClusterNotReady"   // Pre-check failed: not all replicas are UpToDate/Established yet.
	ReplicatedVolumeSnapshotCondAdminLockedReasonHolderUnready     = "HolderUnready"     // DRBDResource of the chosen holder is missing or not Up yet.
	ReplicatedVolumeSnapshotCondAdminLockedReasonNoHolder          = "NoHolder"          // No diskful member in the datamesh to host the lock.
	ReplicatedVolumeSnapshotCondAdminLockedReasonReleased          = "Released"          // Lock released; snapshot reached a terminal phase or is being deleted.
	ReplicatedVolumeSnapshotCondAdminLockedReasonReleasing         = "Releasing"         // LockAdmin DRBDOp deleted; waiting for the agent to drain the finalizer.
	ReplicatedVolumeSnapshotCondAdminLockedReasonRetryingTransient = "RetryingTransient" // Previous LockAdmin DRBDOp ended in Failed; retrying with a fresh op.
)
