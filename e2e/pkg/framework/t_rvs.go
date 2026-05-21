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
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	tk "github.com/deckhouse/sds-replicated-volume/lib/go/testkit"
)

// TestRVS creates a new TestRVS with an auto-generated or named name.
func (f *Framework) TestRVS(name ...string) *TestRVS {
	var zero *v1alpha1.ReplicatedVolumeSnapshot
	return newTestRVS(f, f.autoName(zero, name...))
}

// TestRVSExact creates a new TestRVS with an exact literal name (no prefix/slug).
func (f *Framework) TestRVSExact(fullName string) *TestRVS {
	return newTestRVS(f, fullName)
}

// Snapshot returns a new TestRVS whose Spec.ReplicatedVolumeName is bound
// to this RV. name is optional — if absent, an auto-name is generated.
func (t *TestRV) Snapshot(name ...string) *TestRVS {
	trvs := t.f.TestRVS(name...)
	trvs.buildRVName = t.Name()
	return trvs
}

func newTestRVS(f *Framework, name string) *TestRVS {
	trvs := &TestRVS{f: f}

	trvs.rvrss = tk.NewTrackedGroup(f.Cache, f.Client, gvkRVRS,
		func(rvrsName string) *TestRVRS {
			rvrs := &TestRVRS{f: f}
			rvrs.TrackedObject = tk.NewTrackedObject(f.Cache, f.Client, gvkRVRS, rvrsName,
				tk.Lifecycle[*v1alpha1.ReplicatedVolumeReplicaSnapshot]{
					OnNewEmpty: func() *v1alpha1.ReplicatedVolumeReplicaSnapshot {
						return &v1alpha1.ReplicatedVolumeReplicaSnapshot{}
					},
				})
			return rvrs
		},
	)

	trvs.TrackedObject = tk.NewTrackedObject(f.Cache, f.Client, gvkRVS, name,
		tk.Lifecycle[*v1alpha1.ReplicatedVolumeSnapshot]{
			Debugger:   f.Debugger,
			Standalone: true,
			OnBuild:    func(_ context.Context) *v1alpha1.ReplicatedVolumeSnapshot { return trvs.buildObject() },
			OnNewEmpty: func() *v1alpha1.ReplicatedVolumeSnapshot { return &v1alpha1.ReplicatedVolumeSnapshot{} },
			OnSetup:    func(ctx context.Context) { trvs.registerGroupInformers(ctx) },
			OnTeardown: func() { trvs.removeGroupInformers() },
		})

	return trvs
}

// TestRVS is the domain wrapper for ReplicatedVolumeSnapshot test objects.
// It embeds TrackedObject for state tracking and adds a builder chain and
// child tracking for RVRS / temp DRBDResource / DRBDResourceOperation.
type TestRVS struct {
	*tk.TrackedObject[*v1alpha1.ReplicatedVolumeSnapshot]
	f     *Framework
	rvrss *tk.TrackedGroup[*v1alpha1.ReplicatedVolumeReplicaSnapshot, *TestRVRS]

	extraInformerRegs []tk.InformerReg

	buildRVName string
}

// ---------------------------------------------------------------------------
// Builder chain
// ---------------------------------------------------------------------------

// Volume binds this RVS to the ReplicatedVolume with the given name
// (sets spec.replicatedVolumeName).
func (t *TestRVS) Volume(rvName string) *TestRVS {
	t.buildRVName = rvName
	return t
}

// ---------------------------------------------------------------------------
// buildObject
// ---------------------------------------------------------------------------

func (t *TestRVS) buildObject() *v1alpha1.ReplicatedVolumeSnapshot {
	rvs := &v1alpha1.ReplicatedVolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name: t.Name(),
		},
		Spec: v1alpha1.ReplicatedVolumeSnapshotSpec{
			ReplicatedVolumeName: t.buildRVName,
		},
	}
	if t.f != nil {
		t.f.stampMetadata(rvs)
	}
	return rvs
}

// ---------------------------------------------------------------------------
// Child access
// ---------------------------------------------------------------------------

// TestRVRS returns the TestRVRS for the given full RVRS name. Creates an
// empty wrapper if not yet tracked (events may arrive later).
func (t *TestRVS) TestRVRS(fullName string) *TestRVRS {
	return t.rvrss.Resolve(fullName)
}

// TestRVRSs returns all currently tracked TestRVRSs.
func (t *TestRVS) TestRVRSs() []*TestRVRS {
	return t.rvrss.All()
}

// RVRSCount returns the number of tracked RVRSs.
func (t *TestRVS) RVRSCount() int {
	return t.rvrss.Count()
}

// OnEachRVRS returns a GroupHandle for bulk operations on all current
// and future RVRS children.
func (t *TestRVS) OnEachRVRS() *tk.TrackedGroupHandle[*v1alpha1.ReplicatedVolumeReplicaSnapshot, *TestRVRS] {
	return t.rvrss.OnEach()
}

// ---------------------------------------------------------------------------
// Informer registration
// ---------------------------------------------------------------------------

// registerGroupInformers sets up informer watchers for RVRS children
// (by spec.replicatedVolumeSnapshotName) and for temp DRBDResource /
// DRBDResourceOperation objects (by name prefix "{rvsName}-").
func (t *TestRVS) registerGroupInformers(ctx context.Context) {
	name := t.Name()

	t.rvrss.Watch(ctx, func(rvrs *v1alpha1.ReplicatedVolumeReplicaSnapshot) bool {
		return rvrs.Spec.ReplicatedVolumeSnapshotName == name
	})

	prefix := name + "-"
	t.extraInformerRegs = append(t.extraInformerRegs,
		tk.RegisterInformer(ctx, t.f.Cache, gvkDRBDR, "DRBDR for RVS "+name,
			func(d *v1alpha1.DRBDResource) bool {
				return strings.HasPrefix(d.Name, prefix)
			},
			func(_ watch.EventType, _ *v1alpha1.DRBDResource) {},
		),
		tk.RegisterInformer(ctx, t.f.Cache, gvkDRBDOp, "DRBDOp for RVS "+name,
			func(o *v1alpha1.DRBDResourceOperation) bool {
				return strings.HasPrefix(o.Name, prefix)
			},
			func(_ watch.EventType, _ *v1alpha1.DRBDResourceOperation) {},
		),
	)
}

func (t *TestRVS) removeGroupInformers() {
	t.rvrss.UnwatchAll()
	tk.RemoveInformerRegs(t.extraInformerRegs)
	t.extraInformerRegs = nil
}
