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

package dmte

import (
	"regexp"
	"strconv"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// planIDRegexp validates the PlanID format: "{name}/v{N}" where N >= 1.
var planIDRegexp = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*/v[1-9][0-9]*$`)

// ──────────────────────────────────────────────────────────────────────────────
// Internal plan type
//

// plan describes a registered transition plan: guards, metadata, steps, and
// completion callback. Generic on [G, R] — steps and guards are fully typed.
type plan[G any, R ReplicaCtx] struct {
	// Plan identity.
	scope          Scope
	transitionType TransitionType
	group          TransitionGroup // concurrency group for Tracker admission
	displayName    string          // for progress and blocked-by messages

	// Transition metadata: written to the transition object at creation,
	// not used by the engine.
	replicaType     v1alpha1.ReplicaType
	fromReplicaType v1alpha1.ReplicaType
	toReplicaType   v1alpha1.ReplicaType

	// Slot routing and pre-creation behavior (ReplicaScope only).
	slot                 ReplicaSlotID // routes status output and conflict checks
	cancelActiveOnCreate bool          // cancel active transition in the slot before creating

	// Dispatch guards: evaluated before creating the transition.
	// Exactly one slice is populated, matching scope.
	replicaGuards []ReplicaGuardFunc[G, R]
	globalGuards  []GlobalGuardFunc[G]

	// Execution: steps (at least one), then optional completion callback.
	// For onComplete, at most one is set, matching scope.
	steps             []step[G, R]
	replicaOnComplete ReplicaOnCompleteFunc[G, R]
	globalOnComplete  GlobalOnCompleteFunc[G]
}

// callOnComplete dispatches the onComplete callback based on scope.
func (p *plan[G, R]) callOnComplete(gctx G, rctx R) {
	switch p.scope {
	case GlobalScope:
		if p.globalOnComplete != nil {
			p.globalOnComplete(gctx)
		}
	case ReplicaScope:
		if p.replicaOnComplete != nil {
			p.replicaOnComplete(gctx, rctx)
		}
	default:
		panic("dmte: plan.callOnComplete: invalid scope")
	}
}

// evaluateGuards evaluates guards in order. Returns the first blocking result.
// Returns a non-blocked result if all guards pass.
func (p *plan[G, R]) evaluateGuards(gctx G, rctx R) GuardResult {
	switch p.scope {
	case GlobalScope:
		for _, g := range p.globalGuards {
			if result := g(gctx); result.Blocked {
				return result
			}
		}
	case ReplicaScope:
		for _, g := range p.replicaGuards {
			if result := g(gctx, rctx); result.Blocked {
				return result
			}
		}
	default:
		panic("dmte: plan.evaluateGuards: invalid scope")
	}
	return GuardResult{}
}

// ──────────────────────────────────────────────────────────────────────────────
// PlanBuilder
//

// PlanBuilder constructs a plan via a fluent DSL.
// Created by RegisteredTransition.Plan().
type PlanBuilder[G any, R ReplicaCtx] struct {
	registry      *Registry[G, R]
	key           registryKey
	p             plan[G, R]
	rawSteps      []any // *ReplicaStepBuilder[G, R] or *GlobalStepBuilder[G]
	rawGuards     []any // ReplicaGuardFunc[G, R] or GlobalGuardFunc[G]
	rawOnComplete any   // func(G, R) or func(G)
}

// Group sets the concurrency group for the plan.
// Written to transition.Group when the transition is created.
// Required — panics at Build() if not set.
func (b *PlanBuilder[G, R]) Group(g TransitionGroup) *PlanBuilder[G, R] {
	b.p.group = g
	return b
}

// ReplicaType sets the replica type written to transition.ReplicaType at creation.
func (b *PlanBuilder[G, R]) ReplicaType(t v1alpha1.ReplicaType) *PlanBuilder[G, R] {
	b.p.replicaType = t
	return b
}

// FromReplicaType sets the source replica type written to transition.FromReplicaType at creation.
func (b *PlanBuilder[G, R]) FromReplicaType(t v1alpha1.ReplicaType) *PlanBuilder[G, R] {
	b.p.fromReplicaType = t
	return b
}

// ToReplicaType sets the target replica type written to transition.ToReplicaType at creation.
func (b *PlanBuilder[G, R]) ToReplicaType(t v1alpha1.ReplicaType) *PlanBuilder[G, R] {
	b.p.toReplicaType = t
	return b
}

// DisplayName sets a human-readable name used by the engine to auto-compose
// progress messages and blocked-by-active messages.
func (b *PlanBuilder[G, R]) DisplayName(name string) *PlanBuilder[G, R] {
	b.p.displayName = name
	return b
}

// Guards adds guards to the plan. The variadic accepts ReplicaGuardFunc[G, R]
// for replica plans or GlobalGuardFunc[G] for global plans.
// Validated at Build() time.
func (b *PlanBuilder[G, R]) Guards(guards ...any) *PlanBuilder[G, R] {
	b.rawGuards = append(b.rawGuards, guards...)
	return b
}

// Steps adds steps to the plan. The variadic accepts *ReplicaStepBuilder[G, R]
// and *GlobalStepBuilder[G]. Converted to internal step[G, R] at Build() time.
func (b *PlanBuilder[G, R]) Steps(steps ...any) *PlanBuilder[G, R] {
	b.rawSteps = append(b.rawSteps, steps...)
	return b
}

// OnComplete sets an optional callback invoked when all steps are confirmed.
// Accepts func(G) for global plans or func(G, R) for replica plans.
// Validated at Build() time.
func (b *PlanBuilder[G, R]) OnComplete(fn any) *PlanBuilder[G, R] {
	b.rawOnComplete = fn
	return b
}

// CancelActiveOnCreate sets whether to cancel the active transition in the
// same slot before creating a new one. Only valid for replica plans.
// Validated at Build() time.
func (b *PlanBuilder[G, R]) CancelActiveOnCreate(v bool) *PlanBuilder[G, R] {
	b.p.cancelActiveOnCreate = v
	return b
}

// Build validates the plan, converts steps/guards, and registers it in the registry.
// Panics on any validation error (startup-time safety).
func (b *PlanBuilder[G, R]) Build() {
	id := b.key.planID

	// PlanID format: {name}/v{N}
	if !planIDRegexp.MatchString(string(id)) {
		panic("dmte.PlanBuilder.Build: invalid PlanID format: " + string(id) + " (must be {name}/v{N}, e.g. access/v1)")
	}

	// Group is required.
	if b.p.group == "" {
		panic("dmte.PlanBuilder.Build: Group is required for plan " + string(id))
	}

	// Steps: must have at least one.
	if len(b.rawSteps) == 0 {
		panic("dmte.PlanBuilder.Build: no steps added for plan " + string(id))
	}

	// Convert and validate steps.
	b.p.steps = make([]step[G, R], 0, len(b.rawSteps))
	for i, raw := range b.rawSteps {
		switch s := raw.(type) {
		case *ReplicaStepBuilder[G, R]:
			if b.p.scope == GlobalScope {
				panic("dmte.PlanBuilder.Build: cannot add ReplicaStep to a global plan " + string(id))
			}
			b.p.steps = append(b.p.steps, s.build())
		case *GlobalStepBuilder[G]:
			b.p.steps = append(b.p.steps, buildGlobalStep[G, R](s))
		default:
			panic("dmte.PlanBuilder.Build: unsupported step type at index " + strconv.Itoa(i) + " for plan " + string(id))
		}
	}

	// Validate and store guards.
	for i, raw := range b.rawGuards {
		switch b.p.scope {
		case ReplicaScope:
			g, ok := raw.(ReplicaGuardFunc[G, R])
			if !ok {
				panic("dmte.PlanBuilder.Build: guard at index " + strconv.Itoa(i) + " is not ReplicaGuardFunc for replica plan " + string(id))
			}
			b.p.replicaGuards = append(b.p.replicaGuards, g)
		case GlobalScope:
			g, ok := raw.(GlobalGuardFunc[G])
			if !ok {
				panic("dmte.PlanBuilder.Build: guard at index " + strconv.Itoa(i) + " is not GlobalGuardFunc for global plan " + string(id))
			}
			b.p.globalGuards = append(b.p.globalGuards, g)
		}
	}

	// Validate and store OnComplete.
	if b.rawOnComplete != nil {
		switch b.p.scope {
		case ReplicaScope:
			fn, ok := b.rawOnComplete.(func(G, R))
			if !ok {
				panic("dmte.PlanBuilder.Build: OnComplete must be func(G, R) for replica plan " + string(id))
			}
			b.p.replicaOnComplete = fn
		case GlobalScope:
			fn, ok := b.rawOnComplete.(func(G))
			if !ok {
				panic("dmte.PlanBuilder.Build: OnComplete must be func(G) for global plan " + string(id))
			}
			b.p.globalOnComplete = fn
		}
	}

	// CancelActiveOnCreate only valid for replica plans.
	if b.p.cancelActiveOnCreate && b.p.scope == GlobalScope {
		panic("dmte.PlanBuilder.Build: CancelActiveOnCreate is only valid for replica plans, not global plan " + string(id))
	}

	// ReplicaType / FromReplicaType / ToReplicaType only valid for replica plans.
	if b.p.scope == GlobalScope && (b.p.replicaType != "" || b.p.fromReplicaType != "" || b.p.toReplicaType != "") {
		panic("dmte.PlanBuilder.Build: ReplicaType/FromReplicaType/ToReplicaType are only valid for replica plans, not global plan " + string(id))
	}

	// Duplicate check.
	if _, exists := b.registry.plans[b.key]; exists {
		panic("dmte.PlanBuilder.Build: duplicate plan: (" + string(b.key.transitionType) + ", " + string(id) + ")")
	}

	// Register.
	p := b.p
	b.registry.plans[b.key] = &p
}
