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

package datamesh

// ──────────────────────────────────────────────────────────────────────────────
// Generic helpers
//

// assign sets *dst = val and returns true if the value changed.
// Generic helper for apply callbacks that need change detection.
func assign[T comparable](dst *T, val T) bool {
	if *dst == val {
		return false
	}
	*dst = val
	return true
}

// ──────────────────────────────────────────────────────────────────────────────
// Apply/OnComplete adapters
//

// composeGlobalApply combines multiple global-scoped apply callbacks into one.
// Returns true if any sub-callback changed state (OR of all results).
func composeGlobalApply(fns ...func(*globalContext) bool) func(*globalContext) bool {
	return func(gctx *globalContext) bool {
		changed := false
		for _, fn := range fns {
			changed = fn(gctx) || changed
		}
		return changed
	}
}

// composeReplicaApply combines multiple apply callbacks into one.
// Returns true if any sub-callback changed state (OR of all results).
func composeReplicaApply(fns ...func(*globalContext, *ReplicaContext) bool) func(*globalContext, *ReplicaContext) bool {
	return func(gctx *globalContext, rctx *ReplicaContext) bool {
		changed := false
		for _, fn := range fns {
			changed = fn(gctx, rctx) || changed
		}
		return changed
	}
}

// asReplicaApply adapts a global-scoped apply callback for use in ReplicaStep.
// Forwards the bool return from the inner function.
func asReplicaApply(fn func(*globalContext) bool) func(*globalContext, *ReplicaContext) bool {
	return func(gctx *globalContext, _ *ReplicaContext) bool {
		return fn(gctx)
	}
}

// asGlobalOnComplete adapts a bool-returning global apply callback for use
// as OnComplete (discards the bool return). OnComplete does not need change
// tracking — it runs after confirmation, not during apply.
func asGlobalOnComplete(fn func(*globalContext) bool) func(*globalContext) {
	return func(gctx *globalContext) { fn(gctx) }
}
