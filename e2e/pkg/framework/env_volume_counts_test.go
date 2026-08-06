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
)

// The table below is the whole contract of ParseRolloutVolumes, the single
// parser of E2E_ROLLOUT_VOLUMES in the process.
var _ = Describe("ParseRolloutVolumes", func() {
	DescribeTable("reads a volume count out of one string",
		func(raw string, expected int, accepted bool) {
			n, err := ParseRolloutVolumes(raw)
			if !accepted {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(EnvRolloutVolumes))
				Expect(err.Error()).To(ContainSubstring(raw))
				Expect(err.Error()).To(ContainSubstring("must be a decimal integer >= 1"))
				// A refused value still hands back a usable count: the spec tree is
				// built before the gate can report the error, and a tree nobody runs
				// still has to be built.
				Expect(n).To(Equal(DefaultRolloutVolumes))
				return
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(expected))
		},

		// Unset — the normal case, and the only one that yields the default.
		Entry("empty means the default", "", DefaultRolloutVolumes, true),

		Entry("the default spelled out", "20", 20, true),
		Entry("fewer volumes than the default", "4", 4, true),
		Entry("more volumes than the default", "50", 50, true),
		Entry("one volume", "1", 1, true),
		Entry("an explicit sign", "+20", 20, true),

		// Refused: a scenario that ran on no volumes would pass vacuously.
		Entry("zero", "0", 0, false),
		Entry("negative", "-1", 0, false),

		// Refused: unreadable values, each of which some shell or CI could produce.
		Entry("not a number", "twenty", 0, false),
		Entry("a leading space", " 20", 0, false),
		Entry("scientific notation", "2e1", 0, false),
		Entry("a fraction", "2.5", 0, false),
	)

	It("is a different variable from the one the upgrade suite reads", func() {
		// Two scenarios sizing themselves from one variable would make a stand
		// tuned for one of them silently retune the other.
		Expect(EnvRolloutVolumes).NotTo(Equal(EnvUpgradeVolumes))
	})

	It("names the variable the value came from, not the other one", func() {
		_, rollout := ParseRolloutVolumes("nope")
		_, upgrade := ParseVolumesOverride("nope")
		Expect(rollout.Error()).To(ContainSubstring(EnvRolloutVolumes))
		Expect(rollout.Error()).NotTo(ContainSubstring(EnvUpgradeVolumes))
		Expect(upgrade.Error()).To(ContainSubstring(EnvUpgradeVolumes))
		Expect(upgrade.Error()).NotTo(ContainSubstring(EnvRolloutVolumes))
	})
})
