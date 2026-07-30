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
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const testLayoutMetric = "sds_rv_membership_layout_converged"

// stubScraper is the metricsScraper used by the unit tests: it answers from a
// queue of canned responses and records how many times it was called, so a
// polling helper can be observed without a cluster.
type stubScraper struct {
	responses [][]string
	errs      []error
	calls     int
}

func (s *stubScraper) ScrapeControllerMetrics(_ context.Context) ([]string, error) {
	i := s.calls
	s.calls++
	if i >= len(s.responses) {
		i = len(s.responses) - 1
	}
	var err error
	if i < len(s.errs) {
		err = s.errs[i]
	}
	if err != nil {
		return nil, err
	}
	return s.responses[i], nil
}

var _ = Describe("parseMetricSamples", func() {
	It("returns nothing for an empty response", func() {
		samples, err := parseMetricSamples("", testLayoutMetric)
		Expect(err).NotTo(HaveOccurred())
		Expect(samples).To(BeEmpty())
	})

	It("returns nothing when the metric is absent from a non-empty response", func() {
		text := "# HELP sds_rv_count Current number of ReplicatedVolume objects.\n" +
			"# TYPE sds_rv_count gauge\n" +
			`sds_rv_count{storage_class="e2e",phase="Active"} 3` + "\n"
		samples, err := parseMetricSamples(text, testLayoutMetric)
		Expect(err).NotTo(HaveOccurred())
		Expect(samples).To(BeEmpty())
	})

	It("parses several samples of the metric and ignores the rest of the response", func() {
		text := "# HELP " + testLayoutMetric + " layout convergence.\n" +
			"# TYPE " + testLayoutMetric + " gauge\n" +
			`sds_rv_count{storage_class="e2e",phase="Active"} 2` + "\n" +
			testLayoutMetric + `{name="rv-a",reason="Converged"} 1` + "\n" +
			testLayoutMetric + `{name="rv-b",reason="TransitionUnsupported"} 0` + "\n"
		samples, err := parseMetricSamples(text, testLayoutMetric)
		Expect(err).NotTo(HaveOccurred())
		Expect(samples).To(HaveLen(2))
		Expect(samples[0]).To(Equal(MetricSample{
			Name:   testLayoutMetric,
			Labels: map[string]string{"name": "rv-a", "reason": "Converged"},
			Value:  1,
		}))
		Expect(samples[1].Labels).To(Equal(map[string]string{"name": "rv-b", "reason": "TransitionUnsupported"}))
		Expect(samples[1].Value).To(Equal(float64(0)))
	})

	It("does not confuse a longer metric name with the requested one", func() {
		text := "sds_rv_count 7\nsds_rv_count_total 9\n"
		samples, err := parseMetricSamples(text, "sds_rv_count")
		Expect(err).NotTo(HaveOccurred())
		Expect(samples).To(HaveLen(1))
		Expect(samples[0].Value).To(Equal(float64(7)))
	})

	It("accepts a sample without labels and an optional timestamp", func() {
		samples, err := parseMetricSamples("sds_rv_count 4 1700000000000\n", "sds_rv_count")
		Expect(err).NotTo(HaveOccurred())
		Expect(samples).To(HaveLen(1))
		Expect(samples[0].Labels).To(BeEmpty())
		Expect(samples[0].Value).To(Equal(float64(4)))
	})

	It("unescapes quotes and backslashes in a label value", func() {
		samples, err := parseMetricSamples(testLayoutMetric+`{name="a\"b\\c"} 0`, testLayoutMetric)
		Expect(err).NotTo(HaveOccurred())
		Expect(samples).To(HaveLen(1))
		Expect(samples[0].Labels["name"]).To(Equal(`a"b\c`))
	})

	// A truncated response must never look like "the series is absent": that
	// would turn a broken scrape into a passing assertion.
	It("fails on a truncated label set", func() {
		_, err := parseMetricSamples(testLayoutMetric+`{name="rv-a",reason="Converged"`, testLayoutMetric)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not terminated"))
	})

	It("fails on a sample line without a value", func() {
		_, err := parseMetricSamples(testLayoutMetric+`{name="rv-a"}`, testLayoutMetric)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no value"))
	})

	It("fails on an unreadable value", func() {
		_, err := parseMetricSamples(testLayoutMetric+`{name="rv-a"} not-a-number`, testLayoutMetric)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("parsing the value"))
	})

	It("fails on an unquoted label value", func() {
		_, err := parseMetricSamples(testLayoutMetric+`{name=rv-a} 0`, testLayoutMetric)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not quoted"))
	})
})

var _ = Describe("selectMetricSamples", func() {
	samples := []MetricSample{
		{Name: testLayoutMetric, Labels: map[string]string{"name": "rv-a", "reason": "Converged"}, Value: 1},
		{Name: testLayoutMetric, Labels: map[string]string{"name": "rv-b", "reason": "TransitionUnsupported"}, Value: 0},
		{Name: testLayoutMetric, Labels: map[string]string{"name": "rv-b", "reason": "Converging"}, Value: 0},
	}

	It("selects by the full label set", func() {
		got := selectMetricSamples(samples, map[string]string{"name": "rv-b", "reason": "Converging"})
		Expect(got).To(HaveLen(1))
		Expect(got[0].Labels["reason"]).To(Equal("Converging"))
	})

	It("selects every sample matching a partial label set", func() {
		Expect(selectMetricSamples(samples, map[string]string{"name": "rv-b"})).To(HaveLen(2))
	})

	It("returns nothing when a label value differs", func() {
		Expect(selectMetricSamples(samples, map[string]string{"name": "rv-c"})).To(BeEmpty())
	})

	It("returns everything for an empty selector", func() {
		Expect(selectMetricSamples(samples, nil)).To(HaveLen(3))
	})
})

var _ = Describe("awaitControllerMetric", func() {
	converged := testLayoutMetric + `{name="rv-a",reason="Converged"} 1`
	degraded := testLayoutMetric + `{name="rv-a",reason="TransitionUnsupported"} 0`

	It("returns as soon as every pod reports the expected value", func(ctx SpecContext) {
		s := &stubScraper{responses: [][]string{{degraded, degraded}}}
		err := awaitControllerMetric(ctx, s, time.Millisecond,
			testLayoutMetric, map[string]string{"name": "rv-a", "reason": "TransitionUnsupported"}, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.calls).To(Equal(1))
	})

	It("keeps polling until the series appears", func(ctx SpecContext) {
		s := &stubScraper{responses: [][]string{{""}, {""}, {degraded}}}
		err := awaitControllerMetric(ctx, s, time.Millisecond,
			testLayoutMetric, map[string]string{"name": "rv-a", "reason": "TransitionUnsupported"}, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.calls).To(Equal(3))
	})

	It("retries a scrape error instead of failing on it", func(ctx SpecContext) {
		s := &stubScraper{
			responses: [][]string{nil, {degraded}},
			errs:      []error{errors.New("controller pod is restarting"), nil},
		}
		err := awaitControllerMetric(ctx, s, time.Millisecond,
			testLayoutMetric, map[string]string{"name": "rv-a", "reason": "TransitionUnsupported"}, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.calls).To(Equal(2))
	})

	It("times out while one pod still disagrees, and says so", func() {
		// One replica already reports the degraded series, the other still reports
		// the converged one: `max by (...)` would collapse them, so the helper must
		// not accept a partial agreement.
		s := &stubScraper{responses: [][]string{{degraded, converged}}}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		err := awaitControllerMetric(ctx, s, time.Millisecond,
			testLayoutMetric, map[string]string{"name": "rv-a"}, 0)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("timed out"))
		Expect(err.Error()).To(ContainSubstring("got " + testLayoutMetric))
	})

	It("times out when the series is never exported, and says so", func() {
		s := &stubScraper{responses: [][]string{{""}}}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		err := awaitControllerMetric(ctx, s, time.Millisecond,
			testLayoutMetric, map[string]string{"name": "rv-a"}, 0)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no " + testLayoutMetric + " sample"))
	})

	It("reports the last scrape error when the deadline wins", func() {
		s := &stubScraper{responses: [][]string{nil}, errs: []error{errors.New("proxy refused")}}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		err := awaitControllerMetric(ctx, s, time.Millisecond, testLayoutMetric, nil, 0)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("proxy refused"))
	})

	It("fails on a malformed response instead of waiting it out silently", func() {
		s := &stubScraper{responses: [][]string{{testLayoutMetric + `{name="rv-a"} broken`}}}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		err := awaitControllerMetric(ctx, s, time.Millisecond, testLayoutMetric, nil, 0)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("parsing " + testLayoutMetric))
	})
})
