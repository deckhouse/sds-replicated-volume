/*
Copyright 2024 Flant JSC

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

package main

import (
	"context"
	"net"
	"net/http"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

type accessLogResponseWriter struct {
	http.ResponseWriter
	statusCode int
	size       int
}

func (w *accessLogResponseWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	w.size += n
	return n, err
}

func (w *accessLogResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func accessLogHandler(ctx context.Context, next http.Handler) http.Handler {
	logger := log.FromContext(ctx)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startTime := time.Now()

		accessLogRW := &accessLogResponseWriter{ResponseWriter: w}

		next.ServeHTTP(accessLogRW, r)
		status := accessLogRW.statusCode

		fields := []interface{}{
			"type", "access",
			"response_time", time.Since(startTime).Seconds(),
			"protocol", r.Proto,
			"http_status_code", status,
			"http_method", r.Method,
			"url", r.RequestURI,
			"http_host", r.Host,
			"request_size", r.ContentLength,
			"response_size", accessLogRW.size,
		}
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err == nil {
			fields = append(fields, "remote_ipaddr", ip)
		}
		ua := r.Header.Get("User-Agent")
		if len(ua) > 0 {
			fields = append(fields, "http_user_agent", ua)
		}
		logger.Info("access", fields...)
	})
}
