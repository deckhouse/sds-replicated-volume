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

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/kubetesting"
)

// SetupDisklessToDiskfulReplica creates a full single-node replica: diskless
// DRBDResource -> wait configured -> LLV -> wait created -> patch to diskful
// -> wait configured. Returns the configured DRBDResource and LLV.
// The prefix is included in resource names to avoid conflicts between subtests.
func SetupDisklessToDiskfulReplica(
	e envtesting.E,
	cl client.WithWatch,
	cluster *Cluster,
	prefix string,
	nodeIdx int,
) (*v1alpha1.DRBDResource, *snc.LVMLogicalVolume) {
	var testID TestID
	var drbdrConfiguredTimeout DRBDRConfiguredTimeout
	var llvCreatedTimeout LLVCreatedTimeout
	e.Options(&testID, &drbdrConfiguredTimeout, &llvCreatedTimeout)

	node := cluster.Nodes[nodeIdx]
	name := testID.ResourceName(prefix, fmt.Sprintf("%d", nodeIdx))
	size := cluster.AllocateSize

	// Create DRBDResource (Diskless), wait for configured.
	drbdr := kubetesting.SetupResource(
		e.ScopeWithTimeout(drbdrConfiguredTimeout.Duration),
		cl,
		newDRBDResourceDiskless(name, node.Name, uint8(nodeIdx)),
		isDRBDRTerminal,
	)
	assertDRBDRConfigured(e, drbdr)
	if len(drbdr.Status.Addresses) == 0 {
		e.Fatalf("DRBDResource %q has no addresses after configured", name)
	}

	// Create LLV, wait for created.
	llv := kubetesting.SetupResource(
		e.ScopeWithTimeout(llvCreatedTimeout.Duration),
		cl,
		newLLV(name, size, node.LVG.Name),
		isLLVCreated,
	)

	// Patch DRBDResource to Diskful, wait for configured.
	drbdr = kubetesting.SetupResourcePatch(
		e.ScopeWithTimeout(drbdrConfiguredTimeout.Duration),
		cl,
		client.ObjectKey{Name: name},
		changeDRBDResourceToDiskful(name, size),
		isDRBDRTerminal,
	)
	assertDRBDRConfigured(e, drbdr)
	assertLLVHasAgentFinalizer(e, cl, name)

	return drbdr, llv
}

func newDRBDResourceDiskless(name, nodeName string, nodeID uint8) *v1alpha1.DRBDResource {
	return &v1alpha1.DRBDResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.DRBDResourceSpec{
			NodeName:       nodeName,
			State:          v1alpha1.DRBDResourceStateUp,
			SystemNetworks: []string{"Internal"},
			NodeID:         nodeID,
			Role:           v1alpha1.DRBDRoleSecondary,
			Type:           v1alpha1.DRBDResourceTypeDiskless,
		},
	}
}

func newLLV(name string, size resource.Quantity, lvgName string) *snc.LVMLogicalVolume {
	return &snc.LVMLogicalVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: snc.LVMLogicalVolumeSpec{
			ActualLVNameOnTheNode: name,
			Type:                  "Thick",
			Size:                  size.String(),
			LVMVolumeGroupName:    lvgName,
		},
	}
}

func changeDRBDResourceToDiskful(llvName string, size resource.Quantity) func(*v1alpha1.DRBDResource) {
	return func(d *v1alpha1.DRBDResource) {
		d.Spec.Type = v1alpha1.DRBDResourceTypeDiskful
		d.Spec.LVMLogicalVolumeName = llvName
		d.Spec.Size = &size
	}
}

func isLLVCreated(llv *snc.LVMLogicalVolume) bool {
	return llv.Status != nil && llv.Status.Phase == snc.PhaseCreated
}

func assertLLVHasAgentFinalizer(e envtesting.E, cl client.Client, llvName string) {
	e.Helper()
	llv := &snc.LVMLogicalVolume{}
	if err := cl.Get(e.Context(), client.ObjectKey{Name: llvName}, llv); err != nil {
		e.Fatalf("getting LLV %q: %v", llvName, err)
	}
	if !obju.HasFinalizer(llv, v1alpha1.AgentFinalizer) {
		e.Fatalf("LLV %q does not have agent finalizer %q after diskful link",
			llvName, v1alpha1.AgentFinalizer)
	}
}
