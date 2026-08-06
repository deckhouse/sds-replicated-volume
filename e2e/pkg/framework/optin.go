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
	"os"
	"slices"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
)

// Environment variables that switch the opt-in spec classes on. Nothing in the
// repository ever sets them: no workflow under .github/workflows runs the e2e
// suites and hack/run-e2e-new.sh only picks a Ginkgo label filter. They are
// exported by the person who starts the run, in their own shell.
//
// The names are exported so that a suite whose specs ALL belong to an opt-in
// class can refuse to start at all instead of running its setup and then
// skipping every spec — see DisruptiveEnabled.
const (
	// EnvAllowDisruptive enables the Disruptive class on its own.
	EnvAllowDisruptive = "E2E_ALLOW_DISRUPTIVE"
	// EnvAllowLongHaul enables the LongHaul class on its own.
	EnvAllowLongHaul = "E2E_ALLOW_LONG_HAUL"
	// EnvRunAll is the umbrella variable: it enables every opt-in class at once
	// (Disruptive and LongHaul).
	EnvRunAll = "E2E_RUN_ALL"
)

// truthy reports whether s spells "true" the way strconv.ParseBool reads it:
// true, TRUE, True, 1, t, T. Everything else is false — an empty value, an
// explicit false/0/f, and any value ParseBool cannot read at all (yes, on, a
// typo). A value nobody meant as a yes therefore never switches a class on,
// which matters most for Disruptive: it edits system node labels and removes a
// finalizer by hand, and a shared stand is an expensive place to learn that
// E2E_ALLOW_DISRUPTIVE=false used to mean "yes".
//
// Same shape as parseTimeoutMultiplier (timeout_policy.go): a strconv parse
// with a safe default on error.
func truthy(s string) bool {
	v, err := strconv.ParseBool(s)
	return err == nil && v
}

// optInEnabled reports whether an opt-in spec class is enabled, from the VALUE
// of its own variable and the VALUE of the umbrella one. Both are read by the
// same rule (truthy), so the two gates can never drift apart, and the umbrella
// has no negative veto: E2E_RUN_ALL=true enables the class even when the class
// variable says false.
//
// The function is pure on purpose — it takes values, not variable names — so the
// whole gate table is unit-tested without touching the process environment, and
// no Skip branch has to read os.Getenv itself.
func optInEnabled(classVar, runAllVar string) bool {
	return truthy(classVar) || truthy(runAllVar)
}

// optInEnabledFromEnv applies optInEnabled to the current process environment.
// It is the only place a class gate reads os.Getenv.
func optInEnabledFromEnv(classEnv string) bool {
	return optInEnabled(os.Getenv(classEnv), os.Getenv(EnvRunAll))
}

// DisruptiveEnabled reports whether the Disruptive class is enabled for this
// process, by exactly the rule enforceDisruptive uses (optInEnabled over
// EnvAllowDisruptive and EnvRunAll). Exported so that the decision is asked for,
// never re-implemented: a caller spelling out its own strconv.ParseBool would be
// a second copy of the formula, free to drift from the gate that actually skips
// the specs.
//
// Its intended caller is the entry point of a suite whose scenario is Disruptive
// as a whole (func TestX, before RunSpecs). Such a suite must refuse to run with
// the class off rather than let the gate skip its specs: the class gate lives in
// JustBeforeEach, which Ginkgo reaches only AFTER BeforeAll — a suite that sets
// its whole stand up in a BeforeAll would install, create and attach everything
// and only then report every spec as skipped.
func DisruptiveEnabled() bool {
	return optInEnabledFromEnv(EnvAllowDisruptive)
}

// hasLabelInArgs reports whether args contain a Labels value carrying label.
// Tree-construction transformers see only the node's own args, so this answers
// "was the label written on THIS node", not "does the node inherit it".
func hasLabelInArgs(args []any, label string) bool {
	for _, arg := range args {
		if labels, ok := arg.(Labels); ok {
			if slices.Contains(labels, label) {
				return true
			}
		}
	}
	return false
}

// focusRequested reports whether the run was started with an explicit focus
// (--focus / --focus-file). Focusing is a deliberate act by the person running
// the suite, so it bypasses the LongHaul gate — and ONLY that gate. Disruptive
// stays behind its variable no matter how the run was focused: focusing says
// "run this spec", not "you may reboot a node of this cluster".
func focusRequested(focusStrings, focusFiles []string) bool {
	return len(focusStrings) > 0 || len(focusFiles) > 0
}
