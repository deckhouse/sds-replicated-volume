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

package suite

import (
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/kubetesting"
)

// maintenanceDeletionBlockWindow is how long we observe that the agent keeps
// the DRBDResource (and the LLV finalizer) after a delete in maintenance. It
// must comfortably exceed the agent's reconcile latency: the buggy agent
// removed its DRBDResource finalizer within a single reconcile, so any sustained
// window catches the regression while keeping the test fast.
const maintenanceDeletionBlockWindow = 10 * time.Second

// SetupDeleteDiskfulInMaintenance guards against the reported LLV agent-finalizer
// leak on DRBDResource deletion.
//
// It puts a configured diskful DRBDResource into maintenance mode and deletes
// it. While in maintenance the agent must NOT tear DRBD down or release the LLV
// finalizer (Step 3 is gated on !maintenanceMode), AND it must NOT remove its
// own DRBDResource finalizer (Phase 6 is gated on the LLV release list being
// empty). The net effect is that the deletion is safely deferred: the
// DRBDResource lingers (DeletionTimestamp set, agent finalizer held) and the LLV
// finalizer stays put — nothing is orphaned.
//
// The buggy agent instead removed its DRBDResource finalizer immediately, so the
// DRBDResource vanished while the LLV finalizer leaked. This test fails on that
// behavior.
//
// Once maintenance is lifted, a normal reconcile must finish the job: detach
// DRBD, release the LLV finalizer, then remove the DRBDResource finalizer so the
// object is finally deleted — a clean teardown with no leak.
//
// Requirements (caller's responsibility):
//   - drbdr is a configured, diskful DRBDResource on node `nodeName`.
//   - llv is its backing LVMLogicalVolume and currently carries the agent
//     finalizer.
//   - ne can exec inside the agent pod on `nodeName` (cleanup safety net).
func SetupDeleteDiskfulInMaintenance(
	e envtesting.E,
	cl client.WithWatch,
	ne *kubetesting.NodeExec,
	nodeName string,
	drbdr *v1alpha1.DRBDResource,
	llv *snc.LVMLogicalVolume,
) {
	var drbdrConfiguredTimeout DRBDRConfiguredTimeout
	e.Options(&drbdrConfiguredTimeout)

	// Require: the resource is genuinely a diskful replica backed by llv, so
	// the test exercises the diskful-cleanup path (not a diskless no-op).
	if drbdr.Spec.Type != v1alpha1.DRBDResourceTypeDiskful {
		e.Fatalf("require: DRBDResource %q must be diskful, got %s", drbdr.Name, drbdr.Spec.Type)
	}
	if drbdr.Spec.LVMLogicalVolumeName != llv.Name {
		e.Fatalf("require: DRBDResource %q must be linked to LLV %q, got %q",
			drbdr.Name, llv.Name, drbdr.Spec.LVMLogicalVolumeName)
	}
	// Require: the agent holds its finalizer on the backing LLV.
	assertLLVHasAgentFinalizer(e, cl, llv.Name)

	// Arrange: pause resource reconciliation. While paused the agent neither
	// tears down DRBD nor releases the LLV finalizer on the cleanup path.
	drbdr = SetupMaintenanceMode(e, cl, drbdr)

	// Entering maintenance must not, by itself, release the LLV finalizer: the
	// disk is still attached. This pins the precondition so a later assertion
	// can't pass for the wrong reason.
	assertLLVHasAgentFinalizer(e, cl, llv.Name)

	// Cleanup safety net, runs LIFO before the parent replica cleanup. If the
	// fix ever regresses we may leave a stuck DRBDResource and/or a leaked LLV
	// finalizer; strip them so subsequent runs start clean and the parent's
	// LLV Delete+wait does not hang.
	//
	// NOTE: this deliberately strips agent finalizers — only acceptable because
	// this is the resource under test. Normal suite teardown must never strip
	// finalizers.
	drbdName := "sdsrv-" + drbdr.Name
	e.Cleanup(func() {
		// Lift maintenance so a still-running agent can finish teardown.
		liftMaintenance(e, cl, drbdr.Name)
		// Down any DRBD resource that might still be configured on the node.
		// The agent image is distroless (no shell): exec the binary directly
		// and tolerate failure (it may already be down).
		if _, err := ne.TryExec(e, nodeName, "drbdsetup", "down", drbdName); err != nil {
			e.Logf("cleanup: drbdsetup down %q (best effort): %v", drbdName, err)
		}
		forceReleaseAndDeleteDRBDR(e, cl, drbdr.Name)
		forceReleaseAndDeleteLLV(e, cl, llv.Name)
	})

	// Act: delete the DRBDResource while in maintenance.
	if err := cl.Delete(e.Context(), drbdr); err != nil {
		e.Fatalf("deleting DRBDResource %q: %v", drbdr.Name, err)
	}

	// Assert: the agent defers the deletion. For the whole observation window
	// the DRBDResource must remain (agent finalizer held) and the LLV finalizer
	// must stay put. The buggy agent would have removed its finalizer — letting
	// the DRBDResource vanish — while leaking the LLV finalizer.
	assertDeletionDeferredHoldingLLV(e, cl, drbdr, llv.Name, maintenanceDeletionBlockWindow)

	// Act: lift maintenance. The agent must now finish the deletion cleanly.
	liftMaintenance(e, cl, drbdr.Name)

	// Assert: the DRBDResource is finally deleted, and only after the agent
	// released the LLV finalizer — a clean teardown, no leak.
	waitForDeletion(e, cl, drbdr, drbdrConfiguredTimeout.Duration)
	assertLLVHasNoAgentFinalizer(e, cl, llv.Name)
}

// assertDeletionDeferredHoldingLLV verifies that, throughout window, the agent
// keeps the DRBDResource (does not remove its own finalizer) AND keeps the LLV
// agent finalizer. It also confirms the delete actually reached the API server
// (DeletionTimestamp set), so a pass means the agent is deliberately deferring,
// not merely lagging behind the delete event.
func assertDeletionDeferredHoldingLLV(
	e envtesting.E,
	cl client.Client,
	drbdr *v1alpha1.DRBDResource,
	llvName string,
	window time.Duration,
) {
	key := client.ObjectKeyFromObject(drbdr)
	deadline := time.Now().Add(window)
	sawDeletionTimestamp := false

	for {
		fresh := &v1alpha1.DRBDResource{}
		if err := cl.Get(e.Context(), key, fresh); err != nil {
			if apierrors.IsNotFound(err) {
				e.Fatalf("assert: DRBDResource %q was deleted while LLV %q still holds the agent "+
					"finalizer — leak (agent dropped its finalizer without releasing the LLV)",
					drbdr.Name, llvName)
			}
			e.Fatalf("getting DRBDResource %q: %v", drbdr.Name, err)
		}
		if fresh.DeletionTimestamp != nil {
			sawDeletionTimestamp = true
		}
		if !obju.HasFinalizer(fresh, v1alpha1.AgentFinalizer) {
			e.Fatalf("assert: agent removed its finalizer from DRBDResource %q while LLV %q still "+
				"needs releasing — leak", drbdr.Name, llvName)
		}
		assertLLVHasAgentFinalizer(e, cl, llvName)

		if time.Now().After(deadline) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}

	if !sawDeletionTimestamp {
		e.Fatalf("assert: DRBDResource %q never showed a DeletionTimestamp; the delete may not have "+
			"propagated, making the deferral check meaningless", drbdr.Name)
	}
}

// liftMaintenance clears spec.maintenance on the named DRBDResource. Patching a
// resource that already has a DeletionTimestamp is allowed and lets the agent
// resume reconciliation to finish a deferred teardown.
func liftMaintenance(e envtesting.E, cl client.Client, name string) {
	drbdr := &v1alpha1.DRBDResource{}
	drbdr.SetName(name)
	patch := client.RawPatch(types.MergePatchType, []byte(`{"spec":{"maintenance":null}}`))
	if err := cl.Patch(e.Context(), drbdr, patch); err != nil && !apierrors.IsNotFound(err) {
		e.Fatalf("lifting maintenance on DRBDResource %q: %v", name, err)
	}
}

// forceReleaseAndDeleteDRBDR deletes the DRBDResource and strips its agent
// finalizer. Cleanup-only safety net: tolerant of NotFound and reports problems
// as non-fatal errors so other cleanups still run.
func forceReleaseAndDeleteDRBDR(e envtesting.E, cl client.Client, name string) {
	drbdr := &v1alpha1.DRBDResource{}
	if err := cl.Get(e.Context(), client.ObjectKey{Name: name}, drbdr); err != nil {
		if apierrors.IsNotFound(err) {
			return
		}
		e.Errorf("cleanup: getting DRBDResource %q: %v", name, err)
		return
	}

	if drbdr.DeletionTimestamp == nil {
		if err := cl.Delete(e.Context(), drbdr); err != nil && !apierrors.IsNotFound(err) {
			e.Errorf("cleanup: deleting DRBDResource %q: %v", name, err)
		}
	}

	if obju.HasFinalizer(drbdr, v1alpha1.AgentFinalizer) {
		base := drbdr.DeepCopy()
		obju.RemoveFinalizer(drbdr, v1alpha1.AgentFinalizer)
		if err := cl.Patch(e.Context(), drbdr, client.MergeFrom(base)); err != nil &&
			!apierrors.IsNotFound(err) {
			e.Errorf("cleanup: stripping agent finalizer from DRBDResource %q: %v", name, err)
		}
	}
}

// forceReleaseAndDeleteLLV strips the agent finalizer from the LLV and deletes
// it. Cleanup-only: tolerant of NotFound and reports problems as non-fatal
// errors so other cleanups still run.
func forceReleaseAndDeleteLLV(e envtesting.E, cl client.Client, name string) {
	llv := &snc.LVMLogicalVolume{}
	if err := cl.Get(e.Context(), client.ObjectKey{Name: name}, llv); err != nil {
		if apierrors.IsNotFound(err) {
			return
		}
		e.Errorf("cleanup: getting LLV %q: %v", name, err)
		return
	}

	if obju.HasFinalizer(llv, v1alpha1.AgentFinalizer) {
		base := llv.DeepCopy()
		obju.RemoveFinalizer(llv, v1alpha1.AgentFinalizer)
		if err := cl.Patch(e.Context(), llv, client.MergeFrom(base)); err != nil &&
			!apierrors.IsNotFound(err) {
			e.Errorf("cleanup: stripping agent finalizer from LLV %q: %v", name, err)
		}
	}

	if llv.DeletionTimestamp == nil {
		if err := cl.Delete(e.Context(), llv); err != nil && !apierrors.IsNotFound(err) {
			e.Errorf("cleanup: deleting LLV %q: %v", name, err)
		}
	}
}
