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
	"math/rand"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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
		func(ctx SpecContext, ftt, gmdr byte, expectedReplicas int, wantPrimary bool) {
			replicas := createPreexistingDRBD(ctx, ftt, gmdr)
			Expect(replicas).To(HaveLen(expectedReplicas))

			primaryIdx := -1
			if wantPrimary && len(replicas) > 0 {
				primaryIdx = rand.Intn(len(replicas))
				res := f.Drbdsetup(ctx, replicas[primaryIdx].NodeName, "primary", replicas[primaryIdx].DRBDName)
				Expect(res.ExitCode).To(Equal(0))
			}

			rvName := f.UniqueName()

			llvs := make([]*fw.TestLLV, len(replicas))
			for i, r := range replicas {
				tllv := f.TestLLV()
				b := tllv.ActualLVName(r.ActualLVName).
					LVMVolumeGroupName(r.LVGName).
					Type("Thin").
					Size(r.Size)
				if r.ThinPoolName != "" {
					b = b.ThinPoolName(r.ThinPoolName)
				}
				b.Create(ctx)
				llvs[i] = tllv
			}

			drbdrs := make([]*fw.TestDRBDR, len(replicas))
			for i, r := range replicas {
				rvrName := v1alpha1.FormatReplicatedVolumeReplicaName(rvName, r.NodeID)
				b := f.TestDRBDRExact(rvrName).
					Node(r.NodeName).
					Type(v1alpha1.DRBDResourceTypeDiskful).
					Size(r.Size).
					LVMLogicalVolumeName(llvs[i].Name()).
					SystemNetworks("Internal").
					NodeID(r.NodeID).
					ActualNameOnTheNode(r.DRBDName).
					Maintenance(v1alpha1.MaintenanceModeNoResourceReconciliation)
				if wantPrimary && i == primaryIdx {
					b = b.Role(v1alpha1.DRBDRolePrimary)
				}
				b.Create(ctx)
				drbdrs[i] = b
			}

			for _, td := range drbdrs {
				td.Await(ctx, And(
					DRBDR.HasAddresses(),
					DRBDR.DiskState(v1alpha1.DiskStateUpToDate)))
			}

			swUpToDate := NewSwitch(DRBDR.DiskState(v1alpha1.DiskStateUpToDate))
			for _, td := range drbdrs {
				td.Always(swUpToDate)
			}
			var swPrimaryDevice *Switch
			if wantPrimary {
				swPrimaryDevice = NewSwitch(And(DRBDR.HasDevice(), Not(DRBDR.IOSuspended())))
				drbdrs[primaryIdx].Always(swPrimaryDevice)
			}

			swRVRQuorum := NewSwitch(RVR.NeverLoseQuorum())
			for i, r := range replicas {
				trvr := f.TestRVRExact(rvName, r.NodeID).
					Node(r.NodeName).
					Type(v1alpha1.ReplicaTypeDiskful).
					LVG(r.LVGName)
				if r.ThinPoolName != "" {
					trvr = trvr.ThinPool(r.ThinPoolName)
				}
				trvr.Create(ctx)
				trvr.Always(swRVRQuorum)

				// Randomly set ownerRefs to exercise the controller's ability to
				// adopt pre-existing DRBDRs and LLVs that may or may not already
				// have the correct ownerRef. The controller must set ownerRef
				// on any child that is missing it.
				rvrObj := trvr.Object()
				if rand.Intn(2) == 0 {
					drbdrs[i].Update(ctx, func(d *v1alpha1.DRBDResource) {
						Expect(controllerutil.SetControllerReference(rvrObj, d, f.Scheme)).To(Succeed())
					})
				}
				if rand.Intn(2) == 0 {
					llvs[i].Update(ctx, func(llv *snc.LVMLogicalVolume) {
						Expect(controllerutil.SetControllerReference(rvrObj, llv, f.Scheme)).To(Succeed())
					})
				}
			}

			trv := f.TestRVExact(rvName).Adopt().FTT(ftt).GMDR(gmdr)
			trv.Create(ctx)

			trv.Await(ctx, RV.Members(expectedReplicas))

			for _, td := range drbdrs {
				td.Await(ctx, And(
					DRBDR.PeersMatchSpec(),
					DRBDR.RoleMatchesSpec(),
					DRBDR.LVMMatchesSpec(),
					DRBDR.QuorumMatchesSpec(),
				))
			}

			for _, td := range drbdrs {
				td.Update(ctx, func(d *v1alpha1.DRBDResource) {
					d.Spec.Maintenance = ""
				})
			}

			trv.Await(ctx, RV.FormationComplete())

			swUpToDate.Disable()
			if swPrimaryDevice != nil {
				swPrimaryDevice.Disable()
			}
			swRVRQuorum.Disable()

			Expect(trv.RVRCount()).To(Equal(expectedReplicas))
			for i := 0; i < expectedReplicas; i++ {
				trvr := trv.TestRVR(i)
				trvr.AwaitSequence(ctx,
					Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseConfiguring)),
					Or(Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseProgressing)),
						Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy))),
				)
			}
			rv := trv.Object()
			Expect(rv.Status.Datamesh.Members).To(HaveLen(expectedReplicas))
		},

		Entry("FTT=0 GMDR=0 secondary (1 replica)",
			byte(0), byte(0), 1, false),
		Entry("FTT=0 GMDR=0 primary (1 replica)",
			SpecTimeout(2*time.Minute),
			byte(0), byte(0), 1, true),

		Entry("FTT=0 GMDR=1 secondary (2 replicas)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			byte(0), byte(1), 2, false),
		Entry("FTT=0 GMDR=1 primary (2 replicas)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			byte(0), byte(1), 2, true),

		Entry("FTT=1 GMDR=0 secondary (2 replicas)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			byte(1), byte(0), 2, false),
		Entry("FTT=1 GMDR=0 primary (2 replicas)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			byte(1), byte(0), 2, true),

		Entry("FTT=1 GMDR=1 secondary (3 replicas)",
			SpecTimeout(3*time.Minute), require.MinNodes(3),
			byte(1), byte(1), 3, false),
		Entry("FTT=1 GMDR=1 primary (3 replicas)",
			SpecTimeout(3*time.Minute), require.MinNodes(3),
			byte(1), byte(1), 3, true),
	)

	DescribeTable("adopts preexisting replicas in random order and completes formation",
		func(ctx SpecContext, ftt, gmdr byte, expectedReplicas int, wantPrimary bool) {
			replicas := createPreexistingDRBD(ctx, ftt, gmdr)
			Expect(replicas).To(HaveLen(expectedReplicas))

			primaryIdx := -1
			if wantPrimary && len(replicas) > 0 {
				primaryIdx = rand.Intn(len(replicas))
				res := f.Drbdsetup(ctx, replicas[primaryIdx].NodeName, "primary", replicas[primaryIdx].DRBDName)
				Expect(res.ExitCode).To(Equal(0))
			}

			rvName := f.UniqueName()

			// Pre-build all objects for all replicas (no Create yet).
			llvs := make([]*fw.TestLLV, len(replicas))
			drbdrs := make([]*fw.TestDRBDR, len(replicas))
			trvrs := make([]*fw.TestRVR, len(replicas))

			for i, r := range replicas {
				tllv := f.TestLLV()
				llvB := tllv.ActualLVName(r.ActualLVName).
					LVMVolumeGroupName(r.LVGName).
					Type("Thin").
					Size(r.Size)
				if r.ThinPoolName != "" {
					llvB = llvB.ThinPoolName(r.ThinPoolName)
				}
				_ = llvB // spec configured, Create deferred
				llvs[i] = tllv

				rvrName := v1alpha1.FormatReplicatedVolumeReplicaName(rvName, r.NodeID)
				drbdrB := f.TestDRBDRExact(rvrName).
					Node(r.NodeName).
					Type(v1alpha1.DRBDResourceTypeDiskful).
					Size(r.Size).
					LVMLogicalVolumeName(tllv.Name()).
					SystemNetworks("Internal").
					NodeID(r.NodeID).
					ActualNameOnTheNode(r.DRBDName).
					Maintenance(v1alpha1.MaintenanceModeNoResourceReconciliation)
				if wantPrimary && i == primaryIdx {
					drbdrB = drbdrB.Role(v1alpha1.DRBDRolePrimary)
				}
				_ = drbdrB // spec configured, Create deferred
				drbdrs[i] = drbdrB

				trvr := f.TestRVRExact(rvName, r.NodeID).
					Node(r.NodeName).
					Type(v1alpha1.ReplicaTypeDiskful).
					LVG(r.LVGName)
				if r.ThinPoolName != "" {
					trvr = trvr.ThinPool(r.ThinPoolName)
				}
				trvrs[i] = trvr
			}

			// Create switches before shuffle (captured by closures).
			swUpToDate := NewSwitch(DRBDR.DiskState(v1alpha1.DiskStateUpToDate))
			var swPrimaryDevice *Switch
			if wantPrimary {
				swPrimaryDevice = NewSwitch(And(DRBDR.HasDevice(), Not(DRBDR.IOSuspended())))
			}
			swRVRQuorum := NewSwitch(RVR.NeverLoseQuorum())

			// Track creation state per replica for contextual ownerRef and Await logic.
			type replicaState struct{ llv, drbdr, rvr, upToDateDone bool }
			st := make([]replicaState, len(replicas))

			// Helper: await DiskState UpToDate and register Always switches for replica i.
			awaitUpToDate := func(i int) {
				drbdrs[i].Await(ctx, DRBDR.DiskState(v1alpha1.DiskStateUpToDate))
				drbdrs[i].Always(swUpToDate)
				if wantPrimary && i == primaryIdx {
					drbdrs[i].Always(swPrimaryDevice)
				}
				st[i].upToDateDone = true
			}

			// Build a flat action list: 3 actions per replica, shuffled globally.
			actions := make([]func(), 0, 3*len(replicas))
			for i := range replicas {
				// LLV action.
				actions = append(actions, func() {
					llvs[i].Create(ctx)
					st[i].llv = true
					if st[i].rvr && rand.Intn(2) == 0 {
						rvrObj := trvrs[i].Object()
						llvs[i].Update(ctx, func(llv *snc.LVMLogicalVolume) {
							Expect(controllerutil.SetControllerReference(rvrObj, llv, f.Scheme)).To(Succeed())
						})
					}
					// LLV created after DRBDR: now DRBDR can reach UpToDate.
					if st[i].drbdr && !st[i].upToDateDone {
						awaitUpToDate(i)
					}
				})

				// DRBDR action.
				actions = append(actions, func() {
					drbdrs[i].Create(ctx)
					drbdrs[i].Await(ctx, DRBDR.HasAddresses())
					st[i].drbdr = true
					if st[i].rvr && rand.Intn(2) == 0 {
						rvrObj := trvrs[i].Object()
						drbdrs[i].Update(ctx, func(d *v1alpha1.DRBDResource) {
							Expect(controllerutil.SetControllerReference(rvrObj, d, f.Scheme)).To(Succeed())
						})
					}
					// DRBDR created after LLV: can reach UpToDate immediately.
					if st[i].llv && !st[i].upToDateDone {
						awaitUpToDate(i)
					}
				})

				// RVR action.
				actions = append(actions, func() {
					trvrs[i].Create(ctx)
					trvrs[i].Always(swRVRQuorum)
					st[i].rvr = true
					rvrObj := trvrs[i].Object()
					if st[i].drbdr && rand.Intn(2) == 0 {
						drbdrs[i].Update(ctx, func(d *v1alpha1.DRBDResource) {
							Expect(controllerutil.SetControllerReference(rvrObj, d, f.Scheme)).To(Succeed())
						})
					}
					if st[i].llv && rand.Intn(2) == 0 {
						llvs[i].Update(ctx, func(llv *snc.LVMLogicalVolume) {
							Expect(controllerutil.SetControllerReference(rvrObj, llv, f.Scheme)).To(Succeed())
						})
					}
				})
			}

			rand.Shuffle(len(actions), func(a, b int) { actions[a], actions[b] = actions[b], actions[a] })
			for _, action := range actions {
				action()
			}

			// Finish any replicas where both DRBDR and LLV exist but UpToDate was not yet awaited.
			for i := range replicas {
				if !st[i].upToDateDone {
					awaitUpToDate(i)
				}
			}

			trv := f.TestRVExact(rvName).Adopt().FTT(ftt).GMDR(gmdr)
			trv.Create(ctx)

			trv.Await(ctx, RV.Members(expectedReplicas))

			for _, td := range drbdrs {
				td.Await(ctx, And(
					DRBDR.PeersMatchSpec(),
					DRBDR.RoleMatchesSpec(),
					DRBDR.LVMMatchesSpec(),
					DRBDR.QuorumMatchesSpec(),
				))
			}

			for _, td := range drbdrs {
				td.Update(ctx, func(d *v1alpha1.DRBDResource) {
					d.Spec.Maintenance = ""
				})
			}

			trv.Await(ctx, RV.FormationComplete())

			swUpToDate.Disable()
			if swPrimaryDevice != nil {
				swPrimaryDevice.Disable()
			}
			swRVRQuorum.Disable()

			Expect(trv.RVRCount()).To(Equal(expectedReplicas))
			for i := 0; i < expectedReplicas; i++ {
				trvr := trv.TestRVR(i)
				trvr.AwaitSequence(ctx,
					Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseConfiguring)),
					Or(Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseProgressing)),
						Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy))),
				)
			}
			rv := trv.Object()
			Expect(rv.Status.Datamesh.Members).To(HaveLen(expectedReplicas))
		},

		Entry("FTT=0 GMDR=0 secondary (1 replica)",
			byte(0), byte(0), 1, false),
		Entry("FTT=0 GMDR=0 primary (1 replica)",
			SpecTimeout(2*time.Minute),
			byte(0), byte(0), 1, true),

		Entry("FTT=0 GMDR=1 secondary (2 replicas)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			byte(0), byte(1), 2, false),
		Entry("FTT=0 GMDR=1 primary (2 replicas)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			byte(0), byte(1), 2, true),

		Entry("FTT=1 GMDR=0 secondary (2 replicas)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			byte(1), byte(0), 2, false),
		Entry("FTT=1 GMDR=0 primary (2 replicas)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			byte(1), byte(0), 2, true),

		Entry("FTT=1 GMDR=1 secondary (3 replicas)",
			SpecTimeout(3*time.Minute), require.MinNodes(3),
			byte(1), byte(1), 3, false),
		Entry("FTT=1 GMDR=1 primary (3 replicas)",
			SpecTimeout(3*time.Minute), require.MinNodes(3),
			byte(1), byte(1), 3, true),
	)
})
