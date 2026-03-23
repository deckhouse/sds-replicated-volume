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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	tk "github.com/deckhouse/sds-replicated-volume/lib/go/testkit"
)

// TestRVA creates a new TestRVA with an auto-generated or named name.
func (f *Framework) TestRVA(name ...string) *TestRVA {
	var zero *v1alpha1.ReplicatedVolumeAttachment
	return newTestRVA(f, f.autoName(zero, name...))
}

// TestRVAExact creates a new TestRVA with an exact literal name.
func (f *Framework) TestRVAExact(fullName string) *TestRVA {
	return newTestRVA(f, fullName)
}

func newTestRVA(f *Framework, name string) *TestRVA {
	t := &TestRVA{f: f}
	t.TrackedObject = tk.NewTrackedObject(f.Cache, f.Client, gvkRVA, name,
		tk.Lifecycle[*v1alpha1.ReplicatedVolumeAttachment]{
			Debugger:   f.Debugger,
			Standalone: true,
			OnBuild:    func(_ context.Context) *v1alpha1.ReplicatedVolumeAttachment { return t.buildObject() },
			OnNewEmpty: func() *v1alpha1.ReplicatedVolumeAttachment { return &v1alpha1.ReplicatedVolumeAttachment{} },
		})
	return t
}

// TestRVA is the domain wrapper for ReplicatedVolumeAttachment test objects.
type TestRVA struct {
	*tk.TrackedObject[*v1alpha1.ReplicatedVolumeAttachment]
	f *Framework

	// Builder fields — nil means "not set".
	buildNodeName *string
	buildRVName   string // parent RV name (required)
}

// ---------------------------------------------------------------------------
// Builder chain
// ---------------------------------------------------------------------------

func (t *TestRVA) Node(name string) *TestRVA {
	t.buildNodeName = &name
	return t
}

func (t *TestRVA) RVName(name string) *TestRVA {
	t.buildRVName = name
	return t
}

// ---------------------------------------------------------------------------
// buildObject
// ---------------------------------------------------------------------------

// buildObject returns the API object from builder fields.
func (t *TestRVA) buildObject() *v1alpha1.ReplicatedVolumeAttachment {
	rva := &v1alpha1.ReplicatedVolumeAttachment{
		ObjectMeta: metav1.ObjectMeta{
			Name: t.Name(),
		},
	}
	if t.buildRVName != "" {
		rva.Spec.ReplicatedVolumeName = t.buildRVName
	}
	if t.buildNodeName != nil {
		rva.Spec.NodeName = *t.buildNodeName
	}
	if t.f != nil {
		t.f.stampMetadata(rva)
	}
	return rva
}
