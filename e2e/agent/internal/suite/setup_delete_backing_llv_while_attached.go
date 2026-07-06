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
	"sigs.k8s.io/controller-runtime/pkg/client"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/kubetesting"
)

// backingLLVDeleteObserveWindow is how long we observe that the agent keeps the
// disk attached (Configured=True, size populated) after the backing LLV starts
// deleting. It must comfortably exceed the agent's reconcile latency: the buggy
// agent flipped Configured to False (reason BackingVolumeDeleting) within a
// single reconcile, so any sustained window catches the regression while keeping
// the test fast.
const backingLLVDeleteObserveWindow = 10 * time.Second

// SetupDeleteBackingLLVWhileAttached guards against the reported cleanup deadlock
// when the backing LLV of an Up+Diskful DRBDResource is deleted before the
// DRBDResource itself.
//
// A deletionTimestamp on the LLV object must NOT, by itself, drive the agent to
// detach the still-attached disk or to wedge the resource: while the DRBDResource
// is Up+Diskful and the disk is already attached, the agent must keep the disk
// attached and report Configured=True. Detach is driven by spec
// (Type=Diskless / State=Down) or DRBDResource deletion — never by the LLV
// object's deletionTimestamp. Reporting Configured=True is what lets the
// controller proceed with teardown; the earlier behavior of surfacing an error
// (Configured=False, reason BackingVolumeDeleting) deadlocked cleanup because the
// resource never looked healthy enough for its finalizers to be released.
//
// This helper deletes the LLV first (it lingers, held by the agent finalizer),
// asserts the disk stays attached and Configured=True across a sustained window,
// then deletes the DRBDResource and asserts a clean teardown: the DRBDResource is
// removed, the agent releases the LLV finalizer, and the LLV is finally GC'd —
// no deadlock, no leak. The buggy agent would keep Configured=False for the whole
// window (or detach and drop status.size), and the deletion chain would never
// complete.
//
// Requirements (caller's responsibility):
//   - drbdr is a configured, diskful DRBDResource on node `nodeName`, backed by
//     llv, which currently carries the agent finalizer.
//   - ne can exec inside the agent pod on `nodeName` (cleanup safety net).
func SetupDeleteBackingLLVWhileAttached(
	e envtesting.E,
	cl client.WithWatch,
	ne *kubetesting.NodeExec,
	nodeName string,
	drbdr *v1alpha1.DRBDResource,
	llv *snc.LVMLogicalVolume,
) {
	var drbdrConfiguredTimeout DRBDRConfiguredTimeout
	e.Options(&drbdrConfiguredTimeout)

	// Require: the resource is genuinely a diskful replica backed by llv, so the
	// test exercises the attached-disk path (not a diskless no-op).
	if drbdr.Spec.Type != v1alpha1.DRBDResourceTypeDiskful {
		e.Fatalf("require: DRBDResource %q must be diskful, got %s", drbdr.Name, drbdr.Spec.Type)
	}
	if drbdr.Spec.LVMLogicalVolumeName != llv.Name {
		e.Fatalf("require: DRBDResource %q must be linked to LLV %q, got %q",
			drbdr.Name, llv.Name, drbdr.Spec.LVMLogicalVolumeName)
	}
	// Require: the disk is attached and the agent holds its LLV finalizer. Pins
	// the precondition so a later assertion can't pass for the wrong reason.
	assertDRBDRConfigured(e, drbdr)
	assertDRBDRSizePopulated(e, drbdr)
	assertLLVHasAgentFinalizer(e, cl, llv.Name)

	// Cleanup safety net, runs LIFO before the parent replica cleanup. If the fix
	// regresses we may leave a stuck DRBDResource and/or a leaked LLV finalizer;
	// strip them so subsequent runs start clean and the parent's Delete+wait does
	// not hang.
	//
	// NOTE: this deliberately strips agent finalizers — only acceptable because
	// these are the resources under test. Normal suite teardown must never strip
	// finalizers.
	drbdName := "sdsrv-" + drbdr.Name
	e.Cleanup(func() {
		// Down any DRBD resource that might still be configured on the node. The
		// agent image is distroless (no shell): exec the binary directly and
		// tolerate failure (it may already be down).
		if _, err := ne.TryExec(e, nodeName, "drbdsetup", "down", drbdName); err != nil {
			e.Logf("cleanup: drbdsetup down %q (best effort): %v", drbdName, err)
		}
		forceReleaseAndDeleteDRBDR(e, cl, drbdr.Name)
		forceReleaseAndDeleteLLV(e, cl, llv.Name)
	})

	// Act: delete the backing LLV while the DRBDResource is still Up+Diskful. The
	// agent finalizer keeps the LLV object alive (DeletionTimestamp set).
	if err := cl.Delete(e.Context(), llv); err != nil {
		e.Fatalf("deleting LLV %q: %v", llv.Name, err)
	}

	// Assert: throughout the window the LLV lingers (DeletionTimestamp set, agent
	// finalizer held) AND the DRBDResource keeps the disk attached — Configured=True
	// with status.size populated. The buggy agent would flip Configured to False
	// (reason BackingVolumeDeleting) or detach and clear status.size.
	assertDiskKeptWhileBackingLLVDeleting(e, cl, drbdr, llv.Name, backingLLVDeleteObserveWindow)

	// Act: delete the DRBDResource — the teardown signal the agent honors.
	if err := cl.Delete(e.Context(), drbdr); err != nil {
		e.Fatalf("deleting DRBDResource %q: %v", drbdr.Name, err)
	}

	// Assert: teardown proceeds cleanly and nothing leaks. The DRBDResource is
	// removed, and the LLV is finally GC'd — which can only happen once the agent
	// releases its finalizer, proving the delete chain is no longer wedged.
	waitForDeletion(e, cl, drbdr, drbdrConfiguredTimeout.Duration)
	waitForDeletion(e, cl, llv, drbdrConfiguredTimeout.Duration)
}

// assertDiskKeptWhileBackingLLVDeleting verifies that, throughout window, the
// backing LLV lingers (deleting, agent finalizer held) while the DRBDResource
// keeps the disk attached (Configured=True, status.size populated). It also
// confirms the LLV delete actually reached the API server (DeletionTimestamp
// set), so a pass means the agent is deliberately keeping the disk, not merely
// lagging behind the delete event.
func assertDiskKeptWhileBackingLLVDeleting(
	e envtesting.E,
	cl client.Client,
	drbdr *v1alpha1.DRBDResource,
	llvName string,
	window time.Duration,
) {
	drbdrKey := client.ObjectKeyFromObject(drbdr)
	deadline := time.Now().Add(window)
	sawLLVDeletionTimestamp := false

	for {
		// The LLV must still exist, be marked for deletion, and hold the agent
		// finalizer: releasing it while still backing an Up+Diskful resource would
		// let the LV be removed out from under the attached DRBD device.
		llv := &snc.LVMLogicalVolume{}
		if err := cl.Get(e.Context(), client.ObjectKey{Name: llvName}, llv); err != nil {
			if apierrors.IsNotFound(err) {
				e.Fatalf("assert: LLV %q was deleted while still backing Up+Diskful DRBDResource %q — "+
					"the agent released it prematurely", llvName, drbdr.Name)
			}
			e.Fatalf("getting LLV %q: %v", llvName, err)
		}
		if llv.DeletionTimestamp != nil {
			sawLLVDeletionTimestamp = true
		}
		if !obju.HasFinalizer(llv, v1alpha1.AgentFinalizer) {
			e.Fatalf("assert: agent released its finalizer from deleting LLV %q while still backing "+
				"Up+Diskful DRBDResource %q — the disk would be orphaned", llvName, drbdr.Name)
		}

		// The DRBDResource must keep the disk attached: a deleting backing LLV
		// must not, by itself, detach or wedge it.
		fresh := &v1alpha1.DRBDResource{}
		if err := cl.Get(e.Context(), drbdrKey, fresh); err != nil {
			e.Fatalf("getting DRBDResource %q: %v", drbdr.Name, err)
		}
		assertDRBDRConfigured(e, fresh)
		assertDRBDRSizePopulated(e, fresh)

		if time.Now().After(deadline) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}

	if !sawLLVDeletionTimestamp {
		e.Fatalf("assert: LLV %q never showed a DeletionTimestamp; the delete may not have propagated, "+
			"making the disk-kept check meaningless", llvName)
	}
}
