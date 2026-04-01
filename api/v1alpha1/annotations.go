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

const annotationPrefix = "sds-replicated-volume.deckhouse.io/"

const (
	// LVMVolumeGroupUnschedulableAnnotationKey marks an LVMVolumeGroup as unschedulable
	// for new ReplicatedVolumeReplicas.
	LVMVolumeGroupUnschedulableAnnotationKey = annotationPrefix + "unschedulable"

	// SchedulingReservationIDAnnotationKey stores the scheduler-extender reservation ID
	// used for capacity reservation during RVR scheduling.
	SchedulingReservationIDAnnotationKey = annotationPrefix + "scheduling-reservation-id"

	// ReplicatedVolumePVCNamespaceAnnotationKey is a rv-related pvc namespace
	ReplicatedVolumePVCNamespaceAnnotationKey = annotationPrefix + "pvc-namespace"

	// AdoptRVRAnnotationKey triggers the adopt/v1 formation plan when set on a
	// ReplicatedVolume. Formation will adopt pre-existing RVRs instead of creating
	// new ones: no RVR creation/deletion, no DRBDResourceOperation.
	// Presence-based (value is ignored).
	AdoptRVRAnnotationKey = annotationPrefix + "adopt-rvr"

	// AdoptSharedSecretAnnotationKey carries the DRBD shared secret from a
	// pre-existing datamesh into the adopt/v1 formation plan. When set on a
	// ReplicatedVolume, the controller uses its value instead of generating a
	// random secret. The value must be non-empty and at most 64 characters.
	// Only used during adopt/v1 formation.
	AdoptSharedSecretAnnotationKey = annotationPrefix + "adopt-shared-secret"
)
