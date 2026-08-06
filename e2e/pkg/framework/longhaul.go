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

package framework

import (
	"math"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/types"
)

// registerLongHaulTransformer registers a NodeArgsTransformer that detects the
// LongHaul label and auto-injects the HIGHEST spec priority, so a parallel run
// hands the spec to a worker first and its long wait overlaps with the rest of
// the suite instead of extending it.
//
// Two deliberate differences from the Disruptive transformer:
//
//   - Serial is NOT injected. A LongHaul spec spends its budget waiting for the
//     cluster (an alert with `for: 15m` has to become firing before it can be
//     observed at all); serial specs run after every worker has exited, so
//     Serial would move that wait to the very end of the run, where nothing can
//     overlap it. A LongHaul spec that also needs exclusivity says Serial itself.
//   - The priority goes up, not down: Disruptive is deferred to the end because
//     it damages shared state, LongHaul is pulled to the front because it is slow.
//
// A spec carrying BOTH labels keeps the Disruptive placement (Serial + lowest
// priority), no matter whether Disruptive sits on the spec itself or on one of
// its parent containers: Disruptive is a safety class, and two SpecPriority
// decorators would otherwise contradict each other.
func registerLongHaulTransformer() {
	AddTreeConstructionNodeArgsTransformer(
		func(nodeType types.NodeType, _ Offset, text string, args []any) (string, []any, []error) {
			if !nodeType.Is(types.NodeTypesForContainerAndIt) {
				return text, args, nil
			}
			if !longHaulPriorityApplies(args) {
				return text, args, nil
			}
			args = append(args, SpecPriority(math.MaxInt))
			return text, args, nil
		},
	)
}

// longHaulPriorityApplies reports whether the highest spec priority has to be
// injected into the container/It node described by args. It MUST be called
// during tree construction only — it reads the container chain of the node
// currently being built (a top-level node simply has none).
//
// The two labels are read from deliberately different scopes:
//
//   - LongHaul from the node's OWN args. The priority belongs on the node that
//     declares the class; Ginkgo already propagates it to every spec underneath
//     (Nodes.GetSpecPriority walks outwards from the spec), and re-injecting it
//     on descendants would stack a second SpecPriority decorator on top of one
//     the author wrote by hand.
//   - Disruptive from the labels IN SCOPE for the node — its own plus the ones
//     inherited from parent containers. Disruptive on a Describe holding a
//     LongHaul It is the realistic shape (that is how it is written across this
//     suite), and Ginkgo resolves a spec's priority from the INNERMOST node that
//     sets one: a MaxInt landing on the It would silently beat the container's
//     MinInt and move a destructive spec to the FRONT of the serial phase,
//     ahead of the specs it is meant to run after.
func longHaulPriorityApplies(args []any) bool {
	// Fast path: collectNodeLabels walks the whole container chain, so it is
	// only worth paying for on a node that declares LongHaul at all.
	if !hasLongHaulLabel(args) {
		return false
	}
	return !hasLabel(collectNodeLabels(args), LabelDisruptive)
}

// hasLongHaulLabel reports whether args contain a Labels value with the
// LongHaul entry.
func hasLongHaulLabel(args []any) bool {
	return hasLabelInArgs(args, LabelLongHaul)
}

// enforceLongHaul skips the current spec when it carries the LongHaul label and
// the class is not enabled: LongHaul runs only when E2E_ALLOW_LONG_HAUL=true or
// E2E_RUN_ALL=true. Both values are parsed as booleans (strconv.ParseBool), so
// false, an unrecognized value and an unset variable all keep the class skipped.
//
// A focused run (--focus / --focus-file) bypasses the gate: naming the spec is
// an explicit request to run it. The bypass is granted to LongHaul only —
// LongHaul costs time, Disruptive costs cluster state.
//
// Like every gate in this suite it is a runtime Skip carrying the instruction to
// switch it on, so an unrun spec is reported as Skipped rather than passing
// vacuously.
func enforceLongHaul() {
	if !slices.Contains(CurrentSpecReport().Labels(), LabelLongHaul) {
		return
	}
	if optInEnabledFromEnv(EnvAllowLongHaul) {
		return
	}
	suiteConfig, _ := GinkgoConfiguration()
	if focusRequested(suiteConfig.FocusStrings, suiteConfig.FocusFiles) {
		return
	}
	Skip("LongHaul spec: export " + EnvAllowLongHaul + "=true (or " + EnvRunAll +
		"=true) before the run — this spec waits tens of minutes for the cluster")
}
