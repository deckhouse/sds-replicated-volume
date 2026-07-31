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
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
)

// llvReading is one canned answer of stubLLVGetter.
type llvReading struct {
	uid types.UID // empty means the object is absent
	err error     // when set, the read fails with it
}

// stubLLVGetter answers reads from a script, repeating its last entry once the script
// runs out.
type stubLLVGetter struct {
	mu       sync.Mutex
	readings []llvReading
	reads    int
}

func (g *stubLLVGetter) Get(_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	i := min(g.reads, len(g.readings)-1)
	g.reads++
	reading := g.readings[i]

	switch {
	case reading.err != nil:
		return reading.err
	case reading.uid == "":
		return apierrors.NewNotFound(schema.GroupResource{
			Group:    snc.SchemeGroupVersion.Group,
			Resource: "lvmlogicalvolumes",
		}, key.Name)
	default:
		llv, ok := obj.(*snc.LVMLogicalVolume)
		if !ok {
			return errors.New("stubLLVGetter only serves LVMLogicalVolume")
		}
		llv.ObjectMeta = metav1.ObjectMeta{Name: key.Name, UID: reading.uid}
		return nil
	}
}

func (g *stubLLVGetter) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.reads
}

const (
	testLLVName    = "llv-node-a-pvc-0"
	testLLVOtherID = types.UID("22222222-2222-2222-2222-222222222222")
)

var testLLVIdentity = LLVIdentity{Name: testLLVName, UID: "11111111-1111-1111-1111-111111111111"}

// testLLVWaitPolicy polls without waiting in real time and takes the observation window
// per test, because how many consecutive reads it takes is what several of them assert:
// at a 1ms poll, 0 means one accepting read, 1ms means two and 2ms means three
// (moduleStableSamples).
func testLLVWaitPolicy(window time.Duration) llvWaitPolicy {
	return llvWaitPolicy{Poll: time.Millisecond, ObservationWindow: window}
}

var _ = Describe("classifyLLVIdentity", func() {
	It("calls an absent name gone", func() {
		Expect(classifyLLVIdentity(testLLVIdentity, false, "")).To(Equal(llvIdentityGone))
	})

	It("calls the recorded object the same object", func() {
		Expect(classifyLLVIdentity(testLLVIdentity, true, testLLVIdentity.UID)).To(Equal(llvIdentitySame))
	})

	It("calls another object under the recorded name a reincarnation", func() {
		Expect(classifyLLVIdentity(testLLVIdentity, true, testLLVOtherID)).
			To(Equal(llvIdentityReincarnated))
	})
})

var _ = Describe("awaitLLVIdentityState", func() {
	It("waits for the recorded object to disappear and to stay gone", func(ctx SpecContext) {
		// Three consecutive absent reads span the window, and the first read is not
		// one of them, so the run cannot be satisfied by the moment of disappearance
		// alone.
		getter := &stubLLVGetter{readings: []llvReading{
			{uid: testLLVIdentity.UID},
			{uid: ""},
		}}
		Expect(awaitLLVIdentityState(ctx, getter, testLLVWaitPolicy(2*time.Millisecond),
			testLLVIdentity, llvIdentityGone)).To(Succeed())
		Expect(getter.count()).To(Equal(4))
	})

	It("does not accept a name that comes back under a different uid", func(ctx SpecContext) {
		// The regression this window exists for: the recorded object is deleted, the
		// wait sees the name free, and the module recreates an LVMLogicalVolume under
		// it. Ending the wait on that first absence would call a recreated LV a
		// released one.
		//
		// The deadline is the backstop for the other way this can regress — a wait
		// that neither accepts the reincarnation nor complains about it would hang
		// the suite instead of failing it. The read count below is what says the
		// verdict came from the reading and not from the deadline.
		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		getter := &stubLLVGetter{readings: []llvReading{
			{uid: testLLVIdentity.UID},
			{uid: ""},
			{uid: testLLVOtherID},
		}}
		err := awaitLLVIdentityState(bounded, getter, testLLVWaitPolicy(2*time.Millisecond),
			testLLVIdentity, llvIdentityGone)
		Expect(err).To(MatchError(ContainSubstring("was recreated rather than released")))
		Expect(err).To(MatchError(ContainSubstring(testLLVName)))
		Expect(err).To(MatchError(ContainSubstring(string(testLLVOtherID))))
		// Reported on the read that saw it, not by running the deadline out.
		Expect(getter.count()).To(Equal(3))
	})

	It("does not accept a reincarnation under the same name as a disappearance", func(ctx SpecContext) {
		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		getter := &stubLLVGetter{readings: []llvReading{{uid: testLLVOtherID}}}
		err := awaitLLVIdentityState(bounded, getter, testLLVWaitPolicy(2*time.Millisecond),
			testLLVIdentity, llvIdentityGone)
		Expect(err).To(MatchError(ContainSubstring("was recreated rather than released")))
		Expect(getter.count()).To(Equal(1))
	})

	It("starts the run over when a read fails in the middle of it", func(ctx SpecContext) {
		// An unread name is not an absent one, so the window has to be observed
		// whole: the two absent reads before the failure do not count towards it.
		getter := &stubLLVGetter{readings: []llvReading{
			{uid: ""},
			{uid: ""},
			{err: errors.New("cache not started")},
			{uid: ""},
		}}
		Expect(awaitLLVIdentityState(ctx, getter, testLLVWaitPolicy(2*time.Millisecond),
			testLLVIdentity, llvIdentityGone)).To(Succeed())
		Expect(getter.count()).To(Equal(6))
	})

	It("accepts a survivor that kept its uid on the first read", func(ctx SpecContext) {
		getter := &stubLLVGetter{readings: []llvReading{{uid: testLLVIdentity.UID}}}
		Expect(awaitLLVIdentityState(ctx, getter, testLLVWaitPolicy(0),
			testLLVIdentity, llvIdentitySame)).To(Succeed())
		Expect(getter.count()).To(Equal(1))
	})

	It("rejects a survivor that was quietly recreated", func(ctx SpecContext) {
		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		getter := &stubLLVGetter{readings: []llvReading{{uid: testLLVOtherID}}}
		Expect(awaitLLVIdentityState(bounded, getter, testLLVWaitPolicy(0),
			testLLVIdentity, llvIdentitySame)).
			To(MatchError(ContainSubstring("did not survive")))
		Expect(getter.count()).To(Equal(1))
	})

	It("keeps waiting through a read error and reports the last one on timeout", func(ctx SpecContext) {
		bounded, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()

		getter := &stubLLVGetter{readings: []llvReading{{err: errors.New("cache not started")}}}
		Expect(awaitLLVIdentityState(bounded, getter, testLLVWaitPolicy(0),
			testLLVIdentity, llvIdentityGone)).
			To(MatchError(ContainSubstring("cache not started")))
	})

	It("reports how much of the window it had when the deadline hit", func(ctx SpecContext) {
		// The one read the stub keeps repeating is accepting, so the wait can only end
		// on the deadline: what it has to say then is how close it came.
		bounded, cancel := context.WithTimeout(ctx, 5*time.Millisecond)
		defer cancel()

		getter := &stubLLVGetter{readings: []llvReading{{uid: ""}}}
		Expect(awaitLLVIdentityState(bounded, getter, llvWaitPolicy{Poll: 10 * time.Millisecond,
			ObservationWindow: 60 * time.Millisecond}, testLLVIdentity, llvIdentityGone)).
			To(MatchError(ContainSubstring("gone for 1 of the 7 reads that span the 60ms observation window")))
	})

	It("survives a read error before the state it waits for", func(ctx SpecContext) {
		getter := &stubLLVGetter{readings: []llvReading{
			{err: errors.New("cache not started")},
			{uid: ""},
		}}
		Expect(awaitLLVIdentityState(ctx, getter, testLLVWaitPolicy(0),
			testLLVIdentity, llvIdentityGone)).To(Succeed())
	})
})
