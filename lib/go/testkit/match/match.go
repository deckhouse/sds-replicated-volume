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

// Package match provides composable Gomega-compatible matchers for
// Kubernetes objects. All matchers implement types.GomegaMatcher and
// work with Gomega's And/Or/Not combinators and Expect().To() syntax.
//
// This package is designed for dot-import in test files alongside
// Gomega and Ginkgo:
//
//	import (
//	    . "github.com/onsi/ginkgo/v2"
//	    . "github.com/onsi/gomega"
//	    . "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
//	)
package match

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/onsi/gomega/types"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Phased is implemented by Kubernetes objects that expose a status phase string.
type Phased interface {
	GetStatusPhase() string
}

// Conditioned is implemented by Kubernetes objects that expose status conditions.
type Conditioned interface {
	GetStatusConditions() []metav1.Condition
}

// ---------------------------------------------------------------------------
// Core matcher type (unexported, implements types.GomegaMatcher)
// ---------------------------------------------------------------------------

// matcher is the internal implementation of types.GomegaMatcher.
// All factory functions in this package return *matcher.
type matcher struct {
	fn func(client.Object) (matched bool, detail string)
}

func (m *matcher) Match(actual any) (bool, error) {
	obj, ok := actual.(client.Object)
	if !ok {
		return false, fmt.Errorf("match: expected client.Object, got %T", actual)
	}
	matched, _ := m.fn(obj)
	return matched, nil
}

func (m *matcher) FailureMessage(actual any) string {
	obj, ok := actual.(client.Object)
	if !ok {
		return fmt.Sprintf("match: expected client.Object, got %T", actual)
	}
	_, detail := m.fn(obj)
	return fmt.Sprintf("%s: %s", obj.GetName(), detail)
}

func (m *matcher) NegatedFailureMessage(actual any) string {
	obj, ok := actual.(client.Object)
	if !ok {
		return fmt.Sprintf("match: expected client.Object, got %T", actual)
	}
	_, detail := m.fn(obj)
	return fmt.Sprintf("%s: expected NOT to match, but matched: %s", obj.GetName(), detail)
}

// MatchObject runs the matcher against a typed client.Object and
// returns both the match result and a detail string. This is used by
// the framework for programmatic checks (sequences, continuous checks)
// where the full detail is needed — not just pass/fail.
func (m *matcher) MatchObject(obj client.Object) (bool, string) {
	return m.fn(obj)
}

// NewMatcher creates a GomegaMatcher from a function that checks a client.Object.
func NewMatcher(fn func(client.Object) (bool, string)) types.GomegaMatcher {
	return &matcher{fn: fn}
}

// ---------------------------------------------------------------------------
// ObjectMatcher — for programmatic use by framework
// ---------------------------------------------------------------------------

// ObjectMatcher is implemented by matchers that can run against a
// typed client.Object and return both match result and detail string.
// The framework uses this for programmatic checks where the detail
// is needed for violation reporting.
type ObjectMatcher interface {
	MatchObject(obj client.Object) (matched bool, detail string)
}

// MatchObject runs a GomegaMatcher against a client.Object. If the
// matcher implements ObjectMatcher, the typed method is used. Otherwise,
// falls back to the GomegaMatcher interface.
func MatchObject(m types.GomegaMatcher, obj client.Object) (bool, string) {
	if om, ok := m.(ObjectMatcher); ok {
		return om.MatchObject(obj)
	}
	matched, err := m.Match(obj)
	if err != nil {
		return false, err.Error()
	}
	if matched {
		return true, ""
	}
	return false, m.FailureMessage(obj)
}

// ---------------------------------------------------------------------------
// Phase matchers (generic, via Phased interface)
// ---------------------------------------------------------------------------

func extractPhase(obj client.Object) string {
	if p, ok := obj.(Phased); ok {
		return p.GetStatusPhase()
	}
	return ""
}

// Phase matches when .status.phase equals the expected value.
func Phase(expected string) types.GomegaMatcher {
	return NewMatcher(func(obj client.Object) (bool, string) {
		actual := extractPhase(obj)
		if actual == expected {
			return true, fmt.Sprintf("phase is %s", actual)
		}
		return false, fmt.Sprintf("phase is %s, expected %s", actual, expected)
	})
}

// PhaseNot matches when .status.phase does not equal the excluded value.
func PhaseNot(excluded string) types.GomegaMatcher {
	return NewMatcher(func(obj client.Object) (bool, string) {
		actual := extractPhase(obj)
		if actual != excluded {
			return true, fmt.Sprintf("phase is %s (not %s)", actual, excluded)
		}
		return false, fmt.Sprintf("phase is %s", actual)
	})
}

// AnyPhase matches when .status.phase is one of the given values.
func AnyPhase(phases ...string) types.GomegaMatcher {
	set := make(map[string]bool, len(phases))
	for _, p := range phases {
		set[p] = true
	}
	return NewMatcher(func(obj client.Object) (bool, string) {
		actual := extractPhase(obj)
		if set[actual] {
			return true, fmt.Sprintf("phase is %s", actual)
		}
		return false, fmt.Sprintf("phase is %s, expected one of [%s]", actual, strings.Join(phases, ", "))
	})
}

// ---------------------------------------------------------------------------
// Condition matchers (generic, via Conditioned interface)
// ---------------------------------------------------------------------------

func getCondition(obj client.Object, name string) (found bool, status, reason, message string) {
	sco, ok := obj.(Conditioned)
	if !ok {
		panic(fmt.Sprintf("match: object %T does not implement Conditioned (GetStatusConditions)", obj))
	}
	cond := meta.FindStatusCondition(sco.GetStatusConditions(), name)
	if cond == nil {
		return false, "", "", ""
	}
	return true, string(cond.Status), cond.Reason, cond.Message
}

// HasCondition matches when the named condition exists.
func HasCondition(name string) types.GomegaMatcher {
	return NewMatcher(func(obj client.Object) (bool, string) {
		found, _, _, _ := getCondition(obj, name)
		if found {
			return true, fmt.Sprintf("condition %s exists", name)
		}
		return false, fmt.Sprintf("condition %s not found", name)
	})
}

// NoCondition matches when the named condition does not exist.
func NoCondition(name string) types.GomegaMatcher {
	return NewMatcher(func(obj client.Object) (bool, string) {
		found, _, _, _ := getCondition(obj, name)
		if !found {
			return true, fmt.Sprintf("condition %s absent", name)
		}
		return false, fmt.Sprintf("condition %s exists", name)
	})
}

// ConditionStatus matches when the named condition has the given status.
func ConditionStatus(name, status string) types.GomegaMatcher {
	return NewMatcher(func(obj client.Object) (bool, string) {
		found, actual, _, _ := getCondition(obj, name)
		if !found {
			return false, fmt.Sprintf("condition %s not found", name)
		}
		if actual == status {
			return true, fmt.Sprintf("condition %s status is %s", name, actual)
		}
		return false, fmt.Sprintf("condition %s status is %s, expected %s", name, actual, status)
	})
}

// ConditionReason matches when the named condition has the given reason.
func ConditionReason(name, reason string) types.GomegaMatcher {
	return NewMatcher(func(obj client.Object) (bool, string) {
		found, _, actual, _ := getCondition(obj, name)
		if !found {
			return false, fmt.Sprintf("condition %s not found", name)
		}
		if actual == reason {
			return true, fmt.Sprintf("condition %s reason is %s", name, actual)
		}
		return false, fmt.Sprintf("condition %s reason is %s, expected %s", name, actual, reason)
	})
}

// ConditionMessageContains matches when the named condition's message
// contains the given substring.
func ConditionMessageContains(name, substr string) types.GomegaMatcher {
	return NewMatcher(func(obj client.Object) (bool, string) {
		found, _, _, msg := getCondition(obj, name)
		if !found {
			return false, fmt.Sprintf("condition %s not found", name)
		}
		if strings.Contains(msg, substr) {
			return true, fmt.Sprintf("condition %s message contains %q", name, substr)
		}
		return false, fmt.Sprintf("condition %s message %q does not contain %q", name, msg, substr)
	})
}

// ---------------------------------------------------------------------------
// Metadata matchers (generic, via client.Object interface)
// ---------------------------------------------------------------------------

// HasFinalizer matches when the object has the named finalizer.
func HasFinalizer(name string) types.GomegaMatcher {
	return NewMatcher(func(obj client.Object) (bool, string) {
		for _, f := range obj.GetFinalizers() {
			if f == name {
				return true, fmt.Sprintf("finalizer %s present", name)
			}
		}
		return false, fmt.Sprintf("finalizer %s not found", name)
	})
}

// HasLabel matches when the object has a label with the given key.
func HasLabel(key string) types.GomegaMatcher {
	return NewMatcher(func(obj client.Object) (bool, string) {
		if _, ok := obj.GetLabels()[key]; ok {
			return true, fmt.Sprintf("label %s present", key)
		}
		return false, fmt.Sprintf("label %s not found", key)
	})
}

// LabelEquals matches when the object has a label with the given key and value.
func LabelEquals(key, value string) types.GomegaMatcher {
	return NewMatcher(func(obj client.Object) (bool, string) {
		actual, ok := obj.GetLabels()[key]
		if !ok {
			return false, fmt.Sprintf("label %s not found", key)
		}
		if actual == value {
			return true, fmt.Sprintf("label %s=%s", key, actual)
		}
		return false, fmt.Sprintf("label %s=%s, expected %s", key, actual, value)
	})
}

// ---------------------------------------------------------------------------
// ResourceVersion matchers
// ---------------------------------------------------------------------------

func mustParseRV(rv string) int64 {
	n, err := strconv.ParseInt(rv, 10, 64)
	if err != nil {
		panic(fmt.Sprintf("mustParseRV: invalid resourceVersion %q: %v", rv, err))
	}
	return n
}

// ResourceVersionAtLeast matches when the object's resourceVersion is
// numerically >= the given value.
func ResourceVersionAtLeast(rv string) types.GomegaMatcher {
	target := mustParseRV(rv)
	return NewMatcher(func(obj client.Object) (bool, string) {
		current := mustParseRV(obj.GetResourceVersion())
		if current >= target {
			return true, fmt.Sprintf("resourceVersion %d >= %d", current, target)
		}
		return false, fmt.Sprintf("resourceVersion %d < %d (target %d)", current, target, target)
	})
}

// IsDeleting matches when the object has a deletionTimestamp set.
func IsDeleting() types.GomegaMatcher {
	return NewMatcher(func(obj client.Object) (bool, string) {
		if obj.GetDeletionTimestamp() != nil {
			return true, "deletionTimestamp is set"
		}
		return false, "deletionTimestamp is not set"
	})
}

// ---------------------------------------------------------------------------
// Deleted — sequence-only marker for Delete events
// ---------------------------------------------------------------------------

type deletedEventMatcher struct{}

func (m *deletedEventMatcher) Match(_ interface{}) (bool, error) {
	return false, nil
}

func (m *deletedEventMatcher) FailureMessage(_ interface{}) string {
	return "expected Deleted event"
}

func (m *deletedEventMatcher) NegatedFailureMessage(_ interface{}) string {
	return "did not expect Deleted event"
}

// Deleted returns a lifecycle marker matcher that matches Delete events.
// In Await: succeeds when the object is deleted; does not fail-fast.
// In sequences: marks a deletion step; may be followed by PresentAgain
// for reincarnation.
func Deleted() types.GomegaMatcher { return &deletedEventMatcher{} }

// IsDeletedMatcher reports whether the given matcher is a Deleted() marker.
func IsDeletedMatcher(m types.GomegaMatcher) bool {
	_, ok := m.(*deletedEventMatcher)
	return ok
}

// ---------------------------------------------------------------------------
// Present / PresentAgain — lifecycle markers for object existence
// ---------------------------------------------------------------------------

type presentMatcher struct{}

func (m *presentMatcher) Match(_ interface{}) (bool, error)   { return false, nil }
func (m *presentMatcher) FailureMessage(_ interface{}) string { return "expected object to be present" }
func (m *presentMatcher) NegatedFailureMessage(_ interface{}) string {
	return "did not expect object to be present"
}

// Present returns a lifecycle marker matcher that matches when the object
// is alive (not deleted). In Await: fail-fast if the object is deleted.
// In sequences: matches any non-Delete event.
func Present() types.GomegaMatcher { return &presentMatcher{} }

// IsPresentMatcher reports whether the given matcher is a Present() marker.
func IsPresentMatcher(m types.GomegaMatcher) bool {
	_, ok := m.(*presentMatcher)
	return ok
}

type presentAgainMatcher struct{}

func (m *presentAgainMatcher) Match(_ interface{}) (bool, error) { return false, nil }
func (m *presentAgainMatcher) FailureMessage(_ interface{}) string {
	return "expected object to be present again"
}
func (m *presentAgainMatcher) NegatedFailureMessage(_ interface{}) string {
	return "did not expect object to be present again"
}

// PresentAgain returns a lifecycle marker matcher that matches when the
// object is alive after reincarnation. In Await: tolerates the deleted
// state (does not fail-fast). In sequences: must be preceded by Deleted().
func PresentAgain() types.GomegaMatcher { return &presentAgainMatcher{} }

// IsPresentAgainMatcher reports whether the given matcher is a PresentAgain() marker.
func IsPresentAgainMatcher(m types.GomegaMatcher) bool {
	_, ok := m.(*presentAgainMatcher)
	return ok
}

// IsLifecycleMatcher reports whether the given matcher is any lifecycle
// marker (Deleted, Present, or PresentAgain).
func IsLifecycleMatcher(m types.GomegaMatcher) bool {
	return IsDeletedMatcher(m) || IsPresentMatcher(m) || IsPresentAgainMatcher(m)
}

// ---------------------------------------------------------------------------
// Switch — GomegaMatcher wrapper with Disable/Enable
// ---------------------------------------------------------------------------

// Switch wraps a GomegaMatcher and allows disabling/enabling it at
// runtime. When disabled, Match always returns (true, nil) — the
// check passes silently. Thread-safe.
//
// Switch is the mechanism for suspending continuous checks. A single
// Switch instance can be shared across multiple TrackedObjects (e.g. via
// TrackerGroup.OnEach). Disabling it suspends the check on all objects
// that use it.
type Switch struct {
	mu       sync.RWMutex
	disabled bool
	inner    types.GomegaMatcher
}

// NewSwitch creates an enabled Switch wrapping the given matcher.
func NewSwitch(m types.GomegaMatcher) *Switch {
	return &Switch{inner: m}
}

// Disable turns off the switch. Match will always return (true, nil).
func (s *Switch) Disable() {
	s.mu.Lock()
	s.disabled = true
	s.mu.Unlock()
}

// Enable turns on the switch. Match delegates to the inner matcher.
func (s *Switch) Enable() {
	s.mu.Lock()
	s.disabled = false
	s.mu.Unlock()
}

// IsDisabled reports whether the switch is currently disabled.
func (s *Switch) IsDisabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.disabled
}

// Match implements types.GomegaMatcher. When disabled, always passes.
func (s *Switch) Match(actual any) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.disabled {
		return true, nil
	}
	return s.inner.Match(actual)
}

// FailureMessage implements types.GomegaMatcher.
func (s *Switch) FailureMessage(actual any) string {
	return s.inner.FailureMessage(actual)
}

// NegatedFailureMessage implements types.GomegaMatcher.
func (s *Switch) NegatedFailureMessage(actual any) string {
	return s.inner.NegatedFailureMessage(actual)
}

// WithDisabled disables the switch for the duration of fn, then
// re-enables it. Scope-based — cannot forget to re-enable.
func WithDisabled(sw *Switch, fn func()) {
	sw.Disable()
	defer sw.Enable()
	fn()
}
