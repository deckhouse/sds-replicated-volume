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

// observeDRBDRMetrics compares the DRBDR state before and after reconciliation
// and records appropriate metrics.
func observeDRBDRMetrics(
	drbdr *v1alpha1.DRBDResource,
	statusBase *v1alpha1.DRBDResource,
) {
	if drbdr == nil || statusBase == nil {
		return
	}

	rvLabel := drbdr.Labels[v1alpha1.ReplicatedVolumeLabelKey]

	// Detect Configured condition transition to True.
	oldCond := obju.GetStatusCondition(statusBase, v1alpha1.DRBDResourceCondConfiguredType)
	newCond := obju.GetStatusCondition(drbdr, v1alpha1.DRBDResourceCondConfiguredType)

	wasTrue := oldCond != nil && oldCond.Status == metav1.ConditionTrue
	isTrue := newCond != nil && newCond.Status == metav1.ConditionTrue

	if !wasTrue && isTrue {
		dur := time.Since(drbdr.CreationTimestamp.Time).Seconds()
		metrics.DRBDRConfiguredDuration.Observe(dur)
	}

	// Health gauge: report Configured condition value.
	if newCond != nil {
		val := float64(0)
		if newCond.Status == metav1.ConditionTrue {
			val = 1
		}
		metrics.DRBDRCondition.WithLabelValues(drbdr.Name, rvLabel, v1alpha1.DRBDResourceCondConfiguredType).Set(val)
	}
}

// observeDRBDRDeletion records the deletion duration when the agent finalizer is removed.
func observeDRBDRDeletion(drbdr *v1alpha1.DRBDResource) {
	if drbdr == nil || drbdr.DeletionTimestamp == nil {
		return
	}
	dur := time.Since(drbdr.DeletionTimestamp.Time).Seconds()
	metrics.DRBDRDeletionDuration.Observe(dur)
}

// cleanupDRBDRMetrics removes all per-object metrics for a deleted DRBDR.
func cleanupDRBDRMetrics(drbdr *v1alpha1.DRBDResource) {
	if drbdr == nil {
		return
	}
	rvLabel := drbdr.Labels[v1alpha1.ReplicatedVolumeLabelKey]
	metrics.DRBDRCondition.DeleteLabelValues(drbdr.Name, rvLabel, v1alpha1.DRBDResourceCondConfiguredType)
}
