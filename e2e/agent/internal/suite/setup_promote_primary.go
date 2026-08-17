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

// SetupPromotePrimary patches the DRBDResource to Primary role, waits for
// configured, and asserts the role is reflected in activeConfiguration.
//
// Promotion is also what creates the DRBDMapper: only a Primary has a consumer to
// publish a device to. So this additionally waits until the mapper is Configured
// and its upper device is published, and asserts both. The returned DRBDResource
// therefore has status.device set.
//
// Requires the resource to be promotable — a disk that is still Inconsistent makes
// drbdsetup primary fail, which stops convergence before the mapper is created.
// Callers arrange that with SetupInitialSync.
func SetupPromotePrimary(
	e envtesting.E,
	cl client.WithWatch,
	drbdr *v1alpha1.DRBDResource,
	drbdMapperConfiguredTimeout DRBDMapperConfiguredTimeout,
) *v1alpha1.DRBDResource {
	var drbdrConfiguredTimeout DRBDRConfiguredTimeout
	e.Options(&drbdrConfiguredTimeout)

	drbdr = kubetesting.SetupResourcePatch(
		e.ScopeWithTimeout(drbdrConfiguredTimeout.Duration),
		cl,
		client.ObjectKey{Name: drbdr.Name},
		func(d *v1alpha1.DRBDResource) {
			d.Spec.Role = v1alpha1.DRBDRolePrimary
		},
		isDRBDRTerminal,
	)
	assertDRBDRConfigured(e, drbdr)
	assertDRBDRRole(e, drbdr, v1alpha1.DRBDRolePrimary)

	// Mapper first, then the device: if the DRBDMapper controller wedges, the
	// failure then names its condition instead of an opaque empty status.device.
	drbdm := waitForDRBDMapperConfigured(e, cl, drbdr.Name, drbdMapperConfiguredTimeout.Duration)
	assertDRBDMapperSpec(e, drbdm, drbdr)

	drbdr = waitForDRBDRDevice(e, cl, drbdr, isDRBDRDevicePublished, drbdMapperConfiguredTimeout.Duration)
	assertDRBDRDevicePublished(e, drbdr)

	return drbdr
}
