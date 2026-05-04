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
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/kubetesting"
)

// SetupNonManagedResource creates a DRBD resource without the sdsrv- prefix
// on the node and verifies the agent does not tear it down.
//
// A canary DRBDResource (managed) is created alongside the non-managed one.
// Waiting for the canary to reach Configured=True proves the agent has
// processed events, so the non-managed resource has been seen by the scanner.
func SetupNonManagedResource(
	e envtesting.E,
	cl client.WithWatch,
	cluster *Cluster,
	ne *kubetesting.NodeExec,
	prefix string,
	nodeIdx int,
) {
	var testID TestID
	var drbdrConfiguredTimeout DRBDRConfiguredTimeout
	e.Options(&testID, &drbdrConfiguredTimeout)

	node := cluster.Nodes[nodeIdx]
	nonManagedName := testID.ResourceName(prefix, "nm")

	// Create a non-managed DRBD resource directly on the node.
	ne.Exec(e, node.Name, "drbdsetup", "new-resource", nonManagedName, "31")
	e.Cleanup(func() {
		ne.Exec(e, node.Name, "drbdsetup", "down", nonManagedName)
	})

	// Create a managed (canary) DRBDResource and wait for Configured=True.
	// This proves the agent has processed reconciliation cycles since the
	// non-managed resource was created.
	canaryName := testID.ResourceName(prefix, fmt.Sprintf("%d", nodeIdx))
	kubetesting.SetupResource(
		e.ScopeWithTimeout(drbdrConfiguredTimeout.Duration),
		cl,
		newDRBDResourceDiskless(canaryName, node.Name, uint8(nodeIdx)),
		isDRBDRTerminal,
	)

	// Assert the non-managed resource still exists on the node.
	out := ne.Exec(e, node.Name, "drbdsetup", "status", nonManagedName)
	if !strings.Contains(out, nonManagedName) {
		e.Fatalf("assert: non-managed DRBD resource %q was cleaned up by the agent", nonManagedName)
	}
}
