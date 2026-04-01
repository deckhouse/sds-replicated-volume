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

	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	tk "github.com/deckhouse/sds-replicated-volume/lib/go/testkit"
)

// TestSC creates a new TestSC with an auto-generated or named name.
func (f *Framework) TestSC(name ...string) *TestSC {
	var zero *storagev1.StorageClass
	return newTestSC(f, f.autoName(zero, name...), true)
}

// TestSCExact creates a new TestSC with an exact literal name.
func (f *Framework) TestSCExact(fullName string) *TestSC {
	return newTestSC(f, fullName, true)
}

func newTestSC(f *Framework, name string, standalone bool) *TestSC {
	t := &TestSC{f: f}
	t.TrackedObject = tk.NewTrackedObject(f.Cache, f.Client, gvkSC, name,
		tk.Lifecycle[*storagev1.StorageClass]{
			Debugger:   f.Debugger,
			Standalone: standalone,
			OnBuild:    func(_ context.Context) *storagev1.StorageClass { return t.buildObject() },
			OnNewEmpty: func() *storagev1.StorageClass { return &storagev1.StorageClass{} },
		})
	return t
}

// TestSC is the domain wrapper for StorageClass test objects.
type TestSC struct {
	*tk.TrackedObject[*storagev1.StorageClass]
	f *Framework

	buildProvisioner *string
}

// ---------------------------------------------------------------------------
// Builder chain
// ---------------------------------------------------------------------------

func (t *TestSC) Provisioner(p string) *TestSC {
	t.buildProvisioner = &p
	return t
}

// ---------------------------------------------------------------------------
// buildObject
// ---------------------------------------------------------------------------

func (t *TestSC) buildObject() *storagev1.StorageClass {
	sc := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: t.Name(),
		},
		Provisioner: "kubernetes.io/no-provisioner",
	}
	if t.buildProvisioner != nil {
		sc.Provisioner = *t.buildProvisioner
	}
	if t.f != nil {
		t.f.stampMetadata(sc)
	}
	return sc
}
