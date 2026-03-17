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
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("YAML marshaling", func() {
	Describe("MarshalYAML", func() {
		It("preserves top-level K8s key ordering", func() {
			obj := map[string]any{
				"status":     map[string]any{"phase": "Ready"},
				"apiVersion": "v1",
				"metadata":   map[string]any{"name": "test"},
				"kind":       "Pod",
				"spec":       map[string]any{"replicas": float64(3)},
			}

			result := MarshalYAML(obj)
			expectedOrder := []string{"apiVersion:", "kind:", "metadata:", "spec:", "status:"}

			lastIdx := -1
			for _, key := range expectedOrder {
				idx := strings.Index(result, key)
				Expect(idx).To(BeNumerically(">=", 0), "key %q not found in output:\n%s", key, result)
				Expect(idx).To(BeNumerically(">", lastIdx), "key %q at position %d is not after previous key at %d:\n%s", key, idx, lastIdx, result)
				lastIdx = idx
			}
		})

		It("sorts non-K8s keys alphabetically", func() {
			obj := map[string]any{
				"zebra": "z",
				"alpha": "a",
				"mango": "m",
			}

			lines := YAMLLines(obj)
			Expect(lines).To(HaveLen(3))
			Expect(lines[0]).To(HavePrefix("alpha:"))
			Expect(lines[1]).To(HavePrefix("mango:"))
			Expect(lines[2]).To(HavePrefix("zebra:"))
		})

		It("sorts nested keys alphabetically", func() {
			obj := map[string]any{
				"metadata": map[string]any{
					"name":      "test",
					"namespace": "default",
					"labels": map[string]any{
						"beta":  "2",
						"alpha": "1",
					},
				},
			}

			result := MarshalYAML(obj)
			alphaIdx := strings.Index(result, "alpha:")
			betaIdx := strings.Index(result, "beta:")
			Expect(alphaIdx).To(BeNumerically(">=", 0))
			Expect(betaIdx).To(BeNumerically(">=", 0))
			Expect(alphaIdx).To(BeNumerically("<", betaIdx), "alpha should come before beta:\n%s", result)
		})

		It("renders empty map and slice as inline literals", func() {
			obj := map[string]any{
				"emptyMap":   map[string]any{},
				"emptySlice": []any{},
			}

			result := MarshalYAML(obj)
			Expect(result).To(ContainSubstring("emptyMap: {}"))
			Expect(result).To(ContainSubstring("emptySlice: []"))
		})

		It("renders a slice of maps with dash notation", func() {
			obj := map[string]any{
				"items": []any{
					map[string]any{"name": "first", "value": "a"},
					map[string]any{"name": "second", "value": "b"},
				},
			}

			result := MarshalYAML(obj)
			Expect(result).To(ContainSubstring("- name: first"))
			Expect(result).To(ContainSubstring("  value: a"))
		})

		DescribeTable("scalar values",
			func(key, want string) {
				obj := map[string]any{
					"boolFalse": false,
					"boolTrue":  true,
					"decimal":   float64(3.14),
					"empty":     "",
					"integer":   float64(42),
					"nilVal":    nil,
					"text":      "hello",
				}
				result := MarshalYAML(obj)
				line := findYAMLLine(result, key)
				Expect(line).NotTo(BeEmpty(), "key %q not found in:\n%s", key, result)
				Expect(line).To(ContainSubstring(want))
			},
			Entry("boolTrue", "boolTrue", "true"),
			Entry("boolFalse", "boolFalse", "false"),
			Entry("integer", "integer", "42"),
			Entry("decimal", "decimal", "3.14"),
			Entry("text", "text", "hello"),
			Entry("empty", "empty", `""`),
			Entry("nilVal", "nilVal", "null"),
		)
	})

	DescribeTable("yamlString quoting",
		func(input, want string) {
			Expect(yamlString(input)).To(Equal(want))
		},
		Entry("simple", "simple", "simple"),
		Entry("empty", "", `""`),
		Entry("true", "true", `"true"`),
		Entry("false", "false", `"false"`),
		Entry("null", "null", `"null"`),
		Entry("yes", "yes", `"yes"`),
		Entry("no", "no", `"no"`),
		Entry("on", "on", `"on"`),
		Entry("off", "off", `"off"`),
		Entry("tilde", "~", `"~"`),
		Entry("integer string", "123", `"123"`),
		Entry("float string", "3.14", `"3.14"`),
		Entry("colon", "has:colon", `"has:colon"`),
		Entry("hash", "has#hash", `"has#hash"`),
		Entry("space in middle", "hello world", "hello world"),
		Entry("leading dash", "-dash", `"-dash"`),
		Entry("leading space", " leading", `" leading"`),
		Entry("newline", "line\nnew", `"line\nnew"`),
		Entry("tab", "tab\there", `"tab\there"`),
	)

	DescribeTable("yamlKey quoting",
		func(input, want string) {
			Expect(yamlKey(input)).To(Equal(want))
		},
		Entry("simple", "simple", "simple"),
		Entry("empty", "", `""`),
		Entry("space", "has space", `"has space"`),
		Entry("colon", "has:colon", `"has:colon"`),
		Entry("hyphenated", "normal-key", "normal-key"),
		Entry("underscore", "under_score", "under_score"),
	)
})

var _ = Describe("Unified diff", func() {
	It("returns nil for equal inputs", func() {
		a := []string{"apiVersion: v1", "kind: Pod", "metadata:", "  name: test"}
		b := []string{"apiVersion: v1", "kind: Pod", "metadata:", "  name: test"}
		Expect(UnifiedDiff(a, b)).To(BeNil())
	})

	It("detects a changed value", func() {
		a := []string{"apiVersion: v1", "kind: Pod", "metadata:", "  name: old-name"}
		b := []string{"apiVersion: v1", "kind: Pod", "metadata:", "  name: new-name"}

		diff := UnifiedDiff(a, b)
		Expect(diff).NotTo(BeNil())

		hasRemoval := false
		hasAddition := false
		for _, line := range diff {
			if strings.HasPrefix(line, "-") && strings.Contains(line, "old-name") {
				hasRemoval = true
			}
			if strings.HasPrefix(line, "+") && strings.Contains(line, "new-name") {
				hasAddition = true
			}
		}
		Expect(hasRemoval).To(BeTrue(), "diff should contain removal of old-name, got: %v", diff)
		Expect(hasAddition).To(BeTrue(), "diff should contain addition of new-name, got: %v", diff)
	})

	It("detects an inserted line", func() {
		a := []string{"line1", "line3"}
		b := []string{"line1", "line2", "line3"}

		diff := UnifiedDiff(a, b)
		Expect(diff).NotTo(BeNil())

		hasInsert := false
		for _, line := range diff {
			if strings.HasPrefix(line, "+") && strings.Contains(line, "line2") {
				hasInsert = true
			}
		}
		Expect(hasInsert).To(BeTrue(), "diff should contain insertion of line2, got: %v", diff)
	})

	It("detects a deleted line", func() {
		a := []string{"line1", "line2", "line3"}
		b := []string{"line1", "line3"}

		diff := UnifiedDiff(a, b)
		Expect(diff).NotTo(BeNil())

		hasDeletion := false
		for _, line := range diff {
			if strings.HasPrefix(line, "-") && strings.Contains(line, "line2") {
				hasDeletion = true
			}
		}
		Expect(hasDeletion).To(BeTrue(), "diff should contain deletion of line2, got: %v", diff)
	})

	It("includes a YAML path breadcrumb", func() {
		a := []string{"apiVersion: v1", "kind: Pod", "metadata:", "  name: old"}
		b := []string{"apiVersion: v1", "kind: Pod", "metadata:", "  name: new"}

		diff := UnifiedDiff(a, b)
		Expect(diff).NotTo(BeNil())

		hasPath := false
		for _, line := range diff {
			if strings.HasPrefix(line, "── ") && strings.Contains(line, "metadata") {
				hasPath = true
			}
		}
		Expect(hasPath).To(BeTrue(), "diff should contain YAML path breadcrumb with 'metadata', got: %v", diff)
	})
})

var _ = Describe("YAML path helpers", func() {
	DescribeTable("yamlPath",
		func(idx int, want string) {
			lines := []string{
				"apiVersion: v1",
				"kind: Pod",
				"metadata:",
				"  name: test",
				"  labels:",
				"    app: foo",
				"spec:",
				"  replicas: 3",
			}
			Expect(yamlPath(lines, idx)).To(Equal(want))
		},
		Entry("top-level key", 0, ""),
		Entry("under metadata", 3, "metadata"),
		Entry("under metadata.labels", 5, "metadata.labels"),
		Entry("under spec", 7, "spec"),
	)

	DescribeTable("yamlIndent",
		func(line string, want int) {
			Expect(yamlIndent(line)).To(Equal(want))
		},
		Entry("no indent", "apiVersion: v1", 0),
		Entry("2 spaces", "  name: test", 2),
		Entry("4 spaces", "    app: foo", 4),
		Entry("empty", "", 0),
	)

	DescribeTable("yamlKeyFromLine",
		func(line, want string) {
			Expect(yamlKeyFromLine(line)).To(Equal(want))
		},
		Entry("simple key-value", "apiVersion: v1", "apiVersion"),
		Entry("indented key-value", "  name: test", "name"),
		Entry("list item", "- item", ""),
		Entry("no colon", "no-colon", ""),
		Entry("key only", "metadata:", "metadata"),
	)
})

var _ = Describe("Conditions extraction", func() {
	It("extracts valid conditions", func() {
		obj := map[string]any{
			"metadata": map[string]any{
				"generation": float64(5),
			},
			"status": map[string]any{
				"conditions": []any{
					map[string]any{
						"type":               "Ready",
						"status":             "True",
						"reason":             "AllGood",
						"message":            "everything ok",
						"lastTransitionTime": "2026-03-12T10:00:00Z",
						"observedGeneration": float64(5),
					},
					map[string]any{
						"type":   "Progressing",
						"status": "False",
						"reason": "Done",
					},
				},
			},
		}

		conds := ExtractConditions(obj)
		Expect(conds).To(HaveLen(2))

		c0 := conds[0]
		Expect(c0.Type).To(Equal("Ready"))
		Expect(c0.Status).To(Equal("True"))
		Expect(c0.Reason).To(Equal("AllGood"))
		Expect(c0.ObservedGeneration).To(Equal(int64(5)))
		Expect(c0.Generation).To(Equal(int64(5)))
		Expect(c0.LastTransitionTime).To(Equal("2026-03-12T10:00:00Z"))

		c1 := conds[1]
		Expect(c1.Type).To(Equal("Progressing"))
		Expect(c1.Status).To(Equal("False"))
		Expect(c1.ObservedGeneration).To(Equal(int64(-1)))
	})

	It("returns nil for missing status", func() {
		obj := map[string]any{
			"metadata": map[string]any{"name": "test"},
		}
		Expect(ExtractConditions(obj)).To(BeNil())
	})

	It("returns nil for empty conditions array", func() {
		obj := map[string]any{
			"status": map[string]any{
				"conditions": []any{},
			},
		}
		Expect(ExtractConditions(obj)).To(BeNil())
	})

	It("returns nil for missing conditions field", func() {
		obj := map[string]any{
			"status": map[string]any{
				"phase": "Ready",
			},
		}
		Expect(ExtractConditions(obj)).To(BeNil())
	})

	Describe("RemoveConditions", func() {
		It("removes conditions and preserves other status fields", func() {
			obj := map[string]any{
				"status": map[string]any{
					"phase": "Ready",
					"conditions": []any{
						map[string]any{"type": "Ready", "status": "True"},
					},
				},
			}

			RemoveConditions(obj)

			status := obj["status"].(map[string]any)
			Expect(status).NotTo(HaveKey("conditions"))
			Expect(status["phase"]).To(Equal("Ready"))
		})

		It("does not panic when status is missing", func() {
			obj := map[string]any{"metadata": map[string]any{"name": "test"}}
			Expect(func() { RemoveConditions(obj) }).NotTo(Panic())
		})
	})
})

var _ = Describe("Condition comparison", func() {
	base := Condition{
		Type:               "Ready",
		Status:             "True",
		Reason:             "AllGood",
		Message:            "everything is fine",
		LastTransitionTime: "2026-03-12T10:00:00Z",
		ObservedGeneration: 5,
		Generation:         5,
	}

	DescribeTable("CondChanged per-field",
		func(modify func(Condition) Condition, expectChanged bool) {
			modified := modify(base)
			Expect(CondChanged(base, modified)).To(Equal(expectChanged))
		},
		Entry("identical", func(c Condition) Condition { return c }, false),
		Entry("Status", func(c Condition) Condition { c.Status = "False"; return c }, true),
		Entry("Reason", func(c Condition) Condition { c.Reason = "Changed"; return c }, true),
		Entry("Message", func(c Condition) Condition { c.Message = "new message"; return c }, true),
		Entry("LastTransitionTime", func(c Condition) Condition { c.LastTransitionTime = "2026-03-12T11:00:00Z"; return c }, true),
		Entry("ObservedGeneration", func(c Condition) Condition { c.ObservedGeneration = 4; return c }, true),
		Entry("Generation", func(c Condition) Condition { c.Generation = 6; return c }, true),
	)

	Describe("ConditionsEqual", func() {
		It("treats same conditions in different order as equal", func() {
			a := []Condition{
				{Type: "Ready", Status: "True", Reason: "OK"},
				{Type: "Progressing", Status: "False", Reason: "Done"},
			}
			b := []Condition{
				{Type: "Progressing", Status: "False", Reason: "Done"},
				{Type: "Ready", Status: "True", Reason: "OK"},
			}
			Expect(ConditionsEqual(a, b)).To(BeTrue())
		})

		It("returns false for different lengths", func() {
			a := []Condition{
				{Type: "Ready", Status: "True", Reason: "OK"},
				{Type: "Progressing", Status: "False", Reason: "Done"},
			}
			c := []Condition{
				{Type: "Ready", Status: "True", Reason: "OK"},
			}
			Expect(ConditionsEqual(a, c)).To(BeFalse())
		})

		It("returns false for different status values", func() {
			a := []Condition{
				{Type: "Ready", Status: "True", Reason: "OK"},
				{Type: "Progressing", Status: "False", Reason: "Done"},
			}
			d := []Condition{
				{Type: "Ready", Status: "False", Reason: "OK"},
				{Type: "Progressing", Status: "False", Reason: "Done"},
			}
			Expect(ConditionsEqual(a, d)).To(BeFalse())
		})
	})
})

var _ = Describe("Object cleaning", func() {
	It("removes noisy metadata fields", func() {
		obj := map[string]any{
			"metadata": map[string]any{
				"name":              "test",
				"managedFields":     []any{map[string]any{"manager": "kubectl"}},
				"resourceVersion":   "12345",
				"uid":               "abc-123",
				"creationTimestamp": "2026-01-01T00:00:00Z",
				"annotations": map[string]any{
					"kubectl.kubernetes.io/last-applied-configuration": `{"apiVersion":"v1"}`,
					"custom-annotation": "keep-me",
				},
			},
		}

		CleanObj(obj)
		meta := obj["metadata"].(map[string]any)

		for _, field := range []string{"managedFields", "resourceVersion", "uid", "creationTimestamp"} {
			Expect(meta).NotTo(HaveKey(field))
		}

		ann := meta["annotations"].(map[string]any)
		Expect(ann).NotTo(HaveKey("kubectl.kubernetes.io/last-applied-configuration"))
		Expect(ann["custom-annotation"]).To(Equal("keep-me"))
	})

	It("removes empty annotations map entirely", func() {
		obj := map[string]any{
			"metadata": map[string]any{
				"name": "test",
				"annotations": map[string]any{
					"kubectl.kubernetes.io/last-applied-configuration": `{"apiVersion":"v1"}`,
				},
			},
		}

		CleanObj(obj)
		meta := obj["metadata"].(map[string]any)
		Expect(meta).NotTo(HaveKey("annotations"))
	})

	It("does not panic when metadata is missing", func() {
		obj := map[string]any{"spec": map[string]any{"replicas": float64(3)}}
		Expect(func() { CleanObj(obj) }).NotTo(Panic())
	})

	It("PrettyLines cleans and serializes", func() {
		obj := map[string]any{
			"apiVersion": "v1",
			"kind":       "Pod",
			"metadata": map[string]any{
				"name":            "test",
				"resourceVersion": "999",
				"uid":             "should-be-removed",
			},
			"spec": map[string]any{
				"replicas": float64(1),
			},
		}

		lines := PrettyLines(obj)
		Expect(lines).NotTo(BeEmpty())
		for _, line := range lines {
			Expect(line).NotTo(ContainSubstring("resourceVersion"))
			if !strings.Contains(line, "apiVersion") {
				Expect(line).NotTo(ContainSubstring("uid"))
			}
		}
	})
})

var _ = Describe("Condition table helpers", func() {
	DescribeTable("truncMsg",
		func(msg string, maxW int, want string) {
			Expect(truncMsg(msg, maxW)).To(Equal(want))
		},
		Entry("short message unchanged", "short", 10, "short"),
		Entry("long message truncated", "hello world!", 8, "hello..."),
		Entry("maxW=0 uses fallback", "hello", 0, "hello"),
	)

	DescribeTable("pad",
		func(s string, w int, want string) {
			Expect(pad(s, w)).To(Equal(want))
		},
		Entry("pads shorter string", "hi", 5, "hi   "),
		Entry("returns longer string as-is", "hello", 3, "hello"),
	)

	It("condWidths computes max column widths", func() {
		conds := []Condition{
			{Type: "Ready", Status: "True", Reason: "OK"},
			{Type: "Progressing", Status: "Unknown", Reason: "InProgress"},
		}

		tw, sw, rw := condWidths(conds)
		Expect(tw).To(Equal(len("Progressing")))
		Expect(sw).To(Equal(len("Unknown")))
		Expect(rw).To(Equal(len("InProgress")))
	})

	DescribeTable("StatusIconDim",
		func(status, want string) {
			Expect(StatusIconDim(status)).To(Equal(want))
		},
		Entry("True", "True", "✓"),
		Entry("False", "False", "✗"),
		Entry("Unknown", "Unknown", "?"),
	)

	DescribeTable("GenerationIconDim",
		func(c Condition, want string) {
			Expect(GenerationIconDim(c)).To(Equal(want))
		},
		Entry("absent", Condition{ObservedGeneration: -1, Generation: 5}, "∅"),
		Entry("current", Condition{ObservedGeneration: 5, Generation: 5}, "⊙"),
		Entry("stale", Condition{ObservedGeneration: 4, Generation: 5}, "◌"),
	)
})

var _ = Describe("ConditionsTableNew", func() {
	It("renders header and rows", func() {
		conds := []Condition{
			{Type: "Ready", Status: "True", Reason: "OK", ObservedGeneration: 1, Generation: 1},
		}

		lines := ConditionsTableNew(conds, colorCfg)
		Expect(len(lines)).To(BeNumerically(">=", 2))

		Expect(stripAnsi(lines[0])).To(ContainSubstring("┌ conditions"))
		Expect(stripAnsi(lines[1])).To(ContainSubstring("Ready"))
	})

	It("renders header only for empty conditions", func() {
		lines := ConditionsTableNew([]Condition{}, colorCfg)
		Expect(lines).To(HaveLen(1))
		Expect(stripAnsi(lines[0])).To(Equal("  ┌ conditions"))
	})
})

var _ = Describe("ConditionsTableDiff", func() {
	It("marks unchanged conditions", func() {
		conds := []Condition{
			{Type: "Ready", Status: "True", Reason: "OK", ObservedGeneration: 1, Generation: 1},
		}

		lines := ConditionsTableDiff(conds, conds, colorCfg)
		Expect(lines).NotTo(BeEmpty())
		Expect(stripAnsi(lines[0])).To(ContainSubstring("unchanged"))
	})

	It("detects changed conditions", func() {
		old := []Condition{
			{Type: "Ready", Status: "False", Reason: "NotReady", ObservedGeneration: 1, Generation: 1},
		}
		newConds := []Condition{
			{Type: "Ready", Status: "True", Reason: "AllGood", ObservedGeneration: 2, Generation: 2},
		}

		lines := ConditionsTableDiff(old, newConds, colorCfg)
		Expect(lines).NotTo(BeNil())

		clean := stripAnsi(lines[0])
		Expect(clean).NotTo(ContainSubstring("unchanged"))

		joined := stripAnsi(strings.Join(lines, "\n"))
		Expect(joined).To(ContainSubstring("~"))
	})

	It("returns nil for empty conditions on both sides", func() {
		Expect(ConditionsTableDiff(nil, nil, colorCfg)).To(BeNil())
	})

	It("shows added condition with + prefix", func() {
		old := []Condition{
			{Type: "Ready", Status: "True", Reason: "OK", ObservedGeneration: 1, Generation: 1},
		}
		newConds := []Condition{
			{Type: "Ready", Status: "True", Reason: "OK", ObservedGeneration: 1, Generation: 1},
			{Type: "Progressing", Status: "False", Reason: "Done", ObservedGeneration: 1, Generation: 1},
		}

		lines := ConditionsTableDiff(old, newConds, colorCfg)
		Expect(lines).NotTo(BeNil())

		found := false
		for _, line := range lines {
			clean := stripAnsi(line)
			if strings.Contains(clean, "Progressing") && strings.Contains(clean, "+") {
				found = true
				if ColorsEnabled {
					Expect(line).To(ContainSubstring(ColorGreen))
				}
				Expect(clean).To(ContainSubstring("False"))
				Expect(clean).To(ContainSubstring("Done"))
				break
			}
		}
		Expect(found).To(BeTrue(), "expected line with '+' prefix and 'Progressing', got:\n%s",
			stripAnsi(strings.Join(lines, "\n")))
	})

	It("shows removed condition with - prefix and (removed)", func() {
		old := []Condition{
			{Type: "Ready", Status: "True", Reason: "OK", ObservedGeneration: 1, Generation: 1},
			{Type: "Progressing", Status: "False", Reason: "Done", ObservedGeneration: 1, Generation: 1},
		}
		newConds := []Condition{
			{Type: "Ready", Status: "True", Reason: "OK", ObservedGeneration: 1, Generation: 1},
		}

		lines := ConditionsTableDiff(old, newConds, colorCfg)
		Expect(lines).NotTo(BeNil())

		found := false
		for _, line := range lines {
			clean := stripAnsi(line)
			if strings.Contains(clean, "Progressing") && strings.Contains(clean, "-") && strings.Contains(clean, "(removed)") {
				found = true
				if ColorsEnabled {
					Expect(line).To(ContainSubstring(ColorRed))
				}
				break
			}
		}
		Expect(found).To(BeTrue(), "expected line with '-', 'Progressing', and '(removed)', got:\n%s",
			stripAnsi(strings.Join(lines, "\n")))
	})

	It("shows status transition arrow", func() {
		old := []Condition{
			{Type: "Ready", Status: "True", Reason: "OK", ObservedGeneration: 1, Generation: 1},
		}
		newConds := []Condition{
			{Type: "Ready", Status: "False", Reason: "NotReady", ObservedGeneration: 2, Generation: 2},
		}

		lines := ConditionsTableDiff(old, newConds, colorCfg)
		Expect(lines).NotTo(BeNil())

		joined := stripAnsi(strings.Join(lines, "\n"))
		Expect(joined).To(ContainSubstring("True→False"))
	})

	It("highlights age column when LastTransitionTime changes", func() {
		old := []Condition{
			{Type: "Ready", Status: "True", Reason: "OK",
				LastTransitionTime: "2026-03-12T10:00:00Z",
				ObservedGeneration: 1, Generation: 1},
		}
		newConds := []Condition{
			{Type: "Ready", Status: "True", Reason: "OK",
				LastTransitionTime: "2026-03-12T11:00:00Z",
				ObservedGeneration: 1, Generation: 1},
		}

		lines := ConditionsTableDiff(old, newConds, colorCfg)
		Expect(lines).NotTo(BeNil())

		found := false
		for _, line := range lines {
			clean := stripAnsi(line)
			if strings.Contains(clean, "Ready") && strings.Contains(clean, "~") {
				found = true
				if ColorsEnabled {
					Expect(line).To(ContainSubstring(ColorYellow))
				}
				break
			}
		}
		Expect(found).To(BeTrue(), "expected changed condition line with '~' marker, got:\n%s",
			stripAnsi(strings.Join(lines, "\n")))
	})
})

var _ = Describe("Unified diff edge cases", func() {
	It("all-insertion: empty a, non-empty b", func() {
		diff := UnifiedDiff(nil, []string{"line1", "line2"})
		Expect(diff).NotTo(BeNil())
		hasInsert := false
		for _, line := range diff {
			if strings.HasPrefix(line, "+") {
				hasInsert = true
			}
		}
		Expect(hasInsert).To(BeTrue(), "expected insertion lines, got: %v", diff)
	})

	It("all-deletion: non-empty a, empty b", func() {
		diff := UnifiedDiff([]string{"line1", "line2"}, nil)
		Expect(diff).NotTo(BeNil())
		hasDeletion := false
		for _, line := range diff {
			if strings.HasPrefix(line, "-") {
				hasDeletion = true
			}
		}
		Expect(hasDeletion).To(BeTrue(), "expected deletion lines, got: %v", diff)
	})

	It("both empty returns nil", func() {
		Expect(UnifiedDiff(nil, nil)).To(BeNil())
		Expect(UnifiedDiff([]string{}, []string{})).To(BeNil())
	})
})

var _ = Describe("ExtractConditions edge cases", func() {
	It("skips non-map entries in conditions array", func() {
		obj := map[string]any{
			"metadata": map[string]any{"generation": float64(1)},
			"status": map[string]any{
				"conditions": []any{
					"not a map",
					map[string]any{"type": "Ready", "status": "True"},
					float64(42),
				},
			},
		}
		conds := ExtractConditions(obj)
		Expect(conds).To(HaveLen(1))
		Expect(conds[0].Type).To(Equal("Ready"))
	})

	It("generation defaults to 0 when metadata.generation missing", func() {
		obj := map[string]any{
			"metadata": map[string]any{"name": "test"},
			"status": map[string]any{
				"conditions": []any{
					map[string]any{"type": "Ready", "status": "True", "observedGeneration": float64(5)},
				},
			},
		}
		conds := ExtractConditions(obj)
		Expect(conds).To(HaveLen(1))
		Expect(conds[0].Generation).To(Equal(int64(0)))
		Expect(conds[0].ObservedGeneration).To(Equal(int64(5)))
	})
})

var _ = Describe("ConditionsEqual edge cases", func() {
	It("returns false when b has a type not in a", func() {
		a := []Condition{
			{Type: "Ready", Status: "True"},
		}
		b := []Condition{
			{Type: "Unknown", Status: "True"},
		}
		Expect(ConditionsEqual(a, b)).To(BeFalse())
	})

	It("both nil returns true", func() {
		Expect(ConditionsEqual(nil, nil)).To(BeTrue())
	})
})

var _ = Describe("YAML edge cases", func() {
	It("writeYAMLSlice with scalar items", func() {
		obj := map[string]any{
			"items": []any{"alpha", "beta", "gamma"},
		}
		result := MarshalYAML(obj)
		Expect(result).To(ContainSubstring("- alpha"))
		Expect(result).To(ContainSubstring("- beta"))
		Expect(result).To(ContainSubstring("- gamma"))
	})

	It("writeYAMLSlice with nested arrays", func() {
		obj := map[string]any{
			"matrix": []any{
				[]any{"a", "b"},
				[]any{"c", "d"},
			},
		}
		result := MarshalYAML(obj)
		Expect(result).To(ContainSubstring("- a"))
		Expect(result).To(ContainSubstring("- b"))
		Expect(result).To(ContainSubstring("- c"))
		Expect(result).To(ContainSubstring("- d"))
	})

	It("writeYAMLSlice with bool and number scalars", func() {
		obj := map[string]any{
			"vals": []any{true, false, float64(42), nil},
		}
		result := MarshalYAML(obj)
		Expect(result).To(ContainSubstring("- true"))
		Expect(result).To(ContainSubstring("- false"))
		Expect(result).To(ContainSubstring("- 42"))
		Expect(result).To(ContainSubstring("- null"))
	})
})

var _ = Describe("yamlPath edge cases", func() {
	It("out-of-range negative index returns empty", func() {
		lines := []string{"apiVersion: v1"}
		Expect(yamlPath(lines, -1)).To(BeEmpty())
	})

	It("out-of-range large index returns empty", func() {
		lines := []string{"apiVersion: v1"}
		Expect(yamlPath(lines, 100)).To(BeEmpty())
	})
})

var _ = Describe("StatusColored", func() {
	DescribeTable("returns colored status",
		func(status, wantClean string) {
			result := StatusColored(status, colorCfg)
			clean := stripAnsi(result)
			Expect(strings.TrimSpace(clean)).To(Equal(wantClean))
		},
		Entry("True", "True", "True"),
		Entry("False", "False", "False"),
		Entry("Unknown", "Unknown", "Unknown"),
	)

	It("preserves trailing padding", func() {
		result := StatusColored("True   ", colorCfg)
		clean := stripAnsi(result)
		Expect(clean).To(HaveSuffix("   "))
	})

	It("True uses green color when enabled", func() {
		if !ColorsEnabled {
			Skip("colors disabled")
		}
		Expect(StatusColored("True", colorCfg)).To(ContainSubstring(ColorGreen))
	})

	It("False uses red color when enabled", func() {
		if !ColorsEnabled {
			Skip("colors disabled")
		}
		Expect(StatusColored("False", colorCfg)).To(ContainSubstring(ColorRed))
	})

	It("Unknown uses yellow color when enabled", func() {
		if !ColorsEnabled {
			Skip("colors disabled")
		}
		Expect(StatusColored("Unknown", colorCfg)).To(ContainSubstring(ColorYellow))
	})
})

var _ = Describe("StatusIcon colored", func() {
	DescribeTable("returns correct icon",
		func(status, wantIcon string) {
			result := StatusIcon(status, colorCfg)
			clean := stripAnsi(result)
			Expect(clean).To(Equal(wantIcon))
		},
		Entry("True", "True", "✓"),
		Entry("False", "False", "✗"),
		Entry("Unknown", "Unknown", "?"),
	)

	It("True has green color", func() {
		if !ColorsEnabled {
			Skip("colors disabled")
		}
		Expect(StatusIcon("True", colorCfg)).To(ContainSubstring(ColorGreen))
	})

	It("False has red color", func() {
		if !ColorsEnabled {
			Skip("colors disabled")
		}
		Expect(StatusIcon("False", colorCfg)).To(ContainSubstring(ColorRed))
	})
})

var _ = Describe("GenerationIcon colored", func() {
	DescribeTable("returns correct icon",
		func(c Condition, wantIcon string) {
			result := GenerationIcon(c, colorCfg)
			clean := stripAnsi(result)
			Expect(clean).To(Equal(wantIcon))
		},
		Entry("absent", Condition{ObservedGeneration: -1, Generation: 5}, "∅"),
		Entry("current", Condition{ObservedGeneration: 5, Generation: 5}, "⊙"),
		Entry("stale", Condition{ObservedGeneration: 4, Generation: 5}, "◌"),
	)

	It("absent uses red", func() {
		if !ColorsEnabled {
			Skip("colors disabled")
		}
		Expect(GenerationIcon(Condition{ObservedGeneration: -1}, colorCfg)).To(ContainSubstring(ColorRed))
	})

	It("current uses green", func() {
		if !ColorsEnabled {
			Skip("colors disabled")
		}
		Expect(GenerationIcon(Condition{ObservedGeneration: 1, Generation: 1}, colorCfg)).To(ContainSubstring(ColorGreen))
	})

	It("stale uses yellow", func() {
		if !ColorsEnabled {
			Skip("colors disabled")
		}
		Expect(GenerationIcon(Condition{ObservedGeneration: 1, Generation: 2}, colorCfg)).To(ContainSubstring(ColorYellow))
	})
})

var _ = Describe("truncMsg edge cases", func() {
	It("maxW=3 returns first 3 chars without ellipsis", func() {
		Expect(truncMsg("abcdefgh", 3)).To(Equal("abc"))
	})

	It("maxW=2 returns first 2 chars without ellipsis", func() {
		Expect(truncMsg("abcdefgh", 2)).To(Equal("ab"))
	})

	It("maxW=4 truncates with ellipsis", func() {
		Expect(truncMsg("abcdefgh", 4)).To(Equal("a..."))
	})

	It("exact length message returned as-is", func() {
		Expect(truncMsg("abc", 3)).To(Equal("abc"))
	})
})

var _ = Describe("ConditionsTableNew exact content", func() {
	It("rows contain generation icon, status icon, type, status, reason, message", func() {
		conds := []Condition{
			{
				Type:               "Ready",
				Status:             "True",
				Reason:             "AllGood",
				Message:            "everything ok",
				LastTransitionTime: time.Now().Add(-30 * time.Second).Format(time.RFC3339),
				ObservedGeneration: 1,
				Generation:         1,
			},
		}
		lines := ConditionsTableNew(conds, colorCfg)
		Expect(lines).To(HaveLen(2))

		header := stripAnsi(lines[0])
		Expect(header).To(Equal("  ┌ conditions"))

		row := stripAnsi(lines[1])
		Expect(row).To(ContainSubstring("⊙"))
		Expect(row).To(ContainSubstring("✓"))
		Expect(row).To(ContainSubstring("Ready"))
		Expect(row).To(ContainSubstring("True"))
		Expect(row).To(ContainSubstring("AllGood"))
		Expect(row).To(ContainSubstring("everything ok"))
		Expect(row).To(MatchRegexp(`⏱\d+s`))
	})
})

var _ = Describe("ConditionsTableDiff reason transition", func() {
	It("shows reason transition arrow", func() {
		old := []Condition{
			{Type: "Ready", Status: "True", Reason: "OK", ObservedGeneration: 1, Generation: 1},
		}
		newConds := []Condition{
			{Type: "Ready", Status: "True", Reason: "Degraded", ObservedGeneration: 2, Generation: 2},
		}

		lines := ConditionsTableDiff(old, newConds, colorCfg)
		Expect(lines).NotTo(BeNil())

		joined := stripAnsi(strings.Join(lines, "\n"))
		Expect(joined).To(ContainSubstring("OK→Degraded"))
	})
})

var _ = Describe("condAgeWidth", func() {
	It("returns minimum of 2 for empty conditions", func() {
		Expect(condAgeWidth()).To(Equal(2))
	})

	It("returns width of longest age string", func() {
		conds := []Condition{
			{LastTransitionTime: time.Now().Add(-5 * time.Minute).Format(time.RFC3339)},
			{LastTransitionTime: ""},
		}
		w := condAgeWidth(conds)
		Expect(w).To(BeNumerically(">=", 3))
	})
})

var _ = Describe("SaveSnapshot", func() {
	It("returns empty for empty snapshotsDir", func() {
		Expect(SaveSnapshot("", "rv", "test", "content")).To(BeEmpty())
	})

	It("writes file and returns absolute path", func() {
		tmpDir := GinkgoT().TempDir()
		path := SaveSnapshot(tmpDir, "rv", "my-vol", "apiVersion: v1\nkind: Pod\n")
		Expect(path).NotTo(BeEmpty())
		Expect(path).To(ContainSubstring("rv-my-vol-"))
		Expect(path).To(HaveSuffix(".yaml"))
	})
})

var _ = Describe("strVal", func() {
	It("returns string value for existing key", func() {
		m := map[string]any{"key": "value"}
		Expect(strVal(m, "key")).To(Equal("value"))
	})

	It("returns empty for missing key", func() {
		m := map[string]any{}
		Expect(strVal(m, "nope")).To(BeEmpty())
	})

	It("returns empty for non-string value", func() {
		m := map[string]any{"key": float64(42)}
		Expect(strVal(m, "key")).To(BeEmpty())
	})
})

var _ = Describe("yamlString backslash handling", func() {
	It("double-escapes backslash before quote", func() {
		result := yamlString(`path\to\"file`)
		Expect(result).To(Equal(`"path\\to\\\"file"`))
	})
})

var _ = Describe("condAge", func() {
	It("returns ⏱-- when lastTransitionTime is empty", func() {
		c := Condition{}
		Expect(condAge(c)).To(Equal("⏱--"))
	})

	It("returns ⏱? for unparseable time", func() {
		c := Condition{LastTransitionTime: "not-a-time"}
		Expect(condAge(c)).To(Equal("⏱?"))
	})

	It("returns seconds for recent time", func() {
		c := Condition{LastTransitionTime: time.Now().Add(-5 * time.Second).UTC().Format(time.RFC3339)}
		age := condAge(c)
		Expect(age).To(HavePrefix("⏱"))
		Expect(age).To(MatchRegexp(`⏱\d+s`))
	})

	It("returns minutes for time a few minutes ago", func() {
		c := Condition{LastTransitionTime: time.Now().Add(-3 * time.Minute).UTC().Format(time.RFC3339)}
		age := condAge(c)
		Expect(age).To(MatchRegexp(`⏱\d+m`))
	})

	It("returns hours for time a few hours ago", func() {
		c := Condition{LastTransitionTime: time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)}
		age := condAge(c)
		Expect(age).To(MatchRegexp(`⏱\d+h`))
	})

	It("returns days for old time", func() {
		c := Condition{LastTransitionTime: time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)}
		age := condAge(c)
		Expect(age).To(MatchRegexp(`⏱\d+d`))
	})

	It("parses RFC3339Nano format", func() {
		c := Condition{LastTransitionTime: time.Now().Add(-10 * time.Second).UTC().Format(time.RFC3339Nano)}
		age := condAge(c)
		Expect(age).To(MatchRegexp(`⏱\d+s`))
	})
})

// Suppress unused import warnings.
var _ = fmt.Sprintf
var _ = time.Now

func findYAMLLine(yaml string, key string) string {
	for _, line := range strings.Split(yaml, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, key+":") {
			return trimmed
		}
	}
	return ""
}
