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

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	tk "github.com/deckhouse/sds-replicated-volume/lib/go/testkit"
)

// TestLLV creates a new TestLLV with an auto-generated or named name.
func (f *Framework) TestLLV(name ...string) *TestLLV {
	var zero *snc.LVMLogicalVolume
	return newTestLLV(f, f.autoName(zero, name...))
}

// TestLLVExact creates a new TestLLV with an exact literal name.
func (f *Framework) TestLLVExact(fullName string) *TestLLV {
	return newTestLLV(f, fullName)
}

func newTestLLV(f *Framework, name string) *TestLLV {
	t := &TestLLV{f: f}
	t.TrackedObject = tk.NewTrackedObject(f.Cache, f.Client, gvkLLV, name,
		tk.Lifecycle[*snc.LVMLogicalVolume]{
			Debugger:   f.Debugger,
			Standalone: true,
			OnBuild:    func(_ context.Context) *snc.LVMLogicalVolume { return t.buildObject() },
			OnNewEmpty: func() *snc.LVMLogicalVolume { return &snc.LVMLogicalVolume{} },
		})
	return t
}

// TestLLV is the domain wrapper for LVMLogicalVolume test objects.
type TestLLV struct {
	*tk.TrackedObject[*snc.LVMLogicalVolume]
	f *Framework

	buildActualLVName   *string
	buildType           *string
	buildSize           *string
	buildLVMVolumeGroup *string
	buildThinPoolName   *string
}

// ---------------------------------------------------------------------------
// Builder chain
// ---------------------------------------------------------------------------

func (t *TestLLV) ActualLVName(name string) *TestLLV {
	t.buildActualLVName = &name
	return t
}

func (t *TestLLV) Type(lvType string) *TestLLV {
	t.buildType = &lvType
	return t
}

func (t *TestLLV) Size(s string) *TestLLV {
	t.buildSize = &s
	return t
}

func (t *TestLLV) LVMVolumeGroupName(name string) *TestLLV {
	t.buildLVMVolumeGroup = &name
	return t
}

func (t *TestLLV) ThinPoolName(name string) *TestLLV {
	t.buildThinPoolName = &name
	return t
}

// ---------------------------------------------------------------------------
// buildObject
// ---------------------------------------------------------------------------

// buildObject returns the API object from builder fields.
func (t *TestLLV) buildObject() *snc.LVMLogicalVolume {
	llv := &snc.LVMLogicalVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: t.Name(),
		},
	}
	if t.buildActualLVName != nil {
		llv.Spec.ActualLVNameOnTheNode = *t.buildActualLVName
	}
	if t.buildType != nil {
		llv.Spec.Type = *t.buildType
	}
	if t.buildSize != nil {
		llv.Spec.Size = *t.buildSize
	}
	if t.buildLVMVolumeGroup != nil {
		llv.Spec.LVMVolumeGroupName = *t.buildLVMVolumeGroup
	}
	if t.buildThinPoolName != nil {
		llv.Spec.Thin = &snc.LVMLogicalVolumeThinSpec{
			PoolName: *t.buildThinPoolName,
		}
	}
	if t.f != nil {
		t.f.stampMetadata(llv)
	}
	return llv
}
