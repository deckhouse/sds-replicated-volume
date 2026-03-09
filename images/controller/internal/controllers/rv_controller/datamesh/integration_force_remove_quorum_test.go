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

package datamesh

// Integration test: ForceRemove quorum invariants.
//
// Verifies that cascading ForceRemove operations (simulating sequential
// node failures) never violate the GMDR guarantee:
//   - qmr never decreases (data redundancy floor preserved).
//   - baseline.GMDR never decreases (published API value stable).
//   - q adjusts correctly (voters/2+1 after each removal).
//
// After catastrophic loss (down to 1D), verifies recovery path:
//   - User lowers config.GMDR to 0.
//   - ChangeQuorum lowers qmr → IO restored.

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// removeRVR removes an RVR by name from the slice (simulates dead node).
func removeRVR(rvrs *[]*v1alpha1.ReplicatedVolumeReplica, name string) {
	for i, rvr := range *rvrs {
		if rvr.Name == name {
			*rvrs = append((*rvrs)[:i], (*rvrs)[i+1:]...)
			return
		}
	}
}

var _ = Describe("integration: ForceRemove quorum invariants", func() {
	for _, ff := range featureVariants {

		Context(featureLabel(ff), func() {
			DescribeTable("",
				func(e layoutEntry) {
					rv, rsp, rvrs := setupLayout(e)

					initialQMR := e.initQMR
					initialGMDR := e.gmdr

					// ── ForceRemove D members one by one (last to first) ───────────
					for d := e.initD - 1; d >= 1; d-- {
						deadName := fmt.Sprintf("rv-1-%d", d)

						// Simulate dead node: remove RVR.
						removeRVR(&rvrs, deadName)

						// Add ForceLeave request.
						rv.Status.DatameshReplicaRequests = []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
							mkForceLeaveRequest(deadName),
						}

						runSettleLoop(rv, rsp, rvrs, nil, ff, nil, assertSafetyInvariants)

						// Count surviving voters.
						var voters int
						for _, m := range rv.Status.Datamesh.Members {
							if m.Type.IsVoter() {
								voters++
							}
						}

						step := fmt.Sprintf("after ForceRemove %s (D=%d)", deadName, voters)

						// Invariants.
						Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(
							BeNumerically(">=", initialQMR),
							"%s: qmr must never drop", step)
						Expect(rv.Status.BaselineGuaranteedMinimumDataRedundancy).To(
							BeNumerically(">=", initialGMDR),
							"%s: baseline.GMDR must never drop", step)
						Expect(rv.Status.Datamesh.Quorum).To(
							Equal(expectedQ(voters)),
							"%s: q must equal voters/2+1", step)

						// Clear request for next iteration.
						rv.Status.DatameshReplicaRequests = nil
					}

					// ── ForceRemove TB (if present) ────────────────────────────────
					for t := 0; t < e.initTB; t++ {
						tbID := e.initD + t
						deadName := fmt.Sprintf("rv-1-%d", tbID)

						removeRVR(&rvrs, deadName)

						rv.Status.DatameshReplicaRequests = []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
							mkForceLeaveRequest(deadName),
						}

						runSettleLoop(rv, rsp, rvrs, nil, ff, nil, assertSafetyInvariants)

						// qmr and baseline.GMDR must still not drop.
						Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(
							BeNumerically(">=", initialQMR),
							"after ForceRemove TB: qmr must never drop")
						Expect(rv.Status.BaselineGuaranteedMinimumDataRedundancy).To(
							BeNumerically(">=", initialGMDR),
							"after ForceRemove TB: baseline.GMDR must never drop")

						rv.Status.DatameshReplicaRequests = nil
					}

					// ── State after cascade: 1D remaining ──────────────────────────
					Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(1)), "final: q=1")
					Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(
						BeNumerically(">=", initialQMR), "final: qmr preserved")

					// ── Recovery: lower config.GMDR to 0 ───────────────────────────
					rv.Status.Configuration.FailuresToTolerate = 0
					rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 0

					runSettleLoop(rv, rsp, rvrs, nil, ff, nil, assertSafetyInvariants)

					Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(1)), "recovery: q=1")
					Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(byte(1)), "recovery: qmr=1")
					Expect(rv.Status.BaselineGuaranteedMinimumDataRedundancy).To(Equal(byte(0)), "recovery: baseline.GMDR=0")
					Expect(rv.Status.DatameshTransitions).To(BeEmpty(), "recovery: no active transitions")
				},
				// Ignored topology.
				Entry("1D (FTT=0 GMDR=0)", layoutEntry{ftt: 0, gmdr: 0, initD: 1, initTB: 0, initQ: 1, initQMR: 1}),
				Entry("2D+1TB (FTT=1 GMDR=0)", layoutEntry{ftt: 1, gmdr: 0, initD: 2, initTB: 1, initQ: 2, initQMR: 1}),
				Entry("2D (FTT=0 GMDR=1)", layoutEntry{ftt: 0, gmdr: 1, initD: 2, initTB: 0, initQ: 2, initQMR: 2}),
				Entry("3D (FTT=1 GMDR=1)", layoutEntry{ftt: 1, gmdr: 1, initD: 3, initTB: 0, initQ: 2, initQMR: 2}),
				Entry("4D+1TB (FTT=2 GMDR=1)", layoutEntry{ftt: 2, gmdr: 1, initD: 4, initTB: 1, initQ: 3, initQMR: 2}),
				Entry("4D (FTT=1 GMDR=2)", layoutEntry{ftt: 1, gmdr: 2, initD: 4, initTB: 0, initQ: 3, initQMR: 3}),
				Entry("5D (FTT=2 GMDR=2)", layoutEntry{ftt: 2, gmdr: 2, initD: 5, initTB: 0, initQ: 3, initQMR: 3}),

				// TransZonal topology (3 zones).
				Entry("1D (FTT=0 GMDR=0) [TZ 3z]", layoutEntry{ftt: 0, gmdr: 0, initD: 1, initTB: 0, initQ: 1, initQMR: 1}.transZonal(3)),
				Entry("2D+1TB (FTT=1 GMDR=0) [TZ 3z]", layoutEntry{ftt: 1, gmdr: 0, initD: 2, initTB: 1, initQ: 2, initQMR: 1}.transZonal(3)),
				Entry("2D (FTT=0 GMDR=1) [TZ 3z]", layoutEntry{ftt: 0, gmdr: 1, initD: 2, initTB: 0, initQ: 2, initQMR: 2}.transZonal(3)),
				Entry("3D (FTT=1 GMDR=1) [TZ 3z]", layoutEntry{ftt: 1, gmdr: 1, initD: 3, initTB: 0, initQ: 2, initQMR: 2}.transZonal(3)),
				Entry("4D+1TB (FTT=2 GMDR=1) [TZ 3z]", layoutEntry{ftt: 2, gmdr: 1, initD: 4, initTB: 1, initQ: 3, initQMR: 2}.transZonal(3)),
				Entry("5D (FTT=2 GMDR=2) [TZ 3z]", layoutEntry{ftt: 2, gmdr: 2, initD: 5, initTB: 0, initQ: 3, initQMR: 3}.transZonal(3)),

				// TransZonal topology (4 zones — 4D).
				Entry("4D (FTT=1 GMDR=2) [TZ 4z]", layoutEntry{ftt: 1, gmdr: 2, initD: 4, initTB: 0, initQ: 3, initQMR: 3}.transZonal(4)),

				// TransZonal topology (5 zones — pure).
				Entry("4D+1TB (FTT=2 GMDR=1) [TZ 5z]", layoutEntry{ftt: 2, gmdr: 1, initD: 4, initTB: 1, initQ: 3, initQMR: 2}.transZonal(5)),
				Entry("5D (FTT=2 GMDR=2) [TZ 5z]", layoutEntry{ftt: 2, gmdr: 2, initD: 5, initTB: 0, initQ: 3, initQMR: 3}.transZonal(5)),

				// Zonal topology.
				Entry("1D (FTT=0 GMDR=0) [Zonal]", layoutEntry{ftt: 0, gmdr: 0, initD: 1, initTB: 0, initQ: 1, initQMR: 1}.zonal()),
				Entry("2D+1TB (FTT=1 GMDR=0) [Zonal]", layoutEntry{ftt: 1, gmdr: 0, initD: 2, initTB: 1, initQ: 2, initQMR: 1}.zonal()),
				Entry("2D (FTT=0 GMDR=1) [Zonal]", layoutEntry{ftt: 0, gmdr: 1, initD: 2, initTB: 0, initQ: 2, initQMR: 2}.zonal()),
				Entry("3D (FTT=1 GMDR=1) [Zonal]", layoutEntry{ftt: 1, gmdr: 1, initD: 3, initTB: 0, initQ: 2, initQMR: 2}.zonal()),
				Entry("4D+1TB (FTT=2 GMDR=1) [Zonal]", layoutEntry{ftt: 2, gmdr: 1, initD: 4, initTB: 1, initQ: 3, initQMR: 2}.zonal()),
				Entry("4D (FTT=1 GMDR=2) [Zonal]", layoutEntry{ftt: 1, gmdr: 2, initD: 4, initTB: 0, initQ: 3, initQMR: 3}.zonal()),
				Entry("5D (FTT=2 GMDR=2) [Zonal]", layoutEntry{ftt: 2, gmdr: 2, initD: 5, initTB: 0, initQ: 3, initQMR: 3}.zonal()),
			)
		})
	}
})

// ──────────────────────────────────────────────────────────────────────────────
// Simultaneous ForceRemove (multiple dead nodes in one cycle)
//
// Multiple ForceRemoveReplica operations can execute in a single
// reconciliation (e.g., two nodes failed). q is computed once for
// the final voter count.

var _ = Describe("integration: simultaneous ForceRemove", func() {
	for _, ff := range featureVariants {

		Context(featureLabel(ff), func() {
			It("2 D die simultaneously from 3D", func() {
				e := layoutEntry{ftt: 1, gmdr: 1, initD: 3, initTB: 0, initQ: 2, initQMR: 2}
				rv, rsp, rvrs := setupLayout(e)

				removeRVR(&rvrs, "rv-1-1")
				removeRVR(&rvrs, "rv-1-2")
				rv.Status.DatameshReplicaRequests = []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					mkForceLeaveRequest("rv-1-1"),
					mkForceLeaveRequest("rv-1-2"),
				}

				runSettleLoop(rv, rsp, rvrs, nil, ff, nil, assertSafetyInvariants)

				Expect(rv.Status.Datamesh.Members).To(HaveLen(1))
				Expect(rv.Status.Datamesh.Members[0].Name).To(Equal("rv-1-0"))
				Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(1)))
				Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(
					BeNumerically(">=", e.initQMR), "qmr must never drop")
				Expect(rv.Status.DatameshTransitions).To(BeEmpty())
			})

			It("D + TB die simultaneously from 4D+TB", func() {
				e := layoutEntry{ftt: 2, gmdr: 1, initD: 4, initTB: 1, initQ: 3, initQMR: 2}
				rv, rsp, rvrs := setupLayout(e)

				removeRVR(&rvrs, "rv-1-3")
				removeRVR(&rvrs, "rv-1-4")
				rv.Status.DatameshReplicaRequests = []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					mkForceLeaveRequest("rv-1-3"),
					mkForceLeaveRequest("rv-1-4"),
				}

				runSettleLoop(rv, rsp, rvrs, nil, ff, nil, assertSafetyInvariants)

				var voters, tbs int
				for _, m := range rv.Status.Datamesh.Members {
					if m.Type.IsVoter() {
						voters++
					}
					if m.Type == v1alpha1.DatameshMemberTypeTieBreaker {
						tbs++
					}
				}
				Expect(voters).To(Equal(3))
				Expect(tbs).To(Equal(0))
				Expect(rv.Status.Datamesh.Quorum).To(Equal(expectedQ(3)))
				Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(
					BeNumerically(">=", e.initQMR), "qmr must never drop")
				Expect(rv.Status.DatameshTransitions).To(BeEmpty())
			})

			It("2 D die simultaneously from 5D", func() {
				e := layoutEntry{ftt: 2, gmdr: 2, initD: 5, initTB: 0, initQ: 3, initQMR: 3}
				rv, rsp, rvrs := setupLayout(e)

				removeRVR(&rvrs, "rv-1-3")
				removeRVR(&rvrs, "rv-1-4")
				rv.Status.DatameshReplicaRequests = []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					mkForceLeaveRequest("rv-1-3"),
					mkForceLeaveRequest("rv-1-4"),
				}

				runSettleLoop(rv, rsp, rvrs, nil, ff, nil, assertSafetyInvariants)

				var voters int
				for _, m := range rv.Status.Datamesh.Members {
					if m.Type.IsVoter() {
						voters++
					}
				}
				Expect(voters).To(Equal(3))
				Expect(rv.Status.Datamesh.Quorum).To(Equal(expectedQ(3)))
				Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(
					BeNumerically(">=", e.initQMR), "qmr must never drop")
				Expect(rv.Status.DatameshTransitions).To(BeEmpty())
			})
		})
	}
})
