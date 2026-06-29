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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

var _ = DescribeTable("computeCorrectQuorum",
	func(voters int, configGMDR, currentQMR byte, upToDate int, expectedQ, expectedQMR byte) {
		gctx := &globalContext{}
		gctx.configuration.GuaranteedMinimumDataRedundancy = configGMDR
		gctx.datamesh.quorumMinimumRedundancy = currentQMR

		// Build replica contexts with voters and UpToDate RVRs.
		gctx.allReplicas = make([]ReplicaContext, voters)
		for i := 0; i < voters; i++ {
			gctx.allReplicas[i].gctx = gctx
			gctx.allReplicas[i].member = &v1alpha1.DatameshMember{
				Type: v1alpha1.DatameshMemberTypeDiskful,
			}
			if i < upToDate {
				gctx.allReplicas[i].rvr = &v1alpha1.ReplicatedVolumeReplica{
					Status: v1alpha1.ReplicatedVolumeReplicaStatus{
						BackingVolume: &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
							State: v1alpha1.DiskStateUpToDate,
						},
					},
				}
			}
		}

		q, qmr := computeCorrectQuorum(gctx)
		Expect(q).To(Equal(expectedQ), "q")
		Expect(qmr).To(Equal(expectedQMR), "qmr")
	},
	Entry("1 voter, GMDR=0", 1, byte(0), byte(1), 1, byte(1), byte(1)),
	Entry("3 voters, GMDR=0", 3, byte(0), byte(1), 3, byte(2), byte(1)),
	Entry("4 voters, GMDR=0", 4, byte(0), byte(1), 4, byte(3), byte(1)),
	Entry("qmr lowering: qmr=3 → 1", 3, byte(0), byte(3), 3, byte(2), byte(1)),
	Entry("qmr full raise: 2 UpToDate ≥ target 2", 2, byte(1), byte(1), 2, byte(2), byte(2)),
	Entry("qmr partial raise: 2 UpToDate < target 3, raise to 2", 3, byte(2), byte(1), 2, byte(2), byte(2)),
	Entry("qmr raise blocked: 0 UpToDate", 1, byte(1), byte(1), 0, byte(1), byte(1)),
	Entry("qmr already correct", 3, byte(1), byte(2), 3, byte(2), byte(2)),
)
