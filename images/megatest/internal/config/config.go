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

package config

import (
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
)

// Duration represents a time duration range with min and max values
type DurationMinMax struct {
	Min time.Duration
	Max time.Duration
}

// Count represents a count range with min and max values
type StepMinMax struct {
	Min int
	Max int
}

// MultiVolumeConfig configures the multivolume orchestrator
type MultiVolumeConfig struct {
	StorageClasses                []string
	MaxVolumes                    int
	VolumeStep                    StepMinMax
	StepPeriod                    DurationMinMax
	VolumePeriod                  DurationMinMax
	DisablePodDestroyer           bool
	DisableVolumeResizer          bool
	DisableVolumeReplicaDestroyer bool
	DisableVolumeReplicaCreator   bool
}

// VolumeMainConfig configures the volume-main goroutine
type VolumeMainConfig struct {
	StorageClassName              string
	VolumeLifetime                time.Duration
	InitialSize                   resource.Quantity
	DisableVolumeResizer          bool
	DisableVolumeReplicaDestroyer bool
	DisableVolumeReplicaCreator   bool
}

// VolumePublisherConfig configures the volume-publisher goroutine
type VolumePublisherConfig struct {
	Period DurationMinMax
}

// VolumeReplicaDestroyerConfig configures the volume-replica-destroyer goroutine
type VolumeReplicaDestroyerConfig struct {
	Period DurationMinMax
}

// VolumeReplicaCreatorConfig configures the volume-replica-creator goroutine
type VolumeReplicaCreatorConfig struct {
	Period DurationMinMax
}
