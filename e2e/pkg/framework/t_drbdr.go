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

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	tk "github.com/deckhouse/sds-replicated-volume/lib/go/testkit"
)

// TestDRBDR creates a new TestDRBDR with an auto-generated or named name.
func (f *Framework) TestDRBDR(name ...string) *TestDRBDR {
	var zero *v1alpha1.DRBDResource
	return newTestDRBDR(f, f.autoName(zero, name...))
}

// TestDRBDRExact creates a new TestDRBDR with an exact literal name.
func (f *Framework) TestDRBDRExact(fullName string) *TestDRBDR {
	return newTestDRBDR(f, fullName)
}

func newTestDRBDR(f *Framework, name string) *TestDRBDR {
	t := &TestDRBDR{f: f}
	t.TrackedObject = tk.NewTrackedObject(f.Cache, f.Client, gvkDRBDR, name,
		tk.Lifecycle[*v1alpha1.DRBDResource]{
			Debugger:   f.Debugger,
			Standalone: true,
			OnBuild:    func(_ context.Context) *v1alpha1.DRBDResource { return t.buildObject() },
			OnNewEmpty: func() *v1alpha1.DRBDResource { return &v1alpha1.DRBDResource{} },
		})
	return t
}

// TestDRBDR is the domain wrapper for DRBDResource test objects.
type TestDRBDR struct {
	*tk.TrackedObject[*v1alpha1.DRBDResource]
	f *Framework

	buildNodeName         *string
	buildType             *v1alpha1.DRBDResourceType
	buildSize             *resource.Quantity
	buildRVName           string
	buildLLVName          *string
	buildSystemNetworks   []string
	buildNodeID           *uint8
	buildActualNameOnNode *string
	buildMaintenance      *v1alpha1.MaintenanceMode
	buildRole             *v1alpha1.DRBDRole
}

// ---------------------------------------------------------------------------
// Builder chain
// ---------------------------------------------------------------------------

func (t *TestDRBDR) Node(name string) *TestDRBDR {
	t.buildNodeName = &name
	return t
}

func (t *TestDRBDR) Type(rt v1alpha1.DRBDResourceType) *TestDRBDR {
	t.buildType = &rt
	return t
}

func (t *TestDRBDR) Size(s string) *TestDRBDR {
	q := resource.MustParse(s)
	t.buildSize = &q
	return t
}

func (t *TestDRBDR) RVName(name string) *TestDRBDR {
	t.buildRVName = name
	return t
}

func (t *TestDRBDR) LVMLogicalVolumeName(name string) *TestDRBDR {
	t.buildLLVName = &name
	return t
}

func (t *TestDRBDR) SystemNetworks(names ...string) *TestDRBDR {
	t.buildSystemNetworks = names
	return t
}

func (t *TestDRBDR) NodeID(id uint8) *TestDRBDR {
	t.buildNodeID = &id
	return t
}

func (t *TestDRBDR) ActualNameOnTheNode(name string) *TestDRBDR {
	t.buildActualNameOnNode = &name
	return t
}

func (t *TestDRBDR) Maintenance(mode v1alpha1.MaintenanceMode) *TestDRBDR {
	t.buildMaintenance = &mode
	return t
}

func (t *TestDRBDR) Role(role v1alpha1.DRBDRole) *TestDRBDR {
	t.buildRole = &role
	return t
}

// ---------------------------------------------------------------------------
// buildObject
// ---------------------------------------------------------------------------

// buildObject returns the API object from builder fields.
func (t *TestDRBDR) buildObject() *v1alpha1.DRBDResource {
	drbdr := &v1alpha1.DRBDResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: t.Name(),
		},
	}
	if t.buildNodeName != nil {
		drbdr.Spec.NodeName = *t.buildNodeName
	}
	if t.buildType != nil {
		drbdr.Spec.Type = *t.buildType
	}
	if t.buildSize != nil {
		drbdr.Spec.Size = t.buildSize
	}
	if t.buildLLVName != nil {
		drbdr.Spec.LVMLogicalVolumeName = *t.buildLLVName
	}
	if len(t.buildSystemNetworks) > 0 {
		drbdr.Spec.SystemNetworks = t.buildSystemNetworks
	}
	if t.buildNodeID != nil {
		drbdr.Spec.NodeID = *t.buildNodeID
	}
	if t.buildActualNameOnNode != nil {
		drbdr.Spec.ActualNameOnTheNode = *t.buildActualNameOnNode
	}
	if t.buildMaintenance != nil {
		drbdr.Spec.Maintenance = *t.buildMaintenance
	}
	if t.buildRole != nil {
		drbdr.Spec.Role = *t.buildRole
	}
	if t.buildRVName != "" {
		if drbdr.Labels == nil {
			drbdr.Labels = make(map[string]string)
		}
		drbdr.Labels[v1alpha1.ReplicatedVolumeLabelKey] = t.buildRVName
	}
	if t.f != nil {
		t.f.stampMetadata(drbdr)
	}
	return drbdr
}
