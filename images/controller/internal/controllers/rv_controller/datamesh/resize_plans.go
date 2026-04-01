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
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/drbd_size"
)

// rgStep creates a resize GlobalStep with standard DiagnosticConditions.
func rgStep(
	name string,
	apply func(*globalContext) bool,
	confirm func(*globalContext, int64) dmte.ConfirmResult,
) *dmte.GlobalStepBuilder[*globalContext] {
	return dmte.GlobalStep(name, apply, confirm).
		DiagnosticConditions(v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType)
}

// applyResizeVolume sets datamesh.size to rv.Spec.Size rounded up to 4Ki.
func applyResizeVolume(gctx *globalContext) bool {
	aligned := drbd_size.AlignTo4Ki(gctx.size)
	if gctx.datamesh.size.Equal(aligned) {
		return false
	}
	gctx.datamesh.size = aligned
	return true
}

// registerResizePlans registers the ResizeVolume plan.
//
// resize/v1: 1-step global plan.
//   - Apply: sets datamesh.size = AlignTo4Ki(rv.Spec.Size).
//   - Confirm: confirmAllMembers (revision-based). The rvr_controller gates
//     datameshRevision on DRBD usable size confirmation (step 13c), so
//     revision-based confirm implicitly waits for DRBD resize completion.
//   - Guards: guardNoActiveResync (no active resync on any diskful member),
//     guardBackingVolumesGrown (all diskful members' backing volumes have
//     already grown to the target lower size).
func registerResizePlans(reg *dmte.Registry[*globalContext, *ReplicaContext]) {
	resizeVolume := reg.GlobalTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeResizeVolume)

	resizeVolume.Plan("resize/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupResize).
		DisplayName("Resizing volume").
		Guards(guardHasReadyDiskfulMember, guardNoActiveResync, guardBackingVolumesGrown).
		Steps(
			rgStep("resize",
				applyResizeVolume,
				confirmAllMembers,
			),
		).
		Build()
}
