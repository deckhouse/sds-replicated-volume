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
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Where the controller publishes its Prometheus metrics. The values mirror the
// shipped manifests — templates/controller/service-metrics.yaml (port 4272,
// selector app=controller) and templates/controller/servicemonitor.yaml (scheme
// http, path /metrics) — so a spec reading this endpoint sees exactly the series
// Prometheus scrapes, and therefore exactly the series the alert rules in
// monitoring/prometheus-rules are written against.
const (
	controllerMetricsNamespace = "d8-sds-replicated-volume"
	controllerMetricsSelector  = "app=controller"
	controllerMetricsScheme    = "http"
	controllerMetricsPort      = "4272"
	controllerMetricsPath      = "/metrics"
)

// controllerMetricsPollInterval is how often AwaitControllerMetric re-scrapes.
// The metric is built from the controller cache at scrape time, so the only
// thing being waited out is the cache catching up with a status write.
const controllerMetricsPollInterval = 2 * time.Second

// MetricSample is one sample of a Prometheus metric: the metric name, the label
// set the sample carries, and its value.
type MetricSample struct {
	Name   string
	Labels map[string]string
	Value  float64
}

// String renders the sample the way it appears on /metrics, with the labels in
// a stable order so failure messages are diffable.
func (s MetricSample) String() string {
	keys := make([]string, 0, len(s.Labels))
	for k := range s.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, fmt.Sprintf("%s=%q", k, s.Labels[k]))
	}
	return fmt.Sprintf("%s{%s} %g", s.Name, strings.Join(pairs, ","), s.Value)
}

// metricsScraper is the seam through which framework helpers read the
// controller's /metrics endpoint. The production implementation proxies the
// request through the API server; helper unit tests substitute a stub, so the
// parsing, the selection and the polling are exercised without a cluster.
type metricsScraper interface {
	// ScrapeControllerMetrics returns the raw Prometheus exposition text of every
	// running controller pod, ordered by pod name.
	ScrapeControllerMetrics(ctx context.Context) ([]string, error)
}

// scraper returns the metrics scraper in use: the stub injected by a unit test,
// or the pod-proxy implementation.
func (f *Framework) scraper() metricsScraper {
	if f.metricsScrape != nil {
		return f.metricsScrape
	}
	return podProxyScraper{f: f}
}

// podProxyScraper implements metricsScraper through the API server's
// pods/proxy subresource — the same path `kubectl get --raw` would take, so it
// needs no route from the test host to the pod network.
type podProxyScraper struct {
	f *Framework
}

func (s podProxyScraper) ScrapeControllerMetrics(ctx context.Context) ([]string, error) {
	pods := s.f.clientset.CoreV1().Pods(controllerMetricsNamespace)
	list, err := pods.List(ctx, metav1.ListOptions{LabelSelector: controllerMetricsSelector})
	if err != nil {
		return nil, fmt.Errorf("listing controller pods (%s) in %s: %w",
			controllerMetricsSelector, controllerMetricsNamespace, err)
	}

	names := make([]string, 0, len(list.Items))
	for i := range list.Items {
		if list.Items[i].Status.Phase == corev1.PodRunning {
			names = append(names, list.Items[i].Name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, fmt.Errorf("no running controller pod (%s) in %s",
			controllerMetricsSelector, controllerMetricsNamespace)
	}

	texts := make([]string, 0, len(names))
	for _, name := range names {
		raw, err := pods.
			ProxyGet(controllerMetricsScheme, name, controllerMetricsPort, controllerMetricsPath, nil).
			DoRaw(ctx)
		if err != nil {
			return nil, fmt.Errorf("reading %s from controller pod %s: %w", controllerMetricsPath, name, err)
		}
		texts = append(texts, string(raw))
	}
	return texts, nil
}

// ControllerMetricSamples scrapes /metrics from every running controller pod and
// returns the samples of metricName whose labels contain every pair in labels,
// across all pods. An empty result is not a failure — it means the series is not
// exported right now, and only the caller knows whether that is the answer it
// was looking for.
func (f *Framework) ControllerMetricSamples(
	ctx context.Context,
	metricName string,
	labels map[string]string,
) []MetricSample {
	GinkgoHelper()
	samples, err := controllerMetricSamples(ctx, f.scraper(), metricName, labels)
	if err != nil {
		Fail(err.Error())
	}
	return samples
}

// AwaitControllerMetric blocks until EVERY running controller pod exports
// metricName with the given labels and value, or ctx runs out.
//
// Every pod, not any pod: with more than one controller replica each exports its
// own view of the same cache, and the alert rules collapse the duplicates with
// `max by (...)`. Demanding agreement is therefore the assertion that matches an
// alert which must fire (all replicas report 0) as well as one that must stay
// silent (no replica reports 0).
//
// Transient scrape failures (a controller pod restarting mid-poll) are retried
// until the deadline; the last error is reported if the deadline wins.
func (f *Framework) AwaitControllerMetric(
	ctx context.Context,
	metricName string,
	labels map[string]string,
	value float64,
) {
	GinkgoHelper()
	err := awaitControllerMetric(ctx, f.scraper(), controllerMetricsPollInterval, metricName, labels, value)
	if err != nil {
		Fail(err.Error())
	}
}

// controllerMetricSamples is the failing logic of ControllerMetricSamples.
func controllerMetricSamples(
	ctx context.Context,
	s metricsScraper,
	metricName string,
	labels map[string]string,
) ([]MetricSample, error) {
	texts, err := s.ScrapeControllerMetrics(ctx)
	if err != nil {
		return nil, err
	}
	var out []MetricSample
	for i, text := range texts {
		parsed, err := parseMetricSamples(text, metricName)
		if err != nil {
			return nil, fmt.Errorf("parsing %s from controller pod #%d: %w", metricName, i+1, err)
		}
		out = append(out, selectMetricSamples(parsed, labels)...)
	}
	return out, nil
}

// awaitControllerMetric is the failing logic of AwaitControllerMetric. The poll
// interval is a parameter so unit tests drive it without waiting in real time.
func awaitControllerMetric(
	ctx context.Context,
	s metricsScraper,
	poll time.Duration,
	metricName string,
	labels map[string]string,
	value float64,
) error {
	for {
		last := checkControllerMetric(ctx, s, metricName, labels, value)
		if last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for %s%v to be %g on every controller pod: %w",
				metricName, labels, value, last)
		case <-time.After(poll):
		}
	}
}

// checkControllerMetric reports nil when the metric is exported with exactly the
// expected value everywhere, and otherwise the reason it is not.
func checkControllerMetric(
	ctx context.Context,
	s metricsScraper,
	metricName string,
	labels map[string]string,
	value float64,
) error {
	samples, err := controllerMetricSamples(ctx, s, metricName, labels)
	if err != nil {
		return err
	}
	if len(samples) == 0 {
		return fmt.Errorf("no %s sample with labels %v is exported", metricName, labels)
	}
	for _, sample := range samples {
		if sample.Value != value {
			return fmt.Errorf("expected %s%v to be %g, got %s", metricName, labels, value, sample)
		}
	}
	return nil
}

// parseMetricSamples extracts the samples of ONE metric family from a
// Prometheus text exposition response.
//
// Only lines naming this very metric are read: comments, other families and
// whatever exotic formatting they use are skipped, so the parser never fails
// because of a metric the caller does not care about. A line that DOES name the
// metric but cannot be read is an error, never a dropped sample — dropping it
// would turn a broken exporter into "the series is simply absent", which reads
// as a pass.
func parseMetricSamples(text, name string) ([]MetricSample, error) {
	var out []MetricSample
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.HasPrefix(line, name) {
			continue
		}
		rest := line[len(name):]
		if rest != "" && rest[0] != '{' && rest[0] != ' ' && rest[0] != '\t' {
			// Another family whose name merely starts with name (sds_rv_count vs sds_rv).
			continue
		}
		sample, err := parseMetricSampleLine(name, rest)
		if err != nil {
			return nil, fmt.Errorf("line %d of the /metrics response (%q): %w", i+1, line, err)
		}
		out = append(out, sample)
	}
	return out, nil
}

// parseMetricSampleLine parses everything that follows the metric name on a
// sample line: an optional label set, the value, and an optional timestamp.
func parseMetricSampleLine(name, rest string) (MetricSample, error) {
	labels := map[string]string{}
	if strings.HasPrefix(rest, "{") {
		parsed, consumed, err := parseMetricLabels(rest)
		if err != nil {
			return MetricSample{}, err
		}
		labels = parsed
		rest = rest[consumed:]
	}

	fields := strings.Fields(rest)
	switch {
	case len(fields) == 0:
		return MetricSample{}, errors.New("the sample carries no value")
	case len(fields) > 2:
		return MetricSample{}, fmt.Errorf(
			"expected a value and an optional timestamp after the labels, got %d fields", len(fields))
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return MetricSample{}, fmt.Errorf("parsing the value %q: %w", fields[0], err)
	}
	return MetricSample{Name: name, Labels: labels, Value: value}, nil
}

// parseMetricLabels parses a `{k="v",k2="v2"}` label set starting at s[0] and
// returns the labels together with the number of bytes consumed (including the
// closing brace).
func parseMetricLabels(s string) (map[string]string, int, error) {
	labels := map[string]string{}
	i := 1 // past '{'
	for {
		i = skipMetricSpaces(s, i)
		if i >= len(s) {
			return nil, 0, errors.New("the label set is not terminated")
		}
		if s[i] == '}' {
			return labels, i + 1, nil
		}

		start := i
		for i < len(s) && s[i] != '=' && s[i] != ',' && s[i] != '}' && s[i] != ' ' && s[i] != '\t' {
			i++
		}
		key := s[start:i]
		if key == "" {
			return nil, 0, errors.New("a label name is empty")
		}
		if i >= len(s) || s[i] != '=' {
			return nil, 0, fmt.Errorf("label %q is not followed by '='", key)
		}
		i++

		if i >= len(s) || s[i] != '"' {
			return nil, 0, fmt.Errorf("the value of label %q is not quoted", key)
		}
		value, next, err := parseMetricLabelValue(s, i)
		if err != nil {
			return nil, 0, fmt.Errorf("label %q: %w", key, err)
		}
		labels[key] = value
		i = skipMetricSpaces(s, next)

		if i >= len(s) {
			return nil, 0, errors.New("the label set is not terminated")
		}
		switch s[i] {
		case ',':
			i++
		case '}':
			return labels, i + 1, nil
		default:
			return nil, 0, fmt.Errorf("unexpected %q after the value of label %q", s[i], key)
		}
	}
}

// parseMetricLabelValue parses the quoted label value starting at s[i] (which
// must be the opening quote) and returns it with the index just past the
// closing quote.
func parseMetricLabelValue(s string, i int) (string, int, error) {
	var b strings.Builder
	i++ // past the opening quote
	for i < len(s) {
		switch c := s[i]; c {
		case '\\':
			i++
			if i >= len(s) {
				return "", 0, errors.New("the value ends with a dangling escape")
			}
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			default:
				b.WriteByte(s[i])
			}
			i++
		case '"':
			return b.String(), i + 1, nil
		default:
			b.WriteByte(c)
			i++
		}
	}
	return "", 0, errors.New("the value is not terminated")
}

func skipMetricSpaces(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return i
}

// selectMetricSamples returns the samples whose label set contains every pair in
// want. The match is a SUBSET match on purpose: a spec names the labels it
// reasons about (name, reason) and stays indifferent to labels the exporter or
// the scrape pipeline may add later.
func selectMetricSamples(samples []MetricSample, want map[string]string) []MetricSample {
	var out []MetricSample
	for _, sample := range samples {
		matched := true
		for k, v := range want {
			if sample.Labels[k] != v {
				matched = false
				break
			}
		}
		if matched {
			out = append(out, sample)
		}
	}
	return out
}
