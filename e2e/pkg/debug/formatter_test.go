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
	"bytes"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var _ = Describe("Formatter interface", func() {
	Describe("ColorFormatter", func() {
		var f *ColorFormatter

		BeforeEach(func() {
			f = &ColorFormatter{kr: defaultKindRegistry}
		})

		Describe("FormatAdded", func() {
			It("produces block with ADDED header, body, and box-drawing", func() {
				lines := f.FormatAdded("rv", "test-vol", nil, []string{"apiVersion: v1", "kind: ReplicatedVolume"})
				clean := stripAnsiBlock(lines)

				Expect(clean).To(ContainSubstring("ADDED"))
				Expect(clean).To(ContainSubstring("[rv]"))
				Expect(clean).To(ContainSubstring("test-vol"))
				Expect(clean).To(ContainSubstring("┌"))
				Expect(clean).To(ContainSubstring("└"))
				Expect(clean).To(ContainSubstring("│"))
				Expect(clean).To(ContainSubstring("apiVersion: v1"))
			})

			It("with conditions uses ┌ conditions / ├── / body / └", func() {
				condLines := []string{"  ┌ conditions", "  │   ⊙✓ Ready True OK"}
				lines := f.FormatAdded("rv", "test-vol", condLines, []string{"spec: {}"})
				clean := stripAnsiBlock(lines)

				Expect(clean).To(ContainSubstring("conditions"))
				Expect(clean).To(ContainSubstring("├──"))
				Expect(clean).To(ContainSubstring("spec: {}"))
				Expect(clean).To(ContainSubstring("└"))
			})

			It("contains ANSI escape codes when colors are enabled", func() {
				if !ColorsEnabled {
					Skip("colors disabled in this environment")
				}
				lines := f.FormatAdded("rv", "vol", nil, []string{"a: b"})
				raw := strings.Join(lines, "\n")
				Expect(raw).To(ContainSubstring("\033["))
			})
		})

		Describe("FormatModified", func() {
			It("produces block with diff lines colored", func() {
				diffLines := []string{
					"── status",
					"-phase: Pending",
					"+phase: Healthy",
				}
				lines := f.FormatModified("rv", "test-vol", nil, diffLines)
				clean := stripAnsiBlock(lines)

				Expect(clean).To(ContainSubstring("MODIFIED"))
				Expect(clean).To(ContainSubstring("test-vol"))
				Expect(clean).To(ContainSubstring("-phase: Pending"))
				Expect(clean).To(ContainSubstring("+phase: Healthy"))
				Expect(clean).To(ContainSubstring("── status"))
			})

			It("conditions-only change has conditions but no diff body", func() {
				condLines := []string{"  ┌ conditions", "  │ ~ Ready True→False"}
				lines := f.FormatModified("rv", "test-vol", condLines, nil)
				clean := stripAnsiBlock(lines)

				Expect(clean).To(ContainSubstring("MODIFIED"))
				Expect(clean).To(ContainSubstring("conditions"))
				Expect(clean).To(ContainSubstring("└"))
				Expect(clean).NotTo(ContainSubstring("├──"))
			})

			It("both conditions and diff uses ├── separator", func() {
				condLines := []string{"  ┌ conditions", "  │ row"}
				diffLines := []string{"+new: field"}
				lines := f.FormatModified("rv", "test-vol", condLines, diffLines)
				clean := stripAnsiBlock(lines)

				Expect(clean).To(ContainSubstring("├──"))
			})
		})

		Describe("FormatDeleted", func() {
			It("produces DELETED line", func() {
				line := f.FormatDeleted("rv", "test-vol")
				clean := AnsiRe.ReplaceAllString(line, "")

				Expect(clean).To(ContainSubstring("DELETED"))
				Expect(clean).To(ContainSubstring("[rv]"))
				Expect(clean).To(ContainSubstring("test-vol"))
			})
		})

		Describe("FormatLogEntry", func() {
			It("delegates to FormatLogEntryTracked", func() {
				RegisterKind("rv", "ReplicatedVolume")
				entry := &LogEntry{
					Time:        "2026-03-12T10:00:00Z",
					Level:       "INFO",
					Msg:         "test message",
					Controller:  "rv-ctrl",
					Kind:        "ReplicatedVolume",
					Name:        "obj",
					ReconcileID: "aabb",
				}
				tracker := NewReconcileIDTracker()
				lines := f.FormatLogEntry(entry, "controller", tracker)

				Expect(lines).NotTo(BeEmpty())
				clean := stripAnsiBlock(lines)
				Expect(clean).To(ContainSubstring("test message"))
				Expect(clean).To(ContainSubstring("[controller]"))
			})
		})
	})

	Describe("PlainFormatter", func() {
		var f *PlainFormatter

		BeforeEach(func() {
			f = &PlainFormatter{kr: defaultKindRegistry}
		})

		Describe("FormatAdded", func() {
			It("produces output without ANSI codes", func() {
				lines := f.FormatAdded("rv", "test-vol", nil, []string{"a: b"})
				raw := strings.Join(lines, "\n")

				Expect(raw).NotTo(MatchRegexp(`\033\[`))
				Expect(raw).To(ContainSubstring("ADDED"))
				Expect(raw).To(ContainSubstring("[rv]"))
				Expect(raw).To(ContainSubstring("test-vol"))
				Expect(raw).To(ContainSubstring("a: b"))
			})

			It("structurally identical to ColorFormatter after stripping", func() {
				colorFmt := &ColorFormatter{kr: defaultKindRegistry}
				yamlLines := []string{"apiVersion: v1", "kind: ReplicatedVolume"}

				colorLines := colorFmt.FormatAdded("rv", "vol", nil, yamlLines)
				plainLines := f.FormatAdded("rv", "vol", nil, yamlLines)

				Expect(len(plainLines)).To(Equal(len(colorLines)))
				for i, cl := range colorLines {
					stripped := AnsiRe.ReplaceAllString(cl, "")
					Expect(plainLines[i]).To(Equal(stripped))
				}
			})
		})

		Describe("FormatModified", func() {
			It("produces output without ANSI codes", func() {
				diffLines := []string{"-old: val", "+new: val"}
				lines := f.FormatModified("rv", "vol", nil, diffLines)
				raw := strings.Join(lines, "\n")

				Expect(raw).NotTo(MatchRegexp(`\033\[`))
				Expect(raw).To(ContainSubstring("MODIFIED"))
				Expect(raw).To(ContainSubstring("-old: val"))
				Expect(raw).To(ContainSubstring("+new: val"))
			})
		})

		Describe("FormatDeleted", func() {
			It("produces output without ANSI codes", func() {
				line := f.FormatDeleted("rv", "vol")

				Expect(line).NotTo(MatchRegexp(`\033\[`))
				Expect(line).To(ContainSubstring("DELETED"))
				Expect(line).To(ContainSubstring("[rv]"))
			})
		})

		Describe("FormatLogEntry", func() {
			It("produces output without ANSI codes", func() {
				entry := &LogEntry{
					Time:  "2026-03-12T10:00:00Z",
					Level: "ERROR",
					Msg:   "something broke",
				}
				tracker := NewReconcileIDTracker()
				lines := f.FormatLogEntry(entry, "controller", tracker)

				raw := strings.Join(lines, "\n")
				Expect(raw).NotTo(MatchRegexp(`\033\[`))
				Expect(raw).To(ContainSubstring("something broke"))
			})
		})
	})

	Describe("WithFormatter option", func() {
		It("allows injection of PlainFormatter", func() {
			RegisterKind("rv", "ReplicatedVolume")

			rvInf := &fakeInformer{}
			fc := &fakeCache{
				informers: map[schema.GroupVersionKind]*fakeInformer{testRVGVK: rvInf},
			}
			var buf bytes.Buffer
			d := New(fc, &buf, WithFormatter(&PlainFormatter{kr: defaultKindRegistry}))

			obj := newTestObj(testRVGVK, "fmt-vol")
			Expect(d.Watch(obj)).To(Succeed())
			Expect(d.formatter).To(BeAssignableToTypeOf(&PlainFormatter{}))
		})
	})
})

func stripAnsiBlock(lines []string) string {
	return AnsiRe.ReplaceAllString(strings.Join(lines, "\n"), "")
}
