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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/require"
	tkmatch "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

var _ = Describe("DRBDR: local port conflict re-allocation", Label(fw.LabelSlow), func() {
	It("re-allocates port when local address is occupied", SpecTimeout(2*time.Minute), require.MinNodes(2),
		func(ctx SpecContext) {
			trv := f.SetupLayout(ctx, fw.TestLayout{FTT: 0, GMDR: 1})

			// --- Pick the first datamesh member (ID is not necessarily 0) ---

			rv := trv.Object()
			Expect(rv.Status.Datamesh.Members).To(HaveLen(2), "layout must produce 2 members")
			member := rv.Status.Datamesh.Members[0]
			replicaID := int(member.ID())

			trvr := trv.TestRVR(replicaID)
			trvr.Await(ctx, tkmatch.Present())
			tdrbdr := trvr.DRBDR()
			drbdrName := tdrbdr.Name()
			tdrbdr.Await(ctx, tkmatch.ConditionStatus(
				v1alpha1.DRBDResourceCondConfiguredType, "True"))

			obj := tdrbdr.Object()
			Expect(obj.Status.Addresses).NotTo(BeEmpty(), "DRBDR must have addresses")
			Expect(obj.Spec.Peers).NotTo(BeEmpty(), "DRBDR must have peers")

			originalPort := obj.Status.Addresses[0].Address.Port
			localIP := obj.Status.Addresses[0].Address.IPv4
			nodeName := obj.Spec.NodeName
			drbdResName := "sdsrv-" + drbdrName

			peer := obj.Spec.Peers[0]
			Expect(peer.Paths).NotTo(BeEmpty(), "peer must have paths")
			remoteAddr := fmt.Sprintf("%s:%d", peer.Paths[0].Address.IPv4, peer.Paths[0].Address.Port)
			localAddr := fmt.Sprintf("%s:%d", localIP, originalPort)
			peerNodeID := fmt.Sprintf("%d", peer.NodeID)

			fmt.Fprintf(GinkgoWriter, "[port-conflict] node=%s drbdRes=%s localAddr=%s remoteAddr=%s\n",
				nodeName, drbdResName, localAddr, remoteAddr)

			// --- Enter maintenance to prevent agent from restoring state ---

			tdrbdr.Update(ctx, func(d *v1alpha1.DRBDResource) {
				d.Spec.Maintenance = v1alpha1.MaintenanceModeNoResourceReconciliation
			})
			DeferCleanup(func(cleanupCtx SpecContext) {
				tdrbdr.Update(cleanupCtx, func(d *v1alpha1.DRBDResource) {
					d.Spec.Maintenance = ""
				})
			})

			// --- Disconnect and remove path to free the port ---

			res, err := f.Drbdsetup(ctx, nodeName, "disconnect", drbdResName, peerNodeID)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.ExitCode).To(Equal(0), "disconnect must succeed")

			res, err = f.Drbdsetup(ctx, nodeName, "del-path", drbdResName, peerNodeID, localAddr, remoteAddr)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.ExitCode).To(Equal(0), "del-path must succeed")

			// --- Create dummy DRBD resource to occupy the port ---

			dummyRes := f.UniqueName("port-block")
			DeferCleanup(func(cleanupCtx SpecContext) {
				_, _ = f.Drbdsetup(cleanupCtx, nodeName, "down", dummyRes)
			})

			res, err = f.Drbdsetup(ctx, nodeName, "new-resource", dummyRes, "31")
			Expect(err).NotTo(HaveOccurred())
			Expect(res.ExitCode).To(Equal(0), "new-resource for dummy must succeed")

			res, err = f.Drbdsetup(ctx, nodeName, "new-peer", dummyRes, "0", "--_name", "dummy-peer", "--protocol", "C")
			Expect(err).NotTo(HaveOccurred())
			Expect(res.ExitCode).To(Equal(0), "new-peer for dummy must succeed")

			dummyLocalAddr := fmt.Sprintf("%s:%d", localIP, originalPort)
			dummyRemoteAddr := fmt.Sprintf("%s:%d", localIP, 1)
			res, err = f.Drbdsetup(ctx, nodeName, "new-path", dummyRes, "0", dummyLocalAddr, dummyRemoteAddr)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.ExitCode).To(Equal(0), "new-path for dummy must succeed (occupying port %d)", originalPort)

			fmt.Fprintf(GinkgoWriter, "[port-conflict] port %d on %s is now occupied by dummy resource\n",
				originalPort, localIP)

			// --- Exit maintenance: agent will try new-path with the blocked port ---

			tdrbdr.Update(ctx, func(d *v1alpha1.DRBDResource) {
				d.Spec.Maintenance = ""
			})

			// --- Await port re-allocation ---

			portChanged := tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
				d := obj.(*v1alpha1.DRBDResource)
				if len(d.Status.Addresses) == 0 {
					return false, "no addresses"
				}
				p := d.Status.Addresses[0].Address.Port
				if p != 0 && p != originalPort {
					return true, fmt.Sprintf("port changed: %d -> %d", originalPort, p)
				}
				return false, fmt.Sprintf("port still %d", p)
			})
			tdrbdr.Await(ctx, portChanged)

			newPort := tdrbdr.Object().Status.Addresses[0].Address.Port
			fmt.Fprintf(GinkgoWriter, "[port-conflict] port re-allocated: %d -> %d\n", originalPort, newPort)

			// --- Clean up dummy resource so the real connection can succeed ---

			res, err = f.Drbdsetup(ctx, nodeName, "down", dummyRes)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.ExitCode).To(Equal(0), "down for dummy must succeed")

			// --- Await full convergence ---

			tdrbdr.Await(ctx, tkmatch.ConditionStatus(
				v1alpha1.DRBDResourceCondConfiguredType, "True"))

			Expect(tdrbdr.Object().Status.Addresses[0].Address.Port).NotTo(Equal(originalPort),
				"port must remain different after convergence")
		})
})
