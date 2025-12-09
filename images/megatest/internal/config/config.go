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
type Duration struct {
	Min time.Duration
	Max time.Duration
}

// Size represents a size range with min and max values
type Size struct {
	Min resource.Quantity
	Max resource.Quantity
}

// Count represents a count range with min and max values
type Count struct {
	Min int
	Max int
}

// VolumePublisherConfig configures the volume-publisher goroutine
type VolumePublisherConfig struct {
	Period Duration
}

// VolumeResizerConfig configures the volume-resizer goroutine
type VolumeResizerConfig struct {
	Period Duration
	Step   Size
}

// VolumeReplicaDestroyerConfig configures the volume-replica-destroyer goroutine
type VolumeReplicaDestroyerConfig struct {
	Period Duration
}

// VolumeReplicaCreatorConfig configures the volume-replica-creator goroutine
type VolumeReplicaCreatorConfig struct {
	Period Duration
}

// VolumeMainConfig configures the volume-main goroutine
type VolumeMainConfig struct {
	StorageClassName string
	LifetimePeriod   time.Duration
	InitialSize      resource.Quantity
}

// PodDestroyerConfig configures the pod-destroyer goroutine
type PodDestroyerConfig struct {
	Namespace     string
	LabelSelector string
	PodCount      Count
	Period        Duration
}

// MultiVolumeConfig configures the multivolume orchestrator
type MultiVolumeConfig struct {
	StorageClasses []string
	MaxVolumes     int
	Step           Count
	StepPeriod     Duration
	VolumePeriod   Duration
}

// DefaultVolumePublisherConfig returns default configuration for volume-publisher
func DefaultVolumePublisherConfig(periodMin, periodMax time.Duration) VolumePublisherConfig {
	return VolumePublisherConfig{
		Period: Duration{Min: periodMin, Max: periodMax},
	}
}

// DefaultVolumeResizerConfig returns default configuration for volume-resizer
func DefaultVolumeResizerConfig() VolumeResizerConfig {
	return VolumeResizerConfig{
		Period: Duration{
			Min: 50 * time.Second,
			Max: 50 * time.Second,
		},
		Step: Size{
			Min: resource.MustParse("4Ki"),
			Max: resource.MustParse("64Ki"),
		},
	}
}

// DefaultVolumeReplicaDestroyerConfig returns default configuration for volume-replica-destroyer
func DefaultVolumeReplicaDestroyerConfig() VolumeReplicaDestroyerConfig {
	return VolumeReplicaDestroyerConfig{
		Period: Duration{
			Min: 30 * time.Second,
			Max: 300 * time.Second,
		},
	}
}

// DefaultVolumeReplicaCreatorConfig returns default configuration for volume-replica-creator
func DefaultVolumeReplicaCreatorConfig() VolumeReplicaCreatorConfig {
	return VolumeReplicaCreatorConfig{
		Period: Duration{
			Min: 30 * time.Second,
			Max: 300 * time.Second,
		},
	}
}

// DefaultPodDestroyerConfig returns default configuration for pod-destroyer
func DefaultPodDestroyerConfig(namespace, labelSelector string, podMin, podMax int, periodMin, periodMax time.Duration) PodDestroyerConfig {
	return PodDestroyerConfig{
		Namespace:     namespace,
		LabelSelector: labelSelector,
		PodCount:      Count{Min: podMin, Max: podMax},
		Period:        Duration{Min: periodMin, Max: periodMax},
	}
}

// DefaultMultiVolumeConfig returns default configuration for multivolume orchestrator
func DefaultMultiVolumeConfig(storageClasses []string, maxVolumes int) MultiVolumeConfig {
	return MultiVolumeConfig{
		StorageClasses: storageClasses,
		MaxVolumes:     maxVolumes,
		Step:           Count{Min: 1, Max: 3},
		StepPeriod:     Duration{Min: 10 * time.Second, Max: 30 * time.Second},
		VolumePeriod:   Duration{Min: 60 * time.Second, Max: 300 * time.Second},
	}
}

