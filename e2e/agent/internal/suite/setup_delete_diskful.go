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
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
)

// SetupDeleteDiskful deletes a diskful DRBDResource directly (without reverting
// to diskless first) and asserts the agent releases its finalizer from the LLV.
func SetupDeleteDiskful(
	e envtesting.E,
	cl client.Client,
	drbdr *v1alpha1.DRBDResource,
	llv *snc.LVMLogicalVolume,
) {
	var drbdrConfiguredTimeout DRBDRConfiguredTimeout
	e.Options(&drbdrConfiguredTimeout)

	if err := cl.Delete(e.Context(), drbdr); err != nil {
		e.Fatalf("deleting DRBDResource %q: %v", drbdr.Name, err)
	}

	waitForDeletion(e, cl, drbdr, drbdrConfiguredTimeout.Duration)
	assertLLVHasNoAgentFinalizer(e, cl, llv.Name)
}

func waitForDeletion(e envtesting.E, cl client.Client, obj client.Object, timeout time.Duration) {
	key := client.ObjectKeyFromObject(obj)
	kind := fmt.Sprintf("%T", obj)

	ctx, cancel := context.WithTimeout(e.Context(), timeout)
	defer cancel()

	probe := obj.DeepCopyObject().(client.Object)
	for {
		select {
		case <-ctx.Done():
			e.Fatalf("timed out waiting for %s %q to be deleted", kind, key)
		case <-time.After(200 * time.Millisecond):
		}
		if err := cl.Get(ctx, key, probe); err != nil {
			if apierrors.IsNotFound(err) {
				return
			}
			e.Fatalf("waiting for deletion of %s %q: %v", kind, key, err)
		}
	}
}

func assertLLVHasNoAgentFinalizer(e envtesting.E, cl client.Client, llvName string) {
	llv := &snc.LVMLogicalVolume{}
	if err := cl.Get(e.Context(), client.ObjectKey{Name: llvName}, llv); err != nil {
		e.Fatalf("getting LLV %q: %v", llvName, err)
	}
	if obju.HasFinalizer(llv, v1alpha1.AgentFinalizer) {
		e.Fatalf("assert: LLV %q still has agent finalizer %q after DRBDResource deletion",
			llvName, v1alpha1.AgentFinalizer)
	}
}
