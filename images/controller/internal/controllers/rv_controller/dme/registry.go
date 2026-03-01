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

package dme

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
)

// ──────────────────────────────────────────────────────────────────────────────
// PlanID and Registry
//

// PlanID is a stable string identifier for a registered plan.
// Together with the transition type, forms a composite key in the registry.
// Persisted on transitions in the API for deployment-safe plan evolution.
type PlanID string

// registryKey is the composite key for the registry: (transition type, plan ID).
type registryKey struct {
	transitionType v1alpha1.ReplicatedVolumeDatameshTransitionType
	planID         PlanID
}

// Registry is a factory and lookup store for transitions and their plans.
// Transitions are registered at controller startup via GlobalTransition/ReplicaTransition.
// Plans are then registered under transitions via RegisteredTransition.Plan() + Build().
type Registry struct {
	transitionTypes map[v1alpha1.ReplicatedVolumeDatameshTransitionType]struct{}
	plans           map[registryKey]*plan
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		transitionTypes: make(map[v1alpha1.ReplicatedVolumeDatameshTransitionType]struct{}),
		plans:           make(map[registryKey]*plan),
	}
}

// get returns the plan for the given composite key, or nil if not found.
func (r *Registry) get(transitionType v1alpha1.ReplicatedVolumeDatameshTransitionType, id PlanID) *plan {
	return r.plans[registryKey{transitionType: transitionType, planID: id}]
}

// hasTransitionType returns true if the given transition type is registered.
// O(1) lookup.
func (r *Registry) hasTransitionType(transitionType v1alpha1.ReplicatedVolumeDatameshTransitionType) bool {
	_, ok := r.transitionTypes[transitionType]
	return ok
}

// ──────────────────────────────────────────────────────────────────────────────
// RegisteredTransition
//

// RegisteredTransition is a handle to a transition type registered in the Registry.
// Used to register plans under this transition type via Plan() + Build().
type RegisteredTransition struct {
	registry       *Registry
	transitionType v1alpha1.ReplicatedVolumeDatameshTransitionType
	scope          Scope
}

// GlobalTransition registers a datamesh-wide transition type and returns a handle
// for registering plans under it. Plans under a global transition may only contain GlobalStep.
func (r *Registry) GlobalTransition(transitionType v1alpha1.ReplicatedVolumeDatameshTransitionType) *RegisteredTransition {
	r.transitionTypes[transitionType] = struct{}{}
	return &RegisteredTransition{registry: r, transitionType: transitionType, scope: GlobalScope}
}

// ReplicaTransition registers a replica-level transition type and returns a handle
// for registering plans under it. Plans under a replica transition may contain
// ReplicaStep and GlobalStep (mixed).
func (r *Registry) ReplicaTransition(transitionType v1alpha1.ReplicatedVolumeDatameshTransitionType) *RegisteredTransition {
	r.transitionTypes[transitionType] = struct{}{}
	return &RegisteredTransition{registry: r, transitionType: transitionType, scope: ReplicaScope}
}

// Plan creates a PlanBuilder for a new plan under this transition type.
// Panics if id is empty. The plan is registered in the registry when Build() is called.
func (t *RegisteredTransition) Plan(id PlanID, group v1alpha1.ReplicatedVolumeDatameshTransitionGroup) *PlanBuilder {
	if id == "" {
		panic("dme.RegisteredTransition.Plan: PlanID must not be empty")
	}
	return &PlanBuilder{
		registry: t.registry,
		key:      registryKey{transitionType: t.transitionType, planID: id},
		p:        plan{scope: t.scope, group: group},
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Planner
//

// MembershipPlanner decides which transition plan to use for a given membership request.
type MembershipPlanner interface {
	// Classify maps a request operation to a transition type.
	// Pure mapping, no runtime state. Used by the engine to determine the transition type
	// before calling Plan, and to check if a request still matches an active transition.
	Classify(operation v1alpha1.DatameshMembershipRequestOperation) v1alpha1.ReplicatedVolumeDatameshTransitionType

	// Plan selects a plan for the given transition type and replica context.
	//
	// Returns (PlanID, blockedMessage):
	//   - Non-empty PlanID + empty string: plan selected, engine looks it up from the registry.
	//   - Empty PlanID + non-empty string: blocked, engine writes message to request.Message.
	Plan(
		gctx *GlobalContext,
		rctx *ReplicaContext,
		transitionType v1alpha1.ReplicatedVolumeDatameshTransitionType,
	) (PlanID, string)
}

// ──────────────────────────────────────────────────────────────────────────────
// AttachmentPlanner
//

// AttachmentPlanner decides attachment transitions (Attach/Detach/Multiattach).
// Created per-reconciliation. May be stateful (e.g., track slot counts internally).
type AttachmentPlanner interface {
	// PlanAttachments decides attachment operations for all relevant replicas.
	// rctxs includes all ReplicaContexts where len(RVAs) > 0 or Member is attached.
	// The planner owns internal ordering (FIFO for attaches) and slot allocation.
	// Returns a parallel slice of decisions (one per rctx).
	PlanAttachments(gctx *GlobalContext, rctxs []*ReplicaContext) []AttachmentDecision

	// PlanMultiattach decides if a multiattach toggle transition is needed.
	// Returns nil if no toggle is needed.
	PlanMultiattach(gctx *GlobalContext) *MultiattachDecision
}

// AttachmentDecision is the planner's decision for a single replica.
type AttachmentDecision struct {
	// TransitionType: Attach, Detach, or empty (no transition needed / already settled).
	TransitionType v1alpha1.ReplicatedVolumeDatameshTransitionType
	// PlanID: the plan to use. Empty if blocked or no transition needed.
	PlanID PlanID
	// BlockedMessage: human-readable message when blocked (PlanID is empty but action is needed).
	BlockedMessage string
	// BlockedReason: machine-readable reason for attachment condition when blocked.
	BlockedReason string
}

// MultiattachDecision is the planner's decision for a multiattach toggle.
type MultiattachDecision struct {
	// TransitionType: EnableMultiattach or DisableMultiattach.
	TransitionType v1alpha1.ReplicatedVolumeDatameshTransitionType
	// PlanID: the plan to use.
	PlanID PlanID
}

// ──────────────────────────────────────────────────────────────────────────────
// Scope
//

// Scope distinguishes datamesh-wide and replica-level transitions/plans/steps.
type Scope uint8

const (
	// GlobalScope is for transitions/plans/steps that act on the datamesh as a whole.
	GlobalScope Scope = iota
	// ReplicaScope is for transitions/plans/steps that act on a specific replica/node.
	ReplicaScope
)

// ──────────────────────────────────────────────────────────────────────────────
// plan and step (built via DSL, unexported)
//

// plan describes a transition to create: guards, metadata, and steps.
// Scope is inherited from the RegisteredTransition that created it.
type plan struct {
	scope           Scope
	globalGuards    []GlobalGuardFunc
	replicaGuards   []ReplicaGuardFunc
	group           v1alpha1.ReplicatedVolumeDatameshTransitionGroup
	replicaType     v1alpha1.ReplicaType
	fromReplicaType v1alpha1.ReplicaType
	toReplicaType   v1alpha1.ReplicaType
	steps           []step

	// Optional completion callbacks (nil = no-op). Called when all steps are confirmed,
	// before the transition is removed. Exactly one is set, matching the plan scope.
	globalOnComplete  func(gctx *GlobalContext)
	replicaOnComplete func(gctx *GlobalContext, rctx *ReplicaContext)
}

// step is the internal representation of a plan step.
// Tagged union: exactly one of (globalApply+globalConfirm) or (replicaApply+replicaConfirm) is set.
type step struct {
	name  string
	scope Scope

	// Global step callbacks (scope == GlobalScope).
	globalApply   GlobalApplyFunc
	globalConfirm GlobalConfirmFunc

	// Replica step callbacks (scope == ReplicaScope).
	replicaApply   ReplicaApplyFunc
	replicaConfirm ReplicaConfirmFunc

	// Step options for progress message generation.
	messagePrefix string // prefix for request/condition message (e.g., "Joining datamesh")

	// Error diagnostics: which conditions to check on unconfirmed replicas
	// and which errors to skip when generating diagnostic messages.
	diagnosticConditionTypes []string
	diagnosticSkipError      SkipErrorFunc

	// Attachment condition output (only used for Attachment group transitions).
	attachmentConditionReason string // machine-readable reason (e.g., "Attaching")
}

// SkipErrorFunc allows ignoring specific error conditions during diagnostic message generation.
// Returns true if the error condition should be skipped (not reported as an error).
type SkipErrorFunc func(rctx *ReplicaContext, id uint8, cond *metav1.Condition) bool

// onComplete calls the plan's completion callback if set.
func (p *plan) onComplete(gctx *GlobalContext, rctx *ReplicaContext) {
	switch p.scope {
	case GlobalScope:
		if p.globalOnComplete != nil {
			p.globalOnComplete(gctx)
		}
	case ReplicaScope:
		if p.replicaOnComplete != nil {
			p.replicaOnComplete(gctx, rctx)
		}
	}
}

// confirm dispatches to the correct confirm callback based on scope.
func (s *step) confirm(gctx *GlobalContext, rctx *ReplicaContext, stepRevision int64) ConfirmResult {
	switch s.scope {
	case GlobalScope:
		return s.globalConfirm(gctx, stepRevision)
	case ReplicaScope:
		return s.replicaConfirm(gctx, rctx, stepRevision)
	default:
		return ConfirmResult{}
	}
}

// apply dispatches to the correct apply callback based on scope.
func (s *step) apply(gctx *GlobalContext, rctx *ReplicaContext) {
	switch s.scope {
	case GlobalScope:
		s.globalApply(gctx)
	case ReplicaScope:
		s.replicaApply(gctx, rctx)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// PlanBuilder (DSL)
//

// PlanBuilder constructs a plan via a fluent DSL. Created by RegisteredTransition.Plan().
type PlanBuilder struct {
	registry *Registry
	key      registryKey
	p        plan
}

// ReplicaType sets the replica type (for AddReplica/RemoveReplica/ForceRemoveReplica plans).
func (b *PlanBuilder) ReplicaType(t v1alpha1.ReplicaType) *PlanBuilder {
	b.p.replicaType = t
	return b
}

// FromReplicaType sets the source replica type (for ChangeReplicaType plans).
func (b *PlanBuilder) FromReplicaType(t v1alpha1.ReplicaType) *PlanBuilder {
	b.p.fromReplicaType = t
	return b
}

// ToReplicaType sets the target replica type (for ChangeReplicaType plans).
func (b *PlanBuilder) ToReplicaType(t v1alpha1.ReplicaType) *PlanBuilder {
	b.p.toReplicaType = t
	return b
}

// GlobalOnComplete sets an optional callback invoked when all steps are confirmed
// (before the transition is removed). Panics if called on a replica plan.
func (b *PlanBuilder) GlobalOnComplete(fn func(gctx *GlobalContext)) *PlanBuilder {
	if b.p.scope == ReplicaScope {
		panic("dme.PlanBuilder.GlobalOnComplete: cannot add GlobalOnComplete to a replica plan")
	}
	b.p.globalOnComplete = fn
	return b
}

// ReplicaOnComplete sets an optional callback invoked when all steps are confirmed
// (before the transition is removed). Panics if called on a global plan.
func (b *PlanBuilder) ReplicaOnComplete(fn func(gctx *GlobalContext, rctx *ReplicaContext)) *PlanBuilder {
	if b.p.scope == GlobalScope {
		panic("dme.PlanBuilder.ReplicaOnComplete: cannot add ReplicaOnComplete to a global plan")
	}
	b.p.replicaOnComplete = fn
	return b
}

// GlobalGuard adds a global-level guard. Panics if called on a replica plan.
// Guards are evaluated by the engine in order; the first blocking guard stops evaluation.
func (b *PlanBuilder) GlobalGuard(g GlobalGuardFunc) *PlanBuilder {
	if b.p.scope == ReplicaScope {
		panic("dme.PlanBuilder.GlobalGuard: cannot add GlobalGuard to a replica plan")
	}
	b.p.globalGuards = append(b.p.globalGuards, g)
	return b
}

// ReplicaGuard adds a replica-level guard. Panics if called on a global plan.
// Guards are evaluated by the engine in order; the first blocking guard stops evaluation.
func (b *PlanBuilder) ReplicaGuard(g ReplicaGuardFunc) *PlanBuilder {
	if b.p.scope == GlobalScope {
		panic("dme.PlanBuilder.ReplicaGuard: cannot add replica guard to a global plan")
	}
	b.p.replicaGuards = append(b.p.replicaGuards, g)
	return b
}

// GlobalStep adds a step that acts on the datamesh as a whole (e.g., qmr↑/qmr↓).
// For both global and replica plans. Panics if apply or confirm is nil.
func (b *PlanBuilder) GlobalStep(name string, apply GlobalApplyFunc, confirm GlobalConfirmFunc) *PlanBuilder {
	if apply == nil {
		panic("dme.PlanBuilder.GlobalStep: apply must not be nil")
	}
	if confirm == nil {
		panic("dme.PlanBuilder.GlobalStep: confirm must not be nil")
	}
	b.p.steps = append(b.p.steps, step{
		name:          name,
		scope:         GlobalScope,
		globalApply:   apply,
		globalConfirm: confirm,
	})
	return b
}

// ReplicaStep adds a step that acts on a specific replica/node.
// Panics if called on a global plan or if apply or confirm is nil.
func (b *PlanBuilder) ReplicaStep(name string, apply ReplicaApplyFunc, confirm ReplicaConfirmFunc) *PlanBuilder {
	if b.p.scope == GlobalScope {
		panic("dme.PlanBuilder.ReplicaStep: cannot add ReplicaStep to a global plan")
	}
	if apply == nil {
		panic("dme.PlanBuilder.ReplicaStep: apply must not be nil")
	}
	if confirm == nil {
		panic("dme.PlanBuilder.ReplicaStep: confirm must not be nil")
	}
	b.p.steps = append(b.p.steps, step{
		name:           name,
		scope:          ReplicaScope,
		replicaApply:   apply,
		replicaConfirm: confirm,
	})
	return b
}

// MessagePrefix sets the human-readable prefix for progress messages on the last added step.
// Example: "Joining datamesh" → "Joining datamesh, 3/4 replicas confirmed revision 7".
func (b *PlanBuilder) MessagePrefix(prefix string) *PlanBuilder {
	b.lastStep().messagePrefix = prefix
	return b
}

// AttachmentConditionReason sets the machine-readable reason for the attachment condition
// on the last added step (e.g., "Attaching", "Detaching"). Only meaningful for Attachment group transitions.
func (b *PlanBuilder) AttachmentConditionReason(reason string) *PlanBuilder {
	b.lastStep().attachmentConditionReason = reason
	return b
}

// DiagnosticConditions sets the condition types to check on unconfirmed replicas
// for error reporting in progress messages on the last added step.
func (b *PlanBuilder) DiagnosticConditions(conditionTypes ...string) *PlanBuilder {
	b.lastStep().diagnosticConditionTypes = conditionTypes
	return b
}

// DiagnosticSkipError sets a function that allows ignoring specific error conditions
// during diagnostic message generation on the last added step.
func (b *PlanBuilder) DiagnosticSkipError(fn SkipErrorFunc) *PlanBuilder {
	b.lastStep().diagnosticSkipError = fn
	return b
}

// lastStep returns a pointer to the last added step. Panics if no steps have been added.
func (b *PlanBuilder) lastStep() *step {
	if len(b.p.steps) == 0 {
		panic("dme.PlanBuilder: no steps added yet")
	}
	return &b.p.steps[len(b.p.steps)-1]
}

// Build validates the plan, checks key uniqueness, and registers it in the registry.
// Panics if no steps were added or if the (transitionType, PlanID) pair is already registered.
func (b *PlanBuilder) Build() {
	if len(b.p.steps) == 0 {
		panic("dme.PlanBuilder.Build: no steps added")
	}
	if _, exists := b.registry.plans[b.key]; exists {
		panic("dme.PlanBuilder.Build: duplicate plan: (" + string(b.key.transitionType) + ", " + string(b.key.planID) + ")")
	}
	p := b.p
	b.registry.plans[b.key] = &p
}

// ──────────────────────────────────────────────────────────────────────────────
// Guard functions
//

// GlobalGuardFunc checks a transition-level precondition for a global transition.
// Returns (true, message) if blocked, (false, "") if passed.
type GlobalGuardFunc func(gctx *GlobalContext) (blocked bool, message string)

// ReplicaGuardFunc checks a transition-level precondition for a replica transition.
// Returns (true, message) if blocked, (false, "") if passed.
type ReplicaGuardFunc func(gctx *GlobalContext, rctx *ReplicaContext) (blocked bool, message string)

// ──────────────────────────────────────────────────────────────────────────────
// Step callbacks and results
//

// GlobalApplyFunc is called when a global step is activated.
// It mutates rv.Status for the datamesh as a whole (e.g., change qmr, update EffectiveLayout).
// DatameshRevision bump, step.DatameshRevision, and message generation are handled by the engine.
type GlobalApplyFunc func(gctx *GlobalContext)

// GlobalConfirmFunc computes the confirmation sets for a global step.
// stepRevision is the DatameshRevision assigned to this step by the engine.
// The engine determines completion (Confirmed == MustConfirm) and generates messages.
type GlobalConfirmFunc func(gctx *GlobalContext, stepRevision int64) ConfirmResult

// ReplicaApplyFunc is called when a replica step is activated.
// It mutates rv.Status for a specific replica (add/remove, change type,
// attach/detach disk, update EffectiveLayout).
// DatameshRevision bump, step.DatameshRevision, and message generation are handled by the engine.
type ReplicaApplyFunc func(gctx *GlobalContext, rctx *ReplicaContext)

// ReplicaConfirmFunc computes the confirmation sets for a replica step.
// stepRevision is the DatameshRevision assigned to this step by the engine.
// The engine determines completion (Confirmed == MustConfirm) and generates messages.
type ReplicaConfirmFunc func(gctx *GlobalContext, rctx *ReplicaContext, stepRevision int64) ConfirmResult

// ConfirmResult holds the confirmation sets computed by a confirm callback.
// The engine checks completion (Confirmed == MustConfirm) and auto-generates
// progress messages using the step's MessagePrefix, Reason, and DiagnosticConditions.
type ConfirmResult struct {
	// MustConfirm is the set of replica IDs that must confirm this step's revision.
	MustConfirm idset.IDSet
	// Confirmed is the set of replica IDs that have already confirmed.
	Confirmed idset.IDSet
}
