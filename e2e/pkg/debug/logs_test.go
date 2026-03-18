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
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("processLogStream", func() {
	var (
		buf     bytes.Buffer
		em      *WriterEmitter
		filter  *Filter
		tracker *ReconcileIDTracker
		ls      *LogStreamer
	)

	BeforeEach(func() {
		buf.Reset()
		em = NewWriterEmitter(&buf, nil)
		filter = NewFilter()
		tracker = NewReconcileIDTracker()
		ls = &LogStreamer{
			emitter:      em,
			formatter:    &ColorFormatter{kr: defaultKindRegistry},
			filter:       filter,
			snapshotsDir: "",
		}
	})

	It("emits formatted output for valid JSON", func() {
		RegisterKind("rv", "ReplicatedVolume")
		filter.AddKind("rv")

		input := `{"time":"2026-03-12T10:00:00Z","level":"INFO","msg":"reconcile","controller":"rv-controller","controllerKind":"ReplicatedVolume","name":"test-rv","reconcileID":"aabbccdd"}` + "\n"

		got := ls.processLogStream(context.Background(), strings.NewReader(input), "controller", tracker)
		Expect(got).To(BeTrue())

		output := buf.String()
		Expect(output).To(ContainSubstring("test-rv"))
		Expect(output).To(ContainSubstring("reconcile"))
	})

	It("emits raw text for non-JSON", func() {
		input := "this is not json at all\n"

		got := ls.processLogStream(context.Background(), strings.NewReader(input), "controller", tracker)
		Expect(got).To(BeTrue())

		output := buf.String()
		Expect(output).To(ContainSubstring("this is not json at all"))
	})

	It("skips entries rejected by filter", func() {
		filter.AddKind("rsc")

		input := `{"time":"2026-03-12T10:00:00Z","level":"INFO","msg":"reconcile","controllerKind":"ReplicatedVolume","name":"test-rv"}` + "\n"

		ls.processLogStream(context.Background(), strings.NewReader(input), "controller", tracker)

		output := buf.String()
		Expect(output).NotTo(ContainSubstring("test-rv"))
	})

	It("returns false for empty-only lines", func() {
		input := "\n\n\n"

		got := ls.processLogStream(context.Background(), strings.NewReader(input), "controller", tracker)
		Expect(got).To(BeFalse())
		Expect(buf.Len()).To(Equal(0))
	})

	It("handles cancelled context", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		input := `{"time":"2026-03-12T10:00:00Z","level":"INFO","msg":"test"}` + "\n"
		ls.processLogStream(ctx, strings.NewReader(input), "controller", tracker)
	})

	It("passes global lifecycle messages", func() {
		input := `{"time":"2026-03-12T10:00:00Z","level":"INFO","msg":"Starting Controller"}` + "\n"

		ls.processLogStream(context.Background(), strings.NewReader(input), "controller", tracker)

		output := buf.String()
		Expect(output).To(ContainSubstring("Starting Controller"))
	})

	It("detects reconcile boundary on ID change", func() {
		RegisterKind("rv", "ReplicatedVolume")
		filter.AddKind("rv")

		lines := []string{
			`{"time":"2026-03-12T10:00:00Z","level":"INFO","msg":"reconcile","controller":"rv-ctrl","controllerKind":"ReplicatedVolume","name":"test","reconcileID":"aaaa1111"}`,
			`{"time":"2026-03-12T10:00:01Z","level":"INFO","msg":"reconcile","controller":"rv-ctrl","controllerKind":"ReplicatedVolume","name":"test","reconcileID":"bbbb2222"}`,
		}
		input := strings.Join(lines, "\n") + "\n"

		ls.processLogStream(context.Background(), strings.NewReader(input), "controller", tracker)

		output := buf.String()
		Expect(output).To(ContainSubstring("reconcile"))
	})

	It("respects dynamic filter changes", func() {
		RegisterKind("rv", "ReplicatedVolume")

		line := `{"time":"2026-03-12T10:00:00Z","level":"INFO","msg":"reconcile","controller":"rv-controller","controllerKind":"ReplicatedVolume","name":"test-1","reconcileID":"aaaa1111"}` + "\n"

		ls.processLogStream(context.Background(), strings.NewReader(line), "controller", tracker)
		Expect(buf.String()).NotTo(ContainSubstring("test-1"))

		filter.AddName("rv", "test-1")
		buf.Reset()

		ls.processLogStream(context.Background(), strings.NewReader(line), "controller", tracker)
		Expect(buf.String()).To(ContainSubstring("test-1"))
	})

	It("handles multiple controllers independently", func() {
		RegisterKind("rv", "ReplicatedVolume")
		RegisterKind("rsc", "ReplicatedStorageClass")
		filter.AddKind("rv")
		filter.AddKind("rsc")

		lines := []string{
			`{"time":"2026-03-12T10:00:00Z","level":"INFO","msg":"step-1","controller":"rv-ctrl","controllerKind":"ReplicatedVolume","name":"obj-a","reconcileID":"r1"}`,
			`{"time":"2026-03-12T10:00:01Z","level":"INFO","msg":"step-1","controller":"rsc-ctrl","controllerKind":"ReplicatedStorageClass","name":"obj-b","reconcileID":"s1"}`,
			`{"time":"2026-03-12T10:00:02Z","level":"INFO","msg":"step-2","controller":"rv-ctrl","controllerKind":"ReplicatedVolume","name":"obj-a","reconcileID":"r2"}`,
			`{"time":"2026-03-12T10:00:03Z","level":"INFO","msg":"step-2","controller":"rsc-ctrl","controllerKind":"ReplicatedStorageClass","name":"obj-b","reconcileID":"s1"}`,
		}
		input := strings.Join(lines, "\n") + "\n"

		ls.processLogStream(context.Background(), strings.NewReader(input), "controller", tracker)

		output := buf.String()
		for _, msg := range []string{"step-1", "step-2"} {
			Expect(output).To(ContainSubstring(msg))
		}
		Expect(output).To(ContainSubstring("reconcile r1 done"))
		Expect(output).NotTo(ContainSubstring("reconcile s1 done"))
	})
})

var _ = Describe("processLogStream malformed input", func() {
	var (
		buf     bytes.Buffer
		em      *WriterEmitter
		filter  *Filter
		tracker *ReconcileIDTracker
		ls      *LogStreamer
	)

	BeforeEach(func() {
		buf.Reset()
		em = NewWriterEmitter(&buf, nil)
		filter = NewFilter()
		tracker = NewReconcileIDTracker()
		ls = &LogStreamer{
			emitter:      em,
			formatter:    &ColorFormatter{kr: defaultKindRegistry},
			filter:       filter,
			snapshotsDir: "",
		}
	})

	It("truncated JSON emitted as raw text, next valid JSON still parsed", func() {
		RegisterKind("rv", "ReplicatedVolume")
		filter.AddKind("rv")

		lines := []string{
			`{"time":"2026-03-12T10:00:00Z","msg":"trunc`,
			`{"time":"2026-03-12T10:00:01Z","level":"INFO","msg":"valid","controller":"rv-ctrl","controllerKind":"ReplicatedVolume","name":"obj","reconcileID":"aa11"}`,
		}
		input := strings.Join(lines, "\n") + "\n"

		got := ls.processLogStream(context.Background(), strings.NewReader(input), "controller", tracker)
		Expect(got).To(BeTrue())

		output := buf.String()
		Expect(output).To(ContainSubstring("trunc"))
		Expect(output).To(ContainSubstring("valid"))
	})

	It("JSON array emitted as raw text", func() {
		input := "[1,2,3]\n"
		got := ls.processLogStream(context.Background(), strings.NewReader(input), "controller", tracker)
		Expect(got).To(BeTrue())
		Expect(buf.String()).To(ContainSubstring("[1,2,3]"))
	})

	It("reconcile boundary separator contains box-drawing characters", func() {
		RegisterKind("rv", "ReplicatedVolume")
		filter.AddKind("rv")

		lines := []string{
			`{"time":"2026-03-12T10:00:00Z","level":"INFO","msg":"s1","controller":"rv-ctrl","controllerKind":"ReplicatedVolume","name":"obj","reconcileID":"aaaa1111-0000-0000-0000-000000000000"}`,
			`{"time":"2026-03-12T10:00:01Z","level":"INFO","msg":"s2","controller":"rv-ctrl","controllerKind":"ReplicatedVolume","name":"obj","reconcileID":"bbbb2222-0000-0000-0000-000000000000"}`,
		}
		input := strings.Join(lines, "\n") + "\n"

		ls.processLogStream(context.Background(), strings.NewReader(input), "controller", tracker)

		output := stripAnsi(buf.String())
		Expect(output).To(ContainSubstring("──────────"))
		Expect(output).To(ContainSubstring("reconcile aaaa1111 done"))
	})
})

var _ = Describe("processLogStream filter+reconcile boundary", func() {
	It("draws boundary correctly when intermediate entries are filtered out", func() {
		var buf bytes.Buffer
		em := NewWriterEmitter(&buf, nil)
		filter := NewFilter()
		tracker := NewReconcileIDTracker()
		ls := &LogStreamer{
			emitter:      em,
			formatter:    &ColorFormatter{kr: defaultKindRegistry},
			filter:       filter,
			snapshotsDir: "",
		}

		RegisterKind("rv", "ReplicatedVolume")
		RegisterKind("rsc", "ReplicatedStorageClass")
		filter.AddKind("rv")

		// Entry A: rv with reconcileID=1 (passes filter)
		// Entry B: rsc with reconcileID=2 (filtered out — different kind)
		// Entry C: rv with reconcileID=3 (passes filter, should draw boundary for ID 1→3)
		lines := []string{
			`{"time":"2026-03-12T10:00:00Z","level":"INFO","msg":"step-a","controller":"rv-ctrl","controllerKind":"ReplicatedVolume","name":"obj","reconcileID":"id-1"}`,
			`{"time":"2026-03-12T10:00:01Z","level":"INFO","msg":"step-b","controller":"rsc-ctrl","controllerKind":"ReplicatedStorageClass","name":"obj-b","reconcileID":"id-2"}`,
			`{"time":"2026-03-12T10:00:02Z","level":"INFO","msg":"step-c","controller":"rv-ctrl","controllerKind":"ReplicatedVolume","name":"obj","reconcileID":"id-3"}`,
		}
		input := strings.Join(lines, "\n") + "\n"

		ls.processLogStream(context.Background(), strings.NewReader(input), "controller", tracker)

		output := stripAnsi(buf.String())
		Expect(output).To(ContainSubstring("step-a"))
		Expect(output).NotTo(ContainSubstring("step-b"), "filtered entry should not appear")
		Expect(output).To(ContainSubstring("step-c"))
		Expect(output).To(ContainSubstring("reconcile id-1 done"), "boundary should mark previous reconcile as done")
	})
})

var _ = Describe("sleepCtx", func() {
	It("returns immediately on cancelled context", func() {
		ctx, cancel := context.WithCancel(context.Background())

		done := make(chan struct{})
		go func() {
			sleepCtx(ctx, 10*time.Second)
			close(done)
		}()

		cancel()

		select {
		case <-done:
		case <-time.After(1 * time.Second):
			Fail("sleepCtx should return immediately on context cancellation")
		}
	})

	It("sleeps for short duration", func() {
		start := time.Now()
		sleepCtx(context.Background(), 10*time.Millisecond)
		elapsed := time.Since(start)

		Expect(elapsed).To(BeNumerically("<", 1*time.Second))
	})
})

var _ = Describe("clampDuration", func() {
	DescribeTable("clamps correctly",
		func(input, maximum, expected time.Duration) {
			Expect(clampDuration(input, maximum)).To(Equal(expected))
		},
		Entry("below max", 5*time.Second, 30*time.Second, 5*time.Second),
		Entry("above max", 60*time.Second, 30*time.Second, 30*time.Second),
		Entry("equal to max", 30*time.Second, 30*time.Second, 30*time.Second),
	)
})

var _ = Describe("ReconcileIDTracker", func() {
	var tracker *ReconcileIDTracker

	BeforeEach(func() {
		tracker = NewReconcileIDTracker()
	})

	It("first swap has no previous", func() {
		key := "ctrl\x00name"
		prevID, hasPrev := tracker.Swap(key, "aaa")
		Expect(hasPrev).To(BeFalse())
		Expect(prevID).To(BeEmpty())
	})

	It("detects ID change", func() {
		key := "ctrl\x00name"
		tracker.Swap(key, "aaa")

		prevID, hasPrev := tracker.Swap(key, "bbb")
		Expect(hasPrev).To(BeTrue())
		Expect(prevID).To(Equal("aaa"))
	})

	It("same ID reports no boundary", func() {
		key := "ctrl\x00name"
		tracker.Swap(key, "aaa")

		prevID, hasPrev := tracker.Swap(key, "aaa")
		Expect(hasPrev).To(BeTrue())
		Expect(prevID).To(Equal("aaa"))
	})
})
