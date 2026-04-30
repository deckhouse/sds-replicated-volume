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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	tk "github.com/deckhouse/sds-replicated-volume/lib/go/testkit"
)

// TestNS creates a new TestNS with an auto-generated or named name.
func (f *Framework) TestNS(name ...string) *TestNS {
	var zero *corev1.Namespace
	return newTestNS(f, f.autoName(zero, name...))
}

// TestNSExact creates a new TestNS with an exact literal name.
func (f *Framework) TestNSExact(fullName string) *TestNS {
	return newTestNS(f, fullName)
}

func newTestNS(f *Framework, name string) *TestNS {
	t := &TestNS{f: f}
	t.TrackedObject = tk.NewTrackedObject(f.Cache, f.Client, gvkNS, name,
		tk.Lifecycle[*corev1.Namespace]{
			Standalone: true,
			OnBuild:    func(_ context.Context) *corev1.Namespace { return t.buildObject() },
			OnNewEmpty: func() *corev1.Namespace { return &corev1.Namespace{} },
		})
	return t
}

// TestNS is the domain wrapper for Namespace test objects.
type TestNS struct {
	*tk.TrackedObject[*corev1.Namespace]
	f      *Framework
	shared bool
}

// ---------------------------------------------------------------------------
// buildObject
// ---------------------------------------------------------------------------

func (t *TestNS) buildObject() *corev1.Namespace {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: t.Name(),
		},
	}
	if t.f != nil {
		if t.shared {
			t.f.stampRunMetadata(ns)
		} else {
			t.f.stampMetadata(ns)
		}
	}
	return ns
}
