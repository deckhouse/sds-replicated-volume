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
func (o Outcome) DidChange() bool { return o.changeState >= changedState }

// OptimisticLockRequired reports whether saving the reported change must use optimistic lock semantics
// (e.g. Patch/Update with a resourceVersion precondition).
func (o Outcome) OptimisticLockRequired() bool {
	return o.changeState >= changedAndOptimisticLockRequiredState
}

// Error returns the error carried by the outcome, if any.
func (o Outcome) Error() error { return o.err }

// Errorf returns a copy of Outcome with its error updated by formatted context.
//
// If Outcome already carries an error, Errorf wraps it (like Wrapf).
// If Outcome has no error, Errorf is a no-op and keeps the error nil.
func (o Outcome) Errorf(format string, args ...any) Outcome {
	if o.err == nil {
		return o
	}
	o.err = Wrapf(o.err, format, args...)
	return o
}

// ReportChanged returns a copy of Outcome that records a change to the target object.
// It does not alter the reconcile return decision (continue/done/requeue) or the error.
func (o Outcome) ReportChanged() Outcome {
	o.changeReported = true
	if o.changeState == unchangedState {
		o.changeState = changedState
	}
	return o
}

// ReportChangedIf is like ReportChanged, but it records a change only when cond is true.
// It does not alter the reconcile return decision (continue/done/requeue) or the error.
func (o Outcome) ReportChangedIf(cond bool) Outcome {
	o.changeReported = true
	if cond && o.changeState == unchangedState {
		o.changeState = changedState
	}
	return o
}

// RequireOptimisticLock returns a copy of Outcome upgraded to require optimistic locking for patching.
//
// Contract: it must be called only after a change has been reported via ReportChanged/ReportChangedIf;
// otherwise it panics (developer error).
func (o Outcome) RequireOptimisticLock() Outcome {
	if !o.changeReported {
		panic("flow.Outcome: RequireOptimisticLock called before ReportChanged/ReportChangedIf")
	}
	if o.changeState == changedState {
		o.changeState = changedAndOptimisticLockRequiredState
	}
	return o
}

// ShouldReturn reports whether the Outcome indicates an early return from Reconcile.
func (o Outcome) ShouldReturn() bool { return o.result != nil }

// ToCtrl unwraps Outcome into the controller-runtime Reconcile return values.
//
// If result is nil, it returns an empty ctrl.Result and o.err.
func (o Outcome) ToCtrl() (ctrl.Result, error) {
	if o.result == nil {
		return ctrl.Result{}, o.err
	}
	return *o.result, o.err
}

func (o Outcome) MustToCtrl() (ctrl.Result, error) {
	if o.result == nil {
		panic("flow.Outcome: MustToCtrl called with nil result")
	}
	return *o.result, o.err
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
func BeginPhase(ctx context.Context, phaseName string, keysAndValues ...any) (context.Context, logr.Logger) {
	mustBeValidPhaseName(phaseName)
	l := log.FromContext(ctx).WithName(phaseName)
	if len(keysAndValues) > 0 {
		l = l.WithValues(keysAndValues...)
	}
	ctx = log.IntoContext(ctx, l)
	return ctx, l
}

// Continue indicates that the caller should keep executing the current reconciliation flow.
func Continue() Outcome { return Outcome{} }

// ContinueErr indicates that the caller should keep executing the current reconciliation flow,
// while still returning an error value from the current sub-step (without setting Return).
//
// Typical use: bubble an error to a higher-level handler without selecting a stop/requeue decision.
func ContinueErr(e error) Outcome {
	if e == nil {
		return Continue()
	}
	return Outcome{err: e}
}

// ContinueErrf is like ContinueErr, but wraps err using Wrapf(format, args...).
func ContinueErrf(err error, format string, args ...any) Outcome {
	return ContinueErr(Wrapf(err, format, args...))
}

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
//   - The decision is chosen by priority:
//     1) Fail: if there are errors and at least one non-nil Return.
//     2) RequeueAfter: if there are no errors and at least one Outcome requests RequeueAfter (the smallest wins).
//     3) Done: if there are no errors, no RequeueAfter requests, and at least one non-nil Return.
//     4) Continue: otherwise (Return is nil). If errors were present, Err may be non-nil.
func Merge(results ...Outcome) Outcome {
	if len(results) == 0 {
		return Outcome{}
	}

	var (
		hasReconcileResult bool
		shouldRequeueAfter bool
		requeueAfter       time.Duration
		errs               []error
		maxChangeState     changeState
		anyChangeReported  bool
	)

	for _, r := range results {
		if r.err != nil {
			errs = append(errs, r.err)
		}

		anyChangeReported = anyChangeReported || r.changeReported

		if r.changeState > maxChangeState {
			maxChangeState = r.changeState
		}

		if r.result == nil {
			continue
		}
		hasReconcileResult = true

		if r.result.Requeue {
			panic("flow.Merge: Requeue=true is not supported")
		}

		if r.result.RequeueAfter > 0 {
			if !shouldRequeueAfter || r.result.RequeueAfter < requeueAfter {
				shouldRequeueAfter = true
				requeueAfter = r.result.RequeueAfter
			}
		}
	}

	combinedErr := errors.Join(errs...)

	// 1) Fail: if there are errors and at least one non-nil Return.
	if combinedErr != nil && hasReconcileResult {
		out := Fail(combinedErr)
		out.changeState = maxChangeState
		out.changeReported = anyChangeReported
		return out
	}

	// 2) RequeueAfter: if there are no errors and at least one Outcome requests RequeueAfter.
	if combinedErr == nil && shouldRequeueAfter {
		out := RequeueAfter(requeueAfter)
		out.changeState = maxChangeState
		out.changeReported = anyChangeReported
		return out
	}

	// 3) Done: if there are no errors, no RequeueAfter requests, and at least one non-nil Return.
	if combinedErr == nil && hasReconcileResult {
		out := Done()
		out.changeState = maxChangeState
		out.changeReported = anyChangeReported
		return out
	}

	// 4) Continue: otherwise. If errors were present, Err may be non-nil.
	if combinedErr != nil {
		out := ContinueErr(combinedErr)
		out.changeState = maxChangeState
		out.changeReported = anyChangeReported
		return out
	}
	out := Continue()
	out.changeState = maxChangeState
	out.changeReported = anyChangeReported
	return out
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
