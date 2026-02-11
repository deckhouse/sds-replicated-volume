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
)
