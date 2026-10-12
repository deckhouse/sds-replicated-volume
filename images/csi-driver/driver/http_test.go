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

package driver //nolint:revive

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deckhouse/sds-common-lib/logger"
	"github.com/deckhouse/sds-common-lib/monitoring"
	"github.com/deckhouse/sds-common-lib/monitoring/csimetrics"
)

func get(t *testing.T, h http.Handler, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	body, _ := io.ReadAll(rec.Result().Body)
	return rec.Code, string(body)
}

func TestEndpointHandler_ServesMetricsAndProbesOnOneMux(t *testing.T) {
	log, err := logger.NewLogger(logger.InfoLevel)
	if err != nil {
		t.Fatal(err)
	}
	storage := monitoring.NewStorage()
	rec, err := csimetrics.New(storage, metricsPrefix)
	if err != nil {
		t.Fatal(err)
	}
	d := &Driver{log: log, metrics: rec, metricsStorage: storage, metricsBindAddress: "127.0.0.1:0"}

	if code, _ := get(t, d.endpointHandler(), "/readyz"); code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz before the driver is ready: got %d, want 503", code)
	}
	d.ready = true

	for _, p := range []string{"/healthz", "/readyz", "/health"} {
		if code, body := get(t, d.endpointHandler(), p); code != http.StatusOK || body != "OK" {
			t.Fatalf("%s: got %d %q, want 200 OK", p, code, body)
		}
	}
	code, body := get(t, d.endpointHandler(), "/metrics")
	if code != http.StatusOK || !strings.Contains(body, "# TYPE ") {
		t.Fatalf("/metrics: got %d and no exposition in %q", code, body)
	}
	if code, _ := get(t, d.endpointHandler(), "/debug/pprof/"); code != http.StatusNotFound {
		t.Fatalf("an unknown path: got %d, want 404", code)
	}
}

func TestEndpointHandler_NoMetricsWithoutStorage(t *testing.T) {
	log, err := logger.NewLogger(logger.InfoLevel)
	if err != nil {
		t.Fatal(err)
	}
	d := &Driver{log: log, ready: true}

	if code, _ := get(t, d.endpointHandler(), "/metrics"); code != http.StatusNotFound {
		t.Fatalf("/metrics without a metrics storage: got %d, want 404", code)
	}
	if code, _ := get(t, d.endpointHandler(), "/health"); code != http.StatusOK {
		t.Fatalf("/health: got %d, want 200", code)
	}
}
