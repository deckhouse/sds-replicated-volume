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

// ──────────────────────────────────────────────────────────────────────────────
// Internal step type
//

// step is the internal representation of a plan step.
// Generic on [G, R] — all callback fields are typed.
// Tagged union: exactly one pair of (globalApply+globalConfirm) or
// (replicaApply+replicaConfirm) is set, determined by scope.
type step[G any, R ReplicaCtx] struct {
	name  string
	scope Scope

	// Global step callbacks (scope == GlobalScope).
	globalApply   GlobalApplyFunc[G]
	globalConfirm GlobalConfirmFunc[G]

	// Replica step callbacks (scope == ReplicaScope).
	replicaApply   ReplicaApplyFunc[G, R]
	replicaConfirm ReplicaConfirmFunc[G, R]

	// Step options.
	details                  any
	diagnosticConditionTypes []string
	diagnosticSkipError      SkipErrorFunc[R]
}

// apply dispatches to the correct apply callback based on scope.
func (s *step[G, R]) apply(gctx G, rctx R) {
	switch s.scope {
	case GlobalScope:
		s.globalApply(gctx)
	case ReplicaScope:
		s.replicaApply(gctx, rctx)
	default:
		panic("dmte: step.apply: invalid scope")
	}
}

// confirm dispatches to the correct confirm callback based on scope.
func (s *step[G, R]) confirm(gctx G, rctx R, stepRevision int64) ConfirmResult {
	switch s.scope {
	case GlobalScope:
		return s.globalConfirm(gctx, stepRevision)
	case ReplicaScope:
		return s.replicaConfirm(gctx, rctx, stepRevision)
	default:
		panic("dmte: step.confirm: invalid scope")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// ReplicaStepBuilder
//

// ReplicaStepBuilder configures a replica-scoped step.
// Created via ReplicaStep(). G and R are inferred from the callback types.
type ReplicaStepBuilder[G any, R ReplicaCtx] struct {
	name                     string
	applyFn                  ReplicaApplyFunc[G, R]
	confirmFn                ReplicaConfirmFunc[G, R]
	details                  any
	diagnosticConditionTypes []string
	diagnosticSkipError      SkipErrorFunc[R]
}

// ReplicaStep creates a replica-scoped step builder.
// G and R are inferred from the apply and confirm callback types.
// Panics if name is empty, apply is nil, or confirm is nil.
func ReplicaStep[G any, R ReplicaCtx](name string, apply ReplicaApplyFunc[G, R], confirm ReplicaConfirmFunc[G, R]) *ReplicaStepBuilder[G, R] {
	if name == "" {
		panic("dmte.ReplicaStep: name must not be empty")
	}
	if apply == nil {
		panic("dmte.ReplicaStep: apply must not be nil")
	}
	if confirm == nil {
		panic("dmte.ReplicaStep: confirm must not be nil")
	}
	return &ReplicaStepBuilder[G, R]{
		name:      name,
		applyFn:   apply,
		confirmFn: confirm,
	}
}

// Details sets opaque supplementary data for this step, passed to
// slot.SetStatus alongside the composed status message.
func (b *ReplicaStepBuilder[G, R]) Details(d any) *ReplicaStepBuilder[G, R] {
	b.details = d
	return b
}

// DiagnosticConditions sets the condition types to check on unconfirmed
// replicas for error reporting in progress messages.
func (b *ReplicaStepBuilder[G, R]) DiagnosticConditions(types ...string) *ReplicaStepBuilder[G, R] {
	b.diagnosticConditionTypes = types
	return b
}

// DiagnosticSkipError sets a function that allows ignoring specific error
// conditions during diagnostic message generation.
func (b *ReplicaStepBuilder[G, R]) DiagnosticSkipError(fn SkipErrorFunc[R]) *ReplicaStepBuilder[G, R] {
	b.diagnosticSkipError = fn
	return b
}

// build returns the internal step. Called by PlanBuilder.Steps().
func (b *ReplicaStepBuilder[G, R]) build() step[G, R] {
	return step[G, R]{
		name:                     b.name,
		scope:                    ReplicaScope,
		replicaApply:             b.applyFn,
		replicaConfirm:           b.confirmFn,
		details:                  b.details,
		diagnosticConditionTypes: b.diagnosticConditionTypes,
		diagnosticSkipError:      b.diagnosticSkipError,
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// GlobalStepBuilder
//

// GlobalStepBuilder configures a global-scoped step.
// Created via GlobalStep(). G is inferred from the callback types.
type GlobalStepBuilder[G any] struct {
	name                     string
	applyFn                  GlobalApplyFunc[G]
	confirmFn                GlobalConfirmFunc[G]
	details                  any
	diagnosticConditionTypes []string
}

// GlobalStep creates a global-scoped step builder.
// G is inferred from the apply and confirm callback types.
// Panics if name is empty, apply is nil, or confirm is nil.
func GlobalStep[G any](name string, apply GlobalApplyFunc[G], confirm GlobalConfirmFunc[G]) *GlobalStepBuilder[G] {
	if name == "" {
		panic("dmte.GlobalStep: name must not be empty")
	}
	if apply == nil {
		panic("dmte.GlobalStep: apply must not be nil")
	}
	if confirm == nil {
		panic("dmte.GlobalStep: confirm must not be nil")
	}
	return &GlobalStepBuilder[G]{
		name:      name,
		applyFn:   apply,
		confirmFn: confirm,
	}
}

// Details sets opaque supplementary data for this step, passed to
// slot.SetStatus alongside the composed status message.
func (b *GlobalStepBuilder[G]) Details(d any) *GlobalStepBuilder[G] {
	b.details = d
	return b
}

// DiagnosticConditions sets the condition types to check on unconfirmed
// replicas for error reporting in progress messages.
func (b *GlobalStepBuilder[G]) DiagnosticConditions(types ...string) *GlobalStepBuilder[G] {
	b.diagnosticConditionTypes = types
	return b
}

// buildStep converts the GlobalStepBuilder into a step[G, R].
// R is provided by PlanBuilder[G, R].Steps() — GlobalStepBuilder itself
// does not know R. Global callbacks are typed; replica callbacks are nil.
func buildGlobalStep[G any, R ReplicaCtx](b *GlobalStepBuilder[G]) step[G, R] {
	return step[G, R]{
		name:                     b.name,
		scope:                    GlobalScope,
		globalApply:              b.applyFn,
		globalConfirm:            b.confirmFn,
		details:                  b.details,
		diagnosticConditionTypes: b.diagnosticConditionTypes,
	}
}
