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

var _ = DescribeTable("integration: ForceRemove quorum invariants",
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

			runUntilStable(rv, rsp, rvrs, FeatureFlags{})

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

			runUntilStable(rv, rsp, rvrs, FeatureFlags{})

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

		runUntilStable(rv, rsp, rvrs, FeatureFlags{})

		Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(1)), "recovery: q=1")
		Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(byte(1)), "recovery: qmr=1")
		Expect(rv.Status.BaselineGuaranteedMinimumDataRedundancy).To(Equal(byte(0)), "recovery: baseline.GMDR=0")
		Expect(rv.Status.DatameshTransitions).To(BeEmpty(), "recovery: no active transitions")
	},
	Entry("1D (FTT=0 GMDR=0)", layoutEntry{ftt: 0, gmdr: 0, initD: 1, initTB: 0, initQ: 1, initQMR: 1}),
	Entry("2D+1TB (FTT=1 GMDR=0)", layoutEntry{ftt: 1, gmdr: 0, initD: 2, initTB: 1, initQ: 2, initQMR: 1}),
	Entry("2D (FTT=0 GMDR=1)", layoutEntry{ftt: 0, gmdr: 1, initD: 2, initTB: 0, initQ: 2, initQMR: 2}),
	Entry("3D (FTT=1 GMDR=1)", layoutEntry{ftt: 1, gmdr: 1, initD: 3, initTB: 0, initQ: 2, initQMR: 2}),
	Entry("4D+1TB (FTT=2 GMDR=1)", layoutEntry{ftt: 2, gmdr: 1, initD: 4, initTB: 1, initQ: 3, initQMR: 2}),
	Entry("4D (FTT=1 GMDR=2)", layoutEntry{ftt: 1, gmdr: 2, initD: 4, initTB: 0, initQ: 3, initQMR: 3}),
	Entry("5D (FTT=2 GMDR=2)", layoutEntry{ftt: 2, gmdr: 2, initD: 5, initTB: 0, initQ: 3, initQMR: 3}),
)
