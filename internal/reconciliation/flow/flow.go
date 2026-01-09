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

// -----------------------------------------------------------------------------
// Common types & helpers
// -----------------------------------------------------------------------------

// Wrapf wraps err with formatted context.
//
// It returns nil if err is nil.
func Wrapf(err error, format string, args ...any) error {
	if err == nil {
		return nil
	}
	msg := fmt.Sprintf(format, args...)
	return fmt.Errorf("%s: %w", msg, err)
}

// Outcome bundles a reconcile return decision and an optional error.
//
// If the outcome does not request a controller-runtime return decision, the caller should continue
// executing the current reconciliation flow (i.e. do not return from Reconcile yet).
//
// Outcome may also carry metadata about whether function modified the target object and whether
// the save operation (if any) should use optimistic lock semantics (e.g. Patch/Update with a
// resourceVersion precondition).
type Outcome struct {
	result      *ctrl.Result
	err         error
	changeState changeState

	// changeReported is a developer-safety flag used to validate correct Outcome usage.
	// It is not a semantic part of the reconcile result; it exists only to enforce the contract
	// between helpers (RequireOptimisticLock must be used only after ReportChanged/ReportChangedIf).
	changeReported bool

	// errorLogged indicates whether the error carried by this Outcome has already been logged.
	// It is used to avoid duplicate logs when the same error bubbles up through multiple phases.
	errorLogged bool
}

// -----------------------------------------------------------------------------
// Phase context
// -----------------------------------------------------------------------------

type phaseContextKey struct{}

type phaseContextValue struct {
	name  string
	kv    []string
	start time.Time
}

func formatKV(kv []string) string {
	if len(kv) == 0 {
		return ""
	}

	// Format as "k1=v1 k2=v2 ..." in the original order.
	out := ""
	for i := 0; i < len(kv); i += 2 {
		if i > 0 {
			out += " "
		}
		out += fmt.Sprintf("%s=%s", kv[i], kv[i+1])
	}
	return out
}

// changeState is an internal encoding for Outcome change tracking.
// Values are ordered by "strength": unchanged < changed < changed+optimistic-lock.
type changeState uint8

const (
	unchangedState changeState = iota
	changedState
	changedAndOptimisticLockRequiredState
)

// DidChange reports whether function modified the target object.
func (outcome Outcome) DidChange() bool { return outcome.changeState >= changedState }

// OptimisticLockRequired reports whether saving the reported change must use optimistic lock semantics
// (e.g. Patch/Update with a resourceVersion precondition).
func (outcome Outcome) OptimisticLockRequired() bool {
	return outcome.changeState >= changedAndOptimisticLockRequiredState
}

// Error returns the error carried by the outcome, if any.
func (outcome Outcome) Error() error { return outcome.err }

// ErrorLogged reports whether the error carried by this Outcome has already been logged.
func (outcome Outcome) ErrorLogged() bool { return outcome.errorLogged }

// Enrichf returns a copy of Outcome with its error updated by formatted context.
//
// If Outcome already carries an error, Enrichf wraps it (like Wrapf for errors).
// If Outcome has no error, Enrichf is a no-op and keeps the error nil.
func (outcome Outcome) Enrichf(format string, args ...any) Outcome {
	if outcome.err == nil {
		return outcome
	}
	outcome.err = Wrapf(outcome.err, format, args...)
	return outcome
}

// ReportChanged returns a copy of Outcome that records a change to the target object.
// It does not alter the reconcile return decision (continue/done/requeue) or the error.
func (outcome Outcome) ReportChanged() Outcome {
	outcome.changeReported = true
	if outcome.changeState == unchangedState {
		outcome.changeState = changedState
	}
	return outcome
}

// ReportChangedIf is like ReportChanged, but it records a change only when cond is true.
// It does not alter the reconcile return decision (continue/done/requeue) or the error.
func (outcome Outcome) ReportChangedIf(cond bool) Outcome {
	outcome.changeReported = true
	if cond && outcome.changeState == unchangedState {
		outcome.changeState = changedState
	}
	return outcome
}

// RequireOptimisticLock returns a copy of Outcome upgraded to require optimistic locking for patching.
//
// Contract: it must be called only after a change has been reported via ReportChanged/ReportChangedIf;
// otherwise it panics (developer error).
func (outcome Outcome) RequireOptimisticLock() Outcome {
	if !outcome.changeReported {
		panic("flow.Outcome: RequireOptimisticLock called before ReportChanged/ReportChangedIf")
	}
	if outcome.changeState == changedState {
		outcome.changeState = changedAndOptimisticLockRequiredState
	}
	return outcome
}

// ShouldReturn reports whether the Outcome indicates an early return from Reconcile.
func (outcome Outcome) ShouldReturn() bool { return outcome.result != nil }

// ToCtrl unwraps Outcome into the controller-runtime Reconcile return values.
//
// If result is nil, it returns an empty ctrl.Result and o.err.
func (outcome Outcome) ToCtrl() (ctrl.Result, error) {
	if outcome.result == nil {
		return ctrl.Result{}, outcome.err
	}
	return *outcome.result, outcome.err
}

func (outcome Outcome) MustToCtrl() (ctrl.Result, error) {
	if outcome.result == nil {
		panic("flow.Outcome: MustToCtrl called with nil result")
	}
	return *outcome.result, outcome.err
}

// Merge combines this Outcome with one or more additional Outcome values.
//
// It is a convenience wrapper around the package-level Merge(o, ...).
func (outcome Outcome) Merge(outcomes ...Outcome) Outcome {
	return Merge(append([]Outcome{outcome}, outcomes...)...)
}

// -----------------------------------------------------------------------------
// Main reconcile helpers (top-level Reconcile)
// -----------------------------------------------------------------------------

// Begin starts the root phase of reconciliation.
// It returns ctx and the logger stored in it (or the default logger if ctx has none).
func Begin(ctx context.Context) (context.Context, logr.Logger) {
	l := log.FromContext(ctx)
	return ctx, l
}

// -----------------------------------------------------------------------------
// Subreconcile helpers (phases)
// -----------------------------------------------------------------------------

// BeginPhase starts a regular (non-root) reconciliation phase.
// It returns ctx updated with the phase logger, and the same logger value.
//
// phaseName is validated and this function panics on invalid values (developer error).
func BeginPhase(ctx context.Context, phaseName string, kv ...string) (context.Context, logr.Logger) {
	mustBeValidPhaseName(phaseName)
	if len(kv)%2 != 0 {
		panic("flow.BeginPhase: kv must contain even number of elements (key/value pairs)")
	}

	l := log.FromContext(ctx).WithName(phaseName)
	if len(kv) > 0 {
		anyKV := make([]any, 0, len(kv))
		for _, v := range kv {
			anyKV = append(anyKV, v)
		}
		l = l.WithValues(anyKV...)
	}

	// V(1) begin log (logger is already phase-scoped: name + values).
	l.V(1).Info("phase start")

	ctx = log.IntoContext(ctx, l)

	// Save phase metadata for downstream consumers (e.g., tests/diagnostics, error wrapping).
	//
	// Important: we intentionally do NOT inherit phase name nor kv from the parent phase.
	// Rationale:
	//  1) For logging: we already log via the phase-scoped logger `l` (name + WithValues), so all
	//     necessary phase identity/keys are present in the log entry without duplicating parent data.
	//  2) For error propagation: when this phase returns an error to the parent, the parent already has
	//     its own phase context, so there is no need to copy parent phase metadata into the child and
	//     then re-wrap it back when bubbling up.
	kvCopy := append([]string(nil), kv...)
	ctx = context.WithValue(ctx, phaseContextKey{}, phaseContextValue{
		name:  phaseName,
		kv:    kvCopy,
		start: time.Now(),
	})

	return ctx, l
}

// EndPhase logs V(1) "phase end" with a short, structured summary of the phase outcome.
//
// Intended usage is via defer right after BeginPhase:
//
//	ctx, _ := flow.BeginPhase(ctx, "somePhase", "key", "value")
//	var outcome flow.Outcome
//	defer flow.EndPhase(ctx, &outcome)
//
// Contract:
//   - outcome must be non-nil (developer error);
//   - ctx should come from BeginPhase (or otherwise carry phase metadata), otherwise EndPhase is a no-op.
//
// Notes:
//   - EndPhase logs the error exactly once (when present and not already logged), and marks the Outcome
//     as logged to avoid duplicates when the error bubbles up through multiple phases.
//   - If a panic happens before the deferred EndPhase runs, EndPhase logs it as an error (including
//     panic details) and then re-panics to preserve upstream handling.
func EndPhase(ctx context.Context, outcome *Outcome) {
	if r := recover(); r != nil {
		err := panicToError(r)
		log.FromContext(ctx).Error(err, "phase panic")
		panic(r)
	}

	l := log.FromContext(ctx)

	v, ok := ctx.Value(phaseContextKey{}).(phaseContextValue)
	if !ok || v.name == "" {
		// Not in a phase: nothing to log.
		return
	}

	if outcome == nil {
		panic("flow.EndPhase: outcome is nil")
	}

	kind, requeueAfter := outcomeKind(outcome)

	fields := []any{
		"result", kind,
		"changed", outcome.DidChange(),
		"optimisticLock", outcome.OptimisticLockRequired(),
		"hasError", outcome.Error() != nil,
	}
	if requeueAfter > 0 {
		fields = append(fields, "requeueAfter", requeueAfter)
	}
	if !v.start.IsZero() {
		fields = append(fields, "duration", time.Since(v.start))
	}

	// Emit exactly one log record per phase end.
	//
	// Behavior:
	//   - no error: log "phase end" only in V(1)
	//   - error present and not yet logged: log "phase end" once (Error for Fail*)
	//   - error present but already logged upstream: log "phase end" only in V(1) to keep error details single-shot
	if outcome.err != nil && !outcome.errorLogged {
		// Any error implies a terminal decision (Fail*). If we ever get here with an unexpected kind,
		// still log the error once (defensive).
		l.Error(outcome.err, "phase end", fields...)
		outcome.errorLogged = true
		return
	}
	l.V(1).Info("phase end", fields...)
}

func outcomeKind(outcome *Outcome) (kind string, requeueAfter time.Duration) {
	if outcome == nil {
		panic("flow.outcomeKind: outcome is nil")
	}

	if outcome.result == nil {
		if outcome.err != nil {
			// Invalid by contract: continue-with-error is forbidden, but keep it visible in logs.
			return "invalid", 0
		}
		return "continue", 0
	}

	if outcome.result.Requeue {
		// This repo intentionally does not use ctrl.Result.Requeue=true.
		return "requeue", 0
	}

	if outcome.result.RequeueAfter > 0 {
		return "requeueAfter", outcome.result.RequeueAfter
	}

	if outcome.err != nil {
		return "fail", 0
	}

	return "done", 0
}

func panicToError(r any) error {
	if err, ok := r.(error); ok {
		return Wrapf(err, "panic")
	}
	return fmt.Errorf("panic: %v", r)
}

// Continue indicates that the caller should keep executing the current reconciliation flow.
func Continue() Outcome { return Outcome{} }

// Done indicates that the caller should stop and return (do not requeue).
func Done() Outcome { return Outcome{result: &ctrl.Result{}} }

// Fail indicates that the caller should stop and return an error.
//
// Controller-runtime will typically requeue on non-nil error.
func Fail(e error) Outcome {
	if e == nil {
		panic("flow.Fail: nil error")
	}
	return Outcome{result: &ctrl.Result{}, err: e}
}

// Failf is like Fail, but wraps err using Wrapf(format, args...).
func Failf(err error, format string, args ...any) Outcome {
	return Fail(Wrapf(err, format, args...))
}

// RequeueAfter indicates that the caller should stop and requeue after the given delay.
func RequeueAfter(dur time.Duration) Outcome {
	if dur <= 0 {
		panic("flow.RequeueAfter: duration must be > 0")
	}
	return Outcome{result: &ctrl.Result{RequeueAfter: dur}}
}

// Merge combines one or more Outcome values into a single Outcome.
//
// Rules:
//   - Errors are joined via errors.Join (nil values are ignored).
//   - Change tracking is aggregated by taking the "strongest" state:
//     if any input reports a change, the merged outcome reports a change too;
//     if any input reports a change and requires an optimistic lock, the merged outcome requires it as well.
//   - "error already logged" signal is aggregated conservatively:
//     it is true only if all merged errors were already logged by their respective boundaries.
//   - The decision is chosen by priority:
//     1) Fail: if there are errors.
//     2) RequeueAfter: if there are no errors and at least one Outcome requests RequeueAfter (the smallest wins).
//     3) Done: if there are no errors, no RequeueAfter requests, and at least one non-nil Return.
//     4) Continue: otherwise (Return is nil).
func Merge(outcomes ...Outcome) Outcome {
	if len(outcomes) == 0 {
		return Outcome{}
	}

	var (
		hasReconcileResult bool
		shouldRequeueAfter bool
		requeueAfter       time.Duration
		errs               []error
		allErrorsLogged    = true
		maxChangeState     changeState
		anyChangeReported  bool
	)

	for _, outcome := range outcomes {
		if outcome.err != nil {
			errs = append(errs, outcome.err)
			allErrorsLogged = allErrorsLogged && outcome.errorLogged
		}

		anyChangeReported = anyChangeReported || outcome.changeReported

		if outcome.changeState > maxChangeState {
			maxChangeState = outcome.changeState
		}

		if outcome.result == nil {
			continue
		}
		hasReconcileResult = true

		if outcome.result.Requeue {
			panic("flow.Merge: Requeue=true is not supported")
		}

		if outcome.result.RequeueAfter > 0 {
			if !shouldRequeueAfter || outcome.result.RequeueAfter < requeueAfter {
				shouldRequeueAfter = true
				requeueAfter = outcome.result.RequeueAfter
			}
		}
	}

	combinedErr := errors.Join(errs...)

	// 1) Fail: if there are errors.
	if combinedErr != nil {
		outcome := Fail(combinedErr)
		outcome.changeState = maxChangeState
		outcome.changeReported = anyChangeReported
		outcome.errorLogged = allErrorsLogged
		return outcome
	}

	// 2) RequeueAfter: if there are no errors and at least one Outcome requests RequeueAfter.
	if shouldRequeueAfter {
		outcome := RequeueAfter(requeueAfter)
		outcome.changeState = maxChangeState
		outcome.changeReported = anyChangeReported
		return outcome
	}

	// 3) Done: if there are no errors, no RequeueAfter requests, and at least one non-nil Return.
	if hasReconcileResult {
		outcome := Done()
		outcome.changeState = maxChangeState
		outcome.changeReported = anyChangeReported
		return outcome
	}

	// 4) Continue: otherwise.
	outcome := Continue()
	outcome.changeState = maxChangeState
	outcome.changeReported = anyChangeReported
	return outcome
}

// mustBeValidPhaseName validates phaseName for logger WithName usage and panics on invalid input.
//
// Rules:
//   - non-empty
//   - segments separated by '/'
//   - no empty segments
//   - only ASCII letters/digits and '._-' within segments
func mustBeValidPhaseName(name string) {
	if name == "" {
		panic("flow.BeginPhase: phaseName must be non-empty")
	}

	segLen := 0
	for i := 0; i < len(name); i++ {
		c := name[i]

		// Disallow whitespace and control chars.
		if c <= ' ' || c == 0x7f {
			panic("flow.BeginPhase: phaseName contains whitespace/control characters: " + name)
		}

		if c == '/' {
			// Empty segments and trailing '/' are not allowed.
			if segLen == 0 {
				panic("flow.BeginPhase: phaseName must not contain empty segments (e.g. leading '//' or trailing '/'): " + name)
			}
			segLen = 0
			continue
		}

		// Recommended: ascii identifiers with separators.
		isLetter := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		isDigit := c >= '0' && c <= '9'
		isAllowedPunct := c == '-' || c == '_' || c == '.'
		if !isLetter && !isDigit && !isAllowedPunct {
			panic("flow.BeginPhase: phaseName contains unsupported character '" + string([]byte{c}) + "': " + name)
		}

		segLen++
	}

	if segLen == 0 {
		panic("flow.BeginPhase: phaseName must not end with '/': " + name)
	}
}
