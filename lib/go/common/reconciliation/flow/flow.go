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

package flow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Package flow provides small “phase scopes” that standardize:
// - phase-scoped logging (`phase start` / `phase end` + duration),
// - panic logging + re-panic,
// - and (for reconciliation) a tiny outcome type with `ShouldReturn()` + `ToCtrl()`.
//
// There are three scopes:
//
//   - ReconcileFlow: used by Reconcile methods, returns ReconcileOutcome (flow-control + error).
//   - EnsureFlow: used by ensure helpers, returns EnsureOutcome (error + change tracking).
//   - StepFlow: used by “steps” that should return plain `error` (idiomatic Go).
//
// Typical usage patterns:
//
// Root reconcile (no phase logging, no OnEnd):
//
//	rf := flow.BeginRootReconcile(ctx)
//	// ...
//	return rf.Done().ToCtrl()
//
// Non-root reconcile method:
//
//	func (r *Reconciler) reconcileX(ctx context.Context) (outcome flow.ReconcileOutcome) {
//	  rf := flow.BeginReconcile(ctx, "x")
//	  defer rf.OnEnd(&outcome)
//	  // ...
//	  return rf.Continue()
//	}
//
// Ensure helper:
//
//	func ensureFoo(ctx context.Context, obj *v1alpha1.Foo) (outcome flow.EnsureOutcome) {
//	  ef := flow.BeginEnsure(ctx, "ensure-foo")
//	  defer ef.OnEnd(&outcome)
//	  // mutate obj ...
//	  return ef.Ok().ReportChangedIf(changed)
//	}
//
// Step helper returning error:
//
//	func computeBar(ctx context.Context) (err error) {
//	  sf := flow.BeginStep(ctx, "compute-bar")
//	  defer sf.OnEnd(&err)
//	  // ...
//	  return sf.Errf("bad input: %s", x)
//	}
//
// =============================================================================
// Common utilities
// =============================================================================

// Wrapf wraps err with formatted context.
//
// It returns nil if err is nil.
//
// Example:
//
//	return flow.Wrapf(err, "patching Foo")
func Wrapf(err error, format string, args ...any) error {
	if err == nil {
		return nil
	}
	msg := fmt.Sprintf(format, args...)
	return fmt.Errorf("%s: %w", msg, err)
}

// isExpectedTransientError reports whether err consists entirely of expected
// transient API errors that should be retried silently (logged at Info level,
// not Error level, and not propagated to controller-runtime as an error).
//
// Currently recognized: 409 Conflict (optimistic lock failure).
//
// For joined errors (produced by errors.Join or MergeReconciles), every
// component error must be transient; a single non-transient component causes
// the whole error to be treated as a real failure.
func isExpectedTransientError(err error) bool {
	if err == nil {
		return false
	}

	// Multi-error (errors.Join): every component must be transient.
	if multi, ok := err.(interface{ Unwrap() []error }); ok {
		errs := multi.Unwrap()
		if len(errs) == 0 {
			return false
		}
		for _, e := range errs {
			if !isExpectedTransientError(e) {
				return false
			}
		}
		return true
	}

	// Single wrapping (fmt.Errorf %w, Wrapf, Enrichf): follow the chain.
	if inner := errors.Unwrap(err); inner != nil {
		return isExpectedTransientError(inner)
	}

	// Leaf error: check known transient types.
	return apierrors.IsConflict(err)
}

// phaseContextKey is a private context key for phase metadata.
type phaseContextKey struct{}

// phaseStartKey sentinel type; value is time.Time (start of the phase).
// OnEnd reads it back to compute duration.

// panicToError converts a recovered panic value to an error.
func panicToError(r any) error {
	if err, ok := r.(error); ok {
		return Wrapf(err, "panic")
	}
	return fmt.Errorf("panic: %v", r)
}

// mustBeValidPhaseName validates phaseName used by Begin* and panics on invalid input.
//
// This is treated as a programmer error (hence panic), not a runtime failure.
func mustBeValidPhaseName(name string) {
	if name == "" {
		panic("flow: phaseName must be non-empty")
	}

	segLen := 0
	for i := 0; i < len(name); i++ {
		c := name[i]

		// Disallow whitespace and control chars.
		if c <= ' ' || c == 0x7f {
			panic("flow: phaseName contains whitespace/control characters: " + name)
		}

		if c == '/' {
			// Empty segments and trailing '/' are not allowed.
			if segLen == 0 {
				panic("flow: phaseName must not contain empty segments (e.g. leading '//' or trailing '/'): " + name)
			}
			segLen = 0
			continue
		}

		// Recommended: ascii identifiers with separators.
		isLetter := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		isDigit := c >= '0' && c <= '9'
		isAllowedPunct := c == '-' || c == '_' || c == '.'
		if !isLetter && !isDigit && !isAllowedPunct {
			panic("flow: phaseName contains unsupported character '" + string([]byte{c}) + "': " + name)
		}

		segLen++
	}

	if segLen == 0 {
		panic("flow: phaseName must not end with '/': " + name)
	}
}

// mustBeValidKV validates that kv has an even number of elements (key/value pairs).
// Panics on invalid input to surface programmer errors early.
func mustBeValidKV(kv []string) {
	if len(kv)%2 != 0 {
		panic("flow: kv must contain even number of elements (key/value pairs)")
	}
}

// buildPhaseLogger builds a phase-scoped logger: `WithName(phaseName)` + `WithValues(kv...)`.
func buildPhaseLogger(ctx context.Context, phaseName string, kv []string) logr.Logger {
	l := log.FromContext(ctx).WithName(phaseName)
	if len(kv) > 0 {
		anyKV := make([]any, 0, len(kv))
		for _, v := range kv {
			anyKV = append(anyKV, v)
		}
		l = l.WithValues(anyKV...)
	}
	return l
}

// storePhaseStart attaches the logger to ctx and stores the phase start time for OnEnd duration.
func storePhaseStart(ctx context.Context, l logr.Logger) context.Context {
	ctx = log.IntoContext(ctx, l)
	return context.WithValue(ctx, phaseContextKey{}, time.Now())
}

// getPhaseStart reads the phase start time stored by Begin* (if any).
func getPhaseStart(ctx context.Context) (time.Time, bool) {
	t, ok := ctx.Value(phaseContextKey{}).(time.Time)
	return t, ok && !t.IsZero()
}

// =============================================================================
// ReconcileFlow and ReconcileOutcome
// =============================================================================

// ReconcileFlow is a phase scope for Reconcile methods.
//
// Use it to:
// - get a phase-scoped ctx/logger (`Ctx()`/`Log()`),
// - construct ReconcileOutcome values (`Continue/Done/DoneAndRequeue/DoneAndRequeueAfter/ContinueAndRequeue/ContinueAndRequeueAfter/Fail`),
// - and to standardize phase end handling via `defer rf.OnEnd(&outcome)` in non-root reconciles.
type ReconcileFlow struct {
	ctx context.Context
	log logr.Logger
}

// Ctx returns a context with a phase-scoped logger attached.
func (rf ReconcileFlow) Ctx() context.Context { return rf.ctx }

// Log returns the phase-scoped logger.
func (rf ReconcileFlow) Log() logr.Logger { return rf.log }

// BeginRootReconcile starts the root reconcile scope.
//
// This is intentionally minimal: it does not log `phase start/end` and it does not use `OnEnd`.
// Root reconcile is expected to return via `outcome.ToCtrl()`.
func BeginRootReconcile(ctx context.Context) ReconcileFlow {
	l := log.FromContext(ctx)
	return ReconcileFlow{ctx: ctx, log: l}
}

// BeginReconcile starts a non-root reconciliation phase.
//
// Intended usage:
//
//	func (...) (outcome flow.ReconcileOutcome) {
//	  rf := flow.BeginReconcile(ctx, "my-phase", "k", "v")
//	  defer rf.OnEnd(&outcome)
//	  // ...
//	}
func BeginReconcile(ctx context.Context, phaseName string, kv ...string) ReconcileFlow {
	mustBeValidPhaseName(phaseName)
	mustBeValidKV(kv)

	l := buildPhaseLogger(ctx, phaseName, kv)
	l.V(1).Info("phase start")

	ctx = storePhaseStart(ctx, l)
	return ReconcileFlow{ctx: ctx, log: l}
}

// OnEnd is the deferred "phase end handler" for non-root reconciles.
//
// What it does:
// - logs `phase end` (and duration if available),
// - if the outcome has an error, logs it at Error level exactly once across nested phases,
// - logs `changed` field,
// - if the phase panics, logs `phase panic` and re-panics.
func (rf ReconcileFlow) OnEnd(out *ReconcileOutcome) {
	if r := recover(); r != nil {
		err := panicToError(r)
		rf.log.Error(err, "phase panic")
		panic(r)
	}

	start, ok := getPhaseStart(rf.ctx)
	if !ok {
		return
	}

	if out == nil {
		panic("flow: ReconcileFlow.OnEnd: outcome is nil")
	}

	kind, requeueAfter := reconcileOutcomeKind(out)

	fields := []any{
		"result", kind,
		"changed", out.changed,
		"hasError", out.err != nil,
	}
	if requeueAfter > 0 {
		fields = append(fields, "requeueAfter", requeueAfter)
	}
	fields = append(fields, "duration", time.Since(start))

	// Emit exactly one log record per phase end.
	// Error is logged exactly once: at the first phase that encounters it.
	if out.err != nil && !out.errorLogged {
		if isExpectedTransientError(out.err) {
			// Conflict (optimistic lock) is an expected transient condition;
			// log at Info, not Error.
			rf.log.Info("phase end", append(fields, "err", out.err)...)
		} else {
			rf.log.Error(out.err, "phase end", fields...)
		}
		out.errorLogged = true
		return
	}
	rf.log.V(1).Info("phase end", fields...)
}

// Continue indicates "keep executing" within the current Reconcile method.
// `ShouldReturn()` is false.
func (rf ReconcileFlow) Continue() ReconcileOutcome {
	return ReconcileOutcome{terminal: false}
}

// Done indicates "stop and return; do not requeue".
// `ShouldReturn()` is true.
func (rf ReconcileFlow) Done() ReconcileOutcome {
	return ReconcileOutcome{terminal: true}
}

// DoneAndRequeue indicates "stop and return; requeue immediately".
// `ShouldReturn()` is true.
//
// Use this when you want to stop processing immediately and schedule an immediate requeue
// (e.g., all changes are already persisted, or no further steps are needed).
func (rf ReconcileFlow) DoneAndRequeue() ReconcileOutcome {
	return ReconcileOutcome{terminal: true, requeueIntent: &ctrl.Result{Requeue: true}}
}

// DoneAndRequeueAfter indicates "stop and return; requeue after d".
// `ShouldReturn()` is true.
//
// Use this when you want to stop processing immediately and schedule a delayed requeue
// (e.g., all changes are already persisted, or no further steps are needed).
func (rf ReconcileFlow) DoneAndRequeueAfter(d time.Duration) ReconcileOutcome {
	if d <= 0 {
		panic("flow: DoneAndRequeueAfter: duration must be > 0")
	}
	return ReconcileOutcome{terminal: true, requeueIntent: &ctrl.Result{RequeueAfter: d}}
}

// ContinueAndRequeue indicates "remember requeue intent, but keep processing".
// `ShouldReturn()` is false.
//
// Use this when you need to schedule an immediate requeue but want reconciliation
// to continue (e.g., to run subsequent reconcile steps, persist accumulated changes,
// or perform cleanup before returning).
func (rf ReconcileFlow) ContinueAndRequeue() ReconcileOutcome {
	return ReconcileOutcome{terminal: false, requeueIntent: &ctrl.Result{Requeue: true}}
}

// ContinueAndRequeueAfter indicates "remember requeue intent with delay, but keep processing".
// `ShouldReturn()` is false.
//
// Use this when you need to schedule a delayed requeue but want reconciliation
// to continue (e.g., to run subsequent reconcile steps, persist accumulated changes,
// or perform cleanup before returning).
func (rf ReconcileFlow) ContinueAndRequeueAfter(d time.Duration) ReconcileOutcome {
	if d <= 0 {
		panic("flow: ContinueAndRequeueAfter: duration must be > 0")
	}
	return ReconcileOutcome{terminal: false, requeueIntent: &ctrl.Result{RequeueAfter: d}}
}

// Fail indicates "stop and return with error".
// `ShouldReturn()` is true.
func (rf ReconcileFlow) Fail(err error) ReconcileOutcome {
	if err == nil {
		panic("flow: Fail: nil error")
	}
	return ReconcileOutcome{terminal: true, err: err}
}

// Failf is a convenience wrapper around `Fail(Wrapf(...))`.
func (rf ReconcileFlow) Failf(err error, format string, args ...any) ReconcileOutcome {
	return rf.Fail(Wrapf(err, format, args...))
}

// DoneOrFail returns Done() if err is nil, or Fail(err) otherwise.
// Useful for propagating errors from final operations like patches.
func (rf ReconcileFlow) DoneOrFail(err error) ReconcileOutcome {
	if err != nil {
		return rf.Fail(err)
	}
	return rf.Done()
}

// ReconcileOutcome is the return value for Reconcile methods.
//
// Typical usage is:
// - declare `outcome flow.ReconcileOutcome` as a named return,
// - return `rf.Continue()/Done()/DoneAndRequeue.../ContinueAndRequeue.../Fail...`,
// - and use `outcome.ShouldReturn()` at intermediate boundaries to early-exit.
//
// ReconcileOutcome also supports change tracking,
// enabling sub-reconciles to propagate change information upward:
// - use `ReportChanged()`/`ReportChangedIf(cond)` to mark changes,
// - use `DidChange()` to query whether any change was recorded.
//
// Terminal vs non-terminal outcomes:
//   - Terminal outcomes (Done*, Fail) have ShouldReturn() = true — stop processing now.
//   - Non-terminal outcomes (Continue*) have ShouldReturn() = false — keep processing.
//   - ContinueAndRequeue* variants remember requeue intent without stopping processing,
//     allowing reconciliation to continue before returning.
type ReconcileOutcome struct {
	terminal      bool         // true for Done*/Fail (ShouldReturn = true)
	requeueIntent *ctrl.Result // requeue info for ContinueAndRequeue* variants
	err           error
	errorLogged   bool
	changed       bool
}

// ShouldReturn reports whether the caller should return from the current Reconcile method.
//
// Returns true only for terminal outcomes (Done*, Fail).
// Returns false for non-terminal outcomes (Continue*), even if they have requeue intent.
func (o ReconcileOutcome) ShouldReturn() bool { return o.terminal }

// Error returns the error carried by the outcome, if any.
func (o ReconcileOutcome) Error() error { return o.err }

// Enrichf adds local context to an existing error (no-op if there is no error).
//
// Example:
//
//	return rf.Fail(err).Enrichf("patching ReplicatedVolume")
func (o ReconcileOutcome) Enrichf(format string, args ...any) ReconcileOutcome {
	if o.err == nil {
		return o
	}
	o.err = Wrapf(o.err, format, args...)
	return o
}

// ToCtrl converts ReconcileOutcome to controller-runtime return values.
// Requeue intent (if any) is reflected in ctrl.Result; error (if any) is returned as-is.
//
// Conflict (optimistic lock) errors are automatically converted to a rate-limited
// requeue without error. This prevents controller-runtime from logging them at
// Error level and incrementing error metrics.
func (o ReconcileOutcome) ToCtrl() (ctrl.Result, error) {
	if o.err != nil && isExpectedTransientError(o.err) {
		return ctrl.Result{Requeue: true}, nil
	}
	if o.requeueIntent != nil {
		return *o.requeueIntent, o.err
	}
	return ctrl.Result{}, o.err
}

// MustToCtrl converts ReconcileOutcome to controller-runtime return values.
// It panics if called on Continue (non-terminal without requeue intent).
func (o ReconcileOutcome) MustToCtrl() (ctrl.Result, error) {
	if !o.terminal && o.requeueIntent == nil {
		panic("flow: ReconcileOutcome.MustToCtrl: called on Continue (non-terminal without requeue)")
	}
	return o.ToCtrl()
}

// Merge combines this outcome with others and returns the merged result.
//
// This is a convenience method for chaining: outcome = outcome.Merge(a, b).
func (o ReconcileOutcome) Merge(others ...ReconcileOutcome) ReconcileOutcome {
	return MergeReconciles(append([]ReconcileOutcome{o}, others...)...)
}

// ReportChanged marks that this reconcile step changed something.
func (o ReconcileOutcome) ReportChanged() ReconcileOutcome {
	o.changed = true
	return o
}

// ReportChangedIf is like ReportChanged, but records a change only when cond is true.
func (o ReconcileOutcome) ReportChangedIf(cond bool) ReconcileOutcome {
	o.changed = o.changed || cond
	return o
}

// DidChange reports whether the outcome records a change.
func (o ReconcileOutcome) DidChange() bool { return o.changed }

// WithChangeFrom merges change tracking state from an EnsureOutcome into ReconcileOutcome.
//
// Merge semantics: changed is OR-ed.
//
// This is useful for propagating ensure helper results through reconcile outcomes:
//
//	eo := flow.MergeEnsures(ensureA(...), ensureB(...))
//	if eo.Error() != nil {
//	    return rf.Fail(eo.Error())
//	}
//	return rf.Continue().WithChangeFrom(eo)
func (o ReconcileOutcome) WithChangeFrom(eo EnsureOutcome) ReconcileOutcome {
	o.changed = o.changed || eo.changed
	return o
}

// MergeReconciles combines multiple ReconcileOutcome values into one.
//
// Use this when you intentionally want to run multiple independent steps and then aggregate the decision.
//
// Rules (high-level):
// - Terminal outcomes win over non-terminal.
// - Errors are joined via errors.Join (any error makes the merged outcome a Fail — terminal).
// - Among terminals: errors first, then requeue (min delay wins), then Done.
// - Among non-terminals: requeue intent is merged (min delay wins).
// - Change state is merged deterministically (any changed makes merged changed).
//
// Example:
//
//	outcome := MergeReconciles(stepA(...), stepB(...))
//	if outcome.ShouldReturn() { return outcome }
func MergeReconciles(outcomes ...ReconcileOutcome) ReconcileOutcome {
	if len(outcomes) == 0 {
		return ReconcileOutcome{}
	}

	const noDelay time.Duration = -1
	var (
		errs             []error
		allErrorsLogged  = true
		anyChanged       bool
		hasTerminal      bool
		terminalDelay    = noDelay
		nonTerminalDelay = noDelay
	)

	for _, o := range outcomes {
		if o.err != nil {
			errs = append(errs, o.err)
			allErrorsLogged = allErrorsLogged && o.errorLogged
		}
		anyChanged = anyChanged || o.changed

		delay := requeueDelay(o.requeueIntent)
		if o.terminal {
			hasTerminal = true
			if delay >= 0 && (terminalDelay < 0 || delay < terminalDelay) {
				terminalDelay = delay
			}
		} else if delay >= 0 && (nonTerminalDelay < 0 || delay < nonTerminalDelay) {
			nonTerminalDelay = delay
		}
	}

	result := ReconcileOutcome{
		changed: anyChanged,
	}
	if err := errors.Join(errs...); err != nil {
		result.terminal = true
		result.err = err
		result.errorLogged = allErrorsLogged
		return result
	}
	if hasTerminal {
		result.terminal = true
		result.requeueIntent = delayToRequeueIntent(terminalDelay)
		return result
	}
	result.requeueIntent = delayToRequeueIntent(nonTerminalDelay)
	return result
}

// requeueDelay extracts delay from requeueIntent: -1 = none, 0 = immediate, >0 = delayed.
func requeueDelay(r *ctrl.Result) time.Duration {
	if r == nil {
		return -1
	}
	if r.Requeue { //nolint:staticcheck // handling Requeue field
		return 0
	}
	if r.RequeueAfter > 0 {
		return r.RequeueAfter
	}
	return -1
}

// delayToRequeueIntent converts delay back to requeueIntent.
func delayToRequeueIntent(d time.Duration) *ctrl.Result {
	switch {
	case d < 0:
		return nil
	case d == 0:
		return &ctrl.Result{Requeue: true}
	default:
		return &ctrl.Result{RequeueAfter: d}
	}
}

// reconcileOutcomeKind classifies the outcome for phase-end logging.
func reconcileOutcomeKind(o *ReconcileOutcome) (kind string, requeueAfter time.Duration) {
	if o == nil {
		panic("flow: reconcileOutcomeKind: outcome is nil")
	}
	if o.err != nil {
		if isExpectedTransientError(o.err) {
			return "Conflict", 0
		}
		return "Fail", 0
	}

	prefix := "Continue"
	if o.terminal {
		prefix = "Done"
	}

	switch delay := requeueDelay(o.requeueIntent); {
	case delay < 0:
		return prefix, 0
	case delay == 0:
		return prefix + "AndRequeue", 0
	default:
		return prefix + "AndRequeueAfter", delay
	}
}

// =============================================================================
// EnsureFlow and EnsureOutcome
// =============================================================================

// EnsureFlow is a phase scope for ensure helpers.
//
// Ensure helpers typically mutate an object in-memory (one patch domain) and must report:
// - whether they changed the object (DidChange), and
// - whether they encountered an error.
type EnsureFlow struct {
	ctx context.Context
	log logr.Logger
}

// Ctx returns a context with a phase-scoped logger attached.
func (ef EnsureFlow) Ctx() context.Context { return ef.ctx }

// Log returns the phase-scoped logger.
func (ef EnsureFlow) Log() logr.Logger { return ef.log }

// BeginEnsure starts an ensure phase.
//
// Intended usage:
//
//	func ensureFoo(ctx context.Context, obj *v1alpha1.Foo) (outcome flow.EnsureOutcome) {
//	  ef := flow.BeginEnsure(ctx, "ensure-foo")
//	  defer ef.OnEnd(&outcome)
//	  // mutate obj ...
//	  return ef.Ok().ReportChangedIf(changed)
//	}
func BeginEnsure(ctx context.Context, phaseName string, kv ...string) EnsureFlow {
	mustBeValidPhaseName(phaseName)
	mustBeValidKV(kv)

	l := buildPhaseLogger(ctx, phaseName, kv)
	l.V(1).Info("phase start")

	ctx = storePhaseStart(ctx, l)
	return EnsureFlow{ctx: ctx, log: l}
}

// OnEnd is the deferred “phase end handler” for ensure helpers.
//
// What it does:
// - logs `phase end` with `changed`, `hasError`, and duration,
// - if the phase panics, logs `phase panic` and re-panics.
func (ef EnsureFlow) OnEnd(out *EnsureOutcome) {
	if r := recover(); r != nil {
		err := panicToError(r)
		ef.log.Error(err, "phase panic")
		panic(r)
	}

	start, ok := getPhaseStart(ef.ctx)
	if !ok {
		return
	}

	if out == nil {
		panic("flow: EnsureFlow.OnEnd: outcome is nil")
	}

	fields := []any{
		"changed", out.changed,
		"hasError", out.err != nil,
		"duration", time.Since(start),
	}

	if out.err != nil {
		ef.log.Error(out.err, "phase end", fields...)
		return
	}
	ef.log.V(1).Info("phase end", fields...)
}

// Ok returns an EnsureOutcome indicating success (no error, no change).
func (ef EnsureFlow) Ok() EnsureOutcome {
	return EnsureOutcome{}
}

// Err returns an EnsureOutcome with an error.
func (ef EnsureFlow) Err(err error) EnsureOutcome {
	return EnsureOutcome{err: err}
}

// Errf returns an EnsureOutcome with a formatted error.
func (ef EnsureFlow) Errf(format string, args ...any) EnsureOutcome {
	return EnsureOutcome{err: fmt.Errorf(format, args...)}
}

// EnsureOutcome is the return value for ensure helpers.
//
// It reports:
// - Error(): whether the helper failed,
// - DidChange(): whether the helper mutated the object.
//
// Typical pattern:
//
//	changed := false
//	// mutate obj; set changed=true if needed
//	return ef.Ok().ReportChangedIf(changed)
type EnsureOutcome struct {
	err     error
	changed bool
}

// Error returns the error carried by the outcome, if any.
func (o EnsureOutcome) Error() error { return o.err }

// Enrichf adds local context to an existing error (no-op if there is no error).
func (o EnsureOutcome) Enrichf(format string, args ...any) EnsureOutcome {
	if o.err == nil {
		return o
	}
	o.err = Wrapf(o.err, format, args...)
	return o
}

// ReportChanged marks that the helper changed the object.
func (o EnsureOutcome) ReportChanged() EnsureOutcome {
	o.changed = true
	return o
}

// ReportChangedIf is like ReportChanged, but records a change only when cond is true.
func (o EnsureOutcome) ReportChangedIf(cond bool) EnsureOutcome {
	o.changed = o.changed || cond
	return o
}

// DidChange reports whether the outcome records a change.
func (o EnsureOutcome) DidChange() bool { return o.changed }

// Merge combines this outcome with others and returns the merged result.
//
// This is a convenience method for chaining: eo = eo.Merge(a, b).
func (o EnsureOutcome) Merge(others ...EnsureOutcome) EnsureOutcome {
	return MergeEnsures(append([]EnsureOutcome{o}, others...)...)
}

// MergeEnsures combines multiple EnsureOutcome values into one.
//
// Use this to aggregate outcomes of multiple sub-ensures within the same ensure helper.
//
// - Errors are joined via errors.Join.
// - Change state is merged deterministically (any changed makes merged changed).
func MergeEnsures(outcomes ...EnsureOutcome) EnsureOutcome {
	if len(outcomes) == 0 {
		return EnsureOutcome{}
	}

	var (
		errs       []error
		anyChanged bool
	)

	for _, o := range outcomes {
		if o.err != nil {
			errs = append(errs, o.err)
		}
		anyChanged = anyChanged || o.changed
	}

	return EnsureOutcome{
		err:     errors.Join(errs...),
		changed: anyChanged,
	}
}

// =============================================================================
// StepFlow
// =============================================================================

// StepFlow is a phase scope for steps that should return plain `error`.
//
// This is useful when you want phase logging/panic handling but do not want flow-control outcomes.
type StepFlow struct {
	ctx context.Context
	log logr.Logger
}

// Ctx returns a context with a phase-scoped logger attached.
func (sf StepFlow) Ctx() context.Context { return sf.ctx }

// Log returns the phase-scoped logger.
func (sf StepFlow) Log() logr.Logger { return sf.log }

// BeginStep starts a step phase.
//
// Intended usage:
//
//	func computeFoo(ctx context.Context) (err error) {
//	  sf := flow.BeginStep(ctx, "compute-foo")
//	  defer sf.OnEnd(&err)
//	  // ...
//	  return nil
//	}
func BeginStep(ctx context.Context, phaseName string, kv ...string) StepFlow {
	mustBeValidPhaseName(phaseName)
	mustBeValidKV(kv)

	l := buildPhaseLogger(ctx, phaseName, kv)
	l.V(1).Info("phase start")

	ctx = storePhaseStart(ctx, l)
	return StepFlow{ctx: ctx, log: l}
}

// OnEnd is the deferred “phase end handler” for step functions that return `error`.
//
// What it does:
// - logs `phase end` with `hasError` and duration,
// - if the phase panics, logs `phase panic` and re-panics.
func (sf StepFlow) OnEnd(err *error) {
	if r := recover(); r != nil {
		panicErr := panicToError(r)
		sf.log.Error(panicErr, "phase panic")
		panic(r)
	}

	start, ok := getPhaseStart(sf.ctx)
	if !ok {
		return
	}

	if err == nil {
		panic("flow: StepFlow.OnEnd: err is nil")
	}

	fields := []any{
		"hasError", *err != nil,
		"duration", time.Since(start),
	}

	if *err != nil {
		sf.log.Error(*err, "phase end", fields...)
		return
	}
	sf.log.V(1).Info("phase end", fields...)
}

// Ok returns nil (success).
func (sf StepFlow) Ok() error { return nil }

// Err returns the error as-is. Panics if err is nil.
func (sf StepFlow) Err(err error) error {
	if err == nil {
		panic("flow: StepFlow.Err: nil error")
	}
	return err
}

// Errf returns a formatted error.
func (sf StepFlow) Errf(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}

// Enrichf wraps err with formatted context. Returns nil if err is nil.
//
// Example:
//
//	return sf.Enrichf(err, "doing something")
func (sf StepFlow) Enrichf(err error, format string, args ...any) error {
	return Wrapf(err, format, args...)
}

// MergeSteps combines multiple errors into one via errors.Join.
//
// This is useful when you want to run multiple independent sub-steps and return a single error:
//
//	return MergeSteps(errA, errB, errC)
func MergeSteps(errs ...error) error {
	return errors.Join(errs...)
}
