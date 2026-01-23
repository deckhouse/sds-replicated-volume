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
	// ReplicatedVolumeOperationCondCompletedType indicates whether the operation has completed.
	// When status is True, the operation finished (check reason for success/failure).
	// When status is False, the operation is still in progress or pending.
	//
	// Reasons describe completion state.
	ReplicatedVolumeOperationCondCompletedType             = "Completed"
	ReplicatedVolumeOperationCondCompletedReasonFailed     = "Failed"     // Operation failed.
	ReplicatedVolumeOperationCondCompletedReasonInProgress = "InProgress" // Operation is being processed.
	ReplicatedVolumeOperationCondCompletedReasonPending    = "Pending"    // Operation is waiting to be processed.
	ReplicatedVolumeOperationCondCompletedReasonSucceeded  = "Succeeded"  // Operation completed successfully.
	ReplicatedVolumeOperationCondCompletedReasonVolumeGone = "VolumeGone" // Target volume no longer exists.
)
