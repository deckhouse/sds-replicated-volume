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

package migrator

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestRetryTransient_retriesThenSucceeds(t *testing.T) {
	t.Parallel()

	m := &Migrator{
		log:           slog.Default(),
		retryInterval: time.Millisecond,
	}
	ctx := context.Background()
	var calls int
	err := m.retryTransient(ctx, "test step", func() error {
		calls++
		if calls < 3 {
			return apierrors.NewServiceUnavailable("unavailable")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("retryTransient() = %v, want nil", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestRetryTransient_permanentError(t *testing.T) {
	t.Parallel()

	m := &Migrator{
		log:           slog.Default(),
		retryInterval: time.Millisecond,
	}
	want := errors.New("permanent")
	err := m.retryTransient(context.Background(), "test step", func() error {
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("retryTransient() = %v, want %v", err, want)
	}
}

func TestRetryTransient_contextCanceled(t *testing.T) {
	t.Parallel()

	m := &Migrator{
		log:           slog.Default(),
		retryInterval: time.Hour,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := m.retryTransient(ctx, "test step", func() error {
		return apierrors.NewServiceUnavailable("unavailable")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("retryTransient() = %v, want context.Canceled", err)
	}
}

func TestIsTransientAPIError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"canceled", context.Canceled, false},
		{"deadline", context.DeadlineExceeded, true},
		{"503", apierrors.NewServiceUnavailable("x"), true},
		{"429", apierrors.NewTooManyRequests("x", 0), true},
		{"404", apierrors.NewNotFound(schema.GroupResource{Group: "", Resource: "pods"}, "x"), false},
		{"status timeout", &apierrors.StatusError{ErrStatus: metav1.Status{Code: http.StatusRequestTimeout}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isTransientAPIError(tt.err); got != tt.want {
				t.Fatalf("isTransientAPIError() = %v, want %v", got, tt.want)
			}
		})
	}
}
