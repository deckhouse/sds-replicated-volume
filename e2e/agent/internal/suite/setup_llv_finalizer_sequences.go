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

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/kubetesting"
)

// SetupLLVFinalizerDownUp exercises Down → Up and asserts the LLV finalizer
// is released on Down and re-acquired on Up.
func SetupLLVFinalizerDownUp(
	e envtesting.E,
	cl client.WithWatch,
	drbdr *v1alpha1.DRBDResource,
	llvName string,
) {
	drbdr = SetupStateDown(e, cl, drbdr)
	AssertLLVHasNoAgentFinalizer(e, cl, llvName)

	drbdr = SetupStateUp(e, cl, drbdr)
	AssertLLVHasAgentFinalizer(e, cl, llvName)
}

// SetupLLVFinalizerDownUpDiskless exercises Down → Up+Diskless and asserts
// the LLV finalizer is released on Down and stays absent when returning
// as Diskless.
func SetupLLVFinalizerDownUpDiskless(
	e envtesting.E,
	cl client.WithWatch,
	drbdr *v1alpha1.DRBDResource,
	llvName string,
) {
	drbdr = SetupStateDown(e, cl, drbdr)
	AssertLLVHasNoAgentFinalizer(e, cl, llvName)

	drbdr = setupStateUpDiskless(e, cl, drbdr)
	AssertLLVHasNoAgentFinalizer(e, cl, llvName)
}

// SetupLLVFinalizerDownUpDownUpDiskless exercises Down → Up → Down →
// Up+Diskless and asserts correct finalizer state at each step.
func SetupLLVFinalizerDownUpDownUpDiskless(
	e envtesting.E,
	cl client.WithWatch,
	drbdr *v1alpha1.DRBDResource,
	llvName string,
) {
	drbdr = SetupStateDown(e, cl, drbdr)
	AssertLLVHasNoAgentFinalizer(e, cl, llvName)

	drbdr = SetupStateUp(e, cl, drbdr)
	AssertLLVHasAgentFinalizer(e, cl, llvName)

	drbdr = SetupStateDown(e, cl, drbdr)
	AssertLLVHasNoAgentFinalizer(e, cl, llvName)

	drbdr = setupStateUpDiskless(e, cl, drbdr)
	AssertLLVHasNoAgentFinalizer(e, cl, llvName)
}

// SetupLLVFinalizerDownDisklessThenUp exercises Down → clear spec (while
// Down) → Up and asserts the finalizer stays absent throughout.
func SetupLLVFinalizerDownDisklessThenUp(
	e envtesting.E,
	cl client.WithWatch,
	drbdr *v1alpha1.DRBDResource,
	llvName string,
) {
	var drbdrConfiguredTimeout DRBDRConfiguredTimeout
	e.Options(&drbdrConfiguredTimeout)

	drbdr = SetupStateDown(e, cl, drbdr)
	AssertLLVHasNoAgentFinalizer(e, cl, llvName)

	// Patch spec to Diskless while Down. Don't use SetupDiskfulToDiskless
	// because its assertion (activeConfiguration.type=Diskless) doesn't hold
	// when the resource is Down — DRBD reports the last known type.
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
	AssertLLVHasNoAgentFinalizer(e, cl, llvName)

	drbdr = SetupStateUp(e, cl, drbdr)
	AssertLLVHasNoAgentFinalizer(e, cl, llvName)
}

// setupStateUpDiskless patches state=Up + type=Diskless in a single mutation,
// waits for the agent to reach a terminal Configured condition.
func setupStateUpDiskless(
	e envtesting.E,
	cl client.WithWatch,
	drbdr *v1alpha1.DRBDResource,
) *v1alpha1.DRBDResource {
	var drbdrConfiguredTimeout DRBDRConfiguredTimeout
	e.Options(&drbdrConfiguredTimeout)

	return kubetesting.SetupResourcePatch(
		e.ScopeWithTimeout(drbdrConfiguredTimeout.Duration),
		cl,
		client.ObjectKey{Name: drbdr.Name},
		func(d *v1alpha1.DRBDResource) {
			d.Spec.State = v1alpha1.DRBDResourceStateUp
			d.Spec.Type = v1alpha1.DRBDResourceTypeDiskless
			d.Spec.LVMLogicalVolumeName = ""
			d.Spec.Size = nil
		},
		isDRBDRTerminal,
	)
}

// AssertLLVHasAgentFinalizer asserts the LLV has the agent finalizer.
func AssertLLVHasAgentFinalizer(e envtesting.E, cl client.Client, llvName string) {
	llv := &snc.LVMLogicalVolume{}
	if err := cl.Get(e.Context(), client.ObjectKey{Name: llvName}, llv); err != nil {
		e.Fatalf("getting LLV %q: %v", llvName, err)
	}
	if !obju.HasFinalizer(llv, v1alpha1.AgentFinalizer) {
		e.Fatalf("assert: LLV %q does not have agent finalizer %q",
			llvName, v1alpha1.AgentFinalizer)
	}
}

// AssertLLVHasNoAgentFinalizer asserts the LLV does not have the agent finalizer.
func AssertLLVHasNoAgentFinalizer(e envtesting.E, cl client.Client, llvName string) {
	llv := &snc.LVMLogicalVolume{}
	if err := cl.Get(e.Context(), client.ObjectKey{Name: llvName}, llv); err != nil {
		e.Fatalf("getting LLV %q: %v", llvName, err)
	}
	if obju.HasFinalizer(llv, v1alpha1.AgentFinalizer) {
		e.Fatalf("assert: LLV %q still has agent finalizer %q",
			llvName, v1alpha1.AgentFinalizer)
	}
}
