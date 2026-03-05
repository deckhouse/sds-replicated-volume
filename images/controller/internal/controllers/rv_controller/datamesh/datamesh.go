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
	registerAttachmentPlans(registry)
}

// ProcessTransitions runs the datamesh transition engine for one reconciliation cycle.
//
//  1. Builds contexts from the current state.
//  2. Creates and runs the engine (settle + dispatch).
//  3. Finalizes transitions back to rv.Status.DatameshTransitions.
//  4. If the engine mutated transitions — writes back members and datamesh scalars.
//  5. Always writes back request messages (slot status updates happen even without
//     transition changes).
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

	// 2. Create engine.
	dispatchers := []dmte.DispatchFunc[provider]{
		membershipDispatcher(),
		attachmentDispatcher(),
	}
	tracker := newConcurrencyTracker(gctx)
	engine := dmte.NewEngine(ctx, registry, tracker, dispatchers,
		&rv.Status.DatameshRevision, rv.Status.DatameshTransitions, cp)

	// 3. Process.
	changed := engine.Process(ctx)

	// 4. Finalize transitions.
	rv.Status.DatameshTransitions = engine.Finalize()

	// 5. If engine changed — writeback members + datamesh scalars + baseline layout.
	if changed {
		writebackMembersFromContexts(rv, gctx)
		writebackDatameshFromContext(rv, gctx)
		writebackBaselineLayoutFromContext(rv, gctx)
	}

	// 6. Always writeback request messages.
	if writebackRequestMessagesFromContexts(gctx) {
		changed = true
	}

	// 7. Always compute effective layout (not gated by engine changed —
	// effective layout reflects actual cluster state, which may change even
	// without engine mutations, e.g. due to voter failure/recovery).
	// If the computed value differs from the current one, mark changed.
	if el := computeEffectiveLayout(gctx); !effectiveLayoutEqual(rv.Status.EffectiveLayout, el) {
		rv.Status.EffectiveLayout = el
		changed = true
	}

	return changed, gctx.allReplicas
}
