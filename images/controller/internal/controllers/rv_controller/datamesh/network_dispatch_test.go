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

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// ──────────────────────────────────────────────────────────────────────────────
// RepairNetworkAddresses dispatch
//

var _ = Describe("RepairNetworkAddresses dispatch", func() {
	It("skip: addresses in sync", func() {
		m := mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")
		m.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}

		rv := mkRV(5, []v1alpha1.DatameshMember{m}, nil, nil)
		rv.Status.Datamesh.SystemNetworkNames = []string{"net-A"}

		rvr := mkRVR("rv-1-0", "node-1", 5)
		rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}

		settleEffectiveLayout(rv, rvrs)

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeFalse())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("dispatch: IP changed", func() {
		m := mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")
		m.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}

		rv := mkRV(5, []v1alpha1.DatameshMember{m}, nil, nil)
		rv.Status.Datamesh.SystemNetworkNames = []string{"net-A"}

		rvr := mkRVR("rv-1-0", "node-1", 5)
		rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.99", 7000)}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeRepairNetworkAddresses))
		Expect(t.PlanID).To(Equal("repair/v1"))
	})

	It("dispatch: missing network in member", func() {
		m := mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")
		m.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}

		rv := mkRV(5, []v1alpha1.DatameshMember{m}, nil, nil)
		rv.Status.Datamesh.SystemNetworkNames = []string{"net-A", "net-B"}

		rvr := mkRVR("rv-1-0", "node-1", 5)
		rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.1", 7000),
			mkAddr("net-B", "10.0.0.2", 7000),
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeRepairNetworkAddresses))
	})

	It("dispatch: stale network in member", func() {
		m := mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")
		m.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.1", 7000),
			mkAddr("net-B", "10.0.0.2", 7000),
		}

		rv := mkRV(5, []v1alpha1.DatameshMember{m}, nil, nil)
		rv.Status.Datamesh.SystemNetworkNames = []string{"net-A"}

		rvr := mkRVR("rv-1-0", "node-1", 5)
		rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeRepairNetworkAddresses))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// ChangeSystemNetworks dispatch
//

var _ = Describe("ChangeSystemNetworks dispatch", func() {
	// csnSetup creates a stable 1-member RV+RVR on network "net-A" with
	// addresses in sync (so repair doesn't fire). Returns rv, rvrs ready for
	// CSN dispatch tests. Caller sets RSP systemNetworkNames to create a diff.
	csnSetup := func() (*v1alpha1.ReplicatedVolume, []*v1alpha1.ReplicatedVolumeReplica) {
		m := mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")
		m.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}

		rv := mkRV(5, []v1alpha1.DatameshMember{m}, nil, nil)
		rv.Status.Datamesh.SystemNetworkNames = []string{"net-A"}

		rvr := mkRVR("rv-1-0", "node-1", 5)
		rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}

		return rv, rvrs
	}

	It("skip: RSP nil", func() {
		rv, rvrs := csnSetup()
		settleEffectiveLayout(rv, rvrs)

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeFalse())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("skip: networks in sync", func() {
		rv, rvrs := csnSetup()
		rsp := mkRSP("node-1")
		rsp.systemNetworkNames = []string{"net-A"}
		settleEffectiveLayout(rv, rvrs)

		changed, _ := ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeFalse())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("dispatch: add/v1 — new network added", func() {
		rv, rvrs := csnSetup()
		rsp := mkRSP("node-1")
		rsp.systemNetworkNames = []string{"net-A", "net-B"}

		changed, _ := ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeSystemNetworks))
		Expect(t.PlanID).To(Equal("add/v1"))
	})

	It("dispatch: remove/v1 — network removed", func() {
		// Datamesh has [net-A, net-B], RSP wants only [net-A].
		rv, rvrs := csnSetup()
		rv.Status.Datamesh.SystemNetworkNames = []string{"net-A", "net-B"}
		rv.Status.Datamesh.Members[0].Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.1", 7000),
			mkAddr("net-B", "10.0.0.2", 7000),
		}
		rvrs[0].Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.1", 7000),
			mkAddr("net-B", "10.0.0.2", 7000),
		}
		rsp := mkRSP("node-1")
		rsp.systemNetworkNames = []string{"net-A"}

		changed, _ := ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeSystemNetworks))
		Expect(t.PlanID).To(Equal("remove/v1"))
	})

	It("dispatch: update/v1 — add + remove with remaining", func() {
		// Datamesh has [net-A, net-B], RSP wants [net-A, net-C].
		// net-A remains, net-B removed, net-C added.
		rv, rvrs := csnSetup()
		rv.Status.Datamesh.SystemNetworkNames = []string{"net-A", "net-B"}
		rv.Status.Datamesh.Members[0].Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.1", 7000),
			mkAddr("net-B", "10.0.0.2", 7000),
		}
		rvrs[0].Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.1", 7000),
			mkAddr("net-B", "10.0.0.2", 7000),
		}
		rsp := mkRSP("node-1")
		rsp.systemNetworkNames = []string{"net-A", "net-C"}

		changed, _ := ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeSystemNetworks))
		Expect(t.PlanID).To(Equal("update/v1"))
	})

	It("dispatch: migrate/v1 — full replacement, no remaining", func() {
		// Datamesh has [net-A], RSP wants [net-B]. No intersection.
		rv, rvrs := csnSetup()
		rsp := mkRSP("node-1")
		rsp.systemNetworkNames = []string{"net-B"}

		changed, _ := ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeSystemNetworks))
		Expect(t.PlanID).To(Equal("migrate/v1"))
	})

	It("priority: repair dispatched instead of CSN", func() {
		// Addresses out of sync AND networks differ — repair takes priority.
		rv, rvrs := csnSetup()
		// Make addresses mismatch (repair trigger).
		rv.Status.Datamesh.Members[0].Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.1", 7000),
		}
		rvrs[0].Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.99", 7000), // different IP
		}
		// Also make networks differ (CSN trigger).
		rsp := mkRSP("node-1")
		rsp.systemNetworkNames = []string{"net-A", "net-B"}

		changed, _ := ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		// Repair takes priority over CSN.
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeRepairNetworkAddresses))
		Expect(t.PlanID).To(Equal("repair/v1"))
	})
})
