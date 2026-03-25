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
	storagev1 "k8s.io/api/storage/v1"

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
			OnNewEmpty: func() *storagev1.StorageClass { return &storagev1.StorageClass{} },
		})
	return t
}

// TestSC is the domain wrapper for StorageClass test objects.
// StorageClasses are cluster-scoped and typically created by the
// RSC controller, not by tests directly.
type TestSC struct {
	*tk.TrackedObject[*storagev1.StorageClass]
	f *Framework
}
