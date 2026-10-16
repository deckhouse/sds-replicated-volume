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
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

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
	configured *configuredDRBDRTracker,
) drbdrMetricObservations {
	if drbdr == nil || statusBase == nil {
		return nil
	}

	var observations drbdrMetricObservations
	observations = append(observations, computeDRBDRConfiguredMetricObservations(now, drbdr, statusBase, configured)...)
	return observations
}

// computeDRBDRConfiguredMetricObservations records the time from DRBDR creation to its first
// Configured=True. Later returns to True (after a failed reconfiguration or maintenance) are
// not configuration latency and would otherwise record the age of the resource.
func computeDRBDRConfiguredMetricObservations(
	now time.Time,
	drbdr *v1alpha1.DRBDResource,
	statusBase *v1alpha1.DRBDResource,
	configured *configuredDRBDRTracker,
) drbdrMetricObservations {
	oldCond := obju.GetStatusCondition(statusBase, v1alpha1.DRBDResourceCondConfiguredType)
	newCond := obju.GetStatusCondition(drbdr, v1alpha1.DRBDResourceCondConfiguredType)

	wasTrue := oldCond != nil && oldCond.Status == metav1.ConditionTrue
	isTrue := newCond != nil && newCond.Status == metav1.ConditionTrue
	if wasTrue || !isTrue || !configured.isFirstConfiguration(drbdr) {
		return nil
	}

	name, uid := drbdr.Name, drbdr.UID
	dur := now.Sub(drbdr.CreationTimestamp.Time).Seconds()
	return drbdrMetricObservations{
		func() {
			metrics.DRBDRConfiguredDuration.Observe(dur)
			configured.markConfigured(name, uid)
		},
	}
}

// configuredDRBDRTracker remembers DRBDResources that have already been configured, so that
// only the first Configured=True is reported. The DRBDR status has no field that records a past
// successful configuration for every resource type (deviceUUID exists only for diskful ones),
// so the state is kept in memory. Resources created before the agent started are never
// reported: whether they were configured before the restart is unknown. The cost is one lost
// observation for a resource whose first configuration spans an agent restart.
//
// Entries are keyed by name and hold the UID of the configured object: a resource recreated
// under the same name is a new one, and an entry can be released by name when the object is
// already gone and only its name is known (the reconcile request of a deleted resource).
// So the tracker holds at most one entry per resource name.
//
// startedAt is the node clock, CreationTimestamp is the API server clock: with a clock skew, a
// resource created within the skew around the agent start may be misclassified, one lost or
// extra observation at most. Durations measured from CreationTimestamp carry the same skew.
type configuredDRBDRTracker struct {
	startedAt time.Time

	mu     sync.Mutex
	byName map[string]types.UID
}

func newConfiguredDRBDRTracker(startedAt time.Time) *configuredDRBDRTracker {
	return &configuredDRBDRTracker{startedAt: startedAt, byName: make(map[string]types.UID)}
}

func (t *configuredDRBDRTracker) isFirstConfiguration(drbdr *v1alpha1.DRBDResource) bool {
	if drbdr.CreationTimestamp.Time.Before(t.startedAt) {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	uid, ok := t.byName[drbdr.Name]
	return !ok || uid != drbdr.UID
}

func (t *configuredDRBDRTracker) markConfigured(name string, uid types.UID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.byName[name] = uid
}

func (t *configuredDRBDRTracker) forget(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.byName, name)
}

// observeDRBDRDeletion records the deletion duration when the agent finalizer is removed.
func observeDRBDRDeletion(drbdr *v1alpha1.DRBDResource) {
	if drbdr == nil || drbdr.DeletionTimestamp == nil {
		return
	}
	dur := time.Since(drbdr.DeletionTimestamp.Time).Seconds()
	metrics.DRBDRDeletionDuration.Observe(dur)
}
