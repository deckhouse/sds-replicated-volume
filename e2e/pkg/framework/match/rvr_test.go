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

package match

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

var _ = Describe("RVR.OnNode", func() {
	It("matches correct node", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{}
		rvr.Spec.NodeName = "worker-1"
		Expect(rvr).To(RVR.OnNode("worker-1"))
	})

	It("fails on wrong node", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{}
		rvr.Spec.NodeName = "worker-2"
		Expect(rvr).ToNot(RVR.OnNode("worker-1"))
	})
})

var _ = Describe("RVR.Type", func() {
	It("matches Diskful", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{}
		rvr.Spec.Type = v1alpha1.ReplicaTypeDiskful
		Expect(rvr).To(RVR.Type(v1alpha1.ReplicaTypeDiskful))
	})

	It("fails on mismatch", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{}
		rvr.Spec.Type = v1alpha1.ReplicaTypeAccess
		Expect(rvr).ToNot(RVR.Type(v1alpha1.ReplicaTypeDiskful))
	})
})

var _ = Describe("RVR.Quorum", func() {
	It("matches true", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{}
		q := true
		rvr.Status.Quorum = &q
		Expect(rvr).To(RVR.Quorum(true))
	})

	It("matches false", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{}
		q := false
		rvr.Status.Quorum = &q
		Expect(rvr).To(RVR.Quorum(false))
	})

	It("fails when nil", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{}
		Expect(rvr).ToNot(RVR.Quorum(true))
	})

	It("fails on mismatch", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{}
		q := false
		rvr.Status.Quorum = &q
		Expect(rvr).ToNot(RVR.Quorum(true))
	})
})

var _ = Describe("RVR.BackingVolumeState", func() {
	It("matches state", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{}
		rvr.Status.BackingVolume = &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
			State: "UpToDate",
		}
		Expect(rvr).To(RVR.BackingVolumeState("UpToDate"))
	})

	It("fails on wrong state", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{}
		rvr.Status.BackingVolume = &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
			State: "Inconsistent",
		}
		Expect(rvr).ToNot(RVR.BackingVolumeState("UpToDate"))
	})

	It("fails when backing volume nil", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{}
		Expect(rvr).ToNot(RVR.BackingVolumeState("UpToDate"))
	})
})

// ---------------------------------------------------------------------------
// Catalog matchers
// ---------------------------------------------------------------------------

var _ = Describe("RVR.NeverLoseQuorum", func() {
	It("passes when quorum true", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{}
		q := true
		rvr.Status.Quorum = &q
		Expect(rvr).To(RVR.NeverLoseQuorum())
	})

	It("passes when quorum nil", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{}
		Expect(rvr).To(RVR.NeverLoseQuorum())
	})

	It("fails when quorum false", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{}
		q := false
		rvr.Status.Quorum = &q
		Expect(rvr).ToNot(RVR.NeverLoseQuorum())
	})

})

var _ = Describe("RVR.NeverCritical", func() {
	It("passes for Healthy", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{}
		rvr.Status.Phase = v1alpha1.ReplicatedVolumeReplicaPhaseHealthy
		Expect(rvr).To(RVR.NeverCritical())
	})

	It("fails for Critical", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{}
		rvr.Status.Phase = v1alpha1.ReplicatedVolumeReplicaPhaseCritical
		Expect(rvr).ToNot(RVR.NeverCritical())
	})

	It("passes for empty phase", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{}
		Expect(rvr).To(RVR.NeverCritical())
	})
})

var _ = Describe("RVR.NeverIOSuspended", func() {
	It("passes when no Attached condition", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{}
		Expect(rvr).To(RVR.NeverIOSuspended())
	})

	It("passes with Attached reason=Attached", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{}
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type:               v1alpha1.ReplicatedVolumeReplicaCondAttachedType,
			Status:             metav1.ConditionTrue,
			Reason:             v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonAttached,
			LastTransitionTime: metav1.NewTime(time.Now()),
		})
		Expect(rvr).To(RVR.NeverIOSuspended())
	})

	It("fails with Attached reason=IOSuspended", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{}
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type:               v1alpha1.ReplicatedVolumeReplicaCondAttachedType,
			Status:             metav1.ConditionFalse,
			Reason:             v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonIOSuspended,
			LastTransitionTime: metav1.NewTime(time.Now()),
		})
		Expect(rvr).ToNot(RVR.NeverIOSuspended())
	})
})

var _ = Describe("RVR.NeverDegraded", func() {
	It("passes for Healthy", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{}
		rvr.Status.Phase = v1alpha1.ReplicatedVolumeReplicaPhaseHealthy
		Expect(rvr).To(RVR.NeverDegraded())
	})

	It("fails for Degraded", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{}
		rvr.Status.Phase = v1alpha1.ReplicatedVolumeReplicaPhaseDegraded
		Expect(rvr).ToNot(RVR.NeverDegraded())
	})
})

var _ = Describe("RVR.SafetyChecks", func() {
	It("returns 3 matchers", func() {
		Expect(RVR.SafetyChecks()).To(HaveLen(3))
	})
})

var _ = Describe("RVR.StrictChecks", func() {
	It("returns 4 matchers", func() {
		Expect(RVR.StrictChecks()).To(HaveLen(4))
	})
})

var _ = Describe("RVR.Custom", func() {
	It("receives typed object", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{}
		rvr.Name = "test-rvr-0"
		Expect(rvr).To(RVR.Custom("name check", func(r *v1alpha1.ReplicatedVolumeReplica) bool {
			return r.Name == "test-rvr-0"
		}))
	})
})
