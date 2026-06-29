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

package debug

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Golden output regression", func() {
	var pf *PlainFormatter

	BeforeEach(func() {
		pf = &PlainFormatter{kr: defaultKindRegistry}
	})

	It("ADDED block: header + conditions + YAML body + box-drawing", func() {
		condLines := ConditionsTableNew([]Condition{
			{
				Type:               "Ready",
				Status:             "True",
				Reason:             "AllGood",
				Message:            "ok",
				ObservedGeneration: 1,
				Generation:         1,
			},
		}, plainCfg)

		yamlLines := []string{
			"apiVersion: v1",
			"kind: Pod",
			"metadata:",
			"  name: test",
		}

		block := pf.FormatAdded("rv", "test-vol", condLines, yamlLines)
		clean := stripAnsiBlock(block)

		lines := strings.Split(clean, "\n")
		Expect(len(lines)).To(BeNumerically(">=", 8))

		Expect(lines[0]).To(ContainSubstring("ADDED"))
		Expect(lines[0]).To(ContainSubstring("[rv]"))
		Expect(lines[0]).To(ContainSubstring("test-vol"))

		Expect(lines[1]).To(Equal("  ┌ conditions"))
		Expect(lines[2]).To(ContainSubstring("⊙✓"))
		Expect(lines[2]).To(ContainSubstring("Ready"))
		Expect(lines[2]).To(ContainSubstring("True"))
		Expect(lines[2]).To(ContainSubstring("AllGood"))
		Expect(lines[2]).To(ContainSubstring("ok"))
		Expect(lines[3]).To(Equal("  ├──"))

		Expect(lines[4]).To(ContainSubstring("│"))
		Expect(lines[4]).To(ContainSubstring("apiVersion: v1"))
		Expect(lines[5]).To(ContainSubstring("kind: Pod"))

		Expect(lines[len(lines)-1]).To(Equal("  └"))
	})

	It("MODIFIED block: header + conditions diff + YAML diff", func() {
		oldConds := []Condition{
			{Type: "Ready", Status: "False", Reason: "NotReady", ObservedGeneration: 1, Generation: 1},
		}
		newConds := []Condition{
			{Type: "Ready", Status: "True", Reason: "AllGood", ObservedGeneration: 2, Generation: 2},
		}
		condLines := ConditionsTableDiff(oldConds, newConds, plainCfg)

		diffLines := []string{
			"── status",
			"-phase: Pending",
			"+phase: Healthy",
		}

		block := pf.FormatModified("rv", "test-vol", condLines, diffLines)
		clean := stripAnsiBlock(block)

		lines := strings.Split(clean, "\n")
		Expect(lines[0]).To(ContainSubstring("MODIFIED"))
		Expect(lines[0]).To(ContainSubstring("[rv]"))
		Expect(lines[0]).To(ContainSubstring("test-vol"))

		found := map[string]bool{"conditions": false, "├──": false, "-phase: Pending": false, "+phase: Healthy": false, "└": false}
		for _, l := range lines {
			for k := range found {
				if strings.Contains(l, k) {
					found[k] = true
				}
			}
		}
		for k, v := range found {
			Expect(v).To(BeTrue(), "missing %q in output:\n%s", k, clean)
		}

		Expect(lines[len(lines)-1]).To(Equal("  └"))
	})

	It("DELETED line: header with kind and name", func() {
		line := pf.FormatDeleted("rv", "test-vol")

		Expect(line).To(ContainSubstring("DELETED"))
		Expect(line).To(ContainSubstring("[rv]"))
		Expect(line).To(ContainSubstring("test-vol"))
		Expect(line).NotTo(ContainSubstring("\033["))
	})

	It("Log entry with reconcile boundary separator", func() {
		RegisterKind("rv", "ReplicatedVolume")
		tracker := NewReconcileIDTracker()

		e1 := &LogEntry{
			Time:        "2026-03-12T10:00:00Z",
			Level:       "INFO",
			Msg:         "step-1",
			Controller:  "rv-ctrl",
			Kind:        "ReplicatedVolume",
			Name:        "obj-a",
			ReconcileID: "aaaa1111-0000-0000-0000-000000000000",
		}
		_ = pf.FormatLogEntry(e1, "controller", tracker)

		e2 := &LogEntry{
			Time:        "2026-03-12T10:00:01Z",
			Level:       "INFO",
			Msg:         "step-2",
			Controller:  "rv-ctrl",
			Kind:        "ReplicatedVolume",
			Name:        "obj-a",
			ReconcileID: "bbbb2222-0000-0000-0000-000000000000",
		}
		lines := pf.FormatLogEntry(e2, "controller", tracker)

		Expect(len(lines)).To(BeNumerically(">=", 2))
		Expect(lines[0]).To(Equal("────────── reconcile aaaa1111 done ──────────"))

		parts := strings.Fields(lines[1])
		Expect(parts[0]).To(MatchRegexp(`^\[\d{2}:\d{2}:\d{2}\.\d{3}\]$`))
		Expect(parts[1]).To(Equal("[controller]"))
		Expect(parts).To(ContainElement("step-2"))
	})

	It("253-char name edge case", func() {
		longName := strings.Repeat("a", 253)

		yamlLines := []string{
			"apiVersion: v1",
			"kind: Pod",
			"metadata:",
			"  name: " + longName,
		}

		conds := []Condition{
			{Type: "Ready", Status: "True", Reason: "OK", ObservedGeneration: 1, Generation: 1},
		}
		condLines := ConditionsTableNew(conds, plainCfg)

		Expect(func() {
			block := pf.FormatAdded("rv", longName, condLines, yamlLines)
			Expect(len(block)).To(BeNumerically(">=", 4))
		}).NotTo(Panic())

		block := pf.FormatAdded("rv", longName, condLines, yamlLines)
		clean := stripAnsiBlock(block)
		Expect(clean).To(ContainSubstring("ADDED"))
		Expect(clean).To(ContainSubstring(longName))

		condClean := stripAnsiBlock(condLines)
		Expect(condClean).To(ContainSubstring("Ready"))
		Expect(condClean).To(ContainSubstring("True"))
	})
})
