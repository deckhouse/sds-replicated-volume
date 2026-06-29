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
	"k8s.io/apimachinery/pkg/watch"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	tk "github.com/deckhouse/sds-replicated-volume/lib/go/testkit"
)

// TestRVR creates a new standalone TestRVR. rvName goes through autoName
// to produce the ReplicatedVolume name prefix; id (0–31) is appended as
// the replica suffix. The resulting object name is "{autoName}-{id}".
func (f *Framework) TestRVR(rvName string, id uint8) *TestRVR {
	var zero *v1alpha1.ReplicatedVolumeReplica
	return newTestRVR(f, f.autoName(zero, rvName), id)
}

// TestRVRExact creates a new standalone TestRVR with a literal
// ReplicatedVolume name (no autoName). The object name is "{rvName}-{id}".
func (f *Framework) TestRVRExact(rvName string, id uint8) *TestRVR {
	return newTestRVR(f, rvName, id)
}

func newTestRVR(f *Framework, rvName string, id uint8) *TestRVR {
	fullName := v1alpha1.FormatReplicatedVolumeReplicaName(rvName, id)
	t := &TestRVR{f: f, buildRVName: rvName}
	t.TrackedObject = tk.NewTrackedObject(f.Cache, f.Client, gvkRVR, fullName,
		tk.Lifecycle[*v1alpha1.ReplicatedVolumeReplica]{
			Debugger:   f.Debugger,
			Standalone: true,
			OnBuild:    func(_ context.Context) *v1alpha1.ReplicatedVolumeReplica { return t.buildObject() },
			OnNewEmpty: func() *v1alpha1.ReplicatedVolumeReplica { return &v1alpha1.ReplicatedVolumeReplica{} },
		})
	return t
}

// TestRVR is the domain wrapper for ReplicatedVolumeReplica test objects.
type TestRVR struct {
	*tk.TrackedObject[*v1alpha1.ReplicatedVolumeReplica]
	f     *Framework
	drbdr *tk.TrackedObject[*v1alpha1.DRBDResource]
	llvs  *tk.TrackedGroup[*snc.LVMLogicalVolume, *TestLLV]

	buildNodeName *string
	buildType     *v1alpha1.ReplicaType
	buildLVGName  *string
	buildThinPool *string
	buildRVName   string
}

// ---------------------------------------------------------------------------
// Builder chain
// ---------------------------------------------------------------------------

func (t *TestRVR) Node(name string) *TestRVR {
	t.buildNodeName = &name
	return t
}

func (t *TestRVR) Type(rt v1alpha1.ReplicaType) *TestRVR {
	t.buildType = &rt
	return t
}

func (t *TestRVR) LVG(name string) *TestRVR {
	t.buildLVGName = &name
	return t
}

func (t *TestRVR) ThinPool(name string) *TestRVR {
	t.buildThinPool = &name
	return t
}

// ---------------------------------------------------------------------------
// ID
// ---------------------------------------------------------------------------

// ID extracts the replica ID (0-31) from the RVR name suffix.
// Does not require the API object to be present.
func (t *TestRVR) ID() uint8 {
	return (&v1alpha1.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{Name: t.Name()},
	}).ID()
}

// ---------------------------------------------------------------------------
// buildObject
// ---------------------------------------------------------------------------

// buildObject returns the API object from builder fields.
func (t *TestRVR) buildObject() *v1alpha1.ReplicatedVolumeReplica {
	rvr := &v1alpha1.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{
			Name: t.Name(),
		},
	}
	rvr.Spec.ReplicatedVolumeName = t.buildRVName
	if t.buildNodeName != nil {
		rvr.Spec.NodeName = *t.buildNodeName
	}
	if t.buildType != nil {
		rvr.Spec.Type = *t.buildType
	}
	if t.buildLVGName != nil {
		rvr.Spec.LVMVolumeGroupName = *t.buildLVGName
	}
	if t.buildThinPool != nil {
		rvr.Spec.LVMVolumeGroupThinPoolName = *t.buildThinPool
	}
	if t.f != nil {
		t.f.stampMetadata(rvr)
	}
	return rvr
}

// ---------------------------------------------------------------------------
// DRBDR access (1:1 relationship)
// ---------------------------------------------------------------------------

// ensureDRBDR lazily initializes the drbdr TrackedObject.
func (t *TestRVR) ensureDRBDR() *tk.TrackedObject[*v1alpha1.DRBDResource] {
	if t.drbdr == nil {
		t.drbdr = tk.NewTrackedObject(t.Cache, t.Client, gvkDRBDR, t.Name(), tk.Lifecycle[*v1alpha1.DRBDResource]{})
	}
	return t.drbdr
}

// DRBDR returns the TestDRBDR linked to this RVR.
func (t *TestRVR) DRBDR() *TestDRBDR {
	return &TestDRBDR{TrackedObject: t.ensureDRBDR(), f: t.f}
}

// injectDRBDREvent routes a DRBDR event to the linked TrackedObject.
func (t *TestRVR) injectDRBDREvent(eventType watch.EventType, obj *v1alpha1.DRBDResource) {
	t.ensureDRBDR().InjectEvent(eventType, obj)
}

// ---------------------------------------------------------------------------
// LLV access (1:N relationship — multiple LLVs during backing volume migration)
// ---------------------------------------------------------------------------

func (t *TestRVR) ensureLLVGroup() *tk.TrackedGroup[*snc.LVMLogicalVolume, *TestLLV] {
	if t.llvs == nil {
		t.llvs = tk.NewTrackedGroup(t.Cache, t.Client, gvkLLV,
			func(name string) *TestLLV {
				llv := &TestLLV{f: t.f}
				llv.TrackedObject = tk.NewTrackedObject(t.Cache, t.Client, gvkLLV, name, tk.Lifecycle[*snc.LVMLogicalVolume]{})
				return llv
			},
		)
	}
	return t.llvs
}

// LLVs returns all tracked LLVs for this RVR.
func (t *TestRVR) LLVs() []*TestLLV {
	return t.ensureLLVGroup().All()
}

// injectLLVEvent routes an LLV event into the LLV group.
func (t *TestRVR) injectLLVEvent(eventType watch.EventType, obj *snc.LVMLogicalVolume) {
	t.ensureLLVGroup().InjectEvent(obj.Name, eventType, obj)
}
