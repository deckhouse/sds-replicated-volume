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

package rvcontroller

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: Configured condition
//

// reconcileConfiguredCondition is the SINGLE writer of the Configured condition: whether every
// datamesh member has applied the volume's current datamesh revision. It only reports; the
// rollout itself is driven elsewhere (the formation steps and the datamesh transition engine).
//
// It lives apart from the data-plane conditions on purpose: Configured answers a
// configuration-convergence question, not an availability one, and never enters Ready — a volume
// mid-rollout keeps serving I/O.
//
// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileConfiguredCondition(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "configured-condition")
	defer rf.OnEnd(&outcome)

	return rf.Continue().ReportChangedIf(applyConfiguredCond(rv, computeConfiguredReport(rv, rvrs)))
}

// ──────────────────────────────────────────────────────────────────────────────
// Report
//

// configuredReport is the computed Configured condition report: whether the current datamesh
// revision has been applied by every datamesh member. present=false means the condition must not
// be touched this pass (deletion freezes it; a volume without a configuration has never had it
// evaluated).
type configuredReport struct {
	present bool
	status  metav1.ConditionStatus
	reason  string
	message string
}

// ──────────────────────────────────────────────────────────────────────────────
// Compute
//

// computeConfiguredReport computes the Configured condition by comparing each datamesh member's
// applied datamesh revision against the volume's current one. Ordered, first match wins:
//
//  1. DeletionTimestamp set        → present=false (frozen at the last value)
//  2. Configuration nil            → present=false (absent = not yet evaluated)
//  3. Volume DatameshRevision == 0 → Unknown / ConfigurationUnknown
//  4. No datamesh members          → Unknown / ConfigurationUnknown
//  5. Every member applied it      → True / AllReplicasConfigured
//  6. otherwise                    → False / ConfigurationInProgress
//
// Rule 3 is a correctness guard, not cosmetics: revision 0 means the volume has never been formed
// (or its formation was restarted, which resets the counter), and the lagging replicas sit at 0
// too — so the equality of rule 5 would hold vacuously and report a convergence that never
// happened.
//
// All datamesh member types count, tie-breakers and Access members included: the replica
// controller records DatameshRevision for every member it configures, gated only on datamesh
// membership (see rvr_controller/reconciler_drbd_resource.go). That is unlike Scheduled, which is
// a placement concept and does not apply to Access replicas.
//
// The comparison is against status.datameshRevision and deliberately NOT against the replica's
// own condition of the same name: RVR Configured reports whether a replica has an outstanding
// membership request, so it stays True across a revision bump that no member has applied yet.
// See the Configured section of README.md.
//
// A member whose replica is missing from rvrs counts as not yet configured: it is in the
// denominator and is named in the message. The comparison is a strict equality, so a replica
// reporting a revision the volume never assigned (which should not happen) also reads as not
// configured — the conservative direction.
//
// It is a pure read-only computation: it MUST NOT mutate rv or rvrs.
func computeConfiguredReport(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) configuredReport {
	// 1. Deletion: preserve the last known value.
	if rv.DeletionTimestamp != nil {
		return configuredReport{present: false}
	}

	// 2. No configuration yet: nothing has been evaluated.
	if rv.Status.Configuration == nil {
		return configuredReport{present: false}
	}

	revision := rv.Status.DatameshRevision

	// 3. No revision assigned yet: there is nothing to have converged on.
	if revision == 0 {
		return configuredReport{
			present: true,
			status:  metav1.ConditionUnknown,
			reason:  v1alpha1.ReplicatedVolumeCondConfiguredReasonConfigurationUnknown,
			message: "Datamesh revision has not been assigned yet",
		}
	}

	// 4. No members: the datamesh has not been populated yet.
	total := len(rv.Status.Datamesh.Members)
	if total == 0 {
		return configuredReport{
			present: true,
			status:  metav1.ConditionUnknown,
			reason:  v1alpha1.ReplicatedVolumeCondConfiguredReasonConfigurationUnknown,
			message: "Datamesh has no members yet",
		}
	}

	// Members are traversed in status order, which is stable across passes, so the named laggard
	// does not jitter while the state is unchanged.
	configured := 0
	firstLaggard := ""
	for i := range rv.Status.Datamesh.Members {
		m := &rv.Status.Datamesh.Members[i]
		rvr := findRVRByName(rvrs, m.Name)
		if rvr != nil && rvr.Status.DatameshRevision == revision {
			configured++
			continue
		}
		if firstLaggard == "" {
			firstLaggard = m.Name
		}
	}

	// 5. Fully converged.
	if configured == total {
		return configuredReport{
			present: true,
			status:  metav1.ConditionTrue,
			reason:  v1alpha1.ReplicatedVolumeCondConfiguredReasonAllReplicasConfigured,
			message: fmt.Sprintf("Configuration revision %d applied on all %d replicas", revision, total),
		}
	}

	// 6. Rollout still in flight.
	return configuredReport{
		present: true,
		status:  metav1.ConditionFalse,
		reason:  v1alpha1.ReplicatedVolumeCondConfiguredReasonConfigurationInProgress,
		message: fmt.Sprintf("Configuration revision %d applied on %d/%d replicas: waiting for %s",
			revision, configured, total, firstLaggard),
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Apply
//

// applyConfiguredCond sets the Configured condition from a computed configuredReport.
// When report.present is false the condition is left untouched (the last known value is
// preserved, e.g. during deletion).
func applyConfiguredCond(rv *v1alpha1.ReplicatedVolume, report configuredReport) bool {
	if !report.present {
		return false
	}
	return obju.SetStatusCondition(rv, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeCondConfiguredType,
		Status:  report.status,
		Reason:  report.reason,
		Message: report.message,
	})
}
