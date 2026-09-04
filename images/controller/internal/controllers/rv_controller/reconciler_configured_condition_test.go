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

package rvcontroller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// newConfiguredTestRV builds a formed RV at datamesh revision 7 with the given members, so the
// entries only have to describe what the replicas report back.
func newConfiguredTestRV(members ...v1alpha1.DatameshMember) *v1alpha1.ReplicatedVolume {
	return &v1alpha1.ReplicatedVolume{
		Status: v1alpha1.ReplicatedVolumeStatus{
			DatameshRevision: 7,
			Configuration:    &v1alpha1.ReplicatedVolumeConfiguration{},
			Datamesh:         v1alpha1.ReplicatedVolumeDatamesh{Members: members},
		},
	}
}

// configuredTestMember builds a datamesh member of the given type; only the name is read by
// computeConfiguredReport, the type is there to pin that every member type counts.
func configuredTestMember(name string, typ v1alpha1.DatameshMemberType) v1alpha1.DatameshMember {
	return v1alpha1.DatameshMember{Name: name, Type: typ, NodeName: "n-" + name}
}

// newTestRVRAtRevision builds an RVR reporting the given applied datamesh revision, which is the
// single input computeConfiguredReport reads from a replica.
func newTestRVRAtRevision(name string, revision int64) *v1alpha1.ReplicatedVolumeReplica {
	return &v1alpha1.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     v1alpha1.ReplicatedVolumeReplicaStatus{DatameshRevision: revision},
	}
}

var _ = Describe("Configured condition", func() {
	Describe("computeConfiguredReport", func() {
		DescribeTable("leaves the condition untouched",
			func(rv *v1alpha1.ReplicatedVolume, rvrs []*v1alpha1.ReplicatedVolumeReplica) {
				// The zero value, not merely present=false: a report that must not be applied
				// must not carry a status or a reason either.
				Expect(computeConfiguredReport(rv, rvrs)).To(Equal(configuredReport{}))
			},

			// Rule 1: deletion freezes the condition, like Resilient and AttachedReady.
			Entry("deletion timestamp set",
				func() *v1alpha1.ReplicatedVolume {
					rv := newConfiguredTestRV(configuredTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful))
					rv.DeletionTimestamp = ptr.To(metav1.Now())
					return rv
				}(),
				[]*v1alpha1.ReplicatedVolumeReplica{newTestRVRAtRevision("rv-0", 7)}),

			// Rule 2: absent = not yet evaluated, matching AttachedReady.
			Entry("no configuration yet",
				func() *v1alpha1.ReplicatedVolume {
					rv := newConfiguredTestRV(configuredTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful))
					rv.Status.Configuration = nil
					return rv
				}(),
				[]*v1alpha1.ReplicatedVolumeReplica{newTestRVRAtRevision("rv-0", 7)}),
		)

		DescribeTable("follows the rule order",
			func(rv *v1alpha1.ReplicatedVolume, rvrs []*v1alpha1.ReplicatedVolumeReplica, wantStatus metav1.ConditionStatus, wantReason string) {
				report := computeConfiguredReport(rv, rvrs)
				Expect(report.present).To(BeTrue())
				Expect(report.status).To(Equal(wantStatus))
				Expect(report.reason).To(Equal(wantReason))
			},

			// Rule 3 guards the vacuous match: at revision 0 the laggards report 0 as well, so a
			// plain equality check would report full convergence on a volume that never formed.
			Entry("no revision assigned yet, replicas at 0 too",
				func() *v1alpha1.ReplicatedVolume {
					rv := newConfiguredTestRV(configuredTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful))
					rv.Status.DatameshRevision = 0
					return rv
				}(),
				[]*v1alpha1.ReplicatedVolumeReplica{newTestRVRAtRevision("rv-0", 0)},
				metav1.ConditionUnknown, v1alpha1.ReplicatedVolumeCondConfiguredReasonConfigurationUnknown),

			// Rule 4.
			Entry("datamesh has no members",
				newConfiguredTestRV(),
				nil,
				metav1.ConditionUnknown, v1alpha1.ReplicatedVolumeCondConfiguredReasonConfigurationUnknown),

			// Rule 5. Every member type counts: the replica controller records DatameshRevision
			// for anything it configures as a datamesh member, tie-breakers and Access included.
			Entry("every member applied the revision, all member types counted",
				newConfiguredTestRV(
					configuredTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful),
					configuredTestMember("rv-1", v1alpha1.DatameshMemberTypeTieBreaker),
					configuredTestMember("rv-2", v1alpha1.DatameshMemberTypeAccess),
				),
				[]*v1alpha1.ReplicatedVolumeReplica{
					newTestRVRAtRevision("rv-0", 7),
					newTestRVRAtRevision("rv-1", 7),
					newTestRVRAtRevision("rv-2", 7),
				},
				metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondConfiguredReasonAllReplicasConfigured),

			// Rule 6.
			Entry("one member lags behind",
				newConfiguredTestRV(
					configuredTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful),
					configuredTestMember("rv-1", v1alpha1.DatameshMemberTypeDiskful),
				),
				[]*v1alpha1.ReplicatedVolumeReplica{
					newTestRVRAtRevision("rv-0", 7),
					newTestRVRAtRevision("rv-1", 6),
				},
				metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondConfiguredReasonConfigurationInProgress),

			Entry("a member has no replica object",
				newConfiguredTestRV(
					configuredTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful),
					configuredTestMember("rv-1", v1alpha1.DatameshMemberTypeDiskful),
				),
				[]*v1alpha1.ReplicatedVolumeReplica{newTestRVRAtRevision("rv-0", 7)},
				metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondConfiguredReasonConfigurationInProgress),

			// A replica reporting a revision the volume never assigned should not happen; strict
			// equality reads it as not configured, which is the conservative direction.
			Entry("a member reports a revision ahead of the volume",
				newConfiguredTestRV(configuredTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful)),
				[]*v1alpha1.ReplicatedVolumeReplica{newTestRVRAtRevision("rv-0", 8)},
				metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondConfiguredReasonConfigurationInProgress),
		)

		It("says the revision has not been assigned when the volume never formed", func() {
			// The only coverage this message has: the branch is not observable in e2e, since
			// formation assigns revision 1 before it populates the member list.
			rv := newConfiguredTestRV(configuredTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful))
			rv.Status.DatameshRevision = 0

			report := computeConfiguredReport(rv, []*v1alpha1.ReplicatedVolumeReplica{
				newTestRVRAtRevision("rv-0", 0),
			})

			Expect(report.message).To(Equal("Datamesh revision has not been assigned yet"))
		})

		It("says the datamesh has no members when the member list is empty", func() {
			report := computeConfiguredReport(newConfiguredTestRV(), nil)

			Expect(report.message).To(Equal("Datamesh has no members yet"))
		})

		It("counts the converged members and names the first laggard in member order", func() {
			rv := newConfiguredTestRV(
				configuredTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful),
				configuredTestMember("rv-1", v1alpha1.DatameshMemberTypeDiskful),
				configuredTestMember("rv-2", v1alpha1.DatameshMemberTypeDiskful),
			)
			rvrs := []*v1alpha1.ReplicatedVolumeReplica{
				newTestRVRAtRevision("rv-0", 7),
				newTestRVRAtRevision("rv-1", 6),
				newTestRVRAtRevision("rv-2", 6),
			}

			report := computeConfiguredReport(rv, rvrs)

			Expect(report.message).To(Equal("Configuration revision 7 applied on 1/3 replicas: waiting for rv-1"))
		})

		It("reports the revision and the member count when fully converged", func() {
			rv := newConfiguredTestRV(
				configuredTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful),
				configuredTestMember("rv-1", v1alpha1.DatameshMemberTypeDiskful),
			)
			rvrs := []*v1alpha1.ReplicatedVolumeReplica{
				newTestRVRAtRevision("rv-0", 7),
				newTestRVRAtRevision("rv-1", 7),
			}

			report := computeConfiguredReport(rv, rvrs)

			Expect(report.message).To(Equal("Configuration revision 7 applied on all 2 replicas"))
		})

		It("does not mutate the volume or the replicas", func() {
			rv := newConfiguredTestRV(configuredTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful))
			rvrs := []*v1alpha1.ReplicatedVolumeReplica{newTestRVRAtRevision("rv-0", 6)}
			rvBefore, rvrBefore := rv.DeepCopy(), rvrs[0].DeepCopy()

			computeConfiguredReport(rv, rvrs)

			Expect(rv).To(Equal(rvBefore))
			Expect(rvrs[0]).To(Equal(rvrBefore))
		})
	})

	Describe("applyConfiguredCond", func() {
		It("leaves the condition untouched when the report is absent", func() {
			rv := newConfiguredTestRV()
			Expect(applyConfiguredCond(rv, configuredReport{
				present: true,
				status:  metav1.ConditionTrue,
				reason:  v1alpha1.ReplicatedVolumeCondConfiguredReasonAllReplicasConfigured,
				message: "Configuration revision 7 applied on all 2 replicas",
			})).To(BeTrue())

			Expect(applyConfiguredCond(rv, configuredReport{present: false})).To(BeFalse())

			cond := obju.GetStatusCondition(rv, v1alpha1.ReplicatedVolumeCondConfiguredType)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeCondConfiguredReasonAllReplicasConfigured))
		})

		It("reports no change when the condition already holds the computed value", func() {
			rv := newConfiguredTestRV()
			report := configuredReport{
				present: true,
				status:  metav1.ConditionFalse,
				reason:  v1alpha1.ReplicatedVolumeCondConfiguredReasonConfigurationInProgress,
				message: "Configuration revision 7 applied on 1/2 replicas: waiting for rv-1",
			}

			Expect(applyConfiguredCond(rv, report)).To(BeTrue())
			Expect(applyConfiguredCond(rv, report)).To(BeFalse())
		})
	})
})
