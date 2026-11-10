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

package capabilities

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func TestReporter(t *testing.T) {
	for _, tc := range []struct {
		name      string
		supported bool
		failures  int
		want      string
	}{
		{name: "supported", supported: true, want: "true"},
		{name: "retry", supported: true, failures: 2, want: "true"},
		{name: "unsupported overwrites true", want: "false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				scheme := runtime.NewScheme()
				if err := corev1.AddToScheme(scheme); err != nil {
					t.Fatal(err)
				}
				pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "agent-1", Namespace: "agents", Annotations: map[string]string{
					v1alpha1.AgentShadowDiskfulSupportedAnnotationKey: "true", "unrelated": "keep",
				}}}
				calls := 0
				cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod).
					WithInterceptorFuncs(interceptor.Funcs{Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
						calls++
						if patch.Type() != types.MergePatchType {
							t.Fatalf("patch type = %s", patch.Type())
						}
						if client.ObjectKeyFromObject(obj) != client.ObjectKeyFromObject(pod) {
							t.Fatal("wrong Pod")
						}
						if calls <= tc.failures {
							return errors.New("temporary API failure")
						}
						return cl.Patch(ctx, obj, patch, opts...)
					}}).Build()
				started := time.Now()
				if err := NewReporter(cl, client.ObjectKeyFromObject(pod), tc.supported).Start(t.Context()); err != nil {
					t.Fatal(err)
				}
				if calls != tc.failures+1 {
					t.Fatalf("patch calls = %d", calls)
				}
				if tc.failures == 2 && time.Since(started) != 3*time.Second {
					t.Fatal("expected exponential retry delays of 1s and 2s")
				}
				var got corev1.Pod
				if err := cl.Get(t.Context(), client.ObjectKeyFromObject(pod), &got); err != nil {
					t.Fatal(err)
				}
				if got.Annotations[v1alpha1.AgentShadowDiskfulSupportedAnnotationKey] != tc.want || got.Annotations["unrelated"] != "keep" {
					t.Fatalf("annotations = %v", got.Annotations)
				}
			})
		})
	}
}

func TestReporterCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		calls := 0
		cl := fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
				calls++
				return errors.New("API unavailable")
			},
		}).Build()
		done := make(chan error, 1)
		go func() {
			done <- NewReporter(cl, client.ObjectKey{Name: "agent", Namespace: "agents"}, false).Start(ctx)
		}()
		synctest.Wait()
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if calls != 1 {
			t.Fatalf("patch calls = %d, want 1", calls)
		}
	})
}
