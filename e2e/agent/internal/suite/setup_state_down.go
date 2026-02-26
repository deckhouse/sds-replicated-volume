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

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/kubetesting"
)

// SetupStateDown patches the DRBDResource to state=Down, waits for the agent
// to tear down DRBD and remove its finalizer from the DRBDResource.
// The LLV finalizer is intentionally kept — the resource may come back Up.
func SetupStateDown(
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
			d.Spec.State = v1alpha1.DRBDResourceStateDown
		},
		isDRBDRFinalizerGone,
	)
	assertDRBDRHasNoAgentFinalizer(e, cl, drbdr.Name)

	return drbdr
}

func isDRBDRFinalizerGone(drbdr *v1alpha1.DRBDResource) bool {
	return !obju.HasFinalizer(drbdr, v1alpha1.AgentFinalizer)
}

func assertDRBDRHasNoAgentFinalizer(e envtesting.E, cl client.Client, name string) {
	e.Helper()
	drbdr := &v1alpha1.DRBDResource{}
	if err := cl.Get(e.Context(), client.ObjectKey{Name: name}, drbdr); err != nil {
		e.Fatalf("getting DRBDResource %q: %v", name, err)
	}
	if obju.HasFinalizer(drbdr, v1alpha1.AgentFinalizer) {
		e.Fatalf("DRBDResource %q still has agent finalizer %q", name, v1alpha1.AgentFinalizer)
	}
}
