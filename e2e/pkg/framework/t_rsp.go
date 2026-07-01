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

// TestRSP creates a new TestRSP with an auto-generated or named name.
func (f *Framework) TestRSP(name ...string) *TestRSP {
	var zero *v1alpha1.ReplicatedStoragePool
	return newTestRSP(f, f.autoName(zero, name...))
}

// TestRSPExact creates a new TestRSP with an exact literal name.
func (f *Framework) TestRSPExact(fullName string) *TestRSP {
	return newTestRSP(f, fullName)
}

func newTestRSP(f *Framework, name string) *TestRSP {
	t := &TestRSP{f: f}
	t.TrackedObject = tk.NewTrackedObject(f.Cache, f.Client, gvkRSP, name,
		tk.Lifecycle[*v1alpha1.ReplicatedStoragePool]{
			Debugger:   f.Debugger,
			Standalone: true,
			OnBuild:    func(_ context.Context) *v1alpha1.ReplicatedStoragePool { return t.buildObject() },
			OnNewEmpty: func() *v1alpha1.ReplicatedStoragePool { return &v1alpha1.ReplicatedStoragePool{} },
		})
	return t
}

// TestRSP is the domain wrapper for ReplicatedStoragePool test objects.
type TestRSP struct {
	*tk.TrackedObject[*v1alpha1.ReplicatedStoragePool]
	f *Framework

	buildType            *v1alpha1.ReplicatedStoragePoolType
	buildLVMVolumeGroups []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups
	buildZones           []string
	buildNodeSelector    *metav1.LabelSelector
}

// ---------------------------------------------------------------------------
// Builder chain
// ---------------------------------------------------------------------------

func (t *TestRSP) Type(pt v1alpha1.ReplicatedStoragePoolType) *TestRSP {
	t.buildType = &pt
	return t
}

func (t *TestRSP) LVMVolumeGroups(lvgs ...v1alpha1.ReplicatedStoragePoolLVMVolumeGroups) *TestRSP {
	t.buildLVMVolumeGroups = lvgs
	return t
}

func (t *TestRSP) Zones(zones ...string) *TestRSP {
	t.buildZones = zones
	return t
}

func (t *TestRSP) NodeLabelSelector(sel *metav1.LabelSelector) *TestRSP {
	t.buildNodeSelector = sel
	return t
}

// ---------------------------------------------------------------------------
// buildObject
// ---------------------------------------------------------------------------

// buildObject returns the API object from builder fields.
func (t *TestRSP) buildObject() *v1alpha1.ReplicatedStoragePool {
	rsp := &v1alpha1.ReplicatedStoragePool{
		ObjectMeta: metav1.ObjectMeta{
			Name: t.Name(),
		},
	}
	if t.buildType != nil {
		rsp.Spec.Type = *t.buildType
	}
	if len(t.buildLVMVolumeGroups) > 0 {
		rsp.Spec.LVMVolumeGroups = t.buildLVMVolumeGroups
	}
	if len(t.buildZones) > 0 {
		rsp.Spec.Zones = t.buildZones
	}
	if t.buildNodeSelector != nil {
		rsp.Spec.NodeLabelSelector = t.buildNodeSelector
	}
	if t.f != nil {
		t.f.stampMetadata(rsp)
	}
	return rsp
}
