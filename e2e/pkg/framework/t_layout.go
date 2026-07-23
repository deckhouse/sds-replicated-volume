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

// ExpectedReplicas returns the total replica count (D + TB + Access) derived from
// FTT/GMDR via the controller's own layout formula. Attached does not add replicas
// — it only changes role.
func (l TestLayout) ExpectedReplicas() int {
	d, tb := l.intendedLayout()
	return d + tb + l.Access
}

// intendedLayout returns the diskful voter and tie-breaker counts for this layout
// straight from the api source of truth (ReplicatedVolumeConfiguration.IntendedLayout),
// so the framework carries no local copy of the D/TB formula.
func (l TestLayout) intendedLayout() (diskful, tiebreakers int) {
	return v1alpha1.ReplicatedVolumeConfiguration{
		FailuresToTolerate:              l.FTT,
		GuaranteedMinimumDataRedundancy: l.GMDR,
	}.IntendedLayout()
}

// SetupLayout creates a fully formed RV with the specified layout
// (D + TB + Access + extra attachments), waits for all members to be ready,
// and returns the TestRV handle.
//
// The tie-breaker (when the layout requires one) is created by the controller
// during formation, so the datamesh already holds D + TB members once formation
// completes. The framework MUST NOT create the tie-breaker by hand: doing so
// would race the controller's formation and, once formation also adds one, leave
// an excess TieBreaker that the layout converger reports as
// LayoutConverged=TransitionUnsupported.
//
//	Phase 1: Create RV (MaxAttachments = Attached)
//	Phase 2: Await formation complete (D + TB members present)
//	Phase 3: Create Access/extra RVAs (fire-and-forget)
//	Phase 4: Await RVAs Attached and the full member count
func (f *Framework) SetupLayout(ctx SpecContext, l TestLayout) *TestRV {
	GinkgoHelper()
	Expect(l.Attached).To(BeNumerically(">=", l.Access),
		"Attached must be >= Access (Access replicas are always attached)")

	// --- Phase 1: create RV ---

	trv := f.TestRV().FTT(l.FTT).GMDR(l.GMDR)
	if l.Attached > 0 {
		trv = trv.MaxAttachments(byte(l.Attached))
	}
	trv.Create(ctx)

	// --- Phase 2: wait for formation (diskful + tie-breaker) ---

	d, tb := l.intendedLayout()
	trv.Await(ctx, match.RV.FormationComplete())
	trv.Await(ctx, match.RV.Members(d+tb))

	// --- Phase 3: create Access + extra RVAs (fire-and-forget) ---
	//
	// OccupiedNodes now already includes the auto-created tie-breaker node,
	// so Access replicas avoid colliding with it without any special-casing.

	var allRVAs []*TestRVA
	var accessNodes []string
	for range l.Access {
		except := trv.OccupiedNodes()
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
		// Only diskful members can be attached (Primary); the tie-breaker is
		// diskless and Access members are counted separately above.
		if obj.Spec.Type == v1alpha1.ReplicaTypeDiskful {
			allRVAs = append(allRVAs, trv.Attach(ctx, obj.Spec.NodeName))
			extra--
		}
	}
	Expect(extra).To(Equal(0), "not enough diskful members to satisfy Attached count")

	// --- Phase 4: await attachments and the full member count ---

	for _, trva := range allRVAs {
		trva.Await(ctx, tkmatch.ConditionReason(
			v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
			v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttached))
	}

	trv.Await(ctx, match.RV.Members(l.ExpectedReplicas()))

	return trv
}
