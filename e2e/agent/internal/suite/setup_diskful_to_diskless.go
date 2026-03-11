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

// SetupDiskfulToDiskless patches the DRBDResource from Diskful to Diskless type,
// waits for configured, and asserts activeConfiguration.type is Diskless.
func SetupDiskfulToDiskless(
	e envtesting.E,
	cl client.WithWatch,
	drbdr *v1alpha1.DRBDResource,
) *v1alpha1.DRBDResource {
	var drbdrConfiguredTimeout DRBDRConfiguredTimeout
	e.Options(&drbdrConfiguredTimeout)

	drbdr = kubetesting.SetupResourcePatch(
		e.ScopeWithTimeout(drbdrConfiguredTimeout.Duration),
		cl,
		client.ObjectKey{Name: drbdr.Name},
		func(d *v1alpha1.DRBDResource) {
			d.Spec.Type = v1alpha1.DRBDResourceTypeDiskless
			d.Spec.LVMLogicalVolumeName = ""
			d.Spec.Size = nil
		},
		isDRBDRTerminal,
	)
	assertDRBDRConfigured(e, drbdr)
	assertDRBDRType(e, drbdr, v1alpha1.DRBDResourceTypeDiskless)

	return drbdr
}
