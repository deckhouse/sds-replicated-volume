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

package full

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/match"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/require"
)

// Regression test: snapshot synchronization gets stuck if the temporary
// sync DRBDResource objects disappear mid-sync. The controller watchdog
// (syncNeedsReset, threshold 2m) must detect this and restart the sync
// round; the snapshot must eventually reach Ready.
var _ = Describe("RVS sync resilience", Label(fw.LabelSlow), func() {
	It("recovers from missing temp DRBDResources during Synchronizing",
		SpecTimeout(8*time.Minute), require.MinNodes(3),
		func(ctx SpecContext) {
			trv := f.SetupLayout(ctx, fw.TestLayout{FTT: 1, GMDR: 1})

			trvs := trv.Snapshot()
			trvs.Create(ctx)

			trvs.Await(ctx, match.RVS.PrepareComplete())
			trvs.Await(ctx, match.RVS.PhaseIs(v1alpha1.ReplicatedVolumeSnapshotPhaseSynchronizing))

			prefix := trvs.Name() + "-"

			Eventually(ctx, func() int {
				return countSyncDRBDRs(ctx, prefix)
			}).Should(BeNumerically(">", 0), "no temp sync DRBDResources appeared")

			forceDeleteSyncDRBDRs(ctx, prefix)

			Eventually(ctx, func() int {
				return countSyncDRBDRs(ctx, prefix)
			}).Should(Equal(0), "temp sync DRBDResources did not disappear")

			trvs.Await(ctx, match.RVS.ReadyToUse())
			trvs.Await(ctx, match.RVS.NoActiveTransitions())
		})
})

// countSyncDRBDRs returns the number of DRBDResource objects with the
// given name prefix that are not yet marked for deletion.
func countSyncDRBDRs(ctx SpecContext, prefix string) int {
	var list v1alpha1.DRBDResourceList
	Expect(f.Client.List(ctx, &list)).To(Succeed())
	n := 0
	for i := range list.Items {
		if strings.HasPrefix(list.Items[i].Name, prefix) &&
			list.Items[i].DeletionTimestamp == nil {
			n++
		}
	}
	return n
}

// forceDeleteSyncDRBDRs strips finalizers and deletes every DRBDResource
// whose name starts with prefix, so they disappear immediately.
func forceDeleteSyncDRBDRs(ctx SpecContext, prefix string) {
	GinkgoHelper()
	var list v1alpha1.DRBDResourceList
	Expect(f.Client.List(ctx, &list)).To(Succeed())
	for i := range list.Items {
		d := &list.Items[i]
		if !strings.HasPrefix(d.Name, prefix) {
			continue
		}
		if len(d.Finalizers) > 0 {
			d.SetFinalizers(nil)
			_ = client.IgnoreNotFound(f.Client.Update(ctx, d))
		}
		if err := f.Client.Delete(ctx, d); err != nil && !apierrors.IsNotFound(err) {
			Expect(err).NotTo(HaveOccurred(),
				"delete DRBDResource %q", d.Name)
		}
	}
}
