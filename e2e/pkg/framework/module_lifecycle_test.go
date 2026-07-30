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
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	testDigestOld = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	testDigestNew = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	testImageOld  = "registry.example.com/sds-replicated-volume@" + testDigestOld
	testImageNew  = "registry.example.com/sds-replicated-volume@" + testDigestNew
	testTagOld    = "main"
	testTagNew    = "pr758"
)

// moduleScheme is the scheme the fake client serves the two Deckhouse kinds
// with. Neither has a Go type in this module's dependency set, so both are
// registered as unstructured — the same way the helper builds and reads them.
func moduleScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	for _, gvk := range []schema.GroupVersionKind{gvkModuleConfig, gvkModulePullOverride} {
		s.AddKnownTypeWithName(gvk, &unstructured.Unstructured{})
		s.AddKnownTypeWithName(gvk.GroupVersion().WithKind(gvk.Kind+"List"), &unstructured.UnstructuredList{})
	}
	return s
}

// countingModuleClient records how many writes the cores make, so "an object
// that is already correct is not written at all" is asserted directly instead of
// inferred from its contents.
type countingModuleClient struct {
	moduleObjectClient
	creates int
	updates int
}

func (c *countingModuleClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	c.creates++
	return c.moduleObjectClient.Create(ctx, obj, opts...)
}

func (c *countingModuleClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	c.updates++
	return c.moduleObjectClient.Update(ctx, obj, opts...)
}

// newModuleClient wires a counting client over a fake cluster holding objs.
func newModuleClient(objs ...client.Object) *countingModuleClient {
	return &countingModuleClient{
		moduleObjectClient: fake.NewClientBuilder().WithScheme(moduleScheme()).WithObjects(objs...).Build(),
	}
}

// moduleConfigObject builds the module's ModuleConfig the way a stand may
// already hold it.
func moduleConfigObject(spec map[string]any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{"spec": spec}}
	u.SetGroupVersionKind(gvkModuleConfig)
	u.SetName(ModuleName)
	return u
}

// modulePullOverrideObject builds the module's ModulePullOverride the way a
// stand may already hold it.
func modulePullOverrideObject(spec map[string]any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{"spec": spec}}
	u.SetGroupVersionKind(gvkModulePullOverride)
	u.SetName(ModuleName)
	return u
}

// racingModuleClient models the write race an accidental --procs>1 creates: a
// sibling worker aiming at the SAME tag lands its write between our Get and our
// write, so ours is answered with AlreadyExists or Conflict. The sibling writes
// through the wrapped client, so the error our write gets is the fake API
// server's own — resourceVersion semantics included — and not a hand-made one.
// The counters cover OUR writes only.
type racingModuleClient struct {
	moduleObjectClient
	racesLost int
	creates   int
	updates   int
}

func newRacingModuleClient(racesLost int, objs ...client.Object) *racingModuleClient {
	return &racingModuleClient{
		moduleObjectClient: fake.NewClientBuilder().WithScheme(moduleScheme()).WithObjects(objs...).Build(),
		racesLost:          racesLost,
	}
}

// letSiblingWin performs the write we were about to perform, on a copy of the
// object: what we hold is then either taken (Create) or stale (Update).
func (c *racingModuleClient) letSiblingWin(obj client.Object, write func(client.Object) error) error {
	if c.racesLost <= 0 {
		return nil
	}
	c.racesLost--
	sibling, ok := obj.DeepCopyObject().(client.Object)
	if !ok {
		return fmt.Errorf("%T is not a client.Object", obj)
	}
	return write(sibling)
}

func (c *racingModuleClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if err := c.letSiblingWin(obj, func(sibling client.Object) error {
		return c.moduleObjectClient.Create(ctx, sibling)
	}); err != nil {
		return err
	}
	c.creates++
	return c.moduleObjectClient.Create(ctx, obj, opts...)
}

func (c *racingModuleClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if err := c.letSiblingWin(obj, func(sibling client.Object) error {
		return c.moduleObjectClient.Update(ctx, sibling)
	}); err != nil {
		return err
	}
	c.updates++
	return c.moduleObjectClient.Update(ctx, obj, opts...)
}

// stuckModuleClient answers every Update the way the API server answers a writer
// whose object was modified under it, and never lets the state converge — a stand
// where somebody keeps rewriting the object under us. The conflict it reports
// names the ModuleConfig resource, the only write path it is used on.
type stuckModuleClient struct {
	moduleObjectClient
	updates int
}

func (c *stuckModuleClient) Update(_ context.Context, obj client.Object, _ ...client.UpdateOption) error {
	c.updates++
	return apierrors.NewConflict(
		schema.GroupResource{Group: gvkModuleConfig.Group, Resource: "moduleconfigs"},
		obj.GetName(), errors.New("the object has been modified"))
}

// readModuleObject reads one of the two Deckhouse objects back out of the fake
// cluster.
func readModuleObject(c moduleObjectClient, gvk schema.GroupVersionKind) *unstructured.Unstructured {
	out := &unstructured.Unstructured{}
	out.SetGroupVersionKind(gvk)
	ExpectWithOffset(1, c.Get(context.Background(), client.ObjectKey{Name: ModuleName}, out)).To(Succeed())
	return out
}

// specBool, specString and specInt64 read one field of a Deckhouse object and
// assert it is present. unstructured returns a (value, found, error) triple,
// which cannot be handed to Expect() directly.
func specBool(u *unstructured.Unstructured, fields ...string) bool {
	value, found, err := unstructured.NestedBool(u.Object, fields...)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	ExpectWithOffset(1, found).To(BeTrue(), "%v is not set", fields)
	return value
}

func specString(u *unstructured.Unstructured, fields ...string) string {
	value, found, err := unstructured.NestedString(u.Object, fields...)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	ExpectWithOffset(1, found).To(BeTrue(), "%v is not set", fields)
	return value
}

func specInt64(u *unstructured.Unstructured, fields ...string) int64 {
	value, found, err := unstructured.NestedInt64(u.Object, fields...)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	ExpectWithOffset(1, found).To(BeTrue(), "%v is not set", fields)
	return value
}

// readyWorkloadState builds a workload that finished rolling out: two pods, both
// on the current template, both ready.
func readyWorkloadState(w moduleWorkload, image string) moduleWorkloadState {
	return moduleWorkloadState{
		Workload:           w,
		Found:              true,
		Generation:         3,
		ObservedGeneration: 3,
		Desired:            2,
		Updated:            2,
		Current:            2,
		Ready:              2,
		Available:          2,
		Images:             []string{image},
	}
}

// releaseObservation builds an observation of a stand that runs the module with
// NO ModulePullOverride at all — installed from a release channel — with every
// expected workload healthy on image. It is the state in which creating an
// override must not be mistaken for a finished rollout.
func releaseObservation(image string) moduleObservation {
	var obs moduleObservation
	for _, w := range moduleWorkloads() {
		obs.Workloads = append(obs.Workloads, readyWorkloadState(w, image))
	}
	return obs
}

// readyObservation builds an observation in which the override reports digest and
// every expected workload finished rolling out on image.
func readyObservation(tag, digest, image string) moduleObservation {
	obs := releaseObservation(image)
	obs.MPOFound, obs.MPOTag, obs.MPODigest = true, tag, digest
	return obs
}

// pendingObservation builds an observation of a stand where the override exists
// but not a single workload does — a first installation that has not started.
func pendingObservation(tag, digest string) moduleObservation {
	obs := moduleObservation{MPOFound: true, MPOTag: tag, MPODigest: digest}
	for _, w := range moduleWorkloads() {
		obs.Workloads = append(obs.Workloads, moduleWorkloadState{Workload: w})
	}
	return obs
}

// moduleAnswer is one scripted reply to an observation: a snapshot, or a failure.
type moduleAnswer struct {
	obs moduleObservation
	err error
}

// stubModuleObserver answers observations from a script, so the readiness poll
// can be walked snapshot by snapshot without a cluster. The last entry repeats
// forever, which is how a steady state is written as a single entry instead of a
// filled-in budget.
type stubModuleObserver struct {
	script []moduleAnswer
	reads  int
}

func (s *stubModuleObserver) observeModule(_ context.Context, _ string) (moduleObservation, error) {
	s.reads++
	answer := s.script[min(s.reads, len(s.script))-1]
	return answer.obs, answer.err
}

var _ = Describe("buildModuleConfig", func() {
	It("enables the module and touches nothing else", func() {
		mc := buildModuleConfig(ModuleName)

		Expect(mc.GroupVersionKind()).To(Equal(gvkModuleConfig))
		Expect(mc.GetName()).To(Equal(ModuleName))

		spec, found, err := unstructured.NestedMap(mc.Object, "spec")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(spec).To(Equal(map[string]any{"enabled": true}),
			"a settings/version key of our own would overwrite the stand's configuration")
	})
})

var _ = Describe("buildModulePullOverride", func() {
	It("pins the tag with no rollback and a short scan interval", func() {
		mpo := buildModulePullOverride(ModuleName, testTagNew)

		Expect(mpo.GroupVersionKind()).To(Equal(gvkModulePullOverride))
		Expect(mpo.GetName()).To(Equal(ModuleName))

		spec, found, err := unstructured.NestedMap(mpo.Object, "spec")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(spec).To(Equal(map[string]any{
			"imageTag":     testTagNew,
			"rollback":     false,
			"scanInterval": modulePullOverrideScanInterval,
		}))
	})
})

var _ = Describe("ensureModuleConfigEnabled", func() {
	It("creates an enabled ModuleConfig when there is none", func() {
		c := newModuleClient()

		action, err := ensureModuleConfigEnabled(context.Background(), c, ModuleName)

		Expect(err).NotTo(HaveOccurred())
		Expect(action).To(Equal(moduleObjectCreated))
		Expect(c.creates).To(Equal(1))
		Expect(c.updates).To(BeZero())

		mc := readModuleObject(c, gvkModuleConfig)
		Expect(specBool(mc, "spec", "enabled")).To(BeTrue())
	})

	It("enables a disabled ModuleConfig and keeps its other settings", func() {
		c := newModuleClient(moduleConfigObject(map[string]any{
			"enabled":  false,
			"version":  int64(1),
			"settings": map[string]any{"dataNodes": map[string]any{"nodeSelector": map[string]any{"a": "b"}}},
		}))

		action, err := ensureModuleConfigEnabled(context.Background(), c, ModuleName)

		Expect(err).NotTo(HaveOccurred())
		Expect(action).To(Equal(moduleObjectUpdated))
		Expect(c.creates).To(BeZero())
		Expect(c.updates).To(Equal(1))

		mc := readModuleObject(c, gvkModuleConfig)
		Expect(specBool(mc, "spec", "enabled")).To(BeTrue())
		Expect(specInt64(mc, "spec", "version")).To(Equal(int64(1)))
		Expect(specString(mc, "spec", "settings", "dataNodes", "nodeSelector", "a")).To(Equal("b"))
	})

	It("writes nothing when the module is already enabled", func() {
		c := newModuleClient(moduleConfigObject(map[string]any{"enabled": true}))

		action, err := ensureModuleConfigEnabled(context.Background(), c, ModuleName)

		Expect(err).NotTo(HaveOccurred())
		Expect(action).To(Equal(moduleObjectUnchanged))
		Expect(c.creates).To(BeZero())
		Expect(c.updates).To(BeZero())
	})

	It("retakes a create a sibling worker won", func() {
		c := newRacingModuleClient(1)

		action, err := ensureModuleConfigEnabled(context.Background(), c, ModuleName)

		Expect(err).NotTo(HaveOccurred(), "AlreadyExists means the sibling wrote what we were about to write")
		Expect(action).To(Equal(moduleObjectUnchanged))
		Expect(c.creates).To(Equal(1), "the second pass finds the object already enabled")
		Expect(specBool(readModuleObject(c, gvkModuleConfig), "spec", "enabled")).To(BeTrue())
	})

	It("retakes an update a sibling worker won", func() {
		c := newRacingModuleClient(1, moduleConfigObject(map[string]any{"enabled": false}))

		action, err := ensureModuleConfigEnabled(context.Background(), c, ModuleName)

		Expect(err).NotTo(HaveOccurred(), "Conflict means the sibling wrote what we were about to write")
		Expect(action).To(Equal(moduleObjectUnchanged))
		Expect(c.updates).To(Equal(1), "the second pass finds the object already enabled")
		Expect(specBool(readModuleObject(c, gvkModuleConfig), "spec", "enabled")).To(BeTrue())
	})

	It("gives up when a conflicting writer never lets the state converge", func() {
		c := &stuckModuleClient{
			moduleObjectClient: newModuleClient(moduleConfigObject(map[string]any{"enabled": false})),
		}

		_, err := ensureModuleConfigEnabled(context.Background(), c, ModuleName)

		Expect(err).To(MatchError(ContainSubstring("another writer kept winning through 3 attempts")))
		Expect(c.updates).To(Equal(moduleWriteAttempts))
	})
})

var _ = Describe("ensureModulePullOverride", func() {
	It("creates the override when there is none", func() {
		c := newModuleClient()

		action, previousTag, err := ensureModulePullOverride(context.Background(), c, ModuleName, testTagOld)

		Expect(err).NotTo(HaveOccurred())
		Expect(action).To(Equal(moduleObjectCreated))
		Expect(previousTag).To(BeEmpty())
		Expect(c.creates).To(Equal(1))
		Expect(c.updates).To(BeZero())

		mpo := readModuleObject(c, gvkModulePullOverride)
		Expect(specString(mpo, "spec", "imageTag")).To(Equal(testTagOld))
	})

	It("retags a live override and reports the tag it replaced", func() {
		c := newModuleClient(modulePullOverrideObject(map[string]any{
			"imageTag":     testTagOld,
			"rollback":     false,
			"scanInterval": "1m",
		}))

		action, previousTag, err := ensureModulePullOverride(context.Background(), c, ModuleName, testTagNew)

		Expect(err).NotTo(HaveOccurred())
		Expect(action).To(Equal(moduleObjectUpdated))
		Expect(previousTag).To(Equal(testTagOld))
		Expect(c.creates).To(BeZero())
		Expect(c.updates).To(Equal(1))

		mpo := readModuleObject(c, gvkModulePullOverride)
		Expect(specString(mpo, "spec", "imageTag")).To(Equal(testTagNew))
		Expect(specString(mpo, "spec", "scanInterval")).To(Equal("1m"),
			"a scan interval the stand set on purpose is not ours to overwrite")
	})

	It("fills in the fields a hand-made override lacks", func() {
		c := newModuleClient(modulePullOverrideObject(map[string]any{"imageTag": testTagOld}))

		_, _, err := ensureModulePullOverride(context.Background(), c, ModuleName, testTagNew)

		Expect(err).NotTo(HaveOccurred())
		mpo := readModuleObject(c, gvkModulePullOverride)
		Expect(specBool(mpo, "spec", "rollback")).To(BeFalse())
		Expect(specString(mpo, "spec", "scanInterval")).To(Equal(modulePullOverrideScanInterval))
	})

	It("writes nothing when the override already pins the tag", func() {
		c := newModuleClient(modulePullOverrideObject(map[string]any{"imageTag": testTagNew}))

		action, previousTag, err := ensureModulePullOverride(context.Background(), c, ModuleName, testTagNew)

		Expect(err).NotTo(HaveOccurred())
		Expect(action).To(Equal(moduleObjectUnchanged))
		Expect(previousTag).To(Equal(testTagNew))
		Expect(c.creates).To(BeZero())
		Expect(c.updates).To(BeZero())
	})

	It("retakes a create a sibling worker won", func() {
		c := newRacingModuleClient(1)

		action, previousTag, err := ensureModulePullOverride(context.Background(), c, ModuleName, testTagNew)

		Expect(err).NotTo(HaveOccurred(), "AlreadyExists means the sibling pinned the tag we were pinning")
		Expect(action).To(Equal(moduleObjectUnchanged))
		Expect(previousTag).To(Equal(testTagNew))
		Expect(c.creates).To(Equal(1))
		Expect(specString(readModuleObject(c, gvkModulePullOverride), "spec", "imageTag")).To(Equal(testTagNew))
	})

	It("retakes a retag a sibling worker won", func() {
		c := newRacingModuleClient(1, modulePullOverrideObject(map[string]any{"imageTag": testTagOld}))

		action, previousTag, err := ensureModulePullOverride(context.Background(), c, ModuleName, testTagNew)

		Expect(err).NotTo(HaveOccurred(), "Conflict means the sibling retagged to the tag we were pinning")
		Expect(action).To(Equal(moduleObjectUnchanged))
		Expect(previousTag).To(Equal(testTagNew))
		Expect(c.updates).To(Equal(1))
		Expect(specString(readModuleObject(c, gvkModulePullOverride), "spec", "imageTag")).To(Equal(testTagNew))
	})
})

var _ = Describe("moduleDigestRequirementFor", func() {
	DescribeTable("derives what the criterion must demand from the state before the write",
		func(before moduleObservation, want moduleDigestRequirement) {
			Expect(moduleDigestRequirementFor(before, testTagNew)).To(Equal(want))
		},
		Entry("no override existed, so any published digest is ours",
			moduleObservation{}, digestPublished),
		Entry("no override existed even though the module was running",
			releaseObservation(testImageOld), digestPublished),
		Entry("a live override pinned another tag, so the digest it carries is stale",
			readyObservation(testTagOld, testDigestOld, testImageOld), digestChanged),
		Entry("the tag was already pinned, so no digest transition is coming",
			readyObservation(testTagNew, testDigestOld, testImageOld), digestIgnored),
	)
})

var _ = Describe("checkModuleReady", func() {
	retagged := moduleReadiness{DigestBefore: testDigestOld, Digest: digestChanged}
	created := moduleReadiness{Digest: digestPublished}

	It("accepts a rolled-out module when no digest change is expected", func() {
		Expect(checkModuleReady(moduleReadiness{},
			readyObservation(testTagOld, testDigestOld, testImageOld))).To(Succeed())
	})

	It("keeps waiting while an override this call created carries no digest", func() {
		err := checkModuleReady(created, readyObservation(testTagNew, "", testImageOld))

		Expect(err).To(MatchError(ContainSubstring("carries no status.imageDigest yet")),
			"the healthy workloads are the release build's until Deckhouse resolves our tag")
	})

	It("accepts an override this call created once Deckhouse published a digest", func() {
		Expect(checkModuleReady(created, readyObservation(testTagNew, testDigestNew, testImageNew))).
			To(Succeed())
	})

	It("accepts a retag whose pod templates did NOT change", func() {
		before := readyObservation(testTagOld, testDigestOld, testImageOld)
		after := readyObservation(testTagNew, testDigestNew, testImageOld)
		want := moduleReadiness{
			DigestBefore: testDigestOld,
			Digest:       digestChanged,
			ImagesBefore: observationImages(before),
		}

		Expect(checkModuleReady(want, after)).To(Succeed(),
			"werf builds are content-addressed: components untouched between two tags keep their digest")
		Expect(moduleImagesChanged(want.ImagesBefore, after)).To(BeEmpty())
	})

	It("keeps waiting while the override has published no digest", func() {
		err := checkModuleReady(retagged, readyObservation(testTagNew, "", testImageNew))

		Expect(err).To(MatchError(ContainSubstring("carries no status.imageDigest yet")))
	})

	It("keeps waiting while the override reports the digest it had before", func() {
		err := checkModuleReady(retagged, readyObservation(testTagNew, testDigestOld, testImageOld))

		Expect(err).To(MatchError(ContainSubstring("before the retag")))
	})

	It("rejects an observation with no workload at all", func() {
		err := checkModuleReady(moduleReadiness{}, moduleObservation{MPOFound: true, MPODigest: testDigestNew})

		Expect(err).To(MatchError(ContainSubstring("no workload of the module was observed")))
	})

	It("rejects an empty namespace, so a first installation is really awaited", func() {
		err := checkModuleReady(moduleReadiness{}, pendingObservation(testTagOld, testDigestOld))

		Expect(err).To(MatchError(ContainSubstring("deployment/controller does not exist yet")))
	})

	DescribeTable("rejects a workload that is not through its rollout",
		func(mutate func(*moduleWorkloadState), wantMessage string) {
			obs := readyObservation(testTagNew, testDigestNew, testImageNew)
			mutate(&obs.Workloads[4]) // daemonset/agent

			err := checkModuleReady(moduleReadiness{}, obs)

			Expect(err).To(MatchError(ContainSubstring(wantMessage)))
			Expect(err).To(MatchError(ContainSubstring("daemonset/agent")))
		},
		Entry("its controller has not observed the current generation",
			func(s *moduleWorkloadState) { s.ObservedGeneration = 2 }, "observedGeneration 2"),
		Entry("it wants no pods at all",
			func(s *moduleWorkloadState) { s.Desired, s.Updated, s.Current, s.Ready, s.Available = 0, 0, 0, 0, 0 },
			"wants 0 pods"),
		Entry("some pods still run the previous template",
			func(s *moduleWorkloadState) { s.Updated = 1 }, "1 of 2 pods run the current template"),
		Entry("pods of the previous template are still around",
			func(s *moduleWorkloadState) { s.Current = 3 }, "an older template is still around"),
		Entry("a pod is not available yet",
			func(s *moduleWorkloadState) { s.Available = 1 }, "1 of 2 pods available"),
		Entry("a pod is not ready yet",
			func(s *moduleWorkloadState) { s.Ready = 1 }, "1 of 2 pods ready"),
		Entry("it does not exist",
			func(s *moduleWorkloadState) { *s = moduleWorkloadState{Workload: s.Workload} }, "does not exist yet"),
	)
})

var _ = Describe("moduleImagesChanged", func() {
	It("names the workloads whose pod template was replaced", func() {
		before := readyObservation(testTagOld, testDigestOld, testImageOld)
		after := readyObservation(testTagNew, testDigestNew, testImageOld)
		after.Workloads[0].Images = []string{testImageNew}

		Expect(moduleImagesChanged(observationImages(before), after)).
			To(ConsistOf("deployment/controller"))
	})

	It("ignores a workload that does not exist", func() {
		before := readyObservation(testTagOld, testDigestOld, testImageOld)

		Expect(moduleImagesChanged(observationImages(before), pendingObservation(testTagNew, testDigestNew))).
			To(BeEmpty())
	})
})

var _ = Describe("awaitModuleReady", func() {
	It("returns as soon as the criterion is satisfied", func(ctx SpecContext) {
		observer := &stubModuleObserver{script: []moduleAnswer{
			{obs: readyObservation(testTagNew, testDigestNew, testImageNew)},
		}}

		Expect(awaitModuleReady(ctx, observer, ModuleName, moduleReadiness{},
			5*time.Second, time.Millisecond)).To(Succeed())
		Expect(observer.reads).To(Equal(1), "no retag, so no confirmation poll is needed")
	})

	It("waits for the new bundle digest and confirms the state once more", func(ctx SpecContext) {
		observer := &stubModuleObserver{script: []moduleAnswer{
			{obs: readyObservation(testTagNew, testDigestOld, testImageOld)},
			{obs: readyObservation(testTagNew, testDigestNew, testImageNew)},
			{obs: readyObservation(testTagNew, testDigestNew, testImageNew)},
		}}
		want := moduleReadiness{DigestBefore: testDigestOld, Digest: digestChanged}

		Expect(awaitModuleReady(ctx, observer, ModuleName, want, 5*time.Second, time.Millisecond)).To(Succeed())
		Expect(observer.reads).To(Equal(3), "one rejected poll, then the accepting one and its confirmation")
	})

	It("does not mistake the window between the new digest and the re-apply for a finished rollout",
		func(ctx SpecContext) {
			rolling := readyObservation(testTagNew, testDigestNew, testImageOld)
			rolling.Workloads[4].Generation = 4 // the re-apply landed, the DaemonSet is restarting
			observer := &stubModuleObserver{script: []moduleAnswer{
				{obs: readyObservation(testTagNew, testDigestNew, testImageOld)}, // digest new, old pods intact
				{obs: rolling},
				{obs: readyObservation(testTagNew, testDigestNew, testImageNew)},
			}}
			want := moduleReadiness{DigestBefore: testDigestOld, Digest: digestChanged}

			Expect(awaitModuleReady(ctx, observer, ModuleName, want, 5*time.Second, time.Millisecond)).To(Succeed())
			Expect(observer.reads).To(Equal(4),
				"the premature sample is dropped by the confirmation poll, which sees the rollout start")
		})

	It("reports that an accepted state was never confirmed", func(ctx SpecContext) {
		observer := &stubModuleObserver{script: []moduleAnswer{
			{obs: readyObservation(testTagNew, testDigestNew, testImageNew)},
		}}
		want := moduleReadiness{DigestBefore: testDigestOld, Digest: digestChanged}

		err := awaitModuleReady(ctx, observer, ModuleName, want, 0, time.Millisecond)

		Expect(err).To(MatchError(ContainSubstring("held for 1 of the 2 polls")))
	})

	It("reports the last reason when the budget runs out", func(ctx SpecContext) {
		observer := &stubModuleObserver{script: []moduleAnswer{
			{obs: readyObservation(testTagNew, testDigestOld, testImageOld)},
		}}
		want := moduleReadiness{DigestBefore: testDigestOld, Digest: digestChanged}

		err := awaitModuleReady(ctx, observer, ModuleName, want, 10*time.Millisecond, time.Millisecond)

		Expect(err).To(MatchError(ContainSubstring("did not become ready within 10ms")))
		Expect(err).To(MatchError(ContainSubstring("before the retag")))
	})

	It("treats a failed read as 'not yet', not as a verdict", func(ctx SpecContext) {
		observer := &stubModuleObserver{script: []moduleAnswer{
			{err: errors.New("the webhook is being restarted")},
			{err: errors.New("the webhook is being restarted")},
			{obs: readyObservation(testTagNew, testDigestNew, testImageNew)},
		}}

		Expect(awaitModuleReady(ctx, observer, ModuleName, moduleReadiness{},
			5*time.Second, time.Millisecond)).To(Succeed())
		Expect(observer.reads).To(Equal(3))
	})

	It("reports a read that never succeeded", func(ctx SpecContext) {
		observer := &stubModuleObserver{script: []moduleAnswer{{err: errors.New("connection refused")}}}

		err := awaitModuleReady(ctx, observer, ModuleName, moduleReadiness{},
			10*time.Millisecond, time.Millisecond)

		Expect(err).To(MatchError(ContainSubstring("connection refused")))
	})

	It("stops on a cancelled context and keeps the last state in the message", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		observer := &stubModuleObserver{script: []moduleAnswer{{obs: pendingObservation(testTagNew, testDigestNew)}}}

		err := awaitModuleReady(ctx, observer, ModuleName, moduleReadiness{}, time.Minute, time.Second)

		Expect(err).To(MatchError(context.Canceled))
		Expect(err).To(MatchError(ContainSubstring("does not exist yet")))
	})
})

var _ = Describe("ensureModuleVersion", func() {
	It("retags a live override and waits for a NEW bundle digest", func(ctx SpecContext) {
		c := newModuleClient(
			moduleConfigObject(map[string]any{"enabled": true}),
			modulePullOverrideObject(map[string]any{"imageTag": testTagOld}),
		)
		observer := &stubModuleObserver{script: []moduleAnswer{
			{obs: readyObservation(testTagOld, testDigestOld, testImageOld)}, // before the write
			{obs: readyObservation(testTagNew, testDigestOld, testImageOld)}, // digest not updated yet
			{obs: readyObservation(testTagNew, testDigestNew, testImageNew)},
		}}

		Expect(ensureModuleVersion(ctx, c, observer, ModuleName, testTagNew,
			5*time.Second, time.Millisecond)).To(Succeed())

		Expect(observer.reads).To(Equal(4),
			"the pre-write snapshot, a poll on the old digest, the accepting poll and its confirmation")
		Expect(c.updates).To(Equal(1))
		mpo := readModuleObject(c, gvkModulePullOverride)
		Expect(specString(mpo, "spec", "imageTag")).To(Equal(testTagNew))
	})

	It("does not wait for a digest change when the tag is already pinned", func(ctx SpecContext) {
		c := newModuleClient(
			moduleConfigObject(map[string]any{"enabled": true}),
			modulePullOverrideObject(map[string]any{"imageTag": testTagNew}),
		)
		observer := &stubModuleObserver{script: []moduleAnswer{
			{obs: readyObservation(testTagNew, testDigestOld, testImageOld)},
		}}

		Expect(ensureModuleVersion(ctx, c, observer, ModuleName, testTagNew,
			5*time.Second, time.Millisecond)).To(Succeed())

		Expect(c.creates).To(BeZero())
		Expect(c.updates).To(BeZero())
		Expect(observer.reads).To(Equal(2), "the pre-write snapshot plus one accepting poll")
	})

	It("installs the module from scratch and waits for the workloads to appear", func(ctx SpecContext) {
		c := newModuleClient()
		observer := &stubModuleObserver{script: []moduleAnswer{
			{obs: moduleObservation{Workloads: projectModuleWorkloads(
				moduleWorkloads(), &appsv1.DeploymentList{}, &appsv1.DaemonSetList{})}},
			{obs: pendingObservation(testTagOld, "")},
			{obs: readyObservation(testTagOld, testDigestOld, testImageOld)},
		}}

		Expect(ensureModuleVersion(ctx, c, observer, ModuleName, testTagOld,
			5*time.Second, time.Millisecond)).To(Succeed())

		Expect(c.creates).To(Equal(2), "the ModuleConfig and the ModulePullOverride")
		Expect(observer.reads).To(Equal(4),
			"the pre-write snapshot, the poll without a digest, the accepting poll and its confirmation")
		Expect(specBool(readModuleObject(c, gvkModuleConfig), "spec", "enabled")).To(BeTrue())
		Expect(specString(readModuleObject(c, gvkModulePullOverride), "spec", "imageTag")).
			To(Equal(testTagOld))
	})

	It("does not accept the build a stand already runs as the version it just pinned", func(ctx SpecContext) {
		pinnedNotResolved := releaseObservation(testImageOld)
		pinnedNotResolved.MPOFound, pinnedNotResolved.MPOTag = true, testTagNew
		c := newModuleClient(moduleConfigObject(map[string]any{"enabled": true}))
		observer := &stubModuleObserver{script: []moduleAnswer{
			// Before the write: no override, and every workload of the module healthy
			// on the build a release channel installed.
			{obs: releaseObservation(testImageOld)},
			// Our override exists; Deckhouse has not resolved the tag yet, so the
			// healthy workloads are still the release build's.
			{obs: pinnedNotResolved},
			{obs: pinnedNotResolved},
			{obs: readyObservation(testTagNew, testDigestNew, testImageNew)},
			{obs: readyObservation(testTagNew, testDigestNew, testImageNew)},
		}}

		Expect(ensureModuleVersion(ctx, c, observer, ModuleName, testTagNew,
			5*time.Second, time.Millisecond)).To(Succeed())

		Expect(observer.reads).To(Equal(5),
			"a populated namespace with no digest published is not readiness, however healthy it looks")
		Expect(c.creates).To(Equal(1), "only the ModulePullOverride, the ModuleConfig was already enabled")
	})

	It("keeps waiting for the digest when a sibling worker pinned the tag first", func(ctx SpecContext) {
		c := newRacingModuleClient(2, moduleConfigObject(map[string]any{"enabled": false}))
		observer := &stubModuleObserver{script: []moduleAnswer{
			{obs: releaseObservation(testImageOld)}, // before the write: no override
			{obs: readyObservation(testTagNew, testDigestNew, testImageNew)},
			{obs: readyObservation(testTagNew, testDigestNew, testImageNew)},
		}}

		Expect(ensureModuleVersion(ctx, c, observer, ModuleName, testTagNew,
			5*time.Second, time.Millisecond)).To(Succeed())

		Expect(observer.reads).To(Equal(3),
			"the write reports 'unchanged', but the snapshot had no override, so a digest is still awaited")
		Expect(specBool(readModuleObject(c, gvkModuleConfig), "spec", "enabled")).To(BeTrue())
		Expect(specString(readModuleObject(c, gvkModulePullOverride), "spec", "imageTag")).To(Equal(testTagNew))
	})

	DescribeTable("refuses to write anything on invalid input",
		func(moduleName, imageTag, wantMessage string) {
			c := newModuleClient()
			observer := &stubModuleObserver{script: []moduleAnswer{
				{obs: readyObservation(testTagOld, testDigestOld, testImageOld)},
			}}

			err := ensureModuleVersion(context.Background(), c, observer, moduleName, imageTag,
				time.Second, time.Millisecond)

			Expect(err).To(MatchError(ContainSubstring(wantMessage)))
			Expect(c.creates).To(BeZero())
			Expect(c.updates).To(BeZero())
			Expect(observer.reads).To(BeZero())
		},
		Entry("no module name", "", testTagNew, "module name must not be empty"),
		Entry("no image tag", ModuleName, "", "image tag of module"),
	)

	It("does not write when the pre-write snapshot cannot be read", func(ctx SpecContext) {
		c := newModuleClient()
		observer := &stubModuleObserver{script: []moduleAnswer{{err: errors.New("connection refused")}}}

		err := ensureModuleVersion(ctx, c, observer, ModuleName, testTagNew, time.Second, time.Millisecond)

		Expect(err).To(MatchError(ContainSubstring("before the write")))
		Expect(c.creates).To(BeZero())
		Expect(c.updates).To(BeZero())
	})
})

var _ = Describe("projectModuleWorkloads", func() {
	It("projects a Deployment's rollout counters and defaults its replicas to 1", func() {
		deployments := &appsv1.DeploymentList{Items: []appsv1.Deployment{{
			ObjectMeta: metav1.ObjectMeta{Name: "controller", Namespace: moduleNamespace, Generation: 4},
			Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "controller", Image: testImageNew}},
			}}},
			Status: appsv1.DeploymentStatus{
				ObservedGeneration: 4, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1, AvailableReplicas: 1,
			},
		}}}

		states := projectModuleWorkloads(moduleWorkloads(), deployments, &appsv1.DaemonSetList{})

		Expect(states).To(HaveLen(len(moduleWorkloads())))
		Expect(states[0].Found).To(BeTrue())
		Expect(states[0].Desired).To(Equal(int32(1)))
		Expect(states[0].Images).To(Equal([]string{testImageNew}))
		Expect(states[0].rolloutError()).To(Succeed())
		Expect(states[1].Found).To(BeFalse(), "csi-controller is not in the list")
	})

	It("projects a DaemonSet's rollout counters", func() {
		daemonSets := &appsv1.DaemonSetList{Items: []appsv1.DaemonSet{{
			ObjectMeta: metav1.ObjectMeta{Name: "agent", Namespace: moduleNamespace, Generation: 7},
			Spec: appsv1.DaemonSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				InitContainers: []corev1.Container{{Name: "init", Image: testImageOld}},
				Containers:     []corev1.Container{{Name: "agent", Image: testImageNew}},
			}}},
			Status: appsv1.DaemonSetStatus{
				ObservedGeneration: 7, DesiredNumberScheduled: 4, UpdatedNumberScheduled: 4,
				CurrentNumberScheduled: 4, NumberReady: 4, NumberAvailable: 4,
			},
		}}}

		states := projectModuleWorkloads(moduleWorkloads(), &appsv1.DeploymentList{}, daemonSets)

		agent := states[4]
		Expect(agent.Workload.String()).To(Equal("daemonset/agent"))
		Expect(agent.Found).To(BeTrue())
		Expect(agent.Desired).To(Equal(int32(4)))
		Expect(agent.Images).To(Equal([]string{testImageOld, testImageNew}),
			"init containers count, and the set is sorted")
		Expect(agent.rolloutError()).To(Succeed())
	})

	It("does not satisfy a DaemonSet with a Deployment of the same name", func() {
		deployments := &appsv1.DeploymentList{Items: []appsv1.Deployment{{
			ObjectMeta: metav1.ObjectMeta{Name: "agent", Namespace: moduleNamespace},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To(int32(1))},
		}}}

		states := projectModuleWorkloads(moduleWorkloads(), deployments, &appsv1.DaemonSetList{})

		Expect(states[4].Workload.Kind).To(Equal(workloadDaemonSet))
		Expect(states[4].Found).To(BeFalse())
	})
})

var _ = Describe("WithPreDiscovery", func() {
	It("is not registered by default", func() {
		Expect((&Framework{}).preDiscovery).To(BeNil())
	})

	It("stores the hook it is given", func(ctx SpecContext) {
		called := 0
		f := &Framework{}

		WithPreDiscovery(func(context.Context, *Framework) error {
			called++
			return nil
		})(f)

		Expect(f.preDiscovery).NotTo(BeNil())
		Expect(f.preDiscovery(ctx, f)).To(Succeed())
		Expect(called).To(Equal(1))
	})
})
