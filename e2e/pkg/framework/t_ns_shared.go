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

	. "github.com/onsi/ginkgo/v2"
	corev1 "k8s.io/api/core/v1"

	tk "github.com/deckhouse/sds-replicated-volume/lib/go/testkit"
)

// SharedNS returns a *TestNS pre-configured for shared (run-scoped) usage.
// The TrackedObject is not initialized yet — it is created inside
// CreateShared after the name is resolved. Chain with CreateShared(ctx)
// to ensure the namespace exists.
func (f *Framework) SharedNS() *TestNS {
	return &TestNS{f: f, shared: true}
}

// CreateShared ensures the namespace exists on the cluster as a shared
// run-scoped resource (IgnoreAlreadyExists, no DeferCleanup). On cache
// hit the receiver is replaced with the cached instance (*t = *cached).
//
// An optional suffix is appended to the run prefix to form the namespace
// name (e.g. CreateShared(ctx, "isolated") produces "e2e-{runID}-isolated").
// When called without a suffix the name is just "e2e-{runID}".
//
// The TrackedObject uses NewLiteTrackedObject (keepOnlyLast) to avoid
// history accumulation for long-lived run-scoped objects.
func (t *TestNS) CreateShared(ctx context.Context, suffix ...string) {
	GinkgoHelper()

	name := t.f.prefix
	if len(suffix) > 0 && suffix[0] != "" {
		name = name + "-" + suffix[0]
	}

	if cached, ok := t.f.nsCache[name]; ok {
		*t = *cached
		return
	}

	t.TrackedObject = tk.NewLiteTrackedObject(t.f.Cache, t.f.Client, gvkNS, name,
		tk.Lifecycle[*corev1.Namespace]{
			Standalone: true,
			OnBuild:    func(_ context.Context) *corev1.Namespace { return t.buildObject() },
			OnNewEmpty: func() *corev1.Namespace { return &corev1.Namespace{} },
		})

	t.TrackedObject.CreateShared(ctx)

	t.f.nsCache[name] = t
}
