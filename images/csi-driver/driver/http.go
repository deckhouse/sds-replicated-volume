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

package driver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/deckhouse/sds-common-lib/monitoring"
)

const (
	healthzPath = "/healthz"
	readyzPath  = "/readyz"
	healthPath  = "/health"

	httpReadHeaderTimeout = 10 * time.Second
	httpShutdownTimeout   = 5 * time.Second
)

// endpointHandler serves the driver's HTTP endpoints on one mux: the metrics
// when the driver records them, and the health endpoints always.
func (d *Driver) endpointHandler() http.Handler {
	mux := http.NewServeMux()
	if d.metricsStorage != nil {
		mux.Handle(monitoring.MetricsPath, d.metricsStorage.Handler())
	}
	ok := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "OK")
	}
	mux.HandleFunc(healthzPath, ok)
	mux.HandleFunc(healthPath, ok)
	mux.HandleFunc(readyzPath, func(w http.ResponseWriter, r *http.Request) {
		d.readyMu.Lock()
		ready := d.ready
		d.readyMu.Unlock()
		if !ready {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		ok(w, r)
	})
	return mux
}

// serveEndpoints listens on addr and serves endpointHandler until ctx ends,
// then shuts the server down. The listener is opened before anything is
// served, so a port that is taken fails the driver instead of being logged
// from a background goroutine.
func (d *Driver) serveEndpoints(ctx context.Context, addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	srv := &http.Server{Handler: d.endpointHandler(), ReadHeaderTimeout: httpReadHeaderTimeout}
	d.log.Info("serving the driver's HTTP endpoints", "address", listener.Addr().String())

	errs := make(chan error, 1)
	go func() { errs <- srv.Serve(listener) }()

	select {
	case err := <-errs:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve on %s: %w", addr, err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), httpShutdownTimeout)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
