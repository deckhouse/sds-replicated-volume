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
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/k8sclient"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/logging"
)

const (
	volumeCheckerPollInterval = 2 * time.Second
)

// VolumeChecker watches a ReplicatedVolume and logs state changes
// It monitors that the RV remains Ready and has Quorum
type VolumeChecker struct {
	rvName     string
	client     *k8sclient.Client
	log        *logging.Logger
	instanceID string

	// Last observed state
	lastReady  metav1.ConditionStatus
	lastQuorum bool
}

// NewVolumeChecker creates a new VolumeChecker for the given RV
func NewVolumeChecker(rvName string, client *k8sclient.Client, instanceID string) *VolumeChecker {
	return &VolumeChecker{
		rvName:     rvName,
		client:     client,
		log:        logging.NewLogger(rvName, "volume-checker", instanceID),
		instanceID: instanceID,
		lastReady:  metav1.ConditionUnknown,
		lastQuorum: false,
	}
}

// Name returns the runner name
func (v *VolumeChecker) Name() string {
	return "volume-checker"
}

// Run starts watching the RV until context is cancelled
func (v *VolumeChecker) Run(ctx context.Context) error {
	v.log.Info("starting volume checker")
	defer v.log.Info("volume checker stopped")

	ticker := time.NewTicker(volumeCheckerPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := v.check(ctx); err != nil {
				v.log.Error("error checking volume", err)
			}
		}
	}
}

func (v *VolumeChecker) check(ctx context.Context) error {
	rv, err := v.client.GetRV(ctx, v.rvName)
	if err != nil {
		return err
	}

	currentReady := v.getReadyStatus(rv)
	currentQuorum := v.hasQuorum(rv)

	// Check for Ready status changes
	if currentReady != v.lastReady {
		v.log.StateChanged(
			string(v.lastReady),
			string(currentReady),
			logging.ActionParams{
				"condition": "Ready",
			},
		)
		v.lastReady = currentReady
	}

	// Check for Quorum status changes
	if currentQuorum != v.lastQuorum {
		expectedQuorum := "true"
		observedQuorum := "false"
		if currentQuorum {
			observedQuorum = "true"
		}
		if !v.lastQuorum {
			expectedQuorum = "false"
		}

		v.log.StateChanged(
			expectedQuorum,
			observedQuorum,
			logging.ActionParams{
				"condition": "Quorum",
			},
		)
		v.lastQuorum = currentQuorum
	}

	return nil
}

func (v *VolumeChecker) getReadyStatus(rv *v1alpha3.ReplicatedVolume) metav1.ConditionStatus {
	if rv.Status == nil {
		return metav1.ConditionUnknown
	}
	cond := meta.FindStatusCondition(rv.Status.Conditions, v1alpha3.ConditionTypeReady)
	if cond == nil {
		return metav1.ConditionUnknown
	}
	return cond.Status
}

func (v *VolumeChecker) hasQuorum(rv *v1alpha3.ReplicatedVolume) bool {
	// We determine quorum based on Ready status
	// A more sophisticated check would inspect all replicas' Quorum conditions
	return v.getReadyStatus(rv) == metav1.ConditionTrue
}
