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
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/kubetesting"
)

// SetupMaintenanceModeWithRename sets maintenance mode on a configured
// DRBDResource, renames the underlying DRBD resource on the node, sets
// actualNameOnTheNode, and asserts the agent skips the rename (reports
// InMaintenance instead of executing drbdsetup rename-resource).
//
// Cleanup order (LIFO):
//  1. Revert actualNameOnTheNode to "" (agent still in MM, no DRBD changes).
//  2. Rename DRBD back to standard name on the node (safe because MM is on).
//  3. Revert maintenance mode (agent reconciles normally, reaches Configured=True).
func SetupMaintenanceModeWithRename(
	e envtesting.E,
	cl client.WithWatch,
	ne *kubetesting.NodeExec,
	drbdr *v1alpha1.DRBDResource,
	_ *Cluster,
) {
	var drbdrConfiguredTimeout DRBDRConfiguredTimeout
	e.Options(&drbdrConfiguredTimeout)

	name := drbdr.Name
	nodeName := drbdr.Spec.NodeName
	standardDRBDName := "sdsrv-" + name
	customDRBDName := "custom-mm-" + name

	// Step 1: Set maintenance mode (registers cleanup #A — LIFO last).
	_ = SetupMaintenanceMode(e, cl, drbdr)

	// Step 2: Rename DRBD resource on the node from standard to custom name.
	ne.Exec(e, nodeName, "drbdsetup", "rename-resource", standardDRBDName, customDRBDName)

	// Step 3: Register cleanup to rename DRBD back on the node (cleanup #B — LIFO middle).
	// Safe because MM cleanup (#A) hasn't run yet, so the agent won't rename.
	e.Cleanup(func() {
		ne.Exec(e, nodeName, "drbdsetup", "rename-resource", customDRBDName, standardDRBDName)
	})

	// Step 4: Set actualNameOnTheNode (registers cleanup #C — LIFO first).
	// The agent reconciles: MM is on, so rename is skipped. Reports InMaintenance.
	drbdr = kubetesting.SetupResourcePatch(
		e.ScopeWithTimeout(drbdrConfiguredTimeout.Duration),
		cl,
		client.ObjectKey{Name: name},
		func(d *v1alpha1.DRBDResource) {
			d.Spec.ActualNameOnTheNode = customDRBDName
		},
		isDRBDRCondition(
			v1alpha1.DRBDResourceCondConfiguredType,
			metav1.ConditionFalse,
			v1alpha1.DRBDResourceCondConfiguredReasonInMaintenance,
		),
	)

	// Assert InMaintenance condition.
	assertDRBDRCondition(e, drbdr,
		v1alpha1.DRBDResourceCondConfiguredType,
		metav1.ConditionFalse,
		v1alpha1.DRBDResourceCondConfiguredReasonInMaintenance)

	// Assert the DRBD resource on the node still has the custom name
	// (rename was NOT executed by the agent).
	assertDRBDResourceExistsOnNode(e, ne, nodeName, customDRBDName)

	// Assert actualNameOnTheNode is still set (not cleared, because rename
	// was skipped).
	fresh := &v1alpha1.DRBDResource{}
	if err := cl.Get(e.Context(), client.ObjectKey{Name: name}, fresh); err != nil {
		e.Fatalf("getting DRBDResource %q: %v", name, err)
	}
	if fresh.Spec.ActualNameOnTheNode != customDRBDName {
		e.Fatalf("assert: expected actualNameOnTheNode=%q, got %q",
			customDRBDName, fresh.Spec.ActualNameOnTheNode)
	}
}

func assertDRBDResourceExistsOnNode(
	e envtesting.E,
	ne *kubetesting.NodeExec,
	nodeName, drbdResName string,
) {
	out := ne.Exec(e, nodeName, "drbdsetup", "status", drbdResName)
	if !strings.Contains(out, drbdResName) {
		e.Fatalf("assert: DRBD resource %q not found on node %q, drbdsetup status output: %s",
			drbdResName, nodeName, out)
	}
}
