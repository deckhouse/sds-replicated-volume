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
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/kubetesting"
)

// SetupDemoteToSecondary patches the DRBDResource back to Secondary role,
// waits for configured, and asserts the role.
//
// A demote also destroys the DRBDMapper: its dm layers pin the DRBD device open,
// so drbdsetup secondary cannot run until they are gone. The agent therefore
// defers the demote behind the mapper's deletion, which is why the wait below gets
// the mapper-deletion budget rather than the plain configure one. This also waits
// until the device is unpublished and asserts the mapper is gone and all three
// device fields are cleared.
func SetupDemoteToSecondary(
	e envtesting.E,
	cl client.WithWatch,
	drbdr *v1alpha1.DRBDResource,
	drbdMapperDeletedTimeout DRBDMapperDeletedTimeout,
) *v1alpha1.DRBDResource {
	drbdr = kubetesting.SetupResourcePatch(
		e.ScopeWithTimeout(drbdMapperDeletedTimeout.Duration),
		cl,
		client.ObjectKey{Name: drbdr.Name},
		func(d *v1alpha1.DRBDResource) {
			d.Spec.Role = v1alpha1.DRBDRoleSecondary
		},
		isDRBDRTerminal,
	)
	assertDRBDRConfigured(e, drbdr)
	assertDRBDRRole(e, drbdr, v1alpha1.DRBDRoleSecondary)

	// Reaching Secondary already implies the mapper went first, but assert it so a
	// regression that demoted while the dm layers still existed is named for what
	// it is. The object outlives the delete call on its finalizer.
	waitForDRBDMapperGone(e, cl, drbdr.Name, drbdMapperDeletedTimeout.Duration)

	drbdr = waitForDRBDRDevice(e, cl, drbdr, isDRBDRDeviceUnpublished, drbdMapperDeletedTimeout.Duration)
	assertDRBDRDeviceUnpublished(e, drbdr)

	return drbdr
}
