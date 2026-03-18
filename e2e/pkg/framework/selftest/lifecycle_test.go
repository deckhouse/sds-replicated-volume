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

package selftest

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	. "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/match"
	. "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

var _ = Describe("RVR lifecycle", Label(fw.LabelSmoke), func() {
	It("handles delete and recreate with reset on group member", func(ctx SpecContext) {
		trv := f.TestRV()
		trv.Create(ctx)

		trv.Await(ctx, RV.FormationComplete())

		f.TestRVRExact(trv.Name(), 1).
			Type(v1alpha1.ReplicaTypeTieBreaker).
			Create(ctx)

		grouped := trv.TestRVR(1)
		grouped.Await(ctx, Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))
		Expect(trv.RVRCount()).To(Equal(2))

		grouped.Delete(ctx)
		grouped.Await(ctx, Deleted())
		Expect(grouped.IsDeleted()).To(BeTrue())

		f.TestRVRExact(trv.Name(), 1).
			Type(v1alpha1.ReplicaTypeTieBreaker).
			Create(ctx)

		grouped.Await(ctx, PresentAgain())
		grouped.Await(ctx, Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))
		Expect(grouped.IsDeleted()).To(BeFalse())
	})

	It("lifecycle matchers on group member", func(ctx SpecContext) {
		trv := f.TestRV()
		trv.Create(ctx)
		trv.Await(ctx, RV.FormationComplete())

		f.TestRVRExact(trv.Name(), 1).
			Type(v1alpha1.ReplicaTypeTieBreaker).
			Create(ctx)

		grouped := trv.TestRVR(1)

		grouped.Await(ctx, Present())
		grouped.Await(ctx, Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))

		grouped.Follows(
			Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)),
			PhaseNot(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)),
			Deleted(),
			PresentAgain(),
			Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)),
		)

		grouped.Delete(ctx)
		grouped.Await(ctx, Deleted())
		Expect(grouped.IsDeleted()).To(BeTrue())

		// Register sequence with PresentAgain as first step (object is deleted now).
		grouped.Follows(
			PresentAgain(),
			Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)),
		)

		f.TestRVRExact(trv.Name(), 1).
			Type(v1alpha1.ReplicaTypeTieBreaker).
			Create(ctx)

		grouped.Await(ctx, PresentAgain())
		grouped.Await(ctx, Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))

		// Both sequences complete: the mid-lifecycle one and the PresentAgain-first one.
		grouped.AwaitFollowed(ctx)
	})
})
