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

package controller_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-common-lib/logger"
	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/oldcontroller/pkg/controller"
)

var _ = Describe("StorageClass quorum parameters", func() {
	newRSC := func(name, replication string) *srv.ReplicatedStorageClass {
		return &srv.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: srv.ReplicatedStorageClassSpec{
				StoragePool:   "pool",
				ReclaimPolicy: controller.ReclaimPolicyDelete,
				Replication:   srv.ReplicatedStorageClassReplication(replication),
				VolumeAccess:  controller.VolumeAccessPreferablyLocal,
				Topology:      controller.TopologyIgnored,
			},
		}
	}

	DescribeTable("renders quorum parameters by replication mode",
		func(replication string, wantQuorumParams bool) {
			sc := controller.GenerateStorageClassFromReplicatedStorageClass(newRSC("rsc", replication), nil)

			autoQuorum, hasAutoQuorum := sc.Parameters[controller.StorageClassParamAutoQuorumKey]
			tieBreaker, hasTieBreaker := sc.Parameters[controller.StorageClassParamAutoAddQuorumTieBreakerKey]
			onNoQuorum, hasOnNoQuorum := sc.Parameters[controller.StorageClassParamOnNoQuorumKey]

			if wantQuorumParams {
				Expect(hasAutoQuorum).To(BeTrue())
				Expect(autoQuorum).To(Equal(controller.SuspendIo))
				Expect(tieBreaker).To(Equal("true"))
				Expect(onNoQuorum).To(Equal(controller.SuspendIo))
				return
			}

			Expect(hasAutoQuorum).To(BeTrue())
			Expect(autoQuorum).To(Equal(controller.AutoQuorumDisabled))
			Expect(hasOnNoQuorum).To(BeFalse())
			Expect(hasTieBreaker).To(BeTrue())
			Expect(tieBreaker).To(Equal("false"))
		},
		Entry("None", controller.ReplicationNone, false),
		Entry("Availability", controller.ReplicationAvailability, true),
		Entry("ConsistencyAndAvailability", controller.ReplicationConsistencyAndAvailability, true),
		Entry("empty defaults to ConsistencyAndAvailability", "", true),
	)

	It("replaces a single-replica StorageClass created with the quorum parameters", func() {
		cl := newFakeClientWithStatusSubresource()
		rsc := newRSC("rsc-r1", controller.ReplicationNone)

		oldSC := controller.GenerateStorageClassFromReplicatedStorageClass(rsc, nil)
		oldSC.Parameters[controller.StorageClassParamAutoQuorumKey] = controller.SuspendIo
		oldSC.Parameters[controller.StorageClassParamAutoAddQuorumTieBreakerKey] = "true"
		oldSC.Parameters[controller.StorageClassParamOnNoQuorumKey] = controller.SuspendIo
		Expect(controller.CreateStorageClass(context.Background(), cl, oldSC)).To(Succeed())

		current, err := controller.GetStorageClass(context.Background(), cl, rsc.Name)
		Expect(err).NotTo(HaveOccurred())

		newSC := controller.GetNewStorageClass(rsc, false, nil, srv.ReplicatedStoragePoolTypeLVMThin)
		_, err = controller.UpdateStorageClassIfNeeded(context.Background(), cl, logger.NewNop(), newSC, current, nil)
		Expect(err).NotTo(HaveOccurred())

		got, err := controller.GetStorageClass(context.Background(), cl, rsc.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Parameters).To(HaveKeyWithValue(controller.StorageClassParamAutoQuorumKey, controller.AutoQuorumDisabled))
		Expect(got.Parameters).NotTo(HaveKey(controller.StorageClassParamOnNoQuorumKey))
		Expect(got.Parameters).To(HaveKeyWithValue(controller.StorageClassParamAutoAddQuorumTieBreakerKey, "false"))
	})

	It("still refuses to replace a StorageClass whose placement count differs", func() {
		cl := newFakeClientWithStatusSubresource()
		oldSC := controller.GenerateStorageClassFromReplicatedStorageClass(newRSC("rsc-x", controller.ReplicationAvailability), nil)
		Expect(controller.CreateStorageClass(context.Background(), cl, oldSC)).To(Succeed())
		current, err := controller.GetStorageClass(context.Background(), cl, "rsc-x")
		Expect(err).NotTo(HaveOccurred())

		newSC := controller.GetNewStorageClass(newRSC("rsc-x", controller.ReplicationNone), false, nil, srv.ReplicatedStoragePoolTypeLVMThin)
		_, err = controller.UpdateStorageClassIfNeeded(context.Background(), cl, logger.NewNop(), newSC, current, nil)
		Expect(err).To(HaveOccurred())
	})
})
