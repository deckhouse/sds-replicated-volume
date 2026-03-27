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
	"math/rand"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	. "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/match"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/require"
	. "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

var _ = Describe("Migration: RV adopt/v1 formation with preexisting DRBD", Label(fw.LabelUpgrade, fw.LabelSlow), func() {
	DescribeTable("adopts preexisting replicas and completes formation",
		func(ctx SpecContext, l fw.TestLayout) {
			res := setupAdoptPreexisting(ctx, l)

			fmt.Fprintf(GinkgoWriter, "[adopt] creating RV %s with %d replicas (%d attached)\n",
				res.RVName, len(res.Replicas), l.Attached)

			trv := f.TestRVExact(res.RVName).Adopt().AdoptSharedSecret(res.AdoptSecret).FTT(l.FTT).GMDR(l.GMDR)
			if l.Attached > 0 {
				trv = trv.MaxAttachments(byte(l.Attached))
			}
			trv.Create(ctx)

			for _, r := range res.Replicas {
				if r.Role == v1alpha1.DRBDRolePrimary {
					trv.Attach(ctx, r.NodeName)
				}
			}

			finishAdopt(ctx, trv, res)
		},

		// --- 1D ---
		Entry("1D",
			fw.TestLayout{FTT: 0, GMDR: 0}),
		Entry("1D (1att)",
			fw.TestLayout{FTT: 0, GMDR: 0, Attached: 1}),
		Entry("1D+1A (1att)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 0, Access: 1, Attached: 1}),
		Entry("1D+1A (2att)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 0, Access: 1, Attached: 2}),
		Entry("1D+2A (2att)",
			SpecTimeout(2*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 0, GMDR: 0, Access: 2, Attached: 2}),

		// --- 2D ---
		Entry("2D",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 1}),
		Entry("2D (1att)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 1, Attached: 1}),
		Entry("2D (2att)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 1, Attached: 2}),
		Entry("2D+1A (1att)",
			SpecTimeout(2*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 0, GMDR: 1, Access: 1, Attached: 1}),
		Entry("2D+1A (2att)",
			SpecTimeout(2*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 0, GMDR: 1, Access: 1, Attached: 2}),
		Entry("2D+2A (2att)",
			SpecTimeout(2*time.Minute), require.MinNodes(4),
			fw.TestLayout{FTT: 0, GMDR: 1, Access: 2, Attached: 2}),

		// --- 2D+1TB ---
		Entry("2D+1TB",
			Label(fw.LabelSmoke),
			SpecTimeout(2*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 0}),
		Entry("2D+1TB (1att)",
			SpecTimeout(2*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 0, Attached: 1}),
		Entry("2D+1TB (2att)",
			Label("Bug:MultiattachDRBDLock"), // fails if two D attached
			SpecTimeout(2*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 0, Attached: 2}),
		Entry("2D+1TB+1A (1att)",
			SpecTimeout(2*time.Minute), require.MinNodes(4),
			fw.TestLayout{FTT: 1, GMDR: 0, Access: 1, Attached: 1}),
		Entry("2D+1TB+1A (2att)",
			Label("Bug:MultiattachDRBDLock"), // fails if two D attached
			SpecTimeout(2*time.Minute), require.MinNodes(4),
			fw.TestLayout{FTT: 1, GMDR: 0, Access: 1, Attached: 2}),
		Entry("2D+1TB+2A (2att)",
			Label("Bug:MultiattachDRBDLock"), // fails if two D attached
			SpecTimeout(2*time.Minute), require.MinNodes(5),
			fw.TestLayout{FTT: 1, GMDR: 0, Access: 2, Attached: 2}),

		// --- 3D ---
		Entry("3D",
			SpecTimeout(3*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 1}),
		Entry("3D (1att)",
			SpecTimeout(3*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 1, Attached: 1}),
		Entry("3D (2att)",
			SpecTimeout(3*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 1, Attached: 2}),
		Entry("3D+1A (1att)",
			SpecTimeout(3*time.Minute), require.MinNodes(4),
			fw.TestLayout{FTT: 1, GMDR: 1, Access: 1, Attached: 1}),
		Entry("3D+1A (2att)",
			Label("Bug:MultiattachDRBDLock"), // fails if two D attached
			SpecTimeout(3*time.Minute), require.MinNodes(4),
			fw.TestLayout{FTT: 1, GMDR: 1, Access: 1, Attached: 2}),
		Entry("3D+2A (2att)",
			Label("Bug:MultiattachDRBDLock"), // fails if two D attached
			SpecTimeout(3*time.Minute), require.MinNodes(5),
			fw.TestLayout{FTT: 1, GMDR: 1, Access: 2, Attached: 2}),
	)

	DescribeTable("adopts preexisting replicas in random order and completes formation",
		func(ctx SpecContext, l fw.TestLayout) {
			sourceRV := f.SetupLayout(ctx, l)
			adoptSecret := sourceRV.Object().Status.Datamesh.SharedSecret
			replicas := sourceRV.EmulatePreexisting(ctx)
			expectedReplicas := l.ExpectedReplicas()
			Expect(replicas).To(HaveLen(expectedReplicas))

			rvName := f.UniqueName()

			// Pre-build all objects for all replicas (no Create yet).
			// Only Diskful and TieBreaker replicas participate in this test.
			type replicaBundle struct {
				r     fw.PreexistingDRBDReplica
				llv   *fw.TestLLV
				drbdr *fw.TestDRBDR
				rvr   *fw.TestRVR
			}
			var bundles []replicaBundle

			for _, r := range replicas {
				if r.Type != v1alpha1.ReplicaTypeDiskful && r.Type != v1alpha1.ReplicaTypeTieBreaker {
					continue
				}

				rvrNameFull := v1alpha1.FormatReplicatedVolumeReplicaName(rvName, r.NodeID)
				var drbdType v1alpha1.DRBDResourceType
				var tllv *fw.TestLLV

				switch r.Type {
				case v1alpha1.ReplicaTypeDiskful:
					drbdType = v1alpha1.DRBDResourceTypeDiskful
					tllv = f.TestLLV()
					llvB := tllv.ActualLVName(r.ActualLVName).
						LVMVolumeGroupName(r.LVGName).
						Type("Thin").
						Size(r.Size)
					if r.ThinPoolName != "" {
						llvB = llvB.ThinPoolName(r.ThinPoolName)
					}
					_ = llvB
				case v1alpha1.ReplicaTypeTieBreaker:
					drbdType = v1alpha1.DRBDResourceTypeDiskless
				}

				drbdrB := f.TestDRBDRExact(rvrNameFull).
					Node(r.NodeName).
					Type(drbdType).
					SystemNetworks("Internal").
					NodeID(r.NodeID).
					ActualNameOnTheNode(r.DRBDName).
					Maintenance(v1alpha1.MaintenanceModeNoResourceReconciliation)
				if drbdType == v1alpha1.DRBDResourceTypeDiskful {
					drbdrB = drbdrB.Size(r.Size).LVMLogicalVolumeName(tllv.Name())
				}
				if r.Role == v1alpha1.DRBDRolePrimary {
					drbdrB = drbdrB.Role(v1alpha1.DRBDRolePrimary)
				}
				_ = drbdrB

				trvr := f.TestRVRExact(rvName, r.NodeID).
					Node(r.NodeName).
					Type(r.Type)
				if r.Type == v1alpha1.ReplicaTypeDiskful {
					trvr = trvr.LVG(r.LVGName)
					if r.ThinPoolName != "" {
						trvr = trvr.ThinPool(r.ThinPoolName)
					}
				}

				bundles = append(bundles, replicaBundle{r: r, llv: tllv, drbdr: drbdrB, rvr: trvr})
			}

			swUpToDate := NewSwitch(DRBDR.DiskState(v1alpha1.DiskStateUpToDate))
			var swPrimaryDevice *Switch
			if l.Attached > 0 {
				swPrimaryDevice = NewSwitch(And(DRBDR.HasDevice(), Not(DRBDR.IOSuspended())))
			}
			swRVRQuorum := NewSwitch(RVR.NeverLoseQuorum())

			type replicaState struct{ llv, drbdr, rvr, upToDateDone bool }
			st := make([]replicaState, len(bundles))

			awaitUpToDate := func(i int) {
				if bundles[i].r.Type != v1alpha1.ReplicaTypeDiskful {
					st[i].upToDateDone = true
					return
				}
				bundles[i].drbdr.Await(ctx, DRBDR.DiskState(v1alpha1.DiskStateUpToDate))
				bundles[i].drbdr.Always(swUpToDate)
				if bundles[i].r.Role == v1alpha1.DRBDRolePrimary {
					bundles[i].drbdr.Always(swPrimaryDevice)
				}
				st[i].upToDateDone = true
			}

			actions := make([]func(), 0, 3*len(bundles))
			for i := range bundles {
				if bundles[i].llv != nil {
					actions = append(actions, func() {
						bundles[i].llv.Create(ctx)
						st[i].llv = true
						if st[i].rvr && rand.Intn(2) == 0 {
							rvrObj := bundles[i].rvr.Object()
							bundles[i].llv.Update(ctx, func(llv *snc.LVMLogicalVolume) {
								Expect(controllerutil.SetControllerReference(rvrObj, llv, f.Scheme)).To(Succeed())
							})
						}
						if st[i].drbdr && !st[i].upToDateDone {
							awaitUpToDate(i)
						}
					})
				}

				actions = append(actions, func() {
					bundles[i].drbdr.Create(ctx)
					bundles[i].drbdr.Await(ctx, DRBDR.HasAddresses())
					assertAddressesAdopted(bundles[i].drbdr.Object().Status.Addresses,
						bundles[i].r.Addresses, len(bundles) > 1, bundles[i].drbdr.Name())
					st[i].drbdr = true
					if st[i].rvr && rand.Intn(2) == 0 {
						rvrObj := bundles[i].rvr.Object()
						bundles[i].drbdr.Update(ctx, func(d *v1alpha1.DRBDResource) {
							Expect(controllerutil.SetControllerReference(rvrObj, d, f.Scheme)).To(Succeed())
						})
					}
					if bundles[i].llv != nil && st[i].llv && !st[i].upToDateDone {
						awaitUpToDate(i)
					}
					if bundles[i].llv == nil && !st[i].upToDateDone {
						st[i].upToDateDone = true
					}
				})

				actions = append(actions, func() {
					bundles[i].rvr.Create(ctx)
					bundles[i].rvr.Always(swRVRQuorum)
					st[i].rvr = true
					rvrObj := bundles[i].rvr.Object()
					if st[i].drbdr && rand.Intn(2) == 0 {
						bundles[i].drbdr.Update(ctx, func(d *v1alpha1.DRBDResource) {
							Expect(controllerutil.SetControllerReference(rvrObj, d, f.Scheme)).To(Succeed())
						})
					}
					if bundles[i].llv != nil && st[i].llv && rand.Intn(2) == 0 {
						bundles[i].llv.Update(ctx, func(llv *snc.LVMLogicalVolume) {
							Expect(controllerutil.SetControllerReference(rvrObj, llv, f.Scheme)).To(Succeed())
						})
					}
				})
			}

			rand.Shuffle(len(actions), func(a, b int) { actions[a], actions[b] = actions[b], actions[a] })
			for _, action := range actions {
				action()
			}

			// Clear maintenance on all DRBDRs during cleanup (see first table comment).
			DeferCleanup(func(cleanupCtx SpecContext) {
				patch := client.RawPatch(types.MergePatchType, []byte(`{"spec":{"maintenance":""}}`))
				for i, b := range bundles {
					if !st[i].drbdr {
						continue
					}
					obj := &v1alpha1.DRBDResource{ObjectMeta: metav1.ObjectMeta{Name: b.drbdr.Name()}}
					_ = client.IgnoreNotFound(f.Client.Patch(cleanupCtx, obj, patch))
				}
			})

			for i := range bundles {
				if !st[i].upToDateDone {
					awaitUpToDate(i)
				}
			}

			trv := f.TestRVExact(rvName).Adopt().AdoptSharedSecret(adoptSecret).FTT(l.FTT).GMDR(l.GMDR)
			trv.Create(ctx)

			trv.Await(ctx, RV.Members(expectedReplicas))

			for _, b := range bundles {
				if b.r.Type == v1alpha1.ReplicaTypeDiskful {
					b.drbdr.Await(ctx, And(
						DRBDR.PeersMatchSpec(),
						DRBDR.RoleMatchesSpec(),
						DRBDR.LVMMatchesSpec(),
						DRBDR.QuorumMatchesSpec(),
					))
				}
			}

			trv.Await(ctx, RV.TransitionStepActive(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation, "Exit maintenance"))

			for _, b := range bundles {
				b.drbdr.Update(ctx, func(d *v1alpha1.DRBDResource) {
					d.Spec.Maintenance = ""
				})
			}

			trv.Await(ctx, RV.FormationComplete())

			Expect(trv.Object().Status.Datamesh.SharedSecret).To(Equal(adoptSecret))

			swUpToDate.Disable()
			if swPrimaryDevice != nil {
				swPrimaryDevice.Disable()
			}
			swRVRQuorum.Disable()

			Expect(trv.RVRCount()).To(Equal(expectedReplicas))
			for i := 0; i < expectedReplicas; i++ {
				trvr := trv.TestRVR(i)
				trvr.AwaitSequence(ctx,
					Or(Phase(string(v1alpha1.ReplicatedVolumeReplicaPhasePending)),
						Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseProvisioning))),
					Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseConfiguring)),
					Or(Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseProgressing)),
						Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy))),
				)
			}
			rv := trv.Object()
			Expect(rv.Status.Datamesh.Members).To(HaveLen(expectedReplicas))
		},

		Entry("FTT=0 GMDR=0 (1D)",
			fw.TestLayout{FTT: 0, GMDR: 0}),
		Entry("FTT=0 GMDR=0 (1D, 1att)",
			fw.TestLayout{FTT: 0, GMDR: 0, Attached: 1}),

		Entry("FTT=0 GMDR=1 (2D)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 1}),
		Entry("FTT=0 GMDR=1 (2D, 1att)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 1, Attached: 1}),

		Entry("FTT=1 GMDR=0 (2D+1TB)",
			SpecTimeout(2*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 0}),
		Entry("FTT=1 GMDR=0 (2D+1TB, 1att)",
			SpecTimeout(2*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 0, Attached: 1}),

		Entry("FTT=1 GMDR=1 (3D)",
			SpecTimeout(3*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 1}),
		Entry("FTT=1 GMDR=1 (3D, 1att)",
			SpecTimeout(3*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 1, Attached: 1}),
	)
})
