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
// Scope adapters (Global → Replica)
//
// Wrap global-scoped callbacks for use in replica-scoped plans.
// The replica context is ignored.

// AdaptGlobalApply wraps a GlobalApplyFunc for use as a ReplicaApplyFunc.
func AdaptGlobalApply[G any, R ReplicaCtx](fn GlobalApplyFunc[G]) ReplicaApplyFunc[G, R] {
	return func(gctx G, _ R) bool {
		return fn(gctx)
	}
}

// AdaptGlobalConfirm wraps a GlobalConfirmFunc for use as a ReplicaConfirmFunc.
func AdaptGlobalConfirm[G any, R ReplicaCtx](fn GlobalConfirmFunc[G]) ReplicaConfirmFunc[G, R] {
	return func(gctx G, _ R, stepRevision int64) ConfirmResult {
		return fn(gctx, stepRevision)
	}
}

// AdaptGlobalOnComplete wraps a GlobalOnCompleteFunc for use as a ReplicaOnCompleteFunc.
func AdaptGlobalOnComplete[G any, R ReplicaCtx](fn GlobalOnCompleteFunc[G]) ReplicaOnCompleteFunc[G, R] {
	return func(gctx G, _ R) {
		fn(gctx)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Cross-category adapter

// AdaptApplyToOnComplete wraps a GlobalApplyFunc (returns bool) for use
// as GlobalOnCompleteFunc (returns nothing). The bool return is discarded.
// OnComplete does not need change tracking — it runs after confirmation.
func AdaptApplyToOnComplete[G any](fn GlobalApplyFunc[G]) GlobalOnCompleteFunc[G] {
	return func(gctx G) { fn(gctx) }
}

// ──────────────────────────────────────────────────────────────────────────────
// Combinators

// ComposeGlobalApply combines multiple GlobalApplyFunc into one.
// Returns true if any sub-callback changed state (OR of all results).
func ComposeGlobalApply[G any](fns ...GlobalApplyFunc[G]) GlobalApplyFunc[G] {
	return func(gctx G) bool {
		changed := false
		for _, fn := range fns {
			changed = fn(gctx) || changed
		}
		return changed
	}
}

// ComposeReplicaApply combines multiple ReplicaApplyFunc into one.
// Returns true if any sub-callback changed state (OR of all results).
func ComposeReplicaApply[G any, R ReplicaCtx](fns ...ReplicaApplyFunc[G, R]) ReplicaApplyFunc[G, R] {
	return func(gctx G, rctx R) bool {
		changed := false
		for _, fn := range fns {
			changed = fn(gctx, rctx) || changed
		}
		return changed
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Utility

// SetChanged assigns val to *dst and returns true if the value changed.
// Generic helper for apply callbacks that need change detection.
func SetChanged[T comparable](dst *T, val T) bool {
	if *dst == val {
		return false
	}
	*dst = val
	return true
}
