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

	// testVersionRelease is properties.version of a module a release channel
	// installed: a semantic version, not a tag. It is what the version gate has to
	// refuse while Deckhouse has not restored the module from our override.
	testVersionRelease = "v0.9.1"

	// The readiness transitions of the two test bundles. They look like the
	// timestamps Deckhouse writes, but the helper compares them and never parses
	// them, so only their inequality carries meaning.
	testTransitionOld = "2026-07-31T02:20:00Z"
	testTransitionNew = "2026-07-31T02:32:10Z"

	// testPhaseReconciling is the phase Deckhouse publishes while it re-runs a
	// module.
	testPhaseReconciling = "Reconciling"
)

// transitionFor is the readiness transition of a module that last ran to apply
// bundle digest. Deckhouse takes a module out of ready and back on every run, so
// the two test bundles cannot share one transition — which is why the fixtures
// derive it, and why a test about a module whose bundle changed but which has NOT
// run yet has to set the field by hand.
func transitionFor(digest string) string {
	if digest == testDigestNew {
		return testTransitionNew
	}
	return testTransitionOld
}

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
// NO ModulePullOverride at all — installed from a release channel, so Deckhouse
// reports its release version — with every expected workload healthy on image. It
// is the state in which creating an override must not be mistaken for a finished
// rollout.
func releaseObservation(image string) moduleObservation {
	obs := moduleObservation{
		ModuleFound:           true,
		ModulePhase:           moduleReadyPhase,
		ModuleVersion:         testVersionRelease,
		ModuleReadyTransition: testTransitionOld,
	}
	for _, w := range moduleWorkloads() {
		obs.Workloads = append(obs.Workloads, readyWorkloadState(w, image))
	}
	return obs
}

// readyObservation builds an observation in which the override reports digest,
// Deckhouse reports the module ready at tag, and every expected workload finished
// rolling out on image.
func readyObservation(tag, digest, image string) moduleObservation {
	obs := releaseObservation(image)
	obs.MPOFound, obs.MPOTag, obs.MPODigest = true, tag, digest
	obs.ModuleVersion, obs.ModuleReadyTransition = tag, transitionFor(digest)
	return obs
}

// pendingObservation builds an observation of a stand where the override exists
// but not a single workload does — a first installation that has not started.
func pendingObservation(tag, digest string) moduleObservation {
	obs := moduleObservation{
		MPOFound:              true,
		MPOTag:                tag,
		MPODigest:             digest,
		ModuleFound:           true,
		ModulePhase:           moduleReadyPhase,
		ModuleVersion:         tag,
		ModuleReadyTransition: transitionFor(digest),
	}
	for _, w := range moduleWorkloads() {
		obs.Workloads = append(obs.Workloads, moduleWorkloadState{Workload: w})
	}
	return obs
}

// retaggedReadiness is what a poll waits for after a live override was retagged
// from testTagOld to testTagNew: a new bundle digest, the new tag in the Module,
// and a readiness transition other than the one the old bundle left behind.
func retaggedReadiness() moduleReadiness {
	return moduleReadiness{
		DigestBefore:          testDigestOld,
		Digest:                digestChanged,
		ImageTag:              testTagNew,
		ReadyTransitionBefore: transitionFor(testDigestOld),
	}
}

// staleReadyObservation builds the observation the old criterion was fooled by:
// Deckhouse has published the new bundle digest and restarted itself, but the
// Module still carries the version, the readiness transition and the phase it had
// BEFORE the restart, and every workload still runs the old build, calmly and
// healthily.
func staleReadyObservation() moduleObservation {
	obs := readyObservation(testTagNew, testDigestNew, testImageOld)
	obs.ModuleVersion = testTagOld
	obs.ModuleReadyTransition = transitionFor(testDigestOld)
	return obs
}

// testWaitPolicy budgets a unit-test wait: a real budget, so a bug fails the test
// instead of hanging it, and polls that do not wait in real time. The observation
// window is per test, because how many polls it takes is what several of them
// assert; with a 1ms poll, 0 means one accepting poll, 1ms means two and 2ms
// means three (moduleStableSamples).
func testWaitPolicy(timeout, window time.Duration) moduleWaitPolicy {
	return moduleWaitPolicy{Timeout: timeout, Poll: time.Millisecond, ObservationWindow: window}
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

// The tag rule is what a suite's gate refuses a mistyped E2E_UPGRADE_*_TAG with,
// before a single object is written, so both directions matter: everything a dev
// build is actually tagged with has to pass, and everything a shell can smuggle
// in has to be refused.
var _ = Describe("ValidateModuleImageTag", func() {
	DescribeTable("accepts the tags this project builds",
		func(tag string) {
			Expect(ValidateModuleImageTag(tag)).To(Succeed())
		},
		Entry("a release channel branch", "main"),
		Entry("a CI build of a pull request", "pr758"),
		Entry("a branch name with a slash", "feat/r3-r2-auto-migration"),
		Entry("a semantic version", "v1.2.3-alpha.1"),
		Entry("a digest-looking tag", "sha256-1111"),
	)

	DescribeTable("refuses what an operator can mistype",
		func(tag, wantMessage string) {
			Expect(ValidateModuleImageTag(tag)).To(MatchError(ContainSubstring(wantMessage)))
		},
		Entry("empty", "", "must not be empty"),
		Entry("a quoted value that kept its spaces", "pr758 ", "printable ASCII"),
		Entry("a value with a space inside", "two tags", "printable ASCII"),
		Entry("a tab", "pr758\t", "printable ASCII"),
		Entry("a trailing newline from a command substitution", "pr758\n", "printable ASCII"),
		// Both are written as escapes rather than as the characters themselves, so
		// this file stays ASCII: the module linter refuses non-ASCII letters in the
		// sources, and a rune that is invisible in an editor is a poor thing to
		// keep in a literal anyway.
		Entry("a non-ASCII character", "pr\u03bf758", "printable ASCII"),
		Entry("a non-breaking space that a copy-paste smuggled in", "pr758\u00a0", "printable ASCII"),
	)

	It("is the rule ensureModuleVersion applies, so a gate and the helper agree", func() {
		c := newModuleClient()
		observer := &stubModuleObserver{script: []moduleAnswer{
			{obs: readyObservation(testTagOld, testDigestOld, testImageOld)},
		}}

		err := ensureModuleVersion(context.Background(), c, observer, ModuleName, "pr758 ",
			testWaitPolicy(time.Second, 0))

		Expect(err).To(MatchError(ContainSubstring("printable ASCII")))
		Expect(c.creates).To(BeZero())
		Expect(c.updates).To(BeZero())
	})
})

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
	retagged := retaggedReadiness()
	created := moduleReadiness{
		Digest:                digestPublished,
		ImageTag:              testTagNew,
		ReadyTransitionBefore: testTransitionOld,
	}

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
		want := retagged
		want.ImagesBefore = observationImages(before)

		Expect(checkModuleReady(want, after)).To(Succeed(),
			"werf builds are content-addressed: components untouched between two tags keep their digest")
		Expect(moduleImagesChanged(want.ImagesBefore, after)).To(BeEmpty())
	})

	It("keeps waiting while the Module resource does not exist", func() {
		obs := readyObservation(testTagNew, testDigestNew, testImageNew)
		obs.ModuleFound, obs.ModulePhase, obs.ModuleVersion, obs.ModuleReadyTransition = false, "", "", ""

		Expect(checkModuleReady(retagged, obs)).
			To(MatchError(ContainSubstring("Module resource does not exist yet")))
	})

	It("keeps waiting while Deckhouse still reports the version it ran before the restart", func() {
		err := checkModuleReady(retagged, staleReadyObservation())

		Expect(err).To(MatchError(ContainSubstring("properties.version")),
			"the digest is published before Deckhouse restarts, and only the restarted one"+
				" writes the tag into the Module")
		Expect(err).To(MatchError(ContainSubstring(testTagOld)))
		Expect(err).To(MatchError(ContainSubstring(testTagNew)))
	})

	It("keeps waiting while the module has not run again since the write", func() {
		obs := readyObservation(testTagNew, testDigestNew, testImageOld)
		obs.ModuleReadyTransition = transitionFor(testDigestOld)

		Expect(checkModuleReady(retagged, obs)).
			To(MatchError(ContainSubstring("has not run again yet")))
	})

	It("does not demand a readiness transition when the tag was already pinned", func() {
		obs := readyObservation(testTagNew, testDigestOld, testImageOld)
		obs.ModuleReadyTransition = transitionFor(testDigestOld)
		want := moduleReadiness{ImageTag: testTagNew, ReadyTransitionBefore: obs.ModuleReadyTransition}

		Expect(checkModuleReady(want, obs)).To(Succeed(),
			"nothing is re-applied on that path, so demanding a re-run would wait forever")
	})

	It("keeps waiting while the module is still reconciling", func() {
		obs := readyObservation(testTagNew, testDigestNew, testImageNew)
		obs.ModulePhase = testPhaseReconciling

		Expect(checkModuleReady(retagged, obs)).
			To(MatchError(ContainSubstring(`phase "Reconciling", not "Ready"`)))
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
		err := checkModuleReady(moduleReadiness{}, moduleObservation{
			MPOFound:    true,
			MPODigest:   testDigestNew,
			ModuleFound: true,
			ModulePhase: moduleReadyPhase,
		})

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

var _ = Describe("moduleStableSamples", func() {
	DescribeTable("counts the consecutive polls that span the observation window",
		func(window, poll time.Duration, want int) {
			Expect(moduleStableSamples(window, poll)).To(Equal(want))
		},
		Entry("no window at all is one sample", time.Duration(0), 10*time.Second, 1),
		Entry("a poll of zero cannot span anything", time.Second, time.Duration(0), 1),
		Entry("a window shorter than one poll still costs the next poll", time.Second, 10*time.Second, 2),
		Entry("the shipped pair", moduleObservationWindow, moduleReadyPollInterval, 2),
		Entry("three polls span a window of three", 30*time.Second, 10*time.Second, 4),
		Entry("a window that does not divide evenly rounds up", 25*time.Second, 10*time.Second, 4),
	)
})

// The fingerprint is what makes the observation window a claim about the cluster
// standing still rather than about time passing, so every field the criterion
// reads has to move it.
var _ = Describe("moduleStateFingerprint", func() {
	It("is equal for two identical observations", func() {
		Expect(moduleStateFingerprint(readyObservation(testTagNew, testDigestNew, testImageNew))).
			To(Equal(moduleStateFingerprint(readyObservation(testTagNew, testDigestNew, testImageNew))))
	})

	DescribeTable("changes when anything the window watches moves",
		func(mutate func(*moduleObservation)) {
			base := readyObservation(testTagNew, testDigestNew, testImageNew)
			moved := readyObservation(testTagNew, testDigestNew, testImageNew)
			mutate(&moved)

			Expect(moduleStateFingerprint(moved)).NotTo(Equal(moduleStateFingerprint(base)))
		},
		Entry("the pinned tag", func(obs *moduleObservation) { obs.MPOTag = testTagOld }),
		Entry("the bundle digest", func(obs *moduleObservation) { obs.MPODigest = testDigestOld }),
		Entry("the module version", func(obs *moduleObservation) { obs.ModuleVersion = testTagOld }),
		Entry("the module phase", func(obs *moduleObservation) { obs.ModulePhase = testPhaseReconciling }),
		Entry("the readiness transition",
			func(obs *moduleObservation) { obs.ModuleReadyTransition = testTransitionOld }),
		Entry("a generation bump", func(obs *moduleObservation) { obs.Workloads[4].Generation = 4 }),
		Entry("a rollout counter", func(obs *moduleObservation) { obs.Workloads[2].Updated = 1 }),
		Entry("a pod-template image",
			func(obs *moduleObservation) { obs.Workloads[0].Images = []string{testImageOld} }),
	)
})

var _ = Describe("awaitModuleReady", func() {
	It("returns on the first accepting poll when no window is configured", func(ctx SpecContext) {
		observer := &stubModuleObserver{script: []moduleAnswer{
			{obs: readyObservation(testTagNew, testDigestNew, testImageNew)},
		}}

		Expect(awaitModuleReady(ctx, observer, ModuleName, moduleReadiness{},
			testWaitPolicy(5*time.Second, 0))).To(Succeed())
		Expect(observer.reads).To(Equal(1))
	})

	It("waits for the new bundle digest and then for the observation window", func(ctx SpecContext) {
		observer := &stubModuleObserver{script: []moduleAnswer{
			{obs: readyObservation(testTagNew, testDigestOld, testImageOld)},
			{obs: readyObservation(testTagNew, testDigestNew, testImageNew)},
		}}

		Expect(awaitModuleReady(ctx, observer, ModuleName, retaggedReadiness(),
			testWaitPolicy(5*time.Second, time.Millisecond))).To(Succeed())
		Expect(observer.reads).To(Equal(3),
			"one rejected poll, then the accepting one and the one that spans the window")
	})

	// This is the measured defect, poll by poll. The old criterion accepted the
	// second sample — a published digest next to workloads that were still
	// healthily running the OLD build — and the caller went on to assert against
	// pods that were replaced 45 seconds later.
	It("does not accept the stale Ready Deckhouse leaves behind while it restarts", func(ctx SpecContext) {
		reconciling := readyObservation(testTagNew, testDigestNew, testImageOld)
		reconciling.ModulePhase = testPhaseReconciling
		rolling := readyObservation(testTagNew, testDigestNew, testImageOld)
		rolling.Workloads[4].Generation = 4

		observer := &stubModuleObserver{script: []moduleAnswer{
			// The override controller published the digest and restarted Deckhouse:
			// the version, the readiness transition and the phase are the ones
			// written BEFORE the restart, and no workload has moved.
			{obs: staleReadyObservation()},
			{obs: staleReadyObservation()},
			// The restarted Deckhouse restored the module at our tag and took it out
			// of ready in order to run it.
			{obs: reconciling},
			// The run applied the manifests: the DaemonSet is rolling.
			{obs: rolling},
			{obs: readyObservation(testTagNew, testDigestNew, testImageNew)},
		}}

		Expect(awaitModuleReady(ctx, observer, ModuleName, retaggedReadiness(),
			testWaitPolicy(5*time.Second, time.Millisecond))).To(Succeed())
		Expect(observer.reads).To(Equal(6),
			"accepting either of the two stale samples is the defect this criterion exists to refuse")
	})

	It("starts the observation window over when a rollout begins inside it", func(ctx SpecContext) {
		rolling := readyObservation(testTagNew, testDigestNew, testImageOld)
		rolling.Workloads[4].Generation = 4
		observer := &stubModuleObserver{script: []moduleAnswer{
			{obs: readyObservation(testTagNew, testDigestNew, testImageOld)},
			{obs: readyObservation(testTagNew, testDigestNew, testImageOld)},
			{obs: rolling},
			{obs: readyObservation(testTagNew, testDigestNew, testImageNew)},
		}}

		Expect(awaitModuleReady(ctx, observer, ModuleName, moduleReadiness{},
			testWaitPolicy(5*time.Second, 2*time.Millisecond))).To(Succeed())
		Expect(observer.reads).To(Equal(6),
			"two samples of the window had passed when the rollout started, and all three are retaken")
	})

	It("reports that an accepted state never held for the whole window", func(ctx SpecContext) {
		observer := &stubModuleObserver{script: []moduleAnswer{
			{obs: readyObservation(testTagNew, testDigestNew, testImageNew)},
		}}

		err := awaitModuleReady(ctx, observer, ModuleName, retaggedReadiness(),
			testWaitPolicy(0, time.Millisecond))

		Expect(err).To(MatchError(ContainSubstring("held for 1 of the 2 polls")))
		Expect(err).To(MatchError(ContainSubstring("1ms observation window")))
	})

	It("reports the last reason when the budget runs out", func(ctx SpecContext) {
		observer := &stubModuleObserver{script: []moduleAnswer{
			{obs: readyObservation(testTagNew, testDigestOld, testImageOld)},
		}}

		err := awaitModuleReady(ctx, observer, ModuleName, retaggedReadiness(),
			testWaitPolicy(10*time.Millisecond, time.Millisecond))

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
			testWaitPolicy(5*time.Second, 0))).To(Succeed())
		Expect(observer.reads).To(Equal(3))
	})

	It("reports a read that never succeeded", func(ctx SpecContext) {
		observer := &stubModuleObserver{script: []moduleAnswer{{err: errors.New("connection refused")}}}

		err := awaitModuleReady(ctx, observer, ModuleName, moduleReadiness{},
			testWaitPolicy(10*time.Millisecond, 0))

		Expect(err).To(MatchError(ContainSubstring("connection refused")))
	})

	It("stops on a cancelled context and keeps the last state in the message", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		observer := &stubModuleObserver{script: []moduleAnswer{{obs: pendingObservation(testTagNew, testDigestNew)}}}

		// A poll far longer than the cancellation, so the select cannot pick the
		// timer instead of the cancelled context.
		err := awaitModuleReady(ctx, observer, ModuleName, moduleReadiness{},
			moduleWaitPolicy{Timeout: time.Minute, Poll: time.Second})

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
			testWaitPolicy(5*time.Second, time.Millisecond))).To(Succeed())

		Expect(observer.reads).To(Equal(4),
			"the pre-write snapshot, a poll on the old digest, the accepting poll and the window")
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
			testWaitPolicy(5*time.Second, time.Millisecond))).To(Succeed())

		Expect(c.creates).To(BeZero())
		Expect(c.updates).To(BeZero())
		Expect(observer.reads).To(Equal(3),
			"the pre-write snapshot, the accepting poll and the one that spans the window —"+
				" the window is not skipped here either, because a sibling may have retagged a moment ago")
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
			testWaitPolicy(5*time.Second, time.Millisecond))).To(Succeed())

		Expect(c.creates).To(Equal(2), "the ModuleConfig and the ModulePullOverride")
		Expect(observer.reads).To(Equal(4),
			"the pre-write snapshot, the poll without a digest, the accepting poll and the window")
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
			// healthy workloads are still the release build's and the Module still
			// reports the release version.
			{obs: pinnedNotResolved},
			{obs: pinnedNotResolved},
			{obs: readyObservation(testTagNew, testDigestNew, testImageNew)},
		}}

		Expect(ensureModuleVersion(ctx, c, observer, ModuleName, testTagNew,
			testWaitPolicy(5*time.Second, time.Millisecond))).To(Succeed())

		Expect(observer.reads).To(Equal(5),
			"a populated namespace with no digest published is not readiness, however healthy it looks")
		Expect(c.creates).To(Equal(1), "only the ModulePullOverride, the ModuleConfig was already enabled")
	})

	It("keeps waiting for the digest when a sibling worker pinned the tag first", func(ctx SpecContext) {
		c := newRacingModuleClient(2, moduleConfigObject(map[string]any{"enabled": false}))
		observer := &stubModuleObserver{script: []moduleAnswer{
			{obs: releaseObservation(testImageOld)}, // before the write: no override
			{obs: readyObservation(testTagNew, testDigestNew, testImageNew)},
		}}

		Expect(ensureModuleVersion(ctx, c, observer, ModuleName, testTagNew,
			testWaitPolicy(5*time.Second, time.Millisecond))).To(Succeed())

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
				testWaitPolicy(time.Second, 0))

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

		err := ensureModuleVersion(ctx, c, observer, ModuleName, testTagNew,
			testWaitPolicy(time.Second, 0))

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
