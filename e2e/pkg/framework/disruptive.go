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
	"fmt"
	"math"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/types"
)

// registerDisruptiveTransformer registers a NodeArgsTransformer that
// detects the Disruptive label and auto-injects Serial (run on process #1
// after all other workers exit) and SpecPriority(-1) (run last among
// serial specs).
func registerDisruptiveTransformer() {
	AddTreeConstructionNodeArgsTransformer(
		func(nodeType types.NodeType, _ Offset, text string, args []any) (string, []any, []error) {
			if !nodeType.Is(types.NodeTypesForContainerAndIt) {
				return text, args, nil
			}
			if !hasDisruptiveLabel(args) {
				return text, args, nil
			}
			args = append(args, Serial, SpecPriority(math.MinInt))
			return text, args, nil
		},
	)
}

// hasDisruptiveLabel reports whether args contain a Labels value with
// the Disruptive entry.
func hasDisruptiveLabel(args []any) bool {
	return hasLabelInArgs(args, LabelDisruptive)
}

// enforceDisruptive skips the current spec when it carries the Disruptive label
// and the class is not enabled: Disruptive runs only when
// E2E_ALLOW_DISRUPTIVE=true or E2E_RUN_ALL=true. Both values are parsed as
// booleans (strconv.ParseBool), so false, an unrecognized value and an unset
// variable all keep the class skipped.
//
// The gate is a runtime Skip carrying the instruction to switch it on, never a
// silent return: a skipped spec has to show up in the Ginkgo summary as
// Skipped, because a class that is off on most runs is exactly where a
// vacuous pass would go unnoticed.
func enforceDisruptive() {
	if !slices.Contains(CurrentSpecReport().Labels(), LabelDisruptive) {
		return
	}
	if !optInEnabledFromEnv(envAllowDisruptive) {
		Skip("Disruptive spec: export " + envAllowDisruptive + "=true (or " + envRunAll +
			"=true) before the run to allow destructive actions on this cluster")
	}
}

// RequireDisruptiveSpec makes the Disruptive requirement of a destructive helper
// executable instead of merely documented: it stops the run unless the spec that
// is executing right now carries LabelDisruptive, on itself or on an enclosing
// container. A guarded helper calls it as its FIRST statement, so the requirement
// its doc comment states is checked where the damage would be done. Guarded
// today: Framework.RebootNode, Framework.StartIOWorkload, Framework.SetNodeLabel
// and TestRVR.RemoveFinalizers — every framework helper that damages state shared
// with the rest of the suite. A wrapper around a guarded helper adds no guard of
// its own: two checks of one requirement can only drift apart in wording.
//
// It complements the class gate (enforceDisruptive) and cannot be replaced by it.
// The gate runs in JustBeforeEach and sees only the labels the spec's author
// declared; it has no way of knowing whether the spec is about to call a
// destructive helper. A spec that forgets the label therefore passes the gate and
// then reboots a node of a shared stand anyway. The call site is the only place
// where the two facts — "this operation is destructive" and "these are the labels
// in scope" — are both known.
//
// what names the refused operation in the message ("rebooting node worker-2"), so
// the report says which destructive action was stopped, not merely that one was.
//
// Three call sites are told apart, because a bare label check would misreport two
// of them; see classifyDisruptiveCallSite for the distinction. The one outside a
// spec panics with this package's own message rather than calling Fail: outside a
// Ginkgo node Fail has no spec to attribute the failure to and unwinds with
// Ginkgo's generic UncaughtGinkgoPanic text, which would lose the reason.
func RequireDisruptiveSpec(what string) {
	GinkgoHelper()

	report := CurrentSpecReport()
	site, message := classifyDisruptiveCallSite(what, report.LeafNodeType, report.Labels())
	switch site {
	case disruptiveCallSiteAllowed:
		return
	case disruptiveCallSiteOutsideSpec:
		panic(message)
	case disruptiveCallSiteSuiteNode, disruptiveCallSiteUnlabeledSpec:
		Fail(message)
	}
}

// disruptiveCallSite is where a destructive helper was called from, judged by
// what can be checked and demanded there.
type disruptiveCallSite int

const (
	// disruptiveCallSiteAllowed: a spec carrying LabelDisruptive is executing.
	disruptiveCallSiteAllowed disruptiveCallSite = iota
	// disruptiveCallSiteOutsideSpec: no Ginkgo node is executing, so no labels
	// exist to be read at all.
	disruptiveCallSiteOutsideSpec
	// disruptiveCallSiteSuiteNode: a suite-level node is executing. Ginkgo does
	// report one, but such a node takes no decorators, so the label can never be
	// written on it and the class gate never runs for it.
	disruptiveCallSiteSuiteNode
	// disruptiveCallSiteUnlabeledSpec: a spec is executing without the label.
	disruptiveCallSiteUnlabeledSpec
)

// classifyDisruptiveCallSite is the pure decision behind RequireDisruptiveSpec:
// it maps one spec report — its leaf node type and the labels in scope for it —
// to a verdict and the message that goes with it. The two are produced together
// so they can never disagree about the cause, and the whole decision is unit-
// tested without a running spec.
//
// leafNodeType carries the distinction the label list cannot make. Ginkgo answers
// CurrentSpecReport() outside a spec with a zero-value report, whose label list is
// EMPTY, so "the spec is missing the label" is indistinguishable from "there were
// no labels to read" by labels alone — and the two need opposite answers. The zero
// NodeType (types.NodeTypeInvalid) is unreachable for any real node, so it
// separates them: it means nothing is executing (tree construction, a
// package-level variable, a plain go test), which is a programming error in the
// caller rather than a spec that broke a rule.
//
// A suite-level node is answered before labels are looked at, because demanding a
// label from a node that takes no decorators would be unactionable.
func classifyDisruptiveCallSite(
	what string,
	leafNodeType types.NodeType,
	labels []string,
) (disruptiveCallSite, string) {
	switch {
	case leafNodeType == types.NodeTypeInvalid:
		return disruptiveCallSiteOutsideSpec, fmt.Sprintf(
			"framework: %s is a destructive operation and was called while no Ginkgo node was"+
				" running, so there are no spec labels and the %q requirement could not be"+
				" checked at all. Call destructive helpers from a spec — an It body, or a"+
				" BeforeEach/AfterEach/DeferCleanup belonging to one — never from tree"+
				" construction (a Describe/Context body, a package-level variable) or a plain"+
				" go test.",
			what, LabelDisruptive)
	case leafNodeType.Is(types.NodeTypesForSuiteLevelNodes):
		return disruptiveCallSiteSuiteNode, fmt.Sprintf(
			"%s is a destructive operation and MUST be called from a spec, but it was called"+
				" from %s. A suite-level node carries no labels and the Disruptive gate never"+
				" runs for it, so the requirement cannot be satisfied where this call stands:"+
				" move it into a spec labelled Label(fw.LabelDisruptive), whose run also needs"+
				" %s=true (or %s=true).",
			what, leafNodeType, envAllowDisruptive, envRunAll)
	case !slices.Contains(labels, LabelDisruptive):
		return disruptiveCallSiteUnlabeledSpec, fmt.Sprintf(
			"%s is a destructive operation and needs Label(fw.LabelDisruptive) on the spec or on"+
				" an enclosing container; the labels in scope here are %v. Add the label — it"+
				" also injects Serial and the lowest spec priority — and start the run with"+
				" %s=true (or %s=true).",
			what, labels, envAllowDisruptive, envRunAll)
	default:
		return disruptiveCallSiteAllowed, ""
	}
}
