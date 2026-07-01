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
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/kubetesting"
)

// SetupOrphanCleanup creates a diskless DRBDResource, force-deletes it (removing
// the agent finalizer and then deleting the object), and re-creates it.
// This exercises the orphan cleanup path: the DRBD resource on the node outlives
// the K8S object and must be torn down before the re-created object can converge.
func SetupOrphanCleanup(
	e envtesting.E,
	cl client.WithWatch,
	cluster *Cluster,
	prefix string,
	nodeIdx int,
) *v1alpha1.DRBDResource {
	var testID TestID
	var drbdrConfiguredTimeout DRBDRConfiguredTimeout
	e.Options(&testID, &drbdrConfiguredTimeout)

	node := cluster.Nodes[nodeIdx]
	name := testID.ResourceName(prefix, fmt.Sprintf("%d", nodeIdx))

	// Create diskless DRBDResource, wait for Configured=True.
	drbdr := kubetesting.SetupResource(
		e.ScopeWithTimeout(drbdrConfiguredTimeout.Duration),
		cl,
		newDRBDResourceDiskless(name, node.Name, uint8(nodeIdx)),
		isDRBDRTerminal,
	)
	assertDRBDRConfigured(e, drbdr)

	// Force-delete: remove agent finalizer, then delete.
	forceDeleteDRBDResource(e, cl, drbdr, drbdrConfiguredTimeout)

	// Re-create the same DRBDResource and wait for Configured=True.
	// The agent must handle the orphan DRBD resource on the node.
	drbdr = kubetesting.SetupResource(
		e.ScopeWithTimeout(drbdrConfiguredTimeout.Duration),
		cl,
		newDRBDResourceDiskless(name, node.Name, uint8(nodeIdx)),
		isDRBDRTerminal,
	)
	assertDRBDRConfigured(e, drbdr)

	return drbdr
}

// forceDeleteDRBDResource removes the agent finalizer and deletes the object,
// bypassing the normal agent cleanup flow.
func forceDeleteDRBDResource(
	e envtesting.E,
	cl client.Client,
	drbdr *v1alpha1.DRBDResource,
	timeout DRBDRConfiguredTimeout,
) {
	key := client.ObjectKeyFromObject(drbdr)

	// Patch to remove agent finalizer.
	fresh := &v1alpha1.DRBDResource{}
	if err := cl.Get(e.Context(), key, fresh); err != nil {
		e.Fatalf("getting DRBDResource %q before force-delete: %v", key.Name, err)
	}
	original := fresh.DeepCopy()
	obju.RemoveFinalizer(fresh, v1alpha1.AgentFinalizer)
	if err := cl.Patch(e.Context(), fresh, client.MergeFrom(original)); err != nil {
		e.Fatalf("removing agent finalizer from DRBDResource %q: %v", key.Name, err)
	}

	// Delete the object.
	if err := cl.Delete(e.Context(), fresh); err != nil {
		e.Fatalf("deleting DRBDResource %q: %v", key.Name, err)
	}

	waitForDeletion(e, cl, fresh, timeout.Duration)
}
