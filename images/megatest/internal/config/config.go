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

// Size represents a size range with min and max values
type SizeMinMax struct {
	Min resource.Quantity
	Max resource.Quantity
}

// Float64MinMax represents a float64 range with min and max values
type Float64MinMax struct {
	Min float64
	Max float64
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

// VolumeAttacherConfig configures the volume-attacher goroutine
type VolumeAttacherConfig struct {
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

// VolumeResizerConfig configures the volume-resizer goroutine
type VolumeResizerConfig struct {
	Period DurationMinMax
	Step   SizeMinMax
}

// PodDestroyerConfig configures the pod-destroyer goroutine
type PodDestroyerConfig struct {
	Namespace     string
	LabelSelector string
	PodCount      StepMinMax
	Period        DurationMinMax
}

// ChaosDRBDBlockerConfig configures the chaos-drbd-blocker goroutine
type ChaosDRBDBlockerConfig struct {
	Period           DurationMinMax
	IncidentDuration DurationMinMax
	// Note: DRBD ports are collected dynamically before each incident
	// from the two selected nodes using DRBDPortCollector.
	// If no ports found, the incident is skipped (waiting for RVs to be created).
}

// ChaosNetworkBlockerConfig configures the chaos-network-blocker goroutine
type ChaosNetworkBlockerConfig struct {
	Period           DurationMinMax
	IncidentDuration DurationMinMax
}

// ChaosNetworkDegraderConfig configures the chaos-network-degrader goroutine
type ChaosNetworkDegraderConfig struct {
	Period           DurationMinMax
	IncidentDuration DurationMinMax
	DelayMs          StepMinMax
	LossPercent      Float64MinMax
	RateMbit         StepMinMax // Bandwidth limit in mbit/s (0 = no limit)
}

// ChaosVMReboterConfig configures the chaos-vm-reboter goroutine
type ChaosVMReboterConfig struct {
	Period DurationMinMax
}

// ChaosNetworkPartitionerConfig configures the chaos-network-partitioner goroutine
type ChaosNetworkPartitionerConfig struct {
	Period           DurationMinMax
	IncidentDuration DurationMinMax
	GroupSize        int // Target size for one of the partition groups (0 = split in half)
}
