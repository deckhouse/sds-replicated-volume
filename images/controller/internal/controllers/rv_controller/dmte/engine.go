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
	"context"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// Engine is the main entry point for the datamesh transition engine SDK.
// Created per reconciliation with the current state.
//
// Generic on [G, R, C]:
//   - G is the global context type.
//   - R is the replica context type (satisfies ReplicaCtx).
//   - C is the context provider (satisfies ContextProvider[G, R]).
//
// The engine is pure non-I/O: it mutates transition data and replica context
// status (via slot accessors) in place. The caller owns DeepCopy for patch
// base and persisting the results.
//
// Lifecycle: NewEngine → Process() / CreateTransition → Finalize().
//
// Between NewEngine and Finalize, the backing array of the transitions slice
// passed to NewEngine has indeterminate state: some elements may be modified
// in-place, newly created transitions are heap-allocated, and deletions are
// deferred. Call Finalize() to obtain the authoritative result.
type Engine[G any, R ReplicaCtx, C ContextProvider[G, R]] struct {
	registry    *Registry[G, R]
	tracker     Tracker
	dispatchers []DispatchFunc[C]

	// Mutable revision provided by the caller.
	revision *int64 // &rv.Status.DatameshRevision

	// Original transitions slice (shares backing array with caller).
	// Not modified structurally during Process — only individual elements
	// may be mutated in-place through shadow index pointers.
	originalTransitions []Transition

	// Shadow index: pointers into originalTransitions (for existing)
	// or heap-allocated (for newly created). All operations during
	// Process/CreateTransition use this. Finalize reconciles back.
	transitions []*Transition

	// Context provider (by value for stenciling).
	cp C

	// Finalized flag: set by Finalize(), prevents further mutations.
	finalized bool
}

// NewEngine creates an Engine for one reconciliation cycle.
//
// The transitions slice is captured by value (shared backing array).
// Between NewEngine and Finalize(), the backing array may be partially
// modified — do not read the original slice directly. Call Finalize()
// to obtain the reconciled result.
//
// After construction:
//   - Tracker is populated with all existing transitions.
//   - Slot pointers are set for replica-scoped transitions with known plans.
//   - The engine is ready for Process() and CreateTransition calls.
//
// Panics if registry, tracker, or revision is nil.
func NewEngine[G any, R ReplicaCtx, C ContextProvider[G, R]](
	ctx context.Context,
	registry *Registry[G, R],
	tracker Tracker,
	dispatchers []DispatchFunc[C],
	revision *int64,
	transitions []Transition,
	cp C,
) *Engine[G, R, C] {
	if registry == nil {
		panic("dmte.NewEngine: registry must be non-nil")
	}
	if tracker == nil {
		panic("dmte.NewEngine: tracker must be non-nil")
	}
	if revision == nil {
		panic("dmte.NewEngine: revision must be non-nil")
	}

	// Note: transitions may be nil (empty slice) — that's valid.

	e := &Engine[G, R, C]{
		registry:            registry,
		tracker:             tracker,
		dispatchers:         dispatchers,
		revision:            revision,
		originalTransitions: transitions,
		cp:                  cp,
	}

	// Single pass: build shadow index + init tracker + init slot pointers.
	var zeroR R
	e.transitions = make([]*Transition, 0, len(transitions))
	for i := range transitions {
		t := &transitions[i]

		e.transitions = append(e.transitions, t)
		tracker.Add(t)

		// Slot pointer init: replica-scoped transitions with known plans.
		if t.ReplicaName == "" {
			continue // global transition — no slot
		}

		p := registry.get(t.Type, PlanID(t.PlanID))
		if p == nil {
			log.FromContext(ctx).Error(nil, "unknown transition plan, skipping slot init",
				"transitionType", t.Type, "planID", t.PlanID)
			continue
		}
		if p.scope != ReplicaScope {
			continue
		}

		slot := registry.replicaSlotAccessor(p.slot)
		if slot == nil {
			continue
		}

		rctx := cp.Replica(t.ReplicaID())
		if rctx == zeroR {
			continue // replica context not found — defensive
		}

		slot.SetActiveTransition(rctx, t)
	}

	return e
}

// ──────────────────────────────────────────────────────────────────────────────
// Public transition creation
//

// CreateReplicaTransition creates a replica-scoped transition.
//
// Returns (transition, "") on success, (nil, "") if the same transition
// type is already active on the slot (silent ignore), or (nil, reason) if
// blocked by a different-type slot conflict, guard, or tracker.
// Panics if the engine is already finalized, the plan is unknown, or
// the replica ID is not in the context provider.
func (e *Engine[G, R, C]) CreateReplicaTransition(
	tt TransitionType,
	planID PlanID,
	replicaID uint8,
) (*Transition, string) {
	if e.finalized {
		panic("dmte.Engine.CreateReplicaTransition: engine already finalized")
	}

	p := e.registry.get(tt, planID)
	if p == nil {
		panic("dmte.Engine.CreateReplicaTransition: unknown plan (" + string(tt) + ", " + string(planID) + ")")
	}

	var zeroR R
	rctx := e.cp.Replica(replicaID)
	if rctx == zeroR {
		panic("dmte.Engine.CreateReplicaTransition: unknown replica ID")
	}

	slot := e.registry.replicaSlotAccessor(p.slot)
	return e.dispatchOne(p, planID, rctx, slot)
}

// CreateGlobalTransition creates a global-scoped transition.
// Returns (transition, "") on success, or (nil, reason) if blocked
// by a guard or tracker.
// Panics if the engine is already finalized or the plan is unknown.
func (e *Engine[G, R, C]) CreateGlobalTransition(
	tt TransitionType,
	planID PlanID,
) (*Transition, string) {
	if e.finalized {
		panic("dmte.Engine.CreateGlobalTransition: engine already finalized")
	}

	p := e.registry.get(tt, planID)
	if p == nil {
		panic("dmte.Engine.CreateGlobalTransition: unknown plan (" + string(tt) + ", " + string(planID) + ")")
	}

	var zeroR R
	return e.dispatchOne(p, planID, zeroR, nil)
}

// ──────────────────────────────────────────────────────────────────────────────
// Process
//

// Process runs the datamesh engine loop.
//
//  1. Settle existing transitions until stable (confirm/advance/complete).
//  2. Run each dispatcher to create new transitions.
//
// Returns true if any mutation was made to transition storage.
// Slot status writes (via SetStatus) do NOT affect the return value — the
// caller propagates those to Kubernetes objects with its own diff-checking.
//
// After Process, call Finalize() to obtain the reconciled transitions.
// Panics if the engine is already finalized.
func (e *Engine[G, R, C]) Process(ctx context.Context) bool {
	if e.finalized {
		panic("dmte.Engine.Process: engine already finalized")
	}

	changed := false

	// Outer convergence loop: settle → dispatch → repeat.
	//
	// When an apply callback returns changed=false (no-op), the engine does
	// not bump revision. The settle pass that follows will immediately confirm
	// the no-op step (agents already confirmed the current revision) and
	// advance to the next step — all within a single Process() call.
	//
	// The loop breaks when dispatch creates no new transitions (steady state).
	// Safety bound prevents infinite loops from bugs.
	const maxOuter = 10
	for range maxOuter {
		// Settle existing transitions until no more progress can be made.
		for {
			settleChanged, progressed := e.settleTransitions(ctx)
			changed = changed || settleChanged
			if !progressed {
				break
			}
		}

		// Run each dispatcher.
		dispatched := false
		for _, dispatch := range e.dispatchers {
			if e.runDispatch(dispatch) {
				dispatched = true
				changed = true
			}
		}

		if !dispatched {
			break
		}
	}

	return changed
}

// ──────────────────────────────────────────────────────────────────────────────
// Finalize
//

// Finalize returns the up-to-date transitions slice. After Finalize, the
// engine is sealed — further Process or CreateTransition calls will panic.
//
// If no mutations occurred, returns the original slice as-is. If mutations
// occurred, returns an updated slice with all changes (modifications,
// deletions, new transitions) applied. Slot pointers are updated to point
// into the returned slice.
//
// The caller should assign the result back:
//
//	rv.Status.DatameshTransitions = engine.Finalize()
func (e *Engine[G, R, C]) Finalize() []Transition {
	if e.finalized {
		panic("dmte.Engine.Finalize: engine already finalized")
	}
	e.finalized = true

	orig := e.originalTransitions

	// Pre-grow before the copy loop so that all &orig[i] references below
	// point into the final backing array. Without this, a later grow could
	// reallocate, leaving slot pointers set by replaceSlotPointer dangling
	// on the old backing array.
	if len(e.transitions) > len(orig) {
		orig = slices.Grow(orig, len(e.transitions)-len(orig))
	}

	// Walk in parallel: copy only where addresses differ.
	minLen := min(len(orig), len(e.transitions))
	for i := 0; i < minLen; i++ {
		if e.transitions[i] != &orig[i] {
			e.replaceSlotPointer(e.transitions[i], &orig[i])
			orig[i] = *e.transitions[i]
		}
	}

	if len(e.transitions) <= len(orig) {
		// Deletions (or no change in count): truncate.
		return orig[:len(e.transitions)]
	}

	// Append new transitions (backing array already has capacity from pre-grow).
	for i := minLen; i < len(e.transitions); i++ {
		orig = append(orig, *e.transitions[i])
		e.replaceSlotPointer(e.transitions[i], &orig[len(orig)-1])
	}
	return orig
}

// replaceSlotPointer scans all slots for the given replica and replaces
// oldPtr with newPtr. No plan lookup needed — iterates the fixed-size
// slot array (MaxReplicaSlots).
func (e *Engine[G, R, C]) replaceSlotPointer(oldPtr, newPtr *Transition) {
	if oldPtr.ReplicaName == "" {
		return // global transition — no slot
	}

	var zeroR R
	rctx := e.cp.Replica(oldPtr.ReplicaID())
	if rctx == zeroR {
		return
	}

	for slotID := range MaxReplicaSlots {
		slot := e.registry.replicaSlotAccessor(slotID)
		if slot != nil && slot.GetActiveTransition(rctx) == oldPtr {
			slot.SetActiveTransition(rctx, newPtr)
			return // one slot per transition
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Settle
//

// settleTransitions performs a single pass over active transitions: confirms
// steps, advances to next steps, and completes finished transitions.
//
// Returns (changed, progressed): changed is true if transition storage was
// mutated, progressed is true if any transition was advanced or completed
// (caller should re-run).
func (e *Engine[G, R, C]) settleTransitions(ctx context.Context) (changed, progressed bool) {
	var zeroR R

	for i := 0; i < len(e.transitions); {
		t := e.transitions[i]

		p := e.registry.get(t.Type, PlanID(t.PlanID))
		if p == nil {
			log.FromContext(ctx).Error(nil, "unknown transition plan, skipping settle",
				"transitionType", t.Type, "planID", t.PlanID)
			i++
			continue
		}

		// Resolve replica context and slot.
		var rctx R
		var slot ReplicaSlotAccessor[R]
		if p.scope == ReplicaScope && t.ReplicaName != "" {
			rctx = e.cp.Replica(t.ReplicaID())
			slot = e.registry.replicaSlotAccessor(p.slot)
		}

		stepIdx := findCurrentStepIdx(t)
		if stepIdx < 0 {
			// All steps completed — complete transition.
			e.completeTransition(p, i, rctx, slot)
			changed = true
			progressed = true
			// Don't increment i — next element shifted into this position.
			continue
		}

		// Confirm current step and generate progress message.
		cr, composed := e.confirmStep(p, t, rctx, stepIdx, &changed)

		// Write composed message to slot.
		if slot != nil && rctx != zeroR {
			slot.SetStatus(rctx, composed, p.steps[stepIdx].details)
		}

		// Normalize: Confirmed must not contain IDs outside MustConfirm.
		// This ensures equality check works correctly and diagnostic messages
		// show accurate counts (e.g., "1/2" not "2/1").
		cr.Confirmed = cr.Confirmed.Intersect(cr.MustConfirm)

		// Check completion.
		confirmed := cr.Confirmed == cr.MustConfirm
		if !confirmed {
			i++
			continue
		}

		// Step confirmed — call step-level onComplete (if set).
		p.steps[stepIdx].callOnComplete(e.cp.Global(), rctx)

		if stepIdx+1 < len(t.Steps) {
			// Advance to next step.
			advanceStep(t, stepIdx)
			e.applyStep(p, t, rctx, stepIdx+1)
			_, advComposed := e.confirmStep(p, t, rctx, stepIdx+1, nil)
			if slot != nil && rctx != zeroR {
				slot.SetStatus(rctx, advComposed, p.steps[stepIdx+1].details)
			}
			changed = true
			progressed = true
		} else {
			// Last step confirmed — complete transition.
			e.completeTransition(p, i, rctx, slot)
			changed = true
			progressed = true
			// Don't increment i — next element shifted into this position.
			continue
		}

		i++
	}

	return changed, progressed
}

// completeTransition calls onComplete, clears the slot pointer, and deletes
// the transition at the given index from the shadow index.
//
// rctx and slot are passed by the caller (settleTransitions) which already
// resolved them. For global transitions both are zero/nil.
// rctx may also be zero for replica-scoped transitions when the replica
// was removed from the context index (e.g., ForceRemove + outer loop
// completes the transition in the same Process() call).
func (e *Engine[G, R, C]) completeTransition(p *plan[G, R], idx int, rctx R, slot ReplicaSlotAccessor[R]) {
	var zeroR R

	p.callOnComplete(e.cp.Global(), rctx)

	if slot != nil && rctx != zeroR {
		slot.SetActiveTransition(rctx, nil)
	}

	e.deleteTransition(idx)
}

// ──────────────────────────────────────────────────────────────────────────────
// Dispatch
//

// runDispatch executes a single dispatcher and processes its decisions.
// Returns true if any transition storage mutation occurred.
func (e *Engine[G, R, C]) runDispatch(dispatch DispatchFunc[C]) bool {
	changed := false

	for d := range dispatch(e.cp) {
		// No-dispatch: just set slot status, no transition to create.
		if d.planID == "" {
			if d.replicaCtx != nil && d.message != "" {
				rctx := d.replicaCtx.(R)
				slot := e.registry.replicaSlotAccessor(d.replicaSlot)
				if slot != nil {
					slot.SetStatus(rctx, d.message, d.details)
				}
			}
			continue
		}

		p := e.registry.get(d.transitionType, d.planID)
		if p == nil {
			if d.replicaCtx != nil {
				rctx := d.replicaCtx.(R)
				panic("dmte.Engine.runDispatch: unknown plan (" + string(d.transitionType) + ", " + string(d.planID) + ") for replica " + rctx.Name())
			}
			panic("dmte.Engine.runDispatch: unknown plan (" + string(d.transitionType) + ", " + string(d.planID) + ")")
		}

		var rctx R
		var slot ReplicaSlotAccessor[R]
		if p.scope == ReplicaScope {
			rctx = d.replicaCtx.(R)
			slot = e.registry.replicaSlotAccessor(p.slot)
		}

		if t, _ := e.dispatchOne(p, d.planID, rctx, slot); t != nil {
			changed = true
		}
	}

	return changed
}

// dispatchOne runs the full create pipeline for a single transition decision:
// cancelActive → slot conflict → guards → tracker admission → create →
// apply step[0] → confirm → slot.SetStatus.
//
// slot may be nil for global plans. rctx may be zero for global plans.
//
// Returns:
//   - (transition, "") on success,
//   - (nil, "") if the same transition type is already active on the slot
//     (silent ignore — preserves the settle progress message),
//   - (nil, reason) if blocked by a different-type slot conflict, guard,
//     or tracker.
func (e *Engine[G, R, C]) dispatchOne(
	p *plan[G, R],
	planID PlanID,
	rctx R,
	slot ReplicaSlotAccessor[R],
) (*Transition, string) {
	var zeroR R
	hasSlot := slot != nil && rctx != zeroR

	// Cancel active if plan requires it (e.g., Emergency).
	if p.cancelActiveOnCreate && hasSlot {
		if active := slot.GetActiveTransition(rctx); active != nil {
			for j, tt := range e.transitions {
				if tt == active {
					e.deleteTransition(j)
					break
				}
			}
			slot.SetActiveTransition(rctx, nil)
		}
	}

	// Slot conflict check.
	if hasSlot {
		if active := slot.GetActiveTransition(rctx); active != nil {
			// Same transition type already active on this slot — silently
			// ignore to preserve the settle progress message.
			if active.Type == p.transitionType {
				return nil, ""
			}

			// Different type — report blocked by active transition.
			activePlan := e.registry.get(active.Type, PlanID(active.PlanID))
			activePlanDisplayName := ""
			if activePlan != nil {
				activePlanDisplayName = activePlan.displayName
			}
			msg := composeBlockedByActive(p.displayName, activePlanDisplayName, active.CurrentStep())
			activeStep := active.CurrentStep()
			var details any
			if activeStep != nil && activePlan != nil {
				stepIdx := findCurrentStepIdx(active)
				if stepIdx >= 0 && stepIdx < len(activePlan.steps) {
					details = activePlan.steps[stepIdx].details
				}
			}
			slot.SetStatus(rctx, msg, details)
			return nil, msg
		}
	}

	// Guards.
	guardResult := p.evaluateGuards(e.cp.Global(), rctx)
	if guardResult.Blocked {
		msg := composeBlocked(p.displayName, guardResult.Message)
		if hasSlot {
			slot.SetStatus(rctx, msg, guardResult.Details)
		}
		return nil, msg
	}

	// Tracker admission. tempT is a stack-allocated stub with only the fields
	// the Tracker needs (Type, Group, ReplicaName) to avoid a heap allocation.
	tempT := Transition{Type: p.transitionType, Group: p.group}
	if p.scope == ReplicaScope && rctx != zeroR {
		tempT.ReplicaName = rctx.Name()
	}
	allowed, reason, det := e.tracker.CanAdmit(&tempT)
	if !allowed {
		msg := composeBlocked(p.displayName, reason)
		if hasSlot {
			slot.SetStatus(rctx, msg, det)
		}
		return nil, msg
	}

	// Create transition (heap-allocated, added to shadow index).
	t := e.createTransition(p, planID, rctx)
	if hasSlot {
		slot.SetActiveTransition(rctx, t)
	}
	e.tracker.Add(t)

	// Apply step 0 and generate initial progress.
	e.applyStep(p, t, rctx, 0)
	_, composed := e.confirmStep(p, t, rctx, 0, nil)
	if hasSlot {
		slot.SetStatus(rctx, composed, p.steps[0].details)
	}

	return t, ""
}

// ──────────────────────────────────────────────────────────────────────────────
// Transition helpers
//

// createTransition builds a Transition from a plan and appends it to the
// shadow index. Returns a pointer to the heap-allocated transition.
func (e *Engine[G, R, C]) createTransition(
	p *plan[G, R],
	planID PlanID,
	rctx R,
) *Transition {
	now := metav1.Now()

	// Build steps from plan.
	steps := make([]v1alpha1.ReplicatedVolumeDatameshTransitionStep, len(p.steps))
	for i := range p.steps {
		steps[i].Name = p.steps[i].name
		steps[i].Status = v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending
	}

	// Activate step 0.
	steps[0].Status = v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive
	steps[0].StartedAt = &now

	t := Transition{
		Type:   p.transitionType,
		Group:  p.group,
		PlanID: string(planID),
		Steps:  steps,
	}

	// Replica-scoped: set ReplicaName.
	if p.scope == ReplicaScope {
		t.ReplicaName = rctx.Name()
		if t.ReplicaName == "" {
			panic("dmte.Engine.createTransition: rctx.Name() returned empty for replica-scoped plan " + string(planID))
		}
	}

	// Call Init callback to populate transition-specific metadata.
	p.callInit(e.cp.Global(), rctx, &t)

	e.transitions = append(e.transitions, &t)
	return &t
}

// applyStep applies a step callback, conditionally bumps the engine's revision
// (only if the callback changed state), and records the revision on the step.
//
// When the apply callback returns false (no-op), the revision is NOT bumped.
// Agents that already confirmed the current revision will immediately satisfy
// the confirm check, allowing the engine to advance through no-op steps
// within a single Process() call via the outer settle-dispatch loop.
func (e *Engine[G, R, C]) applyStep(
	p *plan[G, R],
	t *Transition,
	rctx R,
	stepIdx int,
) {
	changed := p.steps[stepIdx].apply(e.cp.Global(), rctx)
	if changed {
		*e.revision++
	}
	t.Steps[stepIdx].DatameshRevision = *e.revision
}

// confirmStep runs the confirm callback, generates the raw progress message,
// and returns the ConfirmResult and composed message for slot.SetStatus.
//
// If the raw progress message changed, writes it to t.Steps[].Message and
// sets *changed = true. If changed is nil, always writes the message.
func (e *Engine[G, R, C]) confirmStep(
	p *plan[G, R],
	t *Transition,
	rctx R,
	stepIdx int,
	changed *bool,
) (ConfirmResult, string) {
	s := &p.steps[stepIdx]

	cr := s.confirm(e.cp.Global(), rctx, t.Steps[stepIdx].DatameshRevision)

	rawProgress := generateProgressMessage(
		e.cp, t.Steps[stepIdx].DatameshRevision, cr,
		s.diagnosticConditionTypes, s.diagnosticSkipError,
	)

	if changed == nil || t.Steps[stepIdx].Message != rawProgress {
		t.Steps[stepIdx].Message = rawProgress
		if changed != nil {
			*changed = true
		}
	}

	composed := composeProgressMessage(p.displayName, s.name, stepIdx, len(p.steps), rawProgress)
	return cr, composed
}

// advanceStep completes the step at fromIdx and activates the next step.
// The completed step retains its last progress message for debug visibility.
func advanceStep(t *Transition, fromIdx int) {
	now := metav1.Now()
	t.Steps[fromIdx].Status = v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted
	t.Steps[fromIdx].CompletedAt = &now
	t.Steps[fromIdx+1].Status = v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive
	t.Steps[fromIdx+1].StartedAt = &now
}

// findCurrentStepIdx returns the index of the first non-Completed step,
// or -1 if all steps are completed.
func findCurrentStepIdx(t *Transition) int {
	for i := range t.Steps {
		if t.Steps[i].Status != v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted {
			return i
		}
	}
	return -1
}

// deleteTransition removes a transition at the given index from the shadow
// index and unregisters it from the tracker.
//
// The caller MUST clear the slot pointer (SetActiveTransition(rctx, nil))
// before calling this method.
func (e *Engine[G, R, C]) deleteTransition(idx int) {
	e.tracker.Remove(e.transitions[idx])
	e.transitions = slices.Delete(e.transitions, idx, idx+1)
}
