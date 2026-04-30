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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/drbd_size"
)

// ──────────────────────────────────────────────────────────────────────────────
// guardHasReadyDiskfulMember
//

var _ = Describe("guardHasReadyDiskfulMember", func() {
	It("passes when a diskful member has Ready=True", func() {
		gctx := &globalContext{
			allReplicas: []ReplicaContext{
				{
					id:   0,
					name: "rv-1-0",
					member: &v1alpha1.DatameshMember{
						Name: "rv-1-0",
						Type: v1alpha1.DatameshMemberTypeDiskful,
					},
					rvr: &v1alpha1.ReplicatedVolumeReplica{
						Status: v1alpha1.ReplicatedVolumeReplicaStatus{
							Conditions: []metav1.Condition{
								{
									Type:   v1alpha1.ReplicatedVolumeReplicaCondReadyType,
									Status: metav1.ConditionTrue,
								},
							},
						},
					},
				},
			},
		}

		result := guardHasReadyDiskfulMember(gctx)
		Expect(result.Blocked).To(BeFalse())
	})

	It("blocks when diskful member has Ready=False", func() {
		gctx := &globalContext{
			allReplicas: []ReplicaContext{
				{
					id:   0,
					name: "rv-1-0",
					member: &v1alpha1.DatameshMember{
						Name: "rv-1-0",
						Type: v1alpha1.DatameshMemberTypeDiskful,
					},
					rvr: &v1alpha1.ReplicatedVolumeReplica{
						Status: v1alpha1.ReplicatedVolumeReplicaStatus{
							Conditions: []metav1.Condition{
								{
									Type:   v1alpha1.ReplicatedVolumeReplicaCondReadyType,
									Status: metav1.ConditionFalse,
								},
							},
						},
					},
				},
			},
		}

		result := guardHasReadyDiskfulMember(gctx)
		Expect(result.Blocked).To(BeTrue())
		Expect(result.Message).To(ContainSubstring("no ready diskful member"))
	})

	It("blocks when no diskful members exist", func() {
		gctx := &globalContext{
			allReplicas: []ReplicaContext{
				{
					id:   0,
					name: "rv-1-0",
					member: &v1alpha1.DatameshMember{
						Name: "rv-1-0",
						Type: v1alpha1.DatameshMemberTypeAccess,
					},
					rvr: &v1alpha1.ReplicatedVolumeReplica{},
				},
			},
		}

		result := guardHasReadyDiskfulMember(gctx)
		Expect(result.Blocked).To(BeTrue())
	})

	It("blocks when no members", func() {
		gctx := &globalContext{}
		result := guardHasReadyDiskfulMember(gctx)
		Expect(result.Blocked).To(BeTrue())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// guardNoActiveResync
//

var _ = Describe("guardNoActiveResync", func() {
	It("passes when all peers are Established", func() {
		gctx := &globalContext{
			allReplicas: []ReplicaContext{
				{
					id:   0,
					name: "rv-1-0",
					member: &v1alpha1.DatameshMember{
						Name: "rv-1-0",
						Type: v1alpha1.DatameshMemberTypeDiskful,
					},
					rvr: &v1alpha1.ReplicatedVolumeReplica{
						Status: v1alpha1.ReplicatedVolumeReplicaStatus{
							Peers: []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
								{Name: "rv-1-1", ReplicationState: v1alpha1.ReplicationStateEstablished},
							},
						},
					},
				},
			},
		}

		result := guardNoActiveResync(gctx)
		Expect(result.Blocked).To(BeFalse())
	})

	It("blocks when a peer is SyncTarget", func() {
		gctx := &globalContext{
			allReplicas: []ReplicaContext{
				{
					id:   0,
					name: "rv-1-0",
					member: &v1alpha1.DatameshMember{
						Name: "rv-1-0",
						Type: v1alpha1.DatameshMemberTypeDiskful,
					},
					rvr: &v1alpha1.ReplicatedVolumeReplica{
						Status: v1alpha1.ReplicatedVolumeReplicaStatus{
							Peers: []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
								{Name: "rv-1-1", ReplicationState: v1alpha1.ReplicationStateSyncTarget},
							},
						},
					},
				},
			},
		}

		result := guardNoActiveResync(gctx)
		Expect(result.Blocked).To(BeTrue())
		Expect(result.Message).To(ContainSubstring("replica rv-1-0 has peer"))
		Expect(result.Message).To(ContainSubstring("SyncTarget"))
	})

	It("skips diskless members", func() {
		gctx := &globalContext{
			allReplicas: []ReplicaContext{
				{
					id:   0,
					name: "rv-1-0",
					member: &v1alpha1.DatameshMember{
						Name: "rv-1-0",
						Type: v1alpha1.DatameshMemberTypeAccess,
					},
					rvr: &v1alpha1.ReplicatedVolumeReplica{
						Status: v1alpha1.ReplicatedVolumeReplicaStatus{
							Peers: []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
								{Name: "rv-1-1", ReplicationState: v1alpha1.ReplicationStateSyncTarget},
							},
						},
					},
				},
			},
		}

		result := guardNoActiveResync(gctx)
		Expect(result.Blocked).To(BeFalse())
	})

	It("passes when no members", func() {
		gctx := &globalContext{}
		result := guardNoActiveResync(gctx)
		Expect(result.Blocked).To(BeFalse())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// guardBackingVolumesGrown
//

var _ = Describe("guardBackingVolumesGrown", func() {
	targetUsable := resource.MustParse("10Gi")
	targetLower := drbd_size.LowerVolumeSize(targetUsable)

	It("passes when all backing volumes are large enough", func() {
		gctx := &globalContext{
			size: targetUsable,
			allReplicas: []ReplicaContext{
				{
					id:   0,
					name: "rv-1-0",
					member: &v1alpha1.DatameshMember{
						Name: "rv-1-0",
						Type: v1alpha1.DatameshMemberTypeDiskful,
					},
					rvr: &v1alpha1.ReplicatedVolumeReplica{
						Status: v1alpha1.ReplicatedVolumeReplicaStatus{
							BackingVolume: &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
								Size: &targetLower,
							},
						},
					},
				},
			},
		}

		result := guardBackingVolumesGrown(gctx)
		Expect(result.Blocked).To(BeFalse())
	})

	It("blocks when backing volume is too small", func() {
		smallSize := resource.MustParse("5Gi")
		gctx := &globalContext{
			size: targetUsable,
			allReplicas: []ReplicaContext{
				{
					id:   0,
					name: "rv-1-0",
					member: &v1alpha1.DatameshMember{
						Name: "rv-1-0",
						Type: v1alpha1.DatameshMemberTypeDiskful,
					},
					rvr: &v1alpha1.ReplicatedVolumeReplica{
						Status: v1alpha1.ReplicatedVolumeReplicaStatus{
							BackingVolume: &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
								Size: &smallSize,
							},
						},
					},
				},
			},
		}

		result := guardBackingVolumesGrown(gctx)
		Expect(result.Blocked).To(BeTrue())
		Expect(result.Message).To(ContainSubstring("not grown yet"))
	})

	It("blocks when backing volume size is nil", func() {
		gctx := &globalContext{
			size: targetUsable,
			allReplicas: []ReplicaContext{
				{
					id:   0,
					name: "rv-1-0",
					member: &v1alpha1.DatameshMember{
						Name: "rv-1-0",
						Type: v1alpha1.DatameshMemberTypeDiskful,
					},
					rvr: &v1alpha1.ReplicatedVolumeReplica{
						Status: v1alpha1.ReplicatedVolumeReplicaStatus{
							BackingVolume: &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
								Size: nil,
							},
						},
					},
				},
			},
		}

		result := guardBackingVolumesGrown(gctx)
		Expect(result.Blocked).To(BeTrue())
		Expect(result.Message).To(ContainSubstring("not yet reported"))
	})

	It("blocks when backing volume is nil", func() {
		gctx := &globalContext{
			size: targetUsable,
			allReplicas: []ReplicaContext{
				{
					id:   0,
					name: "rv-1-0",
					member: &v1alpha1.DatameshMember{
						Name: "rv-1-0",
						Type: v1alpha1.DatameshMemberTypeDiskful,
					},
					rvr: &v1alpha1.ReplicatedVolumeReplica{},
				},
			},
		}

		result := guardBackingVolumesGrown(gctx)
		Expect(result.Blocked).To(BeTrue())
		Expect(result.Message).To(ContainSubstring("not yet reported"))
	})

	It("skips diskless members", func() {
		gctx := &globalContext{
			size: targetUsable,
			allReplicas: []ReplicaContext{
				{
					id:   0,
					name: "rv-1-0",
					member: &v1alpha1.DatameshMember{
						Name: "rv-1-0",
						Type: v1alpha1.DatameshMemberTypeAccess,
					},
					rvr: &v1alpha1.ReplicatedVolumeReplica{},
				},
			},
		}

		result := guardBackingVolumesGrown(gctx)
		Expect(result.Blocked).To(BeFalse())
	})

	It("passes when no members", func() {
		gctx := &globalContext{size: targetUsable}
		result := guardBackingVolumesGrown(gctx)
		Expect(result.Blocked).To(BeFalse())
	})
})
