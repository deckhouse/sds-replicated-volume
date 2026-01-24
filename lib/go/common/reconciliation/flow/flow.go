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
//   - EnsureFlow: used by ensure helpers, returns EnsureOutcome (error + change tracking + optimistic lock intent).
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
//	  return ef.Ok().ReportChangedIf(changed).RequireOptimisticLock()
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

// phaseContextKey is a private context key for phase metadata.
type phaseContextKey struct{}

// phaseContextValue is the minimal metadata OnEnd needs for consistent logging.
type phaseContextValue struct {
	name  string
	kv    []string
	start time.Time
}

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

// storePhaseContext attaches the logger to ctx and stores metadata needed by OnEnd.
func storePhaseContext(ctx context.Context, l logr.Logger, phaseName string, kv []string) context.Context {
	ctx = log.IntoContext(ctx, l)
	kvCopy := append([]string(nil), kv...)
	ctx = context.WithValue(ctx, phaseContextKey{}, phaseContextValue{
		name:  phaseName,
		kv:    kvCopy,
		start: time.Now(),
	})
	return ctx
}

// getPhaseContext reads metadata stored by Begin* (if any).
func getPhaseContext(ctx context.Context) (phaseContextValue, bool) {
	v, ok := ctx.Value(phaseContextKey{}).(phaseContextValue)
	return v, ok && v.name != ""
}

// =============================================================================
// ReconcileFlow and ReconcileOutcome
// =============================================================================

// ReconcileFlow is a phase scope for Reconcile methods.
//
// Use it to:
// - get a phase-scoped ctx/logger (`Ctx()`/`Log()`),
// - construct ReconcileOutcome values (`Continue/Done/Requeue/RequeueAfter/Fail`),
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

	ctx = storePhaseContext(ctx, l, phaseName, kv)
	return ReconcileFlow{ctx: ctx, log: l}
}

// OnEnd is the deferred “phase end handler” for non-root reconciles.
//
// What it does:
// - logs `phase end` (and duration if available),
// - if the outcome has an error, logs it at Error level exactly once across nested phases,
// - if the phase panics, logs `phase panic` and re-panics.
func (rf ReconcileFlow) OnEnd(out *ReconcileOutcome) {
	if r := recover(); r != nil {
		err := panicToError(r)
		rf.log.Error(err, "phase panic")
		panic(r)
	}

	v, ok := getPhaseContext(rf.ctx)
	if !ok {
		return
	}

	if out == nil {
		panic("flow: ReconcileFlow.OnEnd: outcome is nil")
	}

	kind, requeueAfter := reconcileOutcomeKind(out)

	fields := []any{
		"result", kind,
		"hasError", out.err != nil,
	}
	if requeueAfter > 0 {
		fields = append(fields, "requeueAfter", requeueAfter)
	}
	if !v.start.IsZero() {
		fields = append(fields, "duration", time.Since(v.start))
	}

	// Emit exactly one log record per phase end.
	// Error is logged exactly once: at the first phase that encounters it.
	if out.err != nil && !out.errorLogged {
		rf.log.Error(out.err, "phase end", fields...)
		out.errorLogged = true
		return
	}
	rf.log.V(1).Info("phase end", fields...)
}

// Continue indicates “keep executing” within the current Reconcile method.
// `ShouldReturn()` is false.
func (rf ReconcileFlow) Continue() ReconcileOutcome {
	return ReconcileOutcome{}
}

// Done indicates “stop and return; do not requeue”.
// `ShouldReturn()` is true.
func (rf ReconcileFlow) Done() ReconcileOutcome {
	return ReconcileOutcome{result: &ctrl.Result{}}
}

// Requeue indicates “stop and return; requeue immediately”.
// `ShouldReturn()` is true.
func (rf ReconcileFlow) Requeue() ReconcileOutcome {
	return ReconcileOutcome{result: &ctrl.Result{Requeue: true}}
}

// RequeueAfter indicates “stop and return; requeue after d”.
// `ShouldReturn()` is true.
func (rf ReconcileFlow) RequeueAfter(d time.Duration) ReconcileOutcome {
	if d <= 0 {
		panic("flow: RequeueAfter: duration must be > 0")
	}
	return ReconcileOutcome{result: &ctrl.Result{RequeueAfter: d}}
}

// Fail indicates “stop and return with error”.
// `ShouldReturn()` is true.
func (rf ReconcileFlow) Fail(err error) ReconcileOutcome {
	if err == nil {
		panic("flow: Fail: nil error")
	}
	return ReconcileOutcome{result: &ctrl.Result{}, err: err}
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
// - return `rf.Continue()/Done()/Requeue.../Fail...`,
// - and use `outcome.ShouldReturn()` at intermediate boundaries to early-exit.
type ReconcileOutcome struct {
	result      *ctrl.Result
	err         error
	errorLogged bool
}

// ShouldReturn reports whether the caller should return from the current Reconcile method.
func (o ReconcileOutcome) ShouldReturn() bool { return o.result != nil }

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
//
// For Continue (result=nil), this returns `(ctrl.Result{}, nil)` (or `(ctrl.Result{}, err)` if you built an invalid outcome).
// For non-Continue outcomes, this returns the explicit ctrl.Result + error.
func (o ReconcileOutcome) ToCtrl() (ctrl.Result, error) {
	if o.result == nil {
		return ctrl.Result{}, o.err
	}
	return *o.result, o.err
}

// MustToCtrl converts ReconcileOutcome to controller-runtime return values.
// It panics if called on Continue.
func (o ReconcileOutcome) MustToCtrl() (ctrl.Result, error) {
	if o.result == nil {
		panic("flow: ReconcileOutcome.MustToCtrl: result is nil (Continue)")
	}
	return *o.result, o.err
}

// Merge combines this outcome with others and returns the merged result.
//
// This is a convenience method for chaining: outcome = outcome.Merge(a, b).
func (o ReconcileOutcome) Merge(others ...ReconcileOutcome) ReconcileOutcome {
	return MergeReconciles(append([]ReconcileOutcome{o}, others...)...)
}

// MergeReconciles combines multiple ReconcileOutcome values into one.
//
// Use this when you intentionally want to run multiple independent steps and then aggregate the decision.
//
// Rules (high-level):
// - Errors are joined via errors.Join (any error makes the merged outcome a Fail).
// - Requeue/RequeueAfter: treat Requeue as delay=0, RequeueAfter(d) as delay=d, pick minimum delay.
// - Done wins over Continue.
//
// Example:
//
//	outcome := MergeReconciles(stepA(...), stepB(...))
//	if outcome.ShouldReturn() { return outcome }
func MergeReconciles(outcomes ...ReconcileOutcome) ReconcileOutcome {
	if len(outcomes) == 0 {
		return ReconcileOutcome{}
	}

	const (
		noDelay        time.Duration = -1 // sentinel: no requeue requested
		immediateDelay time.Duration = 0  // Requeue() means delay=0
	)

	var (
		hasReconcileResult bool
		minDelay           = noDelay
		errs               []error
		allErrorsLogged    = true
	)

	for _, o := range outcomes {
		if o.err != nil {
			errs = append(errs, o.err)
			allErrorsLogged = allErrorsLogged && o.errorLogged
		}

		if o.result == nil {
			continue
		}
		hasReconcileResult = true

		// Compute delay for this outcome: Requeue → 0, RequeueAfter(d) → d
		delay := noDelay
		if o.result.Requeue { //nolint:staticcheck // handling deprecated Requeue field for backward compatibility
			delay = immediateDelay
		} else if o.result.RequeueAfter > 0 {
			delay = o.result.RequeueAfter
		}

		// Pick minimum delay (noDelay means "no requeue requested")
		if delay != noDelay {
			if minDelay == noDelay || delay < minDelay {
				minDelay = delay
			}
		}
	}

	combinedErr := errors.Join(errs...)

	// 1) Fail: if there are errors.
	if combinedErr != nil {
		return ReconcileOutcome{
			result:      &ctrl.Result{},
			err:         combinedErr,
			errorLogged: allErrorsLogged,
		}
	}

	// 2) Requeue/RequeueAfter: minDelay wins.
	if minDelay == immediateDelay {
		return ReconcileOutcome{result: &ctrl.Result{Requeue: true}}
	}
	if minDelay > immediateDelay {
		return ReconcileOutcome{result: &ctrl.Result{RequeueAfter: minDelay}}
	}

	// 3) Done: at least one non-nil result (no requeue requested).
	if hasReconcileResult {
		return ReconcileOutcome{result: &ctrl.Result{}}
	}

	// 4) Continue.
	return ReconcileOutcome{}
}

// reconcileOutcomeKind classifies the outcome for phase-end logging.
func reconcileOutcomeKind(o *ReconcileOutcome) (kind string, requeueAfter time.Duration) {
	if o == nil {
		panic("flow: reconcileOutcomeKind: outcome is nil")
	}

	if o.result == nil {
		if o.err != nil {
			return "invalid", 0
		}
		return "continue", 0
	}

	if o.result.Requeue { //nolint:staticcheck // handling deprecated Requeue field for backward compatibility
		return "requeue", 0
	}

	if o.result.RequeueAfter > 0 {
		return "requeueAfter", o.result.RequeueAfter
	}

	if o.err != nil {
		return "fail", 0
	}

	return "done", 0
}

// =============================================================================
// EnsureFlow and EnsureOutcome
// =============================================================================

// changeState is internal ordering for EnsureOutcome merge semantics.
type changeState uint8

const (
	unchangedState changeState = iota
	changedState
	changedAndOptimisticLockRequiredState
)

// EnsureFlow is a phase scope for ensure helpers.
//
// Ensure helpers typically mutate an object in-memory (one patch domain) and must report:
// - whether they changed the object (DidChange),
// - whether the subsequent save should use optimistic locking,
// - and whether they encountered an error.
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

	ctx = storePhaseContext(ctx, l, phaseName, kv)
	return EnsureFlow{ctx: ctx, log: l}
}

// OnEnd is the deferred “phase end handler” for ensure helpers.
//
// What it does:
// - logs `phase end` with `changed`, `optimisticLock`, `hasError`, and duration,
// - if the phase panics, logs `phase panic` and re-panics.
func (ef EnsureFlow) OnEnd(out *EnsureOutcome) {
	if r := recover(); r != nil {
		err := panicToError(r)
		ef.log.Error(err, "phase panic")
		panic(r)
	}

	v, ok := getPhaseContext(ef.ctx)
	if !ok {
		return
	}

	if out == nil {
		panic("flow: EnsureFlow.OnEnd: outcome is nil")
	}

	fields := []any{
		"changed", out.DidChange(),
		"optimisticLock", out.OptimisticLockRequired(),
		"hasError", out.err != nil,
	}
	if !v.start.IsZero() {
		fields = append(fields, "duration", time.Since(v.start))
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
// - DidChange(): whether the helper mutated the object,
// - OptimisticLockRequired(): whether the subsequent save should use optimistic locking.
//
// Typical pattern:
//
//	changed := false
//	// mutate obj; set changed=true if needed
//	return ef.Ok().ReportChangedIf(changed).RequireOptimisticLock()
type EnsureOutcome struct {
	err            error
	changeState    changeState
	changeReported bool
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
	o.changeReported = true
	if o.changeState == unchangedState {
		o.changeState = changedState
	}
	return o
}

// ReportChangedIf is like ReportChanged, but records a change only when cond is true.
//
// Call this even for “no change” paths to make subsequent use of RequireOptimisticLock explicit and safe:
//
//	return ef.Ok().ReportChangedIf(changed).RequireOptimisticLock()
func (o EnsureOutcome) ReportChangedIf(cond bool) EnsureOutcome {
	o.changeReported = true
	if cond && o.changeState == unchangedState {
		o.changeState = changedState
	}
	return o
}

// DidChange reports whether the outcome records a change.
func (o EnsureOutcome) DidChange() bool { return o.changeState >= changedState }

// RequireOptimisticLock returns a copy of EnsureOutcome that requires optimistic locking.
//
// Contract: it must be called only after ReportChanged/ReportChangedIf; otherwise it panics
// (this is a guard against forgetting change reporting in ensure helpers).
func (o EnsureOutcome) RequireOptimisticLock() EnsureOutcome {
	if !o.changeReported {
		panic("flow: EnsureOutcome.RequireOptimisticLock called before ReportChanged/ReportChangedIf")
	}
	if o.changeState == changedState {
		o.changeState = changedAndOptimisticLockRequiredState
	}
	return o
}

// OptimisticLockRequired reports whether the outcome requires optimistic locking.
func (o EnsureOutcome) OptimisticLockRequired() bool {
	return o.changeState >= changedAndOptimisticLockRequiredState
}

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
// - Change/lock intent is merged deterministically (strongest wins).
func MergeEnsures(outcomes ...EnsureOutcome) EnsureOutcome {
	if len(outcomes) == 0 {
		return EnsureOutcome{}
	}

	var (
		errs              []error
		maxChangeState    changeState
		anyChangeReported bool
	)

	for _, o := range outcomes {
		if o.err != nil {
			errs = append(errs, o.err)
		}

		anyChangeReported = anyChangeReported || o.changeReported

		if o.changeState > maxChangeState {
			maxChangeState = o.changeState
		}
	}

	return EnsureOutcome{
		err:            errors.Join(errs...),
		changeState:    maxChangeState,
		changeReported: anyChangeReported,
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

	ctx = storePhaseContext(ctx, l, phaseName, kv)
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

	v, ok := getPhaseContext(sf.ctx)
	if !ok {
		return
	}

	if err == nil {
		panic("flow: StepFlow.OnEnd: err is nil")
	}

	fields := []any{
		"hasError", *err != nil,
	}
	if !v.start.IsZero() {
		fields = append(fields, "duration", time.Since(v.start))
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
