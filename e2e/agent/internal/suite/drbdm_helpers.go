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
	"context"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
)

// drbdmPollInterval paces the polls below. It matches waitForDeletion's cadence.
const drbdmPollInterval = 200 * time.Millisecond

// A DRBDMapper is named after the DRBDResource it is layered on, so the resource
// name is enough to address it.

// waitForDRBDMapperConfigured waits until the DRBDMapper for drbdrName exists and
// reports Configured=True, then provides it.
//
// The agent gets there in two steps — the DRBDResource controller creates the
// object, then the DRBDMapper controller builds the dm layers and sets the
// condition — so this cannot be a single read.
func waitForDRBDMapperConfigured(
	e envtesting.E,
	cl client.Client,
	drbdrName string,
	timeout time.Duration,
) *v1alpha1.DRBDMapper {
	ctx, cancel := context.WithTimeout(e.Context(), timeout)
	defer cancel()

	key := client.ObjectKey{Name: drbdrName}
	last := "not found"
	for {
		drbdm := &v1alpha1.DRBDMapper{}
		err := cl.Get(ctx, key, drbdm)
		switch {
		case err == nil && drbdm.DeletionTimestamp != nil:
			// A mapper from a previous lifecycle. Its removal keeps Configured=True
			// when dmsetup remove succeeds first try, so without this the leftover
			// would satisfy the wait and the device about to be cleared would look
			// like the device just published.
			last = "exists but is terminating"
		case err == nil && obju.IsStatusConditionPresentAndTrue(drbdm, v1alpha1.DRBDMapperCondConfiguredType):
			return drbdm
		case err == nil:
			last = "exists but Configured is not True"
		case apierrors.IsNotFound(err):
			last = "not found"
		default:
			e.Fatalf("getting DRBDMapper %q: %v", drbdrName, err)
		}

		select {
		case <-ctx.Done():
			e.Fatalf("timed out waiting for DRBDMapper %q to be Configured: %s", drbdrName, last)
		case <-time.After(drbdmPollInterval):
		}
	}
}

// waitForDRBDMapperGone waits until the DRBDMapper for drbdrName is absent.
//
// The agent deletes the object and the DRBDMapper controller holds it through its
// finalizer until the dm layers are gone, so the object outlives the delete call.
func waitForDRBDMapperGone(
	e envtesting.E,
	cl client.Client,
	drbdrName string,
	timeout time.Duration,
) {
	ctx, cancel := context.WithTimeout(e.Context(), timeout)
	defer cancel()

	key := client.ObjectKey{Name: drbdrName}
	for {
		drbdm := &v1alpha1.DRBDMapper{}
		err := cl.Get(ctx, key, drbdm)
		if apierrors.IsNotFound(err) {
			return
		}
		if err != nil && ctx.Err() == nil {
			e.Fatalf("getting DRBDMapper %q: %v", drbdrName, err)
		}

		select {
		case <-ctx.Done():
			e.Fatalf("timed out waiting for DRBDMapper %q to be deleted (deletionTimestamp=%v, finalizers=%v)",
				drbdrName, drbdm.DeletionTimestamp, drbdm.Finalizers)
		case <-time.After(drbdmPollInterval):
		}
	}
}

// assertDRBDMapperAbsent verifies that no DRBDMapper exists for drbdrName.
//
// A single read suffices wherever the caller has already observed the
// DRBDResource's Configured condition for the current generation: the agent
// creates the mapper while converging, before it patches the status that the
// caller waited on. So a mapper it meant to create is already in the API by the
// time we look, and an absence here is a real absence rather than a race.
func assertDRBDMapperAbsent(e envtesting.E, cl client.Client, drbdrName string) {
	drbdm := &v1alpha1.DRBDMapper{}
	err := cl.Get(e.Context(), client.ObjectKey{Name: drbdrName}, drbdm)
	if err == nil {
		e.Fatalf("assert: DRBDMapper %q exists, want none (nodeName=%q lowerDevicePath=%q deleting=%v)",
			drbdrName, drbdm.Spec.NodeName, drbdm.Spec.LowerDevicePath, drbdm.DeletionTimestamp != nil)
	}
	if !apierrors.IsNotFound(err) {
		e.Fatalf("getting DRBDMapper %q: %v", drbdrName, err)
	}
}

// assertDRBDMapperSpec verifies the DRBDMapper is pinned to the node and the
// device symlink of the DRBDResource it belongs to. There must be exactly one per
// DRBDResource, which its name already guarantees.
//
// Worth more than it looks: the mapper's spec is immutable and the agent adopts an
// existing one by name, so a wrong lowerDevicePath would publish somebody else's
// device as this resource's with no error anywhere.
//
// Requires drbdr to carry a populated spec.nodeName — a hand-built stub would make
// the node comparison vacuous.
func assertDRBDMapperSpec(e envtesting.E, drbdm *v1alpha1.DRBDMapper, drbdr *v1alpha1.DRBDResource) {
	if drbdr.Spec.NodeName == "" {
		e.Fatalf("require: DRBDResource %q has empty spec.nodeName", drbdr.Name)
	}
	if drbdm.Name != drbdr.Name {
		e.Fatalf("assert: DRBDMapper %q must be named after DRBDResource %q", drbdm.Name, drbdr.Name)
	}
	if drbdm.Spec.NodeName != drbdr.Spec.NodeName {
		e.Fatalf("assert: DRBDMapper %q spec.nodeName is %q, want %q",
			drbdm.Name, drbdm.Spec.NodeName, drbdr.Spec.NodeName)
	}
	want := v1alpha1.FormatDRBDResourceDeviceSymlinkPath(drbdr.Name)
	if drbdm.Spec.LowerDevicePath != want {
		e.Fatalf("assert: DRBDMapper %q spec.lowerDevicePath is %q, want %q",
			drbdm.Name, drbdm.Spec.LowerDevicePath, want)
	}
}
