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

package datamesh

import (
	"context"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
)

// FeatureFlags describes cluster-level feature availability for transition path resolution.
type FeatureFlags struct {
	// ShadowDiskful indicates whether ShadowDiskful (sD) replicas are supported.
	// Requires the Flant DRBD kernel extension with the non-voting disk option.
	// When true, sD-based paths are used for D transitions (faster resync via pre-sync).
	// When false, A-based vestibule paths are used as fallback.
	ShadowDiskful bool
}

// registry is the package-level plan registry, initialized once at startup.
var registry *dmte.Registry[*globalContext, *ReplicaContext]

// BuildRegistry builds and stores the plan registry. Called once at controller startup.
// Panics if called twice.
func BuildRegistry() {
	if registry != nil {
		panic("datamesh.BuildRegistry: already initialized")
	}
	registry = dmte.NewRegistry[*globalContext, *ReplicaContext]()
	registerSlots(registry)
	registerMembershipPlans(registry)
	registerQuorumPlans(registry)
	registerAttachmentPlans(registry)
	registerNetworkPlans(registry)
}

// PlanStepCount returns the number of steps for the given transition type
// and plan ID. Returns 0 if the plan is not found.
func PlanStepCount(tt string, planID string) int {
	if registry == nil {
		panic("datamesh.PlanStepCount: registry not initialized, call BuildRegistry first")
	}
	return registry.PlanStepCount(dmte.TransitionType(tt), dmte.PlanID(planID))
}

// MaxPlanStepCount returns the maximum step count across all plans for
// the given transition type. Returns 0 if no plans exist.
func MaxPlanStepCount(tt string) int {
	if registry == nil {
		panic("datamesh.MaxPlanStepCount: registry not initialized, call BuildRegistry first")
	}
	return registry.MaxPlanStepCount(dmte.TransitionType(tt))
}

// ProcessTransitions runs the datamesh transition engine for one reconciliation cycle.
//
// Synchronizes member zone info from RSP, settles in-flight transitions,
// dispatches new transitions from pending requests, writes back all mutations
// (members, q/qmr, baseline GMDR, request messages), and computes the
// effective layout from observable cluster state.
//
// Returns (changed, allReplicas): changed is true if rv.Status was mutated,
// allReplicas is the full slice of replica contexts including orphan RVA nodes.
func ProcessTransitions(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rsp RSP,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	rvas []*v1alpha1.ReplicatedVolumeAttachment,
	features FeatureFlags,
) (bool, []ReplicaContext) {
	// 1. Build contexts.
	cp := buildContexts(rv, rsp, rvrs, rvas, features)
	gctx := cp.Global()

	// 2. Update member zones from RSP (zone may change if RSP is updated).
	zonesChanged := updateMemberZonesFromRSP(gctx)

	// 3. Create engine.
	dispatchers := []dmte.DispatchFunc[provider]{
		networkDispatcher(),
		quorumDispatcher(),
		membershipDispatcher(),
		attachmentDispatcher(),
	}
	tracker := newConcurrencyTracker(gctx)
	engine := dmte.NewEngine(ctx, registry, tracker, dispatchers,
		&rv.Status.DatameshRevision, rv.Status.DatameshTransitions, cp)

	// 4. Process.
	changed := engine.Process(ctx)

	// 5. Finalize transitions.
	rv.Status.DatameshTransitions = engine.Finalize()

	// 6. If engine or zone sync changed — writeback members + datamesh scalars + baseline layout.
	if changed || zonesChanged {
		writebackMembersFromContexts(rv, gctx)
		if changed {
			writebackDatameshFromContext(rv, gctx)
			writebackBaselineGMDRFromContext(rv, gctx)
		}
		changed = true
	}

	// 7. Always writeback request messages.
	if writebackRequestMessagesFromContexts(gctx) {
		changed = true
	}

	// 8. Always compute effective layout (not gated by engine changed —
	// effective layout reflects actual cluster state, which may change even
	// without engine mutations, e.g. due to voter failure/recovery).
	if updateEffectiveLayout(gctx, &rv.Status.EffectiveLayout) {
		changed = true
	}

	return changed, gctx.allReplicas
}
