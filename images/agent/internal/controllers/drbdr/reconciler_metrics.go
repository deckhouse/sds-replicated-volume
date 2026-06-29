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

package drbdr

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/metrics"
)

type drbdrMetricObservation func()

type drbdrMetricObservations []drbdrMetricObservation

// observe records lifecycle/object-state events into the in-process Prometheus registry.
// Call it only after the Kubernetes state described by the observations has been
// successfully committed, or when it already comes from committed status.
func (observations drbdrMetricObservations) observe() {
	for _, observe := range observations {
		observe()
	}
}

// computeDRBDRMetricObservations compares the DRBDR state before and after reconciliation.
func computeDRBDRMetricObservations(
	now time.Time,
	drbdr *v1alpha1.DRBDResource,
	statusBase *v1alpha1.DRBDResource,
) drbdrMetricObservations {
	if drbdr == nil || statusBase == nil {
		return nil
	}

	var observations drbdrMetricObservations

	// Detect Configured condition transition to True.
	oldCond := obju.GetStatusCondition(statusBase, v1alpha1.DRBDResourceCondConfiguredType)
	newCond := obju.GetStatusCondition(drbdr, v1alpha1.DRBDResourceCondConfiguredType)

	wasTrue := oldCond != nil && oldCond.Status == metav1.ConditionTrue
	isTrue := newCond != nil && newCond.Status == metav1.ConditionTrue

	if !wasTrue && isTrue {
		dur := now.Sub(drbdr.CreationTimestamp.Time).Seconds()
		observations = append(observations, func() {
			metrics.DRBDRConfiguredDuration.Observe(dur)
		})
	}

	return observations
}

// observeDRBDRDeletion records the deletion duration when the agent finalizer is removed.
func observeDRBDRDeletion(drbdr *v1alpha1.DRBDResource) {
	if drbdr == nil || drbdr.DeletionTimestamp == nil {
		return
	}
	dur := time.Since(drbdr.DeletionTimestamp.Time).Seconds()
	metrics.DRBDRDeletionDuration.Observe(dur)
}
