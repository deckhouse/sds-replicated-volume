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

// SetupRenameMissingResource creates a diskless DRBDResource whose
// spec.actualNameOnTheNode points at a DRBD resource that does not exist on the
// node, and no DRBD resource exists under the canonical (sdsrv-) name either.
//
// This models a node that rebooted before adoption completed: DRBD state is
// volatile and is wiped on reboot, so the resource to adopt is gone and its
// canonical name is not present yet. The agent must not get stuck trying to
// find the missing resource. Instead it clears actualNameOnTheNode and brings
// the resource up under its canonical name, reaching Configured=True.
func SetupRenameMissingResource(
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
	missingDRBDName := "missing-" + name

	// Create a diskless DRBDResource that references a non-existent DRBD
	// resource via actualNameOnTheNode. Wait until the Configured condition
	// reaches a terminal state, then assert it is Configured=True: the agent
	// dropped the impossible adoption and brought the resource up normally.
	drbdr := newDRBDResourceDiskless(name, node.Name, uint8(nodeIdx))
	drbdr.Spec.ActualNameOnTheNode = missingDRBDName

	drbdr = kubetesting.SetupResource(
		e.ScopeWithTimeout(drbdrConfiguredTimeout.Duration),
		cl,
		drbdr,
		isDRBDRTerminal,
	)
	assertDRBDRConfigured(e, drbdr)
	assertActualNameCleared(e, cl, name)

	return drbdr
}
