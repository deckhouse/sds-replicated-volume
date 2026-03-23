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
	. "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

// adoptSetupResult holds state produced by setupAdoptPreexisting and
// consumed by finishAdopt.
type adoptSetupResult struct {
	RVName        string
	AdoptSecret   string
	RSCName       string
	Replicas      []fw.PreexistingDRBDReplica
	ExpectedCount int
	DRBDRs        []*fw.TestDRBDR

	swUpToDate      *Switch
	swPrimaryDevice *Switch
	swRVRQuorum     *Switch
}

// setupAdoptPreexisting creates a source layout via SetupLayout, tears it
// down via EmulatePreexisting, then recreates the pre-existing state as
// standalone DRBDRs, LLVs, and RVRs — exactly as a real migration would
// look. Returns the source TestRV and an adoptSetupResult for use in
// finishAdopt.
func setupAdoptPreexisting(ctx SpecContext, sourceLayout fw.TestLayout) adoptSetupResult {
	GinkgoHelper()

	sourceRV := f.SetupLayout(ctx, sourceLayout)
	obj := sourceRV.Object()
	adoptSecret := obj.Status.Datamesh.SharedSecret
	rscName := obj.Spec.ReplicatedStorageClassName
	replicas := sourceRV.EmulatePreexisting(ctx)
	expectedReplicas := sourceLayout.ExpectedReplicas()
	Expect(replicas).To(HaveLen(expectedReplicas))

	rvName := f.UniqueName()

	var drbdrs []*fw.TestDRBDR
	var primaryDRBDR *fw.TestDRBDR
	llvByReplicaIdx := make(map[int]*fw.TestLLV)

	for i, r := range replicas {
		rvrName := v1alpha1.FormatReplicatedVolumeReplicaName(rvName, r.NodeID)

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
			llvB.Create(ctx)
			llvByReplicaIdx[i] = tllv
		case v1alpha1.ReplicaTypeTieBreaker, v1alpha1.ReplicaTypeAccess:
			drbdType = v1alpha1.DRBDResourceTypeDiskless
		}

		db := f.TestDRBDRExact(rvrName).
			Node(r.NodeName).
			Type(drbdType).
			SystemNetworks("Internal").
			NodeID(r.NodeID).
			ActualNameOnTheNode(r.DRBDName).
			Maintenance(v1alpha1.MaintenanceModeNoResourceReconciliation)
		if drbdType == v1alpha1.DRBDResourceTypeDiskful {
			db = db.Size(r.Size).LVMLogicalVolumeName(tllv.Name())
		}
		if r.Role == v1alpha1.DRBDRolePrimary {
			db = db.Role(v1alpha1.DRBDRolePrimary)
		}
		db.Create(ctx)

		drbdrs = append(drbdrs, db)

		if r.Role == v1alpha1.DRBDRolePrimary {
			primaryDRBDR = db
		}
	}

	DeferCleanup(func(cleanupCtx SpecContext) {
		patch := client.RawPatch(types.MergePatchType, []byte(`{"spec":{"maintenance":""}}`))
		for _, td := range drbdrs {
			obj := &v1alpha1.DRBDResource{ObjectMeta: metav1.ObjectMeta{Name: td.Name()}}
			_ = client.IgnoreNotFound(f.Client.Patch(cleanupCtx, obj, patch))
		}
	})

	for _, td := range drbdrs {
		td.Await(ctx, DRBDR.HasAddresses())
	}
	multiReplica := len(replicas) > 1
	for i, td := range drbdrs {
		assertAddressesAdopted(td.Object().Status.Addresses, replicas[i].Addresses,
			multiReplica, td.Name())
	}
	for _, td := range drbdrs {
		obj := td.Object()
		if obj.Spec.Type == v1alpha1.DRBDResourceTypeDiskful {
			td.Await(ctx, DRBDR.DiskState(v1alpha1.DiskStateUpToDate))
		}
	}

	swUpToDate := NewSwitch(DRBDR.DiskState(v1alpha1.DiskStateUpToDate))
	for _, td := range drbdrs {
		obj := td.Object()
		if obj.Spec.Type == v1alpha1.DRBDResourceTypeDiskful {
			td.Always(swUpToDate)
		}
	}
	var swPrimaryDevice *Switch
	if primaryDRBDR != nil {
		swPrimaryDevice = NewSwitch(And(DRBDR.HasDevice(), Not(DRBDR.IOSuspended())))
		primaryDRBDR.Always(swPrimaryDevice)
	}

	swRVRQuorum := NewSwitch(RVR.NeverLoseQuorum())
	for i, r := range replicas {
		trvr := f.TestRVRExact(rvName, r.NodeID).
			Node(r.NodeName).
			Type(r.Type)
		if r.Type == v1alpha1.ReplicaTypeDiskful {
			trvr = trvr.LVG(r.LVGName)
			if r.ThinPoolName != "" {
				trvr = trvr.ThinPool(r.ThinPoolName)
			}
		}
		trvr.Create(ctx)
		trvr.Always(swRVRQuorum)

		rvrObj := trvr.Object()
		if rand.Intn(2) == 0 {
			drbdrs[i].Update(ctx, func(d *v1alpha1.DRBDResource) {
				Expect(controllerutil.SetControllerReference(rvrObj, d, f.Scheme)).To(Succeed())
			})
		}
		if tllv := llvByReplicaIdx[i]; tllv != nil && rand.Intn(2) == 0 {
			tllv.Update(ctx, func(llv *snc.LVMLogicalVolume) {
				Expect(controllerutil.SetControllerReference(rvrObj, llv, f.Scheme)).To(Succeed())
			})
		}
	}

	return adoptSetupResult{
		RVName:          rvName,
		AdoptSecret:     adoptSecret,
		RSCName:         rscName,
		Replicas:        replicas,
		ExpectedCount:   expectedReplicas,
		DRBDRs:          drbdrs,
		swUpToDate:      swUpToDate,
		swPrimaryDevice: swPrimaryDevice,
		swRVRQuorum:     swRVRQuorum,
	}
}

// finishAdopt awaits formation completion, exits maintenance on all
// DRBDRs, and verifies the final adopt state (shared secret, RVR count,
// phase sequence, member count).
func finishAdopt(ctx SpecContext, trv *fw.TestRV, res adoptSetupResult) {
	GinkgoHelper()

	trv.Await(ctx, RV.Members(res.ExpectedCount))

	for _, td := range res.DRBDRs {
		obj := td.Object()
		if obj.Spec.Type == v1alpha1.DRBDResourceTypeDiskful {
			td.Await(ctx, And(
				DRBDR.PeersMatchSpec(),
				DRBDR.RoleMatchesSpec(),
				DRBDR.LVMMatchesSpec(),
				DRBDR.QuorumMatchesSpec(),
			))
		}
	}

	trv.Await(ctx, RV.TransitionStepActive(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation, "Exit maintenance"))

	for _, td := range res.DRBDRs {
		td.Update(ctx, func(d *v1alpha1.DRBDResource) {
			d.Spec.Maintenance = ""
		})
	}

	trv.Await(ctx, RV.FormationComplete())

	Expect(trv.Object().Status.Datamesh.SharedSecret).To(Equal(res.AdoptSecret))

	res.swUpToDate.Disable()
	if res.swPrimaryDevice != nil {
		res.swPrimaryDevice.Disable()
	}
	res.swRVRQuorum.Disable()

	// After formation, access replicas without a matching RVA are deleted
	// by normal operation. Collect only replicas expected to survive.
	rvaNodes := trv.RVANodes()
	var surviving []*fw.TestRVR
	for i := 0; i < res.ExpectedCount; i++ {
		trvr := trv.TestRVR(i)
		rvr := trvr.Object()
		if rvr.Spec.Type == v1alpha1.ReplicaTypeAccess && !rvaNodes[rvr.Spec.NodeName] {
			continue
		}
		surviving = append(surviving, trvr)
	}

	trv.Await(ctx, RV.Members(len(surviving)))
	Expect(trv.RVRCount()).To(Equal(len(surviving)))
	for _, trvr := range surviving {
		trvr.AwaitSequence(ctx,
			Phase(string(v1alpha1.ReplicatedVolumeReplicaPhasePending)),
			Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseConfiguring)),
			Or(Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseProgressing)),
				Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy))),
		)
	}
}

// assertAddressesAdopted verifies that actual addresses match expected IPs and
// system networks. For multi-replica resources (where DRBD kernel has
// connections), it also asserts port equality. For standalone resources (0
// connections in kernel), it only asserts that a valid port was allocated —
// the port cannot be adopted because existingPortsFromState has no paths.
func assertAddressesAdopted(
	actual, expected []v1alpha1.DRBDResourceAddressStatus,
	multiReplica bool,
	name string,
) {
	GinkgoHelper()
	Expect(actual).To(HaveLen(len(expected)),
		"DRBDR %s address count mismatch", name)
	for i, a := range actual {
		e := expected[i]
		Expect(a.SystemNetworkName).To(Equal(e.SystemNetworkName),
			"DRBDR %s address[%d] system network must match", name, i)
		Expect(a.Address.IPv4).To(Equal(e.Address.IPv4),
			"DRBDR %s address[%d] IP must match", name, i)
		Expect(a.Address.Port).NotTo(BeZero(),
			"DRBDR %s address[%d] port must be allocated", name, i)
		if multiReplica {
			Expect(a.Address.Port).To(Equal(e.Address.Port),
				"DRBDR %s address[%d] port must match preexisting (multi-replica)", name, i)
		}
	}
}
