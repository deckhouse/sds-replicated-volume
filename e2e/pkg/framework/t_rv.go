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
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/match"
	tk "github.com/deckhouse/sds-replicated-volume/lib/go/testkit"
	tkmatch "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

// TestRV creates a new TestRV with an auto-generated or named name.
func (f *Framework) TestRV(name ...string) *TestRV {
	var zero *v1alpha1.ReplicatedVolume
	return newTestRV(f, f.autoName(zero, name...))
}

// TestRVExact creates a new TestRV with an exact literal name (no prefix/slug).
func (f *Framework) TestRVExact(fullName string) *TestRV {
	return newTestRV(f, fullName)
}

func newTestRV(f *Framework, name string) *TestRV {
	trv := &TestRV{f: f}

	trv.rvrs = tk.NewTrackedGroup(f.Cache, f.Client, gvkRVR,
		func(rvrName string) *TestRVR {
			rvr := &TestRVR{f: f, buildRVName: name}
			rvr.TrackedObject = tk.NewTrackedObject(f.Cache, f.Client, gvkRVR, rvrName,
				tk.Lifecycle[*v1alpha1.ReplicatedVolumeReplica]{
					OnBuild:    func(_ context.Context) *v1alpha1.ReplicatedVolumeReplica { return rvr.buildObject() },
					OnNewEmpty: func() *v1alpha1.ReplicatedVolumeReplica { return &v1alpha1.ReplicatedVolumeReplica{} },
				})
			return rvr
		},
	)
	trv.rvas = tk.NewTrackedGroup(f.Cache, f.Client, gvkRVA,
		func(rvaName string) *TestRVA {
			rva := &TestRVA{f: f}
			rva.TrackedObject = tk.NewTrackedObject(f.Cache, f.Client, gvkRVA, rvaName,
				tk.Lifecycle[*v1alpha1.ReplicatedVolumeAttachment]{
					OnBuild:    func(_ context.Context) *v1alpha1.ReplicatedVolumeAttachment { return rva.buildObject() },
					OnNewEmpty: func() *v1alpha1.ReplicatedVolumeAttachment { return &v1alpha1.ReplicatedVolumeAttachment{} },
				})
			return rva
		},
	)

	trv.TrackedObject = tk.NewTrackedObject(f.Cache, f.Client, gvkRV, name,
		tk.Lifecycle[*v1alpha1.ReplicatedVolume]{
			Debugger:   f.Debugger,
			Standalone: true,
			OnBuild:    func(ctx context.Context) *v1alpha1.ReplicatedVolume { return trv.buildObject(ctx) },
			OnNewEmpty: func() *v1alpha1.ReplicatedVolume { return &v1alpha1.ReplicatedVolume{} },
			OnSetup:    func(ctx context.Context) { trv.registerGroupInformers(ctx) },
			OnTeardown: func() { trv.removeGroupInformers() },
		})

	return trv
}

// TestRV is the domain wrapper for ReplicatedVolume test objects.
// It embeds TestObject for state tracking and adds builder chain,
// child management (RVR, RVA groups), and safety invariants.
type TestRV struct {
	*tk.TrackedObject[*v1alpha1.ReplicatedVolume]
	f    *Framework
	rvrs *tk.TrackedGroup[*v1alpha1.ReplicatedVolumeReplica, *TestRVR]
	rvas *tk.TrackedGroup[*v1alpha1.ReplicatedVolumeAttachment, *TestRVA]

	extraInformerRegs []tk.InformerReg

	// Builder fields — nil means "not set, let API server default".
	buildFTT               *byte
	buildGMDR              *byte
	buildSize              *resource.Quantity
	buildMaxAttach         *byte
	buildTopology          *v1alpha1.ReplicatedStorageClassTopology
	buildAccess            *v1alpha1.ReplicatedStorageClassVolumeAccess
	buildPoolType          *v1alpha1.ReplicatedStoragePoolType
	buildRSCName           string
	buildManualCfg         *v1alpha1.ReplicatedVolumeConfiguration
	buildAdopt             bool
	buildAdoptSharedSecret string
	buildDataSource        *v1alpha1.VolumeDataSource

	// Safety switches (populated by ActivateSafetyInvariants).
	swQuorumCorrect    *tkmatch.Switch
	swNeverLoseQuorum  *tkmatch.Switch
	swNeverCritical    *tkmatch.Switch
	swNeverIOSuspended *tkmatch.Switch
}

// ---------------------------------------------------------------------------
// Builder chain (fluent, returns *TestRV)
// ---------------------------------------------------------------------------

func (t *TestRV) FTT(n byte) *TestRV {
	t.buildFTT = &n
	return t
}

func (t *TestRV) GMDR(n byte) *TestRV {
	t.buildGMDR = &n
	return t
}

func (t *TestRV) Size(s string) *TestRV {
	q := resource.MustParse(s)
	t.buildSize = &q
	return t
}

func (t *TestRV) MaxAttachments(n byte) *TestRV {
	t.buildMaxAttach = &n
	return t
}

func (t *TestRV) Topology(topo v1alpha1.ReplicatedStorageClassTopology) *TestRV {
	t.buildTopology = &topo
	return t
}

func (t *TestRV) VolumeAccess(va v1alpha1.ReplicatedStorageClassVolumeAccess) *TestRV {
	t.buildAccess = &va
	return t
}

func (t *TestRV) WithPoolType(pt v1alpha1.ReplicatedStoragePoolType) *TestRV {
	t.buildPoolType = &pt
	return t
}

func (t *TestRV) RSCName(name string) *TestRV {
	t.buildRSCName = name
	return t
}

func (t *TestRV) ManualConfig(cfg v1alpha1.ReplicatedVolumeConfiguration) *TestRV {
	t.buildManualCfg = &cfg
	return t
}

func (t *TestRV) Adopt() *TestRV {
	t.buildAdopt = true
	return t
}

func (t *TestRV) AdoptSharedSecret(s string) *TestRV {
	t.buildAdoptSharedSecret = s
	return t
}

// DataSourceRVS binds the new RV to a ReplicatedVolumeSnapshot source
// (restore from snapshot). The RV and the source RVS must be in the
// same cluster.
func (t *TestRV) DataSourceRVS(rvsName string) *TestRV {
	t.buildDataSource = &v1alpha1.VolumeDataSource{
		Kind: v1alpha1.VolumeDataSourceKindReplicatedVolumeSnapshot,
		Name: rvsName,
	}
	return t
}

// DataSourceRV binds the new RV to a source ReplicatedVolume (clone).
func (t *TestRV) DataSourceRV(rvName string) *TestRV {
	t.buildDataSource = &v1alpha1.VolumeDataSource{
		Kind: v1alpha1.VolumeDataSourceKindReplicatedVolume,
		Name: rvName,
	}
	return t
}

// ---------------------------------------------------------------------------
// buildObject
// ---------------------------------------------------------------------------

// buildObject returns the API object from builder fields.
func (t *TestRV) buildObject(ctx context.Context) *v1alpha1.ReplicatedVolume {
	rv := &v1alpha1.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: t.Name(),
		},
	}
	if t.buildSize != nil {
		rv.Spec.Size = *t.buildSize
	} else {
		rv.Spec.Size = resource.MustParse("1Mi")
	}
	if t.buildMaxAttach != nil {
		rv.Spec.MaxAttachments = t.buildMaxAttach
	}
	if t.buildManualCfg != nil {
		rv.Spec.ConfigurationMode = v1alpha1.ReplicatedVolumeConfigurationModeManual
		rv.Spec.ManualConfiguration = t.buildManualCfg
	} else {
		rv.Spec.ConfigurationMode = v1alpha1.ReplicatedVolumeConfigurationModeAuto
		if t.buildRSCName != "" {
			rv.Spec.ReplicatedStorageClassName = t.buildRSCName
		} else if t.f != nil {
			trsc := t.f.SharedRSC()
			if t.buildFTT != nil {
				trsc.FTT(*t.buildFTT)
			}
			if t.buildGMDR != nil {
				trsc.GMDR(*t.buildGMDR)
			}
			if t.buildTopology != nil {
				trsc.Topology(*t.buildTopology)
			}
			if t.buildAccess != nil {
				trsc.VolumeAccess(*t.buildAccess)
			}
			if t.buildPoolType != nil {
				trsc.StorageType(*t.buildPoolType)
			}
			trsc.CreateShared(ctx)
			rv.Spec.ReplicatedStorageClassName = trsc.Name()
		}
	}
	if t.f != nil {
		t.f.stampMetadata(rv)
	}
	if t.buildAdopt {
		ann := rv.GetAnnotations()
		if ann == nil {
			ann = make(map[string]string)
		}
		ann[v1alpha1.AdoptRVRAnnotationKey] = ""
		if t.buildAdoptSharedSecret != "" {
			ann[v1alpha1.AdoptSharedSecretAnnotationKey] = t.buildAdoptSharedSecret
		}
		rv.SetAnnotations(ann)
	}
	if t.buildDataSource != nil {
		ds := *t.buildDataSource
		rv.Spec.DataSource = &ds
	}
	return rv
}

// Attach creates a TestRVA for this RV on the given node. The RVA receives
// events from the parent RV's group informer (no extra WatchSelf needed).
func (t *TestRV) Attach(ctx context.Context, nodeName string) *TestRVA {
	rvaName := t.Name() + "-" + sanitizeNodeName(nodeName)
	trva := t.rvas.Resolve(rvaName)
	trva.buildRVName = t.Name()
	n := nodeName
	trva.buildNodeName = &n
	trva.Create(ctx)
	return trva
}

// ---------------------------------------------------------------------------
// Child access
// ---------------------------------------------------------------------------

// TestRVR returns the TestRVR for the RVR with the given suffix ID.
// Creates an empty TestRVR if not yet tracked (events may arrive later).
func (t *TestRV) TestRVR(id int) *TestRVR {
	name := fmt.Sprintf("%s-%d", t.Name(), id)
	return t.rvrs.Resolve(name)
}

// TestRVRs returns all currently tracked TestRVRs.
func (t *TestRV) TestRVRs() []*TestRVR {
	return t.rvrs.All()
}

// RVRCount returns the number of tracked RVRs.
func (t *TestRV) RVRCount() int {
	return t.rvrs.Count()
}

// TestRVAs returns all currently tracked TestRVAs.
func (t *TestRV) TestRVAs() []*TestRVA {
	return t.rvas.All()
}

// RVACount returns the number of tracked RVAs.
func (t *TestRV) RVACount() int {
	return t.rvas.Count()
}

// RVANodes returns the set of node names that have a tracked RVA.
func (t *TestRV) RVANodes() map[string]bool {
	nodes := make(map[string]bool)
	for _, trva := range t.rvas.All() {
		obj := trva.Object()
		if obj != nil && obj.Spec.NodeName != "" {
			nodes[obj.Spec.NodeName] = true
		}
	}
	return nodes
}

// OccupiedNodes returns the node names of all current datamesh members.
func (t *TestRV) OccupiedNodes() []string {
	rv := t.Object()
	nodes := make([]string, len(rv.Status.Datamesh.Members))
	for i, m := range rv.Status.Datamesh.Members {
		nodes[i] = m.NodeName
	}
	return nodes
}

// FreeReplicaID returns the lowest ID (0-31) not used by any current
// datamesh member or tracked RVR. Panics if all 32 IDs are taken.
func (t *TestRV) FreeReplicaID() uint8 {
	taken := make(map[uint8]bool)
	for _, m := range t.Object().Status.Datamesh.Members {
		taken[m.ID()] = true
	}
	for _, rvr := range t.TestRVRs() {
		taken[rvr.ID()] = true
	}
	for id := uint8(0); id <= 31; id++ {
		if !taken[id] {
			return id
		}
	}
	panic("all 32 replica IDs are taken")
}

// OnEachRVR returns a GroupHandle for bulk operations on all current
// and future RVR children.
func (t *TestRV) OnEachRVR() *tk.TrackedGroupHandle[*v1alpha1.ReplicatedVolumeReplica, *TestRVR] {
	return t.rvrs.OnEach()
}

// ---------------------------------------------------------------------------
// Informer registration
// ---------------------------------------------------------------------------

// registerGroupInformers sets up informer watchers for the RV's
// RVR and RVA children, DRBDR and LLV resources. watchSelf for
// the RV itself is handled by Standalone.
func (t *TestRV) registerGroupInformers(ctx context.Context) {
	name := t.Name()

	t.rvrs.Watch(ctx, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return rvr.Spec.ReplicatedVolumeName == name
	})

	t.rvas.Watch(ctx, func(rva *v1alpha1.ReplicatedVolumeAttachment) bool {
		return rva.Spec.ReplicatedVolumeName == name
	})

	t.extraInformerRegs = append(t.extraInformerRegs,
		tk.RegisterInformer(ctx, t.f.Cache, gvkDRBDR, "DRBDR for "+name,
			func(d *v1alpha1.DRBDResource) bool {
				return strings.HasPrefix(d.Name, name+"-")
			},
			func(et watch.EventType, d *v1alpha1.DRBDResource) {
				t.routeDRBDREvent(d.Name, et, d)
			},
		),
		tk.RegisterInformer(ctx, t.f.Cache, gvkLLV, "LLV for "+name,
			func(llv *snc.LVMLogicalVolume) bool {
				for _, ref := range llv.GetOwnerReferences() {
					if ref.Kind == "ReplicatedVolumeReplica" && strings.HasPrefix(ref.Name, name+"-") {
						return true
					}
				}
				return false
			},
			func(et watch.EventType, llv *snc.LVMLogicalVolume) {
				t.routeLLVEvent(llv.Name, et, llv)
			},
		),
	)
}

// removeGroupInformers tears down all informers registered by registerGroupInformers.
func (t *TestRV) removeGroupInformers() {
	t.rvrs.UnwatchAll()
	t.rvas.UnwatchAll()
	tk.RemoveInformerRegs(t.extraInformerRegs)
	t.extraInformerRegs = nil
}

// routeDRBDREvent routes a DRBDR event to the corresponding TestRVR.
// DRBDR name == RVR name (by convention).
func (t *TestRV) routeDRBDREvent(drbdrName string, eventType watch.EventType, drbdr *v1alpha1.DRBDResource) {
	prefix := t.Name() + "-"
	if !strings.HasPrefix(drbdrName, prefix) {
		return
	}
	t.rvrs.Resolve(drbdrName).injectDRBDREvent(eventType, drbdr)
}

// routeLLVEvent routes an LLV event to the corresponding TestRVR.
// LLV is owned by an RVR via ownerReference.
func (t *TestRV) routeLLVEvent(_ string, eventType watch.EventType, llv *snc.LVMLogicalVolume) {
	for _, ref := range llv.GetOwnerReferences() {
		if ref.Kind == "ReplicatedVolumeReplica" {
			t.rvrs.Resolve(ref.Name).injectLLVEvent(eventType, llv)
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Safety invariants
// ---------------------------------------------------------------------------

// ActivateSafetyInvariants creates Switch-wrapped safety checks on
// the RV (QuorumCorrect) and all current+future RVRs (NeverLoseQuorum,
// NeverCritical, NeverIOSuspended). Use WithoutSafetyInvariants to
// temporarily disable them during disruptive operations.
func (t *TestRV) ActivateSafetyInvariants() {
	t.swQuorumCorrect = tkmatch.NewSwitch(match.RV.QuorumCorrect())
	t.Always(t.swQuorumCorrect)

	t.swNeverLoseQuorum = tkmatch.NewSwitch(match.RVR.NeverLoseQuorum())
	t.swNeverCritical = tkmatch.NewSwitch(match.RVR.NeverCritical())
	t.swNeverIOSuspended = tkmatch.NewSwitch(match.RVR.NeverIOSuspended())

	t.OnEachRVR().After(tkmatch.Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)), t.swNeverLoseQuorum)
	t.OnEachRVR().Always(t.swNeverCritical)
	t.OnEachRVR().Always(t.swNeverIOSuspended)
}

// WithoutSafetyInvariants disables all safety switches for the
// duration of fn, then re-enables them. Panics if
// ActivateSafetyInvariants was not called first.
func (t *TestRV) WithoutSafetyInvariants(fn func()) {
	if t.swQuorumCorrect == nil {
		panic("WithoutSafetyInvariants called before ActivateSafetyInvariants")
	}
	t.swQuorumCorrect.Disable()
	t.swNeverLoseQuorum.Disable()
	t.swNeverCritical.Disable()
	t.swNeverIOSuspended.Disable()
	defer func() {
		t.swQuorumCorrect.Enable()
		t.swNeverLoseQuorum.Enable()
		t.swNeverCritical.Enable()
		t.swNeverIOSuspended.Enable()
	}()
	fn()
}
