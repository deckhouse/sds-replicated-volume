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

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/kubetesting"
)

// SetupRename creates a diskless DRBDResource, renames the underlying DRBD
// resource on the node to a non-standard name, patches the K8S object to set
// actualNameOnTheNode, and waits for the agent to rename it back and reach
// Configured=True.
func SetupRename(
	e envtesting.E,
	cl client.WithWatch,
	cluster *Cluster,
	ne *kubetesting.NodeExec,
	prefix string,
	nodeIdx int,
) *v1alpha1.DRBDResource {
	var testID TestID
	var drbdrConfiguredTimeout DRBDRConfiguredTimeout
	e.Options(&testID, &drbdrConfiguredTimeout)

	node := cluster.Nodes[nodeIdx]
	name := testID.ResourceName(prefix, fmt.Sprintf("%d", nodeIdx))
	standardDRBDName := "sdsrv-" + name
	customDRBDName := "custom-" + name

	// Create diskless DRBDResource, wait for Configured=True.
	drbdr := kubetesting.SetupResource(
		e.ScopeWithTimeout(drbdrConfiguredTimeout.Duration),
		cl,
		newDRBDResourceDiskless(name, node.Name, uint8(nodeIdx)),
		isDRBDRTerminal,
	)
	assertDRBDRConfigured(e, drbdr)

	// Rename DRBD resource on the node from standard to custom name.
	ne.Exec(e, node.Name, "drbdsetup", "rename-resource", standardDRBDName, customDRBDName)

	// Patch the K8S object to set actualNameOnTheNode.
	// The agent will rename the DRBD resource back to the standard name
	// and clear actualNameOnTheNode.
	drbdr = kubetesting.SetupResourcePatch(
		e.ScopeWithTimeout(drbdrConfiguredTimeout.Duration),
		cl,
		client.ObjectKey{Name: name},
		func(d *v1alpha1.DRBDResource) {
			d.Spec.ActualNameOnTheNode = customDRBDName
		},
		isDRBDRTerminal,
	)
	assertDRBDRConfigured(e, drbdr)
	assertActualNameCleared(e, cl, name)

	return drbdr
}

func assertActualNameCleared(e envtesting.E, cl client.Client, name string) {
	drbdr := &v1alpha1.DRBDResource{}
	if err := cl.Get(e.Context(), client.ObjectKey{Name: name}, drbdr); err != nil {
		e.Fatalf("getting DRBDResource %q: %v", name, err)
	}
	if drbdr.Spec.ActualNameOnTheNode != "" {
		e.Fatalf("assert: DRBDResource %q still has actualNameOnTheNode=%q after rename",
			name, drbdr.Spec.ActualNameOnTheNode)
	}
}
