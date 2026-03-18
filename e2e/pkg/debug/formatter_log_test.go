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
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ParseLogEntry", func() {
	It("extracts all fields from valid JSON", func() {
		line := `{"time":"2026-03-12T10:00:00Z","level":"INFO","msg":"reconcile","controller":"rv-controller","controllerKind":"ReplicatedVolume","name":"test-rv","reconcileID":"abcd1234-5678","logger":"phase-a/phase-b","result":"Continue","changed":"true","hasError":"false","duration":"1.234s"}`

		e := ParseLogEntry(line, "", nil)
		Expect(e).NotTo(BeNil())

		Expect(e.Time).To(Equal("2026-03-12T10:00:00Z"))
		Expect(e.Level).To(Equal("INFO"))
		Expect(e.Msg).To(Equal("reconcile"))
		Expect(e.Controller).To(Equal("rv-controller"))
		Expect(e.Kind).To(Equal("ReplicatedVolume"))
		Expect(e.Name).To(Equal("test-rv"))
		Expect(e.ReconcileID).To(Equal("abcd1234-5678"))
		Expect(e.Result).To(Equal("Continue"))
		Expect(e.Changed).To(Equal("true"))
		Expect(e.HasError).To(Equal("false"))
		Expect(e.Duration).To(Equal("1.234s"))
		Expect(e.PhasePath).To(Equal([]string{"phase-a", "phase-b"}))
		Expect(e.PhaseName).To(Equal("phase-b"))
	})

	It("returns nil for invalid JSON", func() {
		Expect(ParseLogEntry("not json at all", "", nil)).To(BeNil())
	})

	It("extracts err field", func() {
		line := `{"time":"2026-03-12T10:00:00Z","level":"ERROR","msg":"failed","err":"something broke"}`
		e := ParseLogEntry(line, "", nil)
		Expect(e).NotTo(BeNil())
		Expect(e.Error).To(Equal("something broke"))

		line2 := `{"time":"2026-03-12T10:00:00Z","level":"ERROR","msg":"failed","error":"other error"}`
		e2 := ParseLogEntry(line2, "", nil)
		Expect(e2).NotTo(BeNil())
		Expect(e2.Error).To(Equal("other error"))
	})

	It("collects extra fields", func() {
		line := `{"time":"2026-03-12T10:00:00Z","level":"INFO","msg":"test","customField":"hello","anotherField":42}`
		e := ParseLogEntry(line, "", nil)
		Expect(e).NotTo(BeNil())
		Expect(e.Extras).To(HaveLen(2))
		Expect(e.Extras[0].Key).To(Equal("anotherField"))
		Expect(e.Extras[0].Val).To(Equal("42"))
		Expect(e.Extras[1].Key).To(Equal("customField"))
		Expect(e.Extras[1].Val).To(Equal("hello"))
	})

	It("skips controllerKind from extras", func() {
		line := `{"time":"2026-03-12T10:00:00Z","level":"INFO","msg":"test","controllerKind":"ReplicatedVolume","ReplicatedVolume":{"name":"foo"}}`
		e := ParseLogEntry(line, "", nil)
		Expect(e).NotTo(BeNil())
		for _, kv := range e.Extras {
			Expect(kv.Key).NotTo(Equal("ReplicatedVolume"))
		}
	})
})

var _ = Describe("LevelColored", func() {
	DescribeTable("returns correct colored level string",
		func(level, wantClean string) {
			result := levelColored(level, activePalette())
			clean := strings.TrimSpace(stripAnsi(result))
			Expect(clean).To(Equal(wantClean))
		},
		Entry("INFO", "INFO", "INFO"),
		Entry("ERROR", "ERROR", "ERROR"),
		Entry("WARN", "WARN", "WARN"),
		Entry("DEBUG", "DEBUG", "DEBUG"),
		Entry("V1 (slog -1)", "-1", "V1"),
		Entry("V5 (slog -5)", "-5", "V5"),
		Entry("V8 (slog -8)", "-8", "V8"),
	)
})

var _ = Describe("FormatPhaseStart", func() {
	It("renders phase with indentation and arrow", func() {
		tests := []struct {
			name      string
			entry     *LogEntry
			wantPhase string
			wantDepth int
		}{
			{
				name: "single phase",
				entry: &LogEntry{
					Msg:       "phase start",
					PhasePath: []string{"my-phase"},
					PhaseName: "my-phase",
				},
				wantPhase: "my-phase",
				wantDepth: 1,
			},
			{
				name: "nested phase",
				entry: &LogEntry{
					Msg:       "phase start",
					PhasePath: []string{"parent", "child"},
					PhaseName: "child",
				},
				wantPhase: "child",
				wantDepth: 2,
			},
			{
				name: "deep nesting",
				entry: &LogEntry{
					Msg:       "phase start",
					PhasePath: []string{"a", "b", "c"},
					PhaseName: "c",
				},
				wantPhase: "c",
				wantDepth: 3,
			},
		}

		for _, tt := range tests {
			result := formatPhaseStart(tt.entry, activePalette())
			clean := stripAnsi(result)
			Expect(clean).To(ContainSubstring("▶ "+tt.wantPhase), "case: %s", tt.name)
			if tt.wantDepth > 1 {
				expectedIndent := strings.Repeat("│ ", tt.wantDepth-1)
				Expect(clean).To(ContainSubstring(expectedIndent), "case: %s indent", tt.name)
			}
		}
	})
})

var _ = Describe("FormatPhaseStart depth 5", func() {
	It("renders 4 indent levels for depth 5", func() {
		entry := &LogEntry{
			Msg:       "phase start",
			PhasePath: []string{"a", "b", "c", "d", "e"},
			PhaseName: "e",
		}
		result := formatPhaseStart(entry, activePalette())
		clean := stripAnsi(result)
		Expect(clean).To(ContainSubstring("▶ e"))
		expectedIndent := "│ │ │ │ "
		Expect(clean).To(ContainSubstring(expectedIndent))
	})
})

var _ = Describe("FormatLogEntryCommon exact format", func() {
	It("output contains timestamp badge shortID level controller kind/name message in order", func() {
		RegisterKind("rv", "ReplicatedVolume")
		e := &LogEntry{
			Time:        "2026-03-12T10:00:00Z",
			Level:       "INFO",
			Msg:         "doing work",
			Controller:  "rv-controller",
			Kind:        "ReplicatedVolume",
			Name:        "test-obj",
			ReconcileID: "aabbccdd-1234",
		}
		badge := "[test]"
		shortID := "aabbccdd"
		lines := formatLogEntryCommon(e, badge, shortID, nil, activePalette(), defaultKindRegistry)
		Expect(lines).To(HaveLen(1))

		clean := stripAnsi(lines[0])
		parts := strings.Fields(clean)

		Expect(parts[0]).To(MatchRegexp(`^\[\d{2}:\d{2}:\d{2}\.\d{3}\]$`), "first field should be [HH:MM:SS.mmm]")
		Expect(parts[1]).To(Equal("[test]"), "second field should be badge")
		Expect(parts[2]).To(Equal("aabbccdd"), "third field should be shortID")
		Expect(parts[3]).To(Equal("INFO"), "fourth field should be level")
		Expect(parts[4]).To(Equal("rv-controller"), "fifth field should be controller")
		Expect(parts[5]).To(Equal("rv/test-obj"), "sixth field should be kind/name")
		Expect(parts[6]).To(Equal("doing"), "message should follow")
	})

	It("RFC3339 time is parsed and reformatted with milliseconds", func() {
		t, _ := time.Parse(time.RFC3339, "2026-03-12T10:30:45Z")
		expected := t.Local().Format("[15:04:05.000]")

		e := &LogEntry{
			Time:  "2026-03-12T10:30:45Z",
			Level: "INFO",
			Msg:   "test",
		}
		lines := formatLogEntryCommon(e, "[c]", "", nil, activePalette(), defaultKindRegistry)
		clean := stripAnsi(lines[0])
		Expect(clean).To(ContainSubstring(expected))
	})

	It("RFC3339Nano time is parsed and reformatted", func() {
		t, _ := time.Parse(time.RFC3339Nano, "2026-03-12T10:30:45.123456789Z")
		expected := t.Local().Format("[15:04:05.000]")

		e := &LogEntry{
			Time:  "2026-03-12T10:30:45.123456789Z",
			Level: "INFO",
			Msg:   "test",
		}
		lines := formatLogEntryCommon(e, "[c]", "", nil, activePalette(), defaultKindRegistry)
		clean := stripAnsi(lines[0])
		Expect(clean).To(ContainSubstring(expected))
	})

	It("fallback timestamp for empty time", func() {
		e := &LogEntry{
			Level: "INFO",
			Msg:   "test",
		}
		lines := formatLogEntryCommon(e, "[c]", "", nil, activePalette(), defaultKindRegistry)
		clean := stripAnsi(lines[0])
		Expect(clean).To(MatchRegexp(`^\[\d{2}:\d{2}:\d{2}\.\d{3}\]`))
	})

	It("fallback timestamp for unparseable time", func() {
		e := &LogEntry{
			Time:  "not-a-time",
			Level: "INFO",
			Msg:   "test",
		}
		lines := formatLogEntryCommon(e, "[c]", "", nil, activePalette(), defaultKindRegistry)
		clean := stripAnsi(lines[0])
		Expect(clean).To(MatchRegexp(`^\[\d{2}:\d{2}:\d{2}\.\d{3}\]`))
	})
})

var _ = Describe("FormatPhaseEnd", func() {
	It("renders result, changed, error, duration", func() {
		tests := []struct {
			name        string
			entry       *LogEntry
			wantContain []string
		}{
			{
				name: "continue result",
				entry: &LogEntry{
					Msg:       "phase end",
					PhasePath: []string{"my-phase"},
					PhaseName: "my-phase",
					Result:    "Continue",
				},
				wantContain: []string{"■", "my-phase", "Continue"},
			},
			{
				name: "fail result with error",
				entry: &LogEntry{
					Msg:       "phase end",
					PhasePath: []string{"my-phase"},
					PhaseName: "my-phase",
					Result:    "Fail",
					HasError:  "true",
					Error:     "something failed",
				},
				wantContain: []string{"■", "my-phase", "Fail", "err=something failed"},
			},
			{
				name: "changed flag",
				entry: &LogEntry{
					Msg:       "phase end",
					PhasePath: []string{"my-phase"},
					PhaseName: "my-phase",
					Result:    "Done",
					Changed:   "true",
				},
				wantContain: []string{"■", "my-phase", "Done", "changed"},
			},
			{
				name: "with duration",
				entry: &LogEntry{
					Msg:       "phase end",
					PhasePath: []string{"my-phase"},
					PhaseName: "my-phase",
					Result:    "Continue",
					Duration:  "1.5s",
				},
				wantContain: []string{"■", "my-phase", "Continue", "1.5s"},
			},
			{
				name: "no parts produces dim output",
				entry: &LogEntry{
					Msg:       "phase end",
					PhasePath: []string{"my-phase"},
					PhaseName: "my-phase",
				},
				wantContain: []string{"■", "my-phase"},
			},
		}

		for _, tt := range tests {
			result := formatPhaseEnd(tt.entry, activePalette())
			clean := stripAnsi(result)
			for _, want := range tt.wantContain {
				Expect(clean).To(ContainSubstring(want), "case %q: want %q in %q", tt.name, want, clean)
			}
		}
	})
})

var _ = Describe("BriefKubeResource", func() {
	It("normal object → kind/name rv=X", func() {
		obj := map[string]any{
			"kind": "ReplicatedVolume",
			"metadata": map[string]any{
				"name":            "test-rv",
				"resourceVersion": "12345",
			},
			"spec":   map[string]any{},
			"status": map[string]any{},
		}

		result := BriefKubeResource(obj)
		Expect(result).To(ContainSubstring("test-rv"))
		Expect(result).To(ContainSubstring("rv=12345"))
	})

	It("Status error → Status 404 Reason target", func() {
		RegisterKind("rv", "ReplicatedVolume")
		obj := map[string]any{
			"kind":     "Status",
			"metadata": map[string]any{},
			"code":     float64(404),
			"reason":   "NotFound",
			"details": map[string]any{
				"kind": "ReplicatedVolume",
				"name": "missing-rv",
			},
		}

		result := BriefKubeResource(obj)
		Expect(result).To(ContainSubstring("Status"))
		Expect(result).To(ContainSubstring("404"))
		Expect(result).To(ContainSubstring("NotFound"))
	})

	It("status subresource → kind/name/status", func() {
		obj := map[string]any{
			"kind": "ReplicatedVolume",
			"metadata": map[string]any{
				"name":            "test-rv",
				"resourceVersion": "99",
			},
			"status": map[string]any{
				"phase": "Healthy",
			},
		}

		result := BriefKubeResource(obj)
		Expect(result).To(ContainSubstring("/status"))
	})

	It("non-k8s object → empty", func() {
		obj := map[string]any{"foo": "bar"}
		Expect(BriefKubeResource(obj)).To(BeEmpty())
	})

	It("exact format for status error", func() {
		RegisterKind("rsc", "ReplicatedStorageClass")
		obj := map[string]any{
			"kind":     "Status",
			"metadata": map[string]any{},
			"code":     float64(404),
			"reason":   "NotFound",
			"details": map[string]any{
				"kind": "ReplicatedStorageClass",
				"name": "my-rsc",
			},
		}
		Expect(BriefKubeResource(obj)).To(Equal("Status 404 NotFound rsc/my-rsc"))
	})
})

var _ = Describe("Hex dump", func() {
	DescribeTable("IsHexDump",
		func(input string, want bool) {
			Expect(IsHexDump(input)).To(Equal(want))
		},
		Entry("valid hex dump line", "00000000  6b 38 73 00  |k8s.|", true),
		Entry("valid with different offset", "0000001f  ab cd ef 01  |....|", true),
		Entry("uppercase hex offset", "AABBCCDD  00 11 22 33  |...3|", true),
		Entry("too short", "short", false),
		Entry("plain text", "not a hex dump at all", false),
		Entry("empty string", "", false),
		Entry("missing second space", "00000000 missing second space", false),
		Entry("regular JSON", `{"apiVersion":"v1","kind":"Pod"}`, false),
	)

	It("ParseHexDump extracts bytes", func() {
		input := []byte("k8s\x00\x0a\x05hello")
		dump := hex.Dump(input)
		got := ParseHexDump(dump)
		Expect(got).To(Equal(input))
	})

	It("ExtractK8sProtobufMeta extracts apiVersion and kind", func() {
		var typeMeta []byte
		typeMeta = append(typeMeta, 0x0a, 0x02)
		typeMeta = append(typeMeta, "v1"...)
		typeMeta = append(typeMeta, 0x12, 0x03)
		typeMeta = append(typeMeta, "Pod"...)

		var data []byte
		data = append(data, "k8s\x00"...)
		data = append(data, 0x0a, byte(len(typeMeta)))
		data = append(data, typeMeta...)

		apiVersion, kind := ExtractK8sProtobufMeta(data)
		Expect(apiVersion).To(Equal("v1"))
		Expect(kind).To(Equal("Pod"))
	})

	DescribeTable("ExtractK8sProtobufMeta invalid",
		func(name string, data []byte) {
			apiVersion, kind := ExtractK8sProtobufMeta(data)
			Expect(apiVersion).To(BeEmpty(), "case: %s", name)
			Expect(kind).To(BeEmpty(), "case: %s", name)
		},
		Entry("empty", "empty", []byte(nil)),
		Entry("wrong magic", "wrong magic", []byte("notk\x00\x00\x00\x00")),
		Entry("truncated after magic", "truncated", []byte("k8s\x00")),
	)
})

var _ = Describe("ProcessExtraValue", func() {
	It("long string truncated", func() {
		long := strings.Repeat("x", 200)
		kv := ProcessExtraValue("mykey", long, "")
		Expect(kv.Key).To(Equal("mykey"))
		Expect(kv.Val).To(Equal(long[:LongValueThreshold] + "…"))
	})

	It("map with k8s resource → brief", func() {
		obj := map[string]any{
			"kind": "ConfigMap",
			"metadata": map[string]any{
				"name": "my-config",
			},
		}
		kv := ProcessExtraValue("obj", obj, "")
		Expect(kv.Key).To(Equal("obj"))
		Expect(kv.Val).To(Equal("ConfigMap/my-config"))
	})
})

var _ = Describe("Utility functions", func() {
	DescribeTable("fmtVal",
		func(input any, want string) {
			Expect(fmtVal(input)).To(Equal(want))
		},
		Entry("string", "hello", "hello"),
		Entry("integer float64", float64(42), "42"),
		Entry("decimal float64", float64(3.14), "3.14"),
		Entry("bool true", true, "true"),
		Entry("bool false", false, "false"),
		Entry("nil", nil, "<nil>"),
	)

	It("sortExtras sorts by key", func() {
		extras := []KV{
			{Key: "zebra", Val: "z"},
			{Key: "apple", Val: "a"},
			{Key: "mango", Val: "m"},
		}
		sortExtras(extras)
		Expect(extras[0].Key).To(Equal("apple"))
		Expect(extras[1].Key).To(Equal("mango"))
		Expect(extras[2].Key).To(Equal("zebra"))
	})

	It("ReconcileIDTracker swap", func() {
		tracker := NewReconcileIDTracker()

		prev, hasPrev := tracker.Swap("key1", "id-a")
		Expect(hasPrev).To(BeFalse())
		Expect(prev).To(BeEmpty())

		prev, hasPrev = tracker.Swap("key1", "id-b")
		Expect(hasPrev).To(BeTrue())
		Expect(prev).To(Equal("id-a"))

		prev, hasPrev = tracker.Swap("key1", "id-b")
		Expect(hasPrev).To(BeTrue())
		Expect(prev).To(Equal("id-b"))
	})

	It("BadgeColor returns correct colors", func() {
		Expect(BadgeColor("controller")).To(Equal(ColorMagenta))
		Expect(BadgeColor("agent")).To(Equal(ColorYellow))
		Expect(BadgeColor("other")).To(Equal(ColorCyan))
	})
})

// Preserve test coverage for ColorizeResult and ShortReconcileID.
var _ = Describe("ColorizeResult", func() {
	DescribeTable("returns colored result text",
		func(input, wantText string) {
			result := colorizeResult(input, activePalette())
			clean := stripAnsi(result)
			Expect(clean).To(Equal(wantText))
		},
		Entry("Continue", "Continue", "Continue"),
		Entry("Done", "Done", "Done"),
		Entry("Fail", "Fail", "Fail"),
		Entry("RequeueAfter", "RequeueAfter", "RequeueAfter"),
		Entry("SomethingElse", "SomethingElse", "SomethingElse"),
	)
})

var _ = Describe("ShortReconcileID", func() {
	It("truncates long IDs to 8 chars", func() {
		Expect(ShortReconcileID("abcdef12-3456-7890")).To(Equal("abcdef12"))
	})

	It("returns short IDs unchanged", func() {
		Expect(ShortReconcileID("short")).To(Equal("short"))
	})
})

var _ = Describe("ParseLogEntry edge cases", func() {
	It("returns nil for truncated JSON", func() {
		Expect(ParseLogEntry(`{"time":"2026-03-12T10:00:00Z","msg":"test"`, "", nil)).To(BeNil())
	})

	It("returns nil for JSON array", func() {
		Expect(ParseLogEntry(`[1,2,3]`, "", nil)).To(BeNil())
	})

	It("returns entry with zero-value fields for empty JSON object", func() {
		e := ParseLogEntry(`{}`, "", nil)
		Expect(e).NotTo(BeNil())
		Expect(e.Msg).To(BeEmpty())
		Expect(e.Level).To(BeEmpty())
	})

	It("strips 'kind source: ' prefix from source field", func() {
		line := `{"time":"2026-03-12T10:00:00Z","level":"INFO","msg":"test","source":"kind source: something"}`
		e := ParseLogEntry(line, "", nil)
		Expect(e).NotTo(BeNil())
		found := false
		for _, kv := range e.Extras {
			if kv.Key == "source" {
				Expect(kv.Val).To(Equal("something"))
				found = true
			}
		}
		Expect(found).To(BeTrue(), "source field should appear in extras")
	})

	It("passes source field without prefix unchanged", func() {
		line := `{"time":"2026-03-12T10:00:00Z","level":"INFO","msg":"test","source":"plain source"}`
		e := ParseLogEntry(line, "", nil)
		Expect(e).NotTo(BeNil())
		for _, kv := range e.Extras {
			if kv.Key == "source" {
				Expect(kv.Val).To(Equal("plain source"))
			}
		}
	})
})

var _ = Describe("ProcessExtraValue dispatch", func() {
	It("short array rendered inline as JSON", func() {
		arr := []any{"a", "b"}
		kv := ProcessExtraValue("items", arr, "")
		Expect(kv.Key).To(Equal("items"))
		Expect(kv.Val).To(Equal(`["a","b"]`))
		Expect(kv.File).To(BeEmpty())
	})

	It("long array saved to file with item count", func() {
		tmpDir := GinkgoT().TempDir()
		arr := make([]any, 50)
		for i := range arr {
			arr[i] = fmt.Sprintf("item-%d-with-some-padding-to-make-it-long", i)
		}
		kv := ProcessExtraValue("biglist", arr, tmpDir)
		Expect(kv.Key).To(Equal("biglist"))
		Expect(kv.Val).To(Equal("[50 items]"))
		Expect(kv.File).NotTo(BeEmpty())
	})

	It("hex dump string dispatches to ProcessHexDumpValue", func() {
		data := []byte("k8s\x00hello")
		dump := hex.Dump(data)
		kv := ProcessExtraValue("body", dump, "")
		Expect(kv.Key).To(Equal("body"))
		Expect(kv.Val).To(ContainSubstring("pb"))
	})

	It("long JSON string parsed as map", func() {
		obj := `{"kind":"ConfigMap","metadata":{"name":"very-long-name-` + strings.Repeat("x", 120) + `"}}`
		kv := ProcessExtraValue("obj", obj, "")
		Expect(kv.Key).To(Equal("obj"))
		Expect(kv.Val).To(ContainSubstring("ConfigMap"))
	})

	It("float value formatted via fmtVal", func() {
		kv := ProcessExtraValue("count", float64(42), "")
		Expect(kv.Val).To(Equal("42"))
	})

	It("bool value formatted via fmtVal", func() {
		kv := ProcessExtraValue("flag", true, "")
		Expect(kv.Val).To(Equal("true"))
	})

	It("nil value formatted via fmtVal", func() {
		kv := ProcessExtraValue("nothing", nil, "")
		Expect(kv.Val).To(Equal("<nil>"))
	})

	It("short string returned as-is", func() {
		kv := ProcessExtraValue("key", "short", "")
		Expect(kv.Val).To(Equal("short"))
	})
})

var _ = Describe("ProcessHexDumpValue", func() {
	It("k8s protobuf returns brief with apiVersion and kind", func() {
		var typeMeta []byte
		typeMeta = append(typeMeta, 0x0a, 0x02)
		typeMeta = append(typeMeta, "v1"...)
		typeMeta = append(typeMeta, 0x12, 0x03)
		typeMeta = append(typeMeta, "Pod"...)
		var data []byte
		data = append(data, "k8s\x00"...)
		data = append(data, 0x0a, byte(len(typeMeta)))
		data = append(data, typeMeta...)

		dump := hex.Dump(data)
		kv := ProcessHexDumpValue("body", dump, "")
		Expect(kv.Val).To(ContainSubstring("pb"))
		Expect(kv.Val).To(ContainSubstring("v1"))
		Expect(kv.Val).To(ContainSubstring("bytes"))
	})

	It("non-protobuf binary returns binary N bytes", func() {
		data := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x01, 0x02, 0x03}
		dump := hex.Dump(data)
		kv := ProcessHexDumpValue("body", dump, "")
		Expect(kv.Val).To(Equal(fmt.Sprintf("binary %d bytes", len(data))))
	})

	It("empty hex dump returns hexdump N chars", func() {
		kv := ProcessHexDumpValue("body", "00000000  \n", "")
		Expect(kv.Val).To(HavePrefix("hexdump"))
	})
})

var _ = Describe("ProcessMapValue", func() {
	It("non-k8s short map rendered as compact JSON", func() {
		obj := map[string]any{"a": "b"}
		kv := ProcessMapValue("data", obj, "")
		Expect(kv.Val).To(Equal(`{"a":"b"}`))
		Expect(kv.File).To(BeEmpty())
	})

	It("non-k8s large map saved to file", func() {
		tmpDir := GinkgoT().TempDir()
		obj := make(map[string]any)
		for i := 0; i < 50; i++ {
			obj[fmt.Sprintf("key-%d-with-padding", i)] = fmt.Sprintf("value-%d-with-long-padding-text", i)
		}
		kv := ProcessMapValue("data", obj, tmpDir)
		Expect(kv.Val).To(ContainSubstring("fields"))
		Expect(kv.File).NotTo(BeEmpty())
	})
})

var _ = Describe("FormatExtras", func() {
	It("returns empty for no extras", func() {
		e := &LogEntry{}
		Expect(formatExtras(e, activePalette())).To(BeEmpty())
	})

	It("formats key=value pairs", func() {
		e := &LogEntry{
			Extras: []KV{
				{Key: "alpha", Val: "one"},
				{Key: "beta", Val: "two"},
			},
		}
		result := formatExtras(e, activePalette())
		clean := stripAnsi(result)
		Expect(clean).To(ContainSubstring("alpha=one"))
		Expect(clean).To(ContainSubstring("beta=two"))
	})

	It("wraps file values in OSC8 links", func() {
		e := &LogEntry{
			Extras: []KV{
				{Key: "body", Val: "summary", File: "/tmp/test.json"},
			},
		}
		result := formatExtras(e, activePalette())
		Expect(result).To(ContainSubstring("\033]8;;"))
		Expect(result).To(ContainSubstring("summary"))
	})
})

var _ = Describe("FormatMessage", func() {
	It("non-phase message with phasePath, error, and extras", func() {
		e := &LogEntry{
			Msg:       "doing work",
			PhasePath: []string{"parent", "child"},
			Error:     "oops",
			Extras:    []KV{{Key: "extra", Val: "val"}},
		}
		result := formatMessage(e, activePalette())
		clean := stripAnsi(result)
		Expect(clean).To(ContainSubstring("parent › child"))
		Expect(clean).To(ContainSubstring("doing work"))
		Expect(clean).To(ContainSubstring(`err="oops"`))
		Expect(clean).To(ContainSubstring("extra=val"))
	})

	It("plain message with no extras or path", func() {
		e := &LogEntry{Msg: "simple"}
		result := formatMessage(e, activePalette())
		Expect(stripAnsi(result)).To(Equal("simple"))
	})

	It("dispatches phase start message", func() {
		e := &LogEntry{
			Msg:       "phase start",
			PhasePath: []string{"p"},
			PhaseName: "p",
		}
		result := formatMessage(e, activePalette())
		Expect(stripAnsi(result)).To(ContainSubstring("▶ p"))
	})

	It("dispatches phase end message", func() {
		e := &LogEntry{
			Msg:       "phase end",
			PhasePath: []string{"p"},
			PhaseName: "p",
			Result:    "Done",
		}
		result := formatMessage(e, activePalette())
		Expect(stripAnsi(result)).To(ContainSubstring("■ p"))
		Expect(stripAnsi(result)).To(ContainSubstring("Done"))
	})
})

var _ = Describe("FormatLogEntryTracked", func() {
	It("emits exact reconcile boundary separator format", func() {
		tracker := NewReconcileIDTracker()
		RegisterKind("rv", "ReplicatedVolume")

		e1 := &LogEntry{
			Time:        "2026-03-12T10:00:00Z",
			Level:       "INFO",
			Msg:         "step-1",
			Controller:  "rv-ctrl",
			Kind:        "ReplicatedVolume",
			Name:        "obj-a",
			ReconcileID: "aaaa1111-0000-0000-0000-000000000000",
		}
		_ = FormatLogEntryTracked(e1, "controller", tracker)

		e2 := &LogEntry{
			Time:        "2026-03-12T10:00:01Z",
			Level:       "INFO",
			Msg:         "step-2",
			Controller:  "rv-ctrl",
			Kind:        "ReplicatedVolume",
			Name:        "obj-a",
			ReconcileID: "bbbb2222-0000-0000-0000-000000000000",
		}
		lines := FormatLogEntryTracked(e2, "controller", tracker)

		Expect(len(lines)).To(BeNumerically(">=", 2))
		separator := stripAnsi(lines[0])
		Expect(separator).To(Equal("────────── reconcile aaaa1111 done ──────────"))
	})

	It("does not emit separator for first entry", func() {
		tracker := NewReconcileIDTracker()
		e := &LogEntry{
			Time:        "2026-03-12T10:00:00Z",
			Level:       "INFO",
			Msg:         "hello",
			Controller:  "rv-ctrl",
			Kind:        "ReplicatedVolume",
			Name:        "obj-a",
			ReconcileID: "aaaa1111",
		}
		lines := FormatLogEntryTracked(e, "controller", tracker)
		Expect(lines).To(HaveLen(1))
		Expect(stripAnsi(lines[0])).NotTo(ContainSubstring("────────── reconcile"))
	})

	It("does not emit separator when reconcileID unchanged", func() {
		tracker := NewReconcileIDTracker()
		e := &LogEntry{
			Time:        "2026-03-12T10:00:00Z",
			Level:       "INFO",
			Msg:         "step",
			Controller:  "rv-ctrl",
			Kind:        "ReplicatedVolume",
			Name:        "obj-a",
			ReconcileID: "same-id",
		}
		_ = FormatLogEntryTracked(e, "controller", tracker)
		lines := FormatLogEntryTracked(e, "controller", tracker)
		Expect(lines).To(HaveLen(1))
	})
})

var _ = Describe("fmtVal edge cases", func() {
	It("truncates string longer than 200 chars", func() {
		long := strings.Repeat("x", 250)
		result := fmtVal(long)
		Expect(result).To(HaveLen(203))
		Expect(result).To(HaveSuffix("..."))
	})

	It("formats complex type via JSON marshal", func() {
		val := []any{"a", float64(1)}
		result := fmtVal(val)
		Expect(result).To(Equal(`["a",1]`))
	})

	It("truncates long JSON-marshaled complex type", func() {
		arr := make([]any, 200)
		for i := range arr {
			arr[i] = fmt.Sprintf("item-%d-with-long-text-padding-here", i)
		}
		result := fmtVal(arr)
		Expect(len(result)).To(BeNumerically("<=", 203))
		Expect(result).To(HaveSuffix("..."))
	})
})

var _ = Describe("OSC8Link", func() {
	It("returns text unchanged for empty path", func() {
		Expect(OSC8Link("hello", "")).To(Equal("hello"))
	})

	It("wraps text in well-formed OSC8 escape sequence", func() {
		result := OSC8Link("click me", "/tmp/file.txt")
		Expect(result).To(Equal("\033]8;;file:///tmp/file.txt\033\\click me\033]8;;\033\\"))
	})
})

var _ = Describe("SaveLogBody", func() {
	It("returns empty for empty snapshotsDir", func() {
		Expect(SaveLogBody("", "key", []byte("data"))).To(BeEmpty())
	})

	It("writes file and returns absolute path", func() {
		tmpDir := GinkgoT().TempDir()
		path := SaveLogBody(tmpDir, "mykey", []byte(`{"some":"data"}`))
		Expect(path).NotTo(BeEmpty())
		Expect(filepath.IsAbs(path)).To(BeTrue())
		content, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(content)).To(Equal(`{"some":"data"}`))
	})

	It("returns empty for non-existent directory", func() {
		path := SaveLogBody("/nonexistent/dir/nowhere", "key", []byte("data"))
		Expect(path).To(BeEmpty())
	})
})

var _ = Describe("BriefKubeResource extras", func() {
	It("includes namespace in output", func() {
		obj := map[string]any{
			"kind": "Pod",
			"metadata": map[string]any{
				"name":      "my-pod",
				"namespace": "kube-system",
			},
			"spec": map[string]any{},
		}
		result := BriefKubeResource(obj)
		Expect(result).To(Equal("Pod/kube-system/my-pod"))
	})

	It("Status with no code/reason/details returns bare Status", func() {
		obj := map[string]any{
			"kind":     "Status",
			"metadata": map[string]any{},
		}
		Expect(BriefKubeResource(obj)).To(Equal("Status"))
	})

	It("returns empty for kind with no metadata", func() {
		obj := map[string]any{
			"kind": "Pod",
		}
		Expect(BriefKubeResource(obj)).To(BeEmpty())
	})

	It("kind with no name returns just kind", func() {
		obj := map[string]any{
			"kind":     "Pod",
			"metadata": map[string]any{},
			"spec":     map[string]any{},
		}
		Expect(BriefKubeResource(obj)).To(Equal("Pod"))
	})
})

var _ = Describe("kindRegistry", func() {
	It("FullKindFor returns full kind for registered short name", func() {
		RegisterKind("rv", "ReplicatedVolume")
		Expect(FullKindFor("rv")).To(Equal("ReplicatedVolume"))
	})

	It("FullKindFor returns input for unknown short name", func() {
		Expect(FullKindFor("unknown-short")).To(Equal("unknown-short"))
	})

	It("ShortKindFor returns input for unknown full kind", func() {
		Expect(ShortKindFor("NeverRegisteredKind")).To(Equal("NeverRegisteredKind"))
	})

	It("roundtrip RegisterKind→ShortKindFor→FullKindFor", func() {
		RegisterKind("drbdr", "DRBDReplicatedVolumeReplica")
		short := ShortKindFor("DRBDReplicatedVolumeReplica")
		Expect(short).To(Equal("drbdr"))
		full := FullKindFor("drbdr")
		Expect(full).To(Equal("DRBDReplicatedVolumeReplica"))
	})

	It("concurrent RegisterKind + ShortKindFor + FullKindFor without race", func() {
		const goroutines = 10
		var wg sync.WaitGroup
		wg.Add(goroutines * 2)
		for i := 0; i < goroutines; i++ {
			go func(n int) {
				defer wg.Done()
				short := fmt.Sprintf("kind%d", n)
				full := fmt.Sprintf("FullKind%d", n)
				RegisterKind(short, full)
			}(i)
			go func(n int) {
				defer wg.Done()
				short := fmt.Sprintf("kind%d", n)
				full := fmt.Sprintf("FullKind%d", n)
				_ = ShortKindFor(full)
				_ = FullKindFor(short)
			}(i)
		}
		wg.Wait()
	})
})

var _ = Describe("DisableColors", func() {
	var savedColors struct {
		Enabled                                                             bool
		Red, Green, Yellow, Cyan, Magenta, Dim, Bold, BoldRed, BoldGreen, R string
	}

	BeforeEach(func() {
		savedColors.Enabled = ColorsEnabled
		savedColors.Red = ColorRed
		savedColors.Green = ColorGreen
		savedColors.Yellow = ColorYellow
		savedColors.Cyan = ColorCyan
		savedColors.Magenta = ColorMagenta
		savedColors.Dim = ColorDim
		savedColors.Bold = ColorBold
		savedColors.BoldRed = ColorBoldRed
		savedColors.BoldGreen = ColorBoldGreen
		savedColors.R = ColorReset
	})

	AfterEach(func() {
		ColorsEnabled = savedColors.Enabled
		ColorRed = savedColors.Red
		ColorGreen = savedColors.Green
		ColorYellow = savedColors.Yellow
		ColorCyan = savedColors.Cyan
		ColorMagenta = savedColors.Magenta
		ColorDim = savedColors.Dim
		ColorBold = savedColors.Bold
		ColorBoldRed = savedColors.BoldRed
		ColorBoldGreen = savedColors.BoldGreen
		ColorReset = savedColors.R
	})

	It("clears all color variables", func() {
		ColorsEnabled = true
		ColorRed = "\033[31m"
		ColorGreen = "\033[32m"

		DisableColors()

		Expect(ColorsEnabled).To(BeFalse())
		Expect(ColorRed).To(BeEmpty())
		Expect(ColorGreen).To(BeEmpty())
		Expect(ColorYellow).To(BeEmpty())
		Expect(ColorCyan).To(BeEmpty())
		Expect(ColorMagenta).To(BeEmpty())
		Expect(ColorDim).To(BeEmpty())
		Expect(ColorBold).To(BeEmpty())
		Expect(ColorBoldRed).To(BeEmpty())
		Expect(ColorBoldGreen).To(BeEmpty())
		Expect(ColorReset).To(BeEmpty())
	})
})

var _ = Describe("ReconcileIDTracker concurrency", func() {
	It("concurrent Swap from multiple goroutines without race", func() {
		tracker := NewReconcileIDTracker()
		const goroutines = 10
		const iterations = 100

		var wg sync.WaitGroup
		wg.Add(goroutines)
		for g := 0; g < goroutines; g++ {
			go func(id int) {
				defer wg.Done()
				key := fmt.Sprintf("ctrl\x00obj-%d", id%3)
				for i := 0; i < iterations; i++ {
					tracker.Swap(key, fmt.Sprintf("id-%d-%d", id, i))
				}
			}(g)
		}
		wg.Wait()
	})
})
