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

package framework

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/match"
	tkmatch "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

// TestLayout describes an RV layout for creating a fully formed RV with
// diskful replicas, optional TieBreaker, Access replicas, and attachments.
type TestLayout struct {
	FTT      byte
	GMDR     byte
	Access   int
	Attached int
}

// ExpectedReplicas returns the total replica count (D + TB + Access)
// derived from FTT/GMDR using the same formula as the controller.
// Attached does not add replicas — it only changes role.
func (l TestLayout) ExpectedReplicas() int {
	D := int(l.FTT) + int(l.GMDR) + 1
	n := D + l.Access
	if D%2 == 0 && D > 0 && int(l.FTT) == D/2 {
		n++
	}
	return n
}

// SetupLayout creates a fully formed RV with the specified layout
// (D + TB + Access + extra attachments), waits for all members to be ready,
// and returns the TestRV handle.
//
// The flow is optimized to parallelize creation and defer awaits:
//
//	Phase 1: Create RV (MaxAttachments = max(Attached, Access))
//	Phase 2: Await D diskful members (to learn occupied nodes/IDs)
//	Phase 3: Create TB RVR + Access/extra RVAs (fire-and-forget)
//	Phase 4: Await FormationComplete, TB Healthy, RVAs Attached
func (f *Framework) SetupLayout(ctx SpecContext, l TestLayout) *TestRV {
	GinkgoHelper()
	Expect(l.Attached).To(BeNumerically(">=", l.Access),
		"Attached must be >= Access (Access replicas are always attached)")

	D := int(l.FTT) + int(l.GMDR) + 1
	needTB := D%2 == 0 && D > 0 && int(l.FTT) == D/2

	// --- Phase 1: create RV ---

	trv := f.TestRV().FTT(l.FTT).GMDR(l.GMDR)
	if l.Attached > 0 {
		trv = trv.MaxAttachments(byte(l.Attached))
	}
	trv.Create(ctx)

	// --- Phase 2: wait for D diskful members ---

	trv.Await(ctx, match.RV.Members(D))

	// --- Phase 3: create TB + RVAs (fire-and-forget) ---

	var tbRVR *TestRVR
	var tbNodeName string
	if needTB {
		tbNodeName = f.Discovery.AnyNode(trv.OccupiedNodes()...)
		tbRVR = f.TestRVRExact(trv.Name(), trv.FreeReplicaID()).
			Node(tbNodeName).
			Type(v1alpha1.ReplicaTypeTieBreaker)
		tbRVR.Create(ctx)
	}

	var allRVAs []*TestRVA
	var accessNodes []string
	for range l.Access {
		except := trv.OccupiedNodes()
		if tbNodeName != "" {
			except = append(except, tbNodeName)
		}
		except = append(except, accessNodes...)
		node := f.Discovery.AnyNode(except...)
		allRVAs = append(allRVAs, trv.Attach(ctx, node))
		accessNodes = append(accessNodes, node)
	}

	extra := l.Attached - l.Access
	for _, trvr := range trv.TestRVRs() {
		if extra == 0 {
			break
		}
		trvr.Await(ctx, tkmatch.Present())
		obj := trvr.Object()
		if obj.Spec.Type != v1alpha1.ReplicaTypeAccess {
			allRVAs = append(allRVAs, trv.Attach(ctx, obj.Spec.NodeName))
			extra--
		}
	}
	Expect(extra).To(Equal(0), "not enough non-Access members to satisfy Attached count")

	// --- Phase 4: await everything ---

	trv.Await(ctx, match.RV.FormationComplete())
	if tbRVR != nil {
		tbRVR.Await(ctx, tkmatch.Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))
	}
	for _, trva := range allRVAs {
		trva.Await(ctx, tkmatch.ConditionReason(
			v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
			v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttached))
	}

	trv.Await(ctx, match.RV.Members(l.ExpectedReplicas()))

	return trv
}
