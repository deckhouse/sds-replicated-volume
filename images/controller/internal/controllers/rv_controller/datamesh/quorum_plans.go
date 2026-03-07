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

// IMPORTANT: PlanID versioning
//
// PlanIDs are persisted in rv.Status.DatameshTransitions. Changing a plan
// in a way that breaks in-flight transitions requires a NEW version:
//   - Step composition changed (added, removed, reordered)
//   - Step apply semantics changed (different mutations)
//
// Safe changes (no new version needed):
//   - Guards, confirm, DisplayName, diagnostics, OnComplete
//
// To introduce a new version: keep the old version registered (settle-only),
// register the new version, update the dispatcher to yield the new PlanID.

package datamesh

import (
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
)

// registerQuorumPlans registers ChangeQuorum plans.
//
// 4 plans covering all direction combinations of q and qmr changes:
//
//   - lower/v1: q↓ and/or qmr↓ (protection drops then rises)
//   - raise/v1: q↑ and/or qmr↑ (protection drops then rises)
//   - lower-q-raise-qmr/v1: q↓ + qmr↑ (both raise protection)
//   - raise-q-lower-qmr/v1: q↑ + qmr↓ (both lower protection)
//
// All plans are 2-step. Each step applies one component (q or qmr) with
// the correct baseline update timing:
//   - qmr-dropping step: compose with updateBaselineGMDR (apply).
//   - qmr-raising step: OnComplete(updateBaselineGMDR).
//   - q-only steps: no updateBaselineGMDR (baseline depends only on qmr).
//
// When only one of q/qmr changes, the other step is a no-op (sets the
// same value, quick confirm roundtrip).
func registerQuorumPlans(reg *dmte.Registry[*globalContext, *ReplicaContext]) {
	changeQuorum := reg.GlobalTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeQuorum)

	// lower/v1: q↓ and/or qmr↓.
	// Step 1: qmr↓ drops GMDR → baseline in apply.
	// Step 2: q↓ only (no qmr change, no baseline update needed).
	changeQuorum.Plan("lower/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupQuorum).
		DisplayName("Adjusting quorum").
		Steps(
			mgStep("qmr↓",
				composeGlobalApply(
					setCorrectQMR,
					updateBaselineGMDR,
				),
				confirmAllMembers,
			),
			mgStep("q↓",
				setCorrectQ,
				confirmAllMembers,
			),
		).
		Build()

	// raise/v1: q↑ and/or qmr↑.
	// Step 1: q↑ only (no qmr change, no baseline update needed).
	// Step 2: qmr↑ raises GMDR → baseline in OnComplete.
	changeQuorum.Plan("raise/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupQuorum).
		DisplayName("Adjusting quorum").
		Steps(
			mgStep("q↑",
				setCorrectQ,
				confirmAllMembers,
			),
			mgStep("qmr↑",
				setCorrectQMR,
				confirmAllMembers,
			).OnComplete(updateBaselineGMDR),
		).
		Build()

	// lower-q-raise-qmr/v1: q↓ + qmr↑.
	// Step 1: q↓ only (no qmr change, no baseline update needed).
	// Step 2: qmr↑ raises GMDR → baseline in OnComplete.
	changeQuorum.Plan("lower-q-raise-qmr/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupQuorum).
		DisplayName("Adjusting quorum").
		Steps(
			mgStep("q↓",
				setCorrectQ,
				confirmAllMembers,
			),
			mgStep("qmr↑",
				setCorrectQMR,
				confirmAllMembers,
			).OnComplete(updateBaselineGMDR),
		).
		Build()

	// raise-q-lower-qmr/v1: q↑ + qmr↓.
	// Step 1: q↑ only (no qmr change, no baseline update needed).
	// Step 2: qmr↓ drops GMDR → baseline in apply.
	changeQuorum.Plan("raise-q-lower-qmr/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupQuorum).
		DisplayName("Adjusting quorum").
		Steps(
			mgStep("q↑",
				setCorrectQ,
				confirmAllMembers,
			),
			mgStep("qmr↓",
				composeGlobalApply(
					setCorrectQMR,
					updateBaselineGMDR,
				),
				confirmAllMembers,
			),
		).
		Build()
}

// ──────────────────────────────────────────────────────────────────────────────
// Apply functions
//

// setCorrectQ sets only q from computeCorrectQuorum.
func setCorrectQ(gctx *globalContext) {
	q, _ := computeCorrectQuorum(gctx)
	gctx.datamesh.quorum = q
}

// setCorrectQMR sets only qmr from computeCorrectQuorum.
func setCorrectQMR(gctx *globalContext) {
	_, qmr := computeCorrectQuorum(gctx)
	gctx.datamesh.quorumMinimumRedundancy = qmr
}
