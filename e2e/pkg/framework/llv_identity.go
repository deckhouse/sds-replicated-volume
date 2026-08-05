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
	"time"

	. "github.com/onsi/ginkgo/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
)

const (
	// llvIdentityPoll is how often the LVMLogicalVolume identity waits re-read the object.
	llvIdentityPoll = time.Second

	// llvGoneObservationWindow is how long the recorded name has to stay FREE before
	// AwaitLLVGone calls the volume released.
	//
	// A single absent read is not a release: the name outlives the object, and a
	// module that deletes an LVMLogicalVolume only to create another one under the
	// same name would pass through "absent" on its way there. The window is the
	// interval in which that recreation has to show up, and 10s spans several
	// reconcile rounds of both the controller and the agent, which requeue in
	// seconds. It is the same window the module readiness wait holds its accepted
	// state for (moduleObservationWindow), and it is short on purpose: callers
	// reach this only after the volume converged and the replica already reports
	// no backing volume, so the deletion is long done and the window is guarding
	// against a straggling writer, not waiting for the main flow.
	llvGoneObservationWindow = 10 * time.Second
)

// LLVIdentity names one LVMLogicalVolume instance: the object's name plus the UID of
// the exact object that carried that name when the identity was recorded.
//
// Names are recycled here. A replica retype releases the backing LV and the module may
// later create another LVMLogicalVolume with the very same name — same node, same
// volume group, same naming scheme. Waiting on the name alone would accept that
// replacement as proof of the release, so every wait in this file compares UIDs.
type LLVIdentity struct {
	Name string
	UID  types.UID
}

// String renders the identity for failure messages.
func (id LLVIdentity) String() string {
	return fmt.Sprintf("%s (uid %s)", id.Name, id.UID)
}

// AwaitLLVGone blocks until the recorded LVMLogicalVolume is gone from the cluster and
// STAYS gone for llvGoneObservationWindow.
//
// Gone means the recorded object was released and nothing took its place, which is a
// statement about an interval and cannot be made from one read. Both ways a name can
// lie are failures rather than an end of the wait: an object already carrying the name
// with a different UID, and — the reason for the window — the name coming back with a
// different UID after a moment of absence. The retype path needs the spec to be able
// to say "this LV was released", not "some LV of this name was absent when I looked".
func (f *Framework) AwaitLLVGone(ctx context.Context, id LLVIdentity) {
	GinkgoHelper()
	policy := llvWaitPolicy{Poll: llvIdentityPoll, ObservationWindow: llvGoneObservationWindow}
	if err := awaitLLVIdentityState(ctx, f.Client, policy, id, llvIdentityGone); err != nil {
		Fail(err.Error())
	}
}

// AwaitLLVSameUID blocks until the recorded LVMLogicalVolume is present and is still the
// very object that was recorded.
//
// It is the survivor side of AwaitLLVGone: a migration that quietly deleted and
// recreated an LV that was supposed to be untouched fails here, where a presence check
// on the name would pass.
//
// No observation window: one read of the recorded UID already proves the object itself
// outlived the migration, which is the whole claim. A later delete would not unmake it,
// and the spec has nothing to gain from watching for one.
func (f *Framework) AwaitLLVSameUID(ctx context.Context, id LLVIdentity) {
	GinkgoHelper()
	policy := llvWaitPolicy{Poll: llvIdentityPoll}
	if err := awaitLLVIdentityState(ctx, f.Client, policy, id, llvIdentitySame); err != nil {
		Fail(err.Error())
	}
}

// llvWaitPolicy budgets an identity wait: how often the object is re-read, and how long
// an accepted reading has to hold before it is believed. They are named because two
// positional time.Duration arguments are a chance to swap them.
type llvWaitPolicy struct {
	// Poll is how often the recorded name is re-read; see llvIdentityPoll.
	Poll time.Duration
	// ObservationWindow is how long the awaited state must hold; see
	// llvGoneObservationWindow. Zero means the first accepting read is enough.
	ObservationWindow time.Duration
}

// llvIdentityState is what one read of the recorded name says about the recorded object.
type llvIdentityState int

const (
	// llvIdentityGone means no object carries the recorded name.
	llvIdentityGone llvIdentityState = iota
	// llvIdentitySame means the object carrying the recorded name is the recorded one.
	llvIdentitySame
	// llvIdentityReincarnated means an object carries the recorded name, but a different
	// one: the recorded object is gone and something took its name.
	llvIdentityReincarnated
)

// String renders the state for failure messages.
func (s llvIdentityState) String() string {
	switch s {
	case llvIdentityGone:
		return "gone"
	case llvIdentitySame:
		return "present with the recorded uid"
	case llvIdentityReincarnated:
		return "present with a different uid"
	default:
		return fmt.Sprintf("unknown state %d", int(s))
	}
}

// llvIdentityGetter reads one object by key. It is the seam the identity waits are unit
// tested through.
type llvIdentityGetter interface {
	Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error
}

// classifyLLVIdentity turns one read into a state.
func classifyLLVIdentity(id LLVIdentity, found bool, observed types.UID) llvIdentityState {
	switch {
	case !found:
		return llvIdentityGone
	case observed == id.UID:
		return llvIdentitySame
	default:
		return llvIdentityReincarnated
	}
}

// readLLVIdentity reads the recorded name once and classifies what it found, together
// with the UID it saw — which is what a failure has to name when the UID is not the
// recorded one.
func readLLVIdentity(
	ctx context.Context,
	getter llvIdentityGetter,
	id LLVIdentity,
) (llvIdentityState, types.UID, error) {
	var llv snc.LVMLogicalVolume
	err := getter.Get(ctx, client.ObjectKey{Name: id.Name}, &llv)
	switch {
	case apierrors.IsNotFound(err):
		return classifyLLVIdentity(id, false, ""), "", nil
	case err != nil:
		return llvIdentityGone, "", fmt.Errorf("reading LVMLogicalVolume %s: %w", id.Name, err)
	default:
		return classifyLLVIdentity(id, true, llv.UID), llv.UID, nil
	}
}

// llvReplacedError says what a namesake found under the recorded name means for the
// wait that ran into it. Both waits are looking for a statement about one object, and
// both have their answer here — a different one each.
func llvReplacedError(id LLVIdentity, observed types.UID, want llvIdentityState) error {
	verdict := "did not survive"
	if want == llvIdentityGone {
		verdict = "was recreated rather than released"
	}
	return fmt.Errorf("LVMLogicalVolume %s %s: its name is now held by another object (uid %s)",
		id, verdict, observed)
}

// awaitLLVIdentityState polls the recorded name until want holds across the observation
// window of the policy, and reports what it was seeing last when the context ran out.
//
// Two things make it more than a poll for a matching read:
//
//   - want has to hold for a RUN of reads spanning the window, and anything else starts
//     the run over. This is what stops a momentary absence — a name between two objects
//     — from being taken for a release.
//   - a reincarnation ends the wait immediately, in failure. UIDs are not reused, so
//     once another object holds the recorded name, no later read can produce either
//     awaited state; waiting out the deadline would only trade a precise complaint for
//     a timeout. Whether the reincarnation was there from the first read or appeared
//     mid-window makes no difference to that.
//
// A failed read is not a verdict — the cache behind the client can be restarting — but
// it does interrupt the run: absence has to be observed, never assumed.
func awaitLLVIdentityState(
	ctx context.Context,
	getter llvIdentityGetter,
	policy llvWaitPolicy,
	id LLVIdentity,
	want llvIdentityState,
) error {
	// The run is counted in reads rather than measured against a clock, so a unit test
	// walking the loop cannot be thrown off by a poll that overslept; see
	// moduleStableSamples, the shared sample count of this package's waits.
	required := moduleStableSamples(policy.ObservationWindow, policy.Poll)

	var (
		last     string
		accepted int
	)
	for {
		state, uid, err := readLLVIdentity(ctx, getter, id)
		switch {
		case err != nil:
			last, accepted = err.Error(), 0
		case state == llvIdentityReincarnated && want != llvIdentityReincarnated:
			return llvReplacedError(id, uid, want)
		case state == want:
			accepted++
			if accepted >= required {
				return nil
			}
			last = fmt.Sprintf("%s for %d of the %d reads that span the %s observation window",
				state, accepted, required, policy.ObservationWindow)
		default:
			last, accepted = state.String(), 0
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for LVMLogicalVolume %s to be %s, it is %s", id, want, last)
		case <-time.After(policy.Poll):
		}
	}
}
