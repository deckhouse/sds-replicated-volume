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

// Package dmte provides the datamesh transition engine SDK — a generic
// mechanism for managing multi-step transitions with guards, admission
// tracking, and slot-based concurrency control and status routing.
//
// The engine is pure non-I/O: it mutates transition storage and replica
// context status in place. The caller owns DeepCopy for patch base and
// persisting the results after the engine runs.
package dmte

import (
	"iter"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
)

// ──────────────────────────────────────────────────────────────────────────────
// Core type aliases
//

// Transition is the API type managed by the engine.
type Transition = v1alpha1.ReplicatedVolumeDatameshTransition

// TransitionType identifies a kind of transition (e.g., AddReplica, Attach).
// The engine is opaque to concrete values — it stores and passes them through
// without interpretation. Concrete constants are defined by domain code.
type TransitionType = v1alpha1.ReplicatedVolumeDatameshTransitionType

// TransitionGroup categorizes transitions for concurrency rules.
// The engine is opaque to concrete values — it writes the group to new
// transitions and passes them to the Tracker, but never interprets them.
// Concrete constants and concurrency rules are defined by domain code.
type TransitionGroup = v1alpha1.ReplicatedVolumeDatameshTransitionGroup

// PlanID is a stable versioned identifier for a registered plan.
// Format: "{name}/v{N}" (e.g., "access/v1"). Validated at Build() time.
// Together with TransitionType, forms the composite registry key.
// Persisted on transitions in the API for deployment-safe plan evolution.
type PlanID string

// ──────────────────────────────────────────────────────────────────────────────
// Scope
//

// Scope distinguishes global and replica-level transitions/plans/steps.
type Scope uint8

const (
	// GlobalScope is for transitions/plans/steps that act on the datamesh as a whole.
	GlobalScope Scope = iota
	// ReplicaScope is for transitions/plans/steps that act on a specific replica/node.
	ReplicaScope
)

// ──────────────────────────────────────────────────────────────────────────────
// Replica slots
//

// ReplicaSlotID identifies a replica slot (uint8 index into a fixed-size array).
// Domain code defines slot constants (e.g., MembershipSlot, AttachmentSlot).
type ReplicaSlotID uint8

// MaxReplicaSlots is the maximum number of replica slots.
// Increase when a new slot is needed.
const MaxReplicaSlots ReplicaSlotID = 2

// ──────────────────────────────────────────────────────────────────────────────
// ReplicaCtx constraint
//

// ReplicaCtx is the constraint for the replica context type.
// Implemented by the caller's concrete replica context struct (pointer receiver).
//
// Includes comparable so the engine can detect unset entries.
// All pointer types satisfy comparable.
type ReplicaCtx interface {
	comparable
	// ID returns the replica ID (0–31). Used by the engine for internal indexing
	// and for looking up replica contexts by transition.ReplicaID().
	ID() uint8

	// Name returns the replica name (e.g., "rv-1-5"). Written to
	// transition.ReplicaName when creating replica-scoped transitions.
	// The implementation resolves the name from the best available source.
	// May return empty if no name is available.
	Name() string

	// Exists reports whether this replica has observable state for diagnostics.
	// When false, the engine reports "Replica not found"
	// in progress messages. Generation() and Conditions() are only called
	// when Exists() returns true.
	Exists() bool

	// Generation returns the replica's metadata generation.
	// Used by the engine to filter stale conditions in diagnostic messages.
	Generation() int64

	// Conditions returns the replica's status conditions.
	// Used by the engine for diagnostic error reporting in progress messages.
	Conditions() []metav1.Condition
}

// ──────────────────────────────────────────────────────────────────────────────
// ContextProvider
//

// ContextProvider provides access to global and per-replica contexts.
// Implemented by the caller's concrete struct. Value type with value
// receivers is recommended for performance.
type ContextProvider[G any, R ReplicaCtx] interface {
	// Global returns the global context.
	Global() G
	// Replica returns the replica context for the given ID (0–31).
	// Returns zero R if the replica context is not available.
	Replica(id uint8) R
}

// ──────────────────────────────────────────────────────────────────────────────
// ReplicaSlotAccessor
//

// ReplicaSlotAccessor binds a slot to concrete fields on the replica context.
// Each method receives the typed replica context R — the implementation
// knows the concrete type and accesses fields directly.
type ReplicaSlotAccessor[R ReplicaCtx] interface {
	// GetActiveTransition returns the active transition pointer for this slot
	// on the given replica context. Nil if no active transition.
	GetActiveTransition(rctx R) *Transition

	// SetActiveTransition stores the active transition pointer on the replica
	// context. Called by the engine when creating a transition, during startup
	// initialization from existing transitions, and on completion (nil to clear).
	SetActiveTransition(rctx R, t *Transition)

	// SetStatus sets the current slot status on the replica context.
	// Called by the engine during settle (progress) and dispatch (blocked/created).
	// This is the ONE path for all status output — guards, tracker,
	// slot conflicts, and progress all flow through here.
	// The implementation decides where/how to store message and details on R.
	// Output only — no return value, no diff-checking.
	SetStatus(rctx R, message string, details any)
}

// ──────────────────────────────────────────────────────────────────────────────
// Tracker
//

// Tracker tracks active transitions and decides whether new ones can be admitted.
// The engine initializes the tracker from existing transitions at construction
// (calls Add for each active transition).
type Tracker interface {
	// Add registers a newly created transition.
	Add(t *Transition)

	// Remove unregisters a completed/cancelled transition.
	// Must be called BEFORE the transition is removed from storage.
	Remove(t *Transition)

	// CanAdmit returns (true, "", nil) if the proposed transition is allowed,
	// (false, message, details) if blocked.
	// message and details are passed to slot.SetStatus if blocked.
	CanAdmit(t *Transition) (allowed bool, message string, details any)
}

// ──────────────────────────────────────────────────────────────────────────────
// Dispatch
//

// DispatchFunc is a function that examines the current state and yields
// dispatch decisions. Multiple dispatchers can be registered on the engine;
// they execute in registration order.
// C is the ContextProvider — dispatchers call cp.Global() for domain-level
// collections and cp.Replica(id) for per-replica lookups.
type DispatchFunc[C any] func(cp C) iter.Seq[DispatchDecision]

// DispatchDecision represents a dispatcher's decision for one replica or a
// global action. Created exclusively via constructor functions below.
type DispatchDecision struct {
	// replicaCtx is the replica context (as any); nil for global plans.
	// The engine asserts it back to R via d.replicaCtx.(R) — type safety
	// is ensured by the generic constructors (DispatchReplica, NoDispatch).
	replicaCtx any

	// transitionType + planID form the composite registry key for plan lookup.
	// Both set for dispatch decisions; both empty for no-dispatch decisions.
	transitionType TransitionType
	planID         PlanID

	// No-dispatch fields (when planID is empty — no transition to create,
	// just write message+details to the slot). Used when the dispatcher
	// determines no transition is needed but the slot should reflect the
	// current state (e.g., "Volume is attached", "Node is not eligible").
	// replicaSlot is required for no-dispatch decisions because the engine
	// doesn't know which slot to write to without a plan.
	replicaSlot ReplicaSlotID
	message     string
	details     any
}

// DispatchReplica creates a decision to start a replica-scoped transition.
// R is inferred from rctx — no explicit type params needed at call sites.
func DispatchReplica[R ReplicaCtx](rctx R, tt TransitionType, planID PlanID) DispatchDecision {
	return DispatchDecision{
		replicaCtx:     rctx,
		transitionType: tt,
		planID:         planID,
	}
}

// DispatchGlobal creates a decision to start a global-scoped transition.
// No type params needed (no replica context for global plans).
func DispatchGlobal(tt TransitionType, planID PlanID) DispatchDecision {
	return DispatchDecision{
		transitionType: tt,
		planID:         planID,
	}
}

// NoDispatch creates a no-dispatch decision: no transition to create, but
// write (message, details) to the given slot to reflect the current state.
// R is inferred from rctx. Used when the dispatcher determines no transition
// is needed (e.g., "Volume is attached").
func NoDispatch[R ReplicaCtx](rctx R, slot ReplicaSlotID, msg string, details any) DispatchDecision {
	return DispatchDecision{
		replicaCtx:  rctx,
		replicaSlot: slot,
		message:     msg,
		details:     details,
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Guard callbacks
//

// ReplicaGuardFunc checks a precondition for a replica-scoped transition.
// Returns GuardResult with Blocked=true and a message if the guard blocks.
type ReplicaGuardFunc[G any, R ReplicaCtx] = func(gctx G, rctx R) GuardResult

// GlobalGuardFunc checks a precondition for a global-scoped transition.
// Returns GuardResult with Blocked=true and a message if the guard blocks.
type GlobalGuardFunc[G any] = func(gctx G) GuardResult

// GuardResult is the result of a guard check.
type GuardResult struct {
	// Blocked is true if the guard blocks the transition.
	Blocked bool
	// Message is a human-readable message explaining the block.
	Message string
	// Details is opaque supplementary data passed to slot.SetStatus.
	Details any
}

// ──────────────────────────────────────────────────────────────────────────────
// Plan callbacks
//

// ReplicaOnCompleteFunc is called when all steps of a replica-scoped plan are
// confirmed (before the transition is removed). Optional.
type ReplicaOnCompleteFunc[G any, R ReplicaCtx] = func(gctx G, rctx R)

// GlobalOnCompleteFunc is called when all steps of a global-scoped plan are
// confirmed (before the transition is removed). Optional.
type GlobalOnCompleteFunc[G any] = func(gctx G)

// ──────────────────────────────────────────────────────────────────────────────
// Step callbacks
//

// ReplicaApplyFunc is called when a replica-scoped step is activated.
// It mutates state through gctx (e.g., add/remove member, set Attached flag).
// DatameshRevision bump, step.DatameshRevision, and message generation are
// handled by the engine.
type ReplicaApplyFunc[G any, R ReplicaCtx] = func(gctx G, rctx R)

// GlobalApplyFunc is called when a global-scoped step is activated.
// It mutates state through gctx (e.g., change qmr, update EffectiveLayout).
type GlobalApplyFunc[G any] = func(gctx G)

// ReplicaConfirmFunc computes the confirmation sets for a replica-scoped step.
// stepRevision is the DatameshRevision assigned to this step by the engine.
// The engine determines completion (Confirmed == MustConfirm) and generates messages.
type ReplicaConfirmFunc[G any, R ReplicaCtx] = func(gctx G, rctx R, stepRevision int64) ConfirmResult

// GlobalConfirmFunc computes the confirmation sets for a global-scoped step.
// stepRevision is the DatameshRevision assigned to this step by the engine.
type GlobalConfirmFunc[G any] = func(gctx G, stepRevision int64) ConfirmResult

// ConfirmResult holds the confirmation sets computed by a confirm callback.
// The engine normalizes Confirmed to Confirmed ∩ MustConfirm, then checks
// completion (Confirmed == MustConfirm) and auto-generates progress messages.
type ConfirmResult struct {
	// MustConfirm is the set of replica IDs that must confirm this step's revision.
	MustConfirm idset.IDSet
	// Confirmed is the set of replica IDs that have already confirmed.
	Confirmed idset.IDSet
}

// SkipErrorFunc allows ignoring specific error conditions during diagnostic
// message generation. Returns true if the error condition should be skipped
// (not reported as an error in progress messages).
// Only used on ReplicaStepBuilder (global steps have no per-replica skip logic).
type SkipErrorFunc[R ReplicaCtx] = func(rctx R, id uint8, cond *metav1.Condition) bool
