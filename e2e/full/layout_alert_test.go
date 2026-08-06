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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/utils/ptr"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/match"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/require"
	tkmatch "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

// BE2E-4 — the alerting pipeline end to end: the collector emits the series, the
// controller's ServiceMonitor gets it scraped, the rule in
// monitoring/prometheus-rules/replicated-volume-layout.yaml turns it into
// D8ReplicatedVolumeLayoutDegraded, Alertmanager posts it to the alerts-receiver
// and a ClusterAlert object shows up. Everything before the rule is covered by
// unit tests and BE2E-3; only a real cluster can cover the rest.
//
// Why it is LongHaul and not Disruptive: the rule waits `for: 15m` before it
// fires, and only firing alerts are materialized as objects, so the spec is
// dominated by waiting. LongHaul buys a 30-minute budget and the HIGHEST spec
// priority, so a parallel run starts it first and its wait overlaps with the
// rest of the suite. It must NOT carry Disruptive: that label injects Serial and
// the lowest priority, which would move the wait to the end of the run where
// nothing overlaps it — and it is not needed, because the spec strips no
// finalizer, touches no node label and writes to no raw device.
//
// Both volumes are degraded up front so their two 15-minute windows run at the
// same time.
var _ = Describe("Layout: a degraded layout raises a ClusterAlert",
	Label(fw.LabelLongHaul), Label(fw.LabelFeatureStatus), func() {

		It("raises a firing D8ReplicatedVolumeLayoutDegraded for every volume that lost a diskful replica",
			require.MinNodes(3, 1), func(ctx SpecContext) {
				if !f.ClusterAlertsAvailable(ctx) {
					Skip("clusteralerts.deckhouse.io is not installed on this cluster: without the " +
						"Deckhouse observability modules (Prometheus + alerts-receiver) an alert is " +
						"never materialized as an object. Run this spec on a stand with monitoring " +
						"enabled, or verify the rule there.")
				}

				By("creating a 3D volume and a 2D+1TB volume, both converged")
				trv3 := f.TestRV().FTT(1).GMDR(1)
				trv3.Create(ctx)
				trv2 := f.TestRV().FTT(1).GMDR(0)
				trv2.Create(ctx)

				for _, trv := range []*fw.TestRV{trv3, trv2} {
					trv.Await(ctx, match.RV.FormationComplete())
					trv.Await(ctx, match.RV.Members(3))
					trv.Await(ctx, tkmatch.ConditionReason(
						v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
						v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged))
				}
				Expect(membershipLayoutOf(trv3)).To(Equal(ptr.To("3D")))
				Expect(membershipLayoutOf(trv2)).To(Equal(ptr.To("2D+1TB")))

				By("degrading both volumes downwards, one diskful replica each")
				// No attachment anywhere in this spec, so any diskful member is a
				// legal victim. Both losses happen before either alert is awaited:
				// the `for: 15m` windows have to overlap, or the spec would need
				// twice its budget.
				loseOneDiskful(ctx, trv3)
				loseOneDiskful(ctx, trv2)

				By("both volumes report the degradation with the exact arithmetic")
				trv3.Await(ctx, layoutDegraded("2D", "3D"))
				trv2.Await(ctx, layoutDegraded("1D+1TB", "2D+1TB"))

				By("the surviving tie-breaker is still an intentional diskless client")
				// The degradation this spec alerts on must not drag a second alert
				// behind it: a tie-breaker whose device stopped being intentionally
				// diskless would fire D8DrbdDeviceIsUnintentionalDiskless as well.
				trv2.AwaitIntentionalDiskless(ctx, 1)

				By("the metric is exported for both — so a missing alert cannot be blamed on the collector")
				// This is the precondition that keeps a failure diagnosable: if the
				// series is here and the ClusterAlert never arrives, the defect is in
				// the scrape, the rule or the receiver, not in the controller.
				degraded := v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonTransitionUnsupported
				awaitLayoutMetric(ctx, trv3, degraded, 0)
				awaitLayoutMetric(ctx, trv2, degraded, 0)

				By("waiting for a firing ClusterAlert naming each volume")
				// The labels come straight out of the rule's `max by (name, reason)`,
				// so an alert about any other volume can never satisfy this.
				for _, trv := range []*fw.TestRV{trv3, trv2} {
					alert := f.AwaitFiringClusterAlert(ctx, layoutDegradedAlertName, map[string]string{
						"name":   trv.Name(),
						"reason": degraded,
					})
					Expect(alert.Status).To(Equal(fw.ClusterAlertStatusFiring))
					Expect(alert.SeverityLevel).To(Equal("6"),
						"alert %s carries an unexpected severity level", alert)
				}
			})
	})

// loseOneDiskful degrades trv by one diskful replica, picking a victim that is
// not attached. It exists so the spec above states its intent once per volume
// instead of repeating the node bookkeeping.
func loseOneDiskful(ctx SpecContext, trv *fw.TestRV) {
	GinkgoHelper()
	attachedNodes := trv.RVANodes()
	for _, node := range memberNodesOfType(trv, v1alpha1.DatameshMemberTypeDiskful) {
		if !attachedNodes[node] {
			simulateDiskfulLoss(ctx, trv, node)
			return
		}
	}
	Fail("volume " + trv.Name() + " has no unattached diskful replica to lose")
}
