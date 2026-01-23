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

package rvrschedulingcontroller

import (
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

type SchedulingContext struct {
	RV              *v1alpha1.ReplicatedVolume
	RSC             *v1alpha1.ReplicatedStorageClass
	EligibleNodes   []v1alpha1.ReplicatedStorageClassEligibleNode
	AttachToNodes   []string
	NodeToZone      map[string]string
	LVGToNode       map[string]LVGInfo
	StoragePoolType string

	AllRVRs               []*v1alpha1.ReplicatedVolumeReplica
	ScheduledDiskful      []*v1alpha1.ReplicatedVolumeReplica
	UnscheduledDiskful    []*v1alpha1.ReplicatedVolumeReplica
	ScheduledTieBreaker   []*v1alpha1.ReplicatedVolumeReplica
	UnscheduledTieBreaker []*v1alpha1.ReplicatedVolumeReplica
	OccupiedNodes         map[string]struct{}
}

type NodeCandidate struct {
	Name  string
	Zone  string
	Score int
}

type LVGInfo struct {
	NodeName     string
	ThinPoolName string
}
