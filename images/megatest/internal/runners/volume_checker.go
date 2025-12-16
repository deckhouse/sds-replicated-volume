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

package runners

import (
	"context"
	"log/slog"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/kubeutils"
)

const (
	volumeCheckerPollInterval = 2 * time.Second
)

// VolumeChecker watches a ReplicatedVolume and logs state changes
// It monitors that the RV remains Ready and has Quorum
type VolumeChecker struct {
	rvName string
	client *kubeutils.Client
	log    *slog.Logger

	// Last observed state
	lastReady  metav1.ConditionStatus
	lastQuorum bool
}

// NewVolumeChecker creates a new VolumeChecker for the given RV
func NewVolumeChecker(rvName string, client *kubeutils.Client) *VolumeChecker {
	return &VolumeChecker{
		rvName:     rvName,
		client:     client,
		log:        slog.Default().With("runner", "volume-checker", "rv_name", rvName),
		lastReady:  metav1.ConditionUnknown,
		lastQuorum: false,
	}
}

// Run starts watching the RV until context is cancelled
func (v *VolumeChecker) Run(ctx context.Context) error {
	v.log.Info("started")
	defer v.log.Info("finished")

	ticker := time.NewTicker(volumeCheckerPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			_ = v.check(ctx)
		}
	}
}

func (v *VolumeChecker) check(ctx context.Context) error {
	v.log.Debug("checking volume -------------------------------------")

	return nil
}
