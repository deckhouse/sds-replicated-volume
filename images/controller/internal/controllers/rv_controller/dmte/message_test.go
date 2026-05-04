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

package dmte

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
)

var _ = Describe("generateProgressMessage", func() {
	It("all confirmed", func() {
		cp := testCtx()
		msg := generateProgressMessage(cp, 7, ConfirmResult{
			MustConfirm: ids(0, 1, 2),
			Confirmed:   ids(0, 1, 2),
		}, nil, nil)
		Expect(msg).To(Equal("3/3 replicas confirmed revision 7"))
	})

	It("some waiting, no diagnostics", func() {
		cp := testCtx()
		msg := generateProgressMessage(cp, 7, ConfirmResult{
			MustConfirm: ids(0, 1, 2, 3),
			Confirmed:   ids(0, 1),
		}, nil, nil)
		Expect(msg).To(ContainSubstring("2/4 replicas confirmed revision 7"))
		Expect(msg).To(ContainSubstring("Waiting:"))
	})

	It("waiting replica does not exist", func() {
		cp := testCtx(
			&testReplicaCtx{id: 0, exists: true, generation: 1},
			&testReplicaCtx{id: 3, exists: false}, // exists in provider but Exists() == false
		)
		msg := generateProgressMessage(cp, 5, ConfirmResult{
			MustConfirm: ids(0, 3),
			Confirmed:   idset.Of(0),
		}, []string{"DRBDConfigured"}, nil)
		Expect(msg).To(ContainSubstring("#3 replica not found"))
	})

	It("diagnostic error on unconfirmed replica", func() {
		cp := testCtx(
			&testReplicaCtx{id: 2, exists: true, generation: 1, conditions: []metav1.Condition{
				{Type: "DRBDConfigured", Status: metav1.ConditionFalse, Reason: "SomeError", Message: "disk failed", ObservedGeneration: 1},
			}},
		)
		msg := generateProgressMessage(cp, 5, ConfirmResult{
			MustConfirm: idset.Of(2),
			Confirmed:   idset.IDSet(0),
		}, []string{"DRBDConfigured"}, nil)
		Expect(msg).To(ContainSubstring("Errors: #2 DRBDConfigured/SomeError: disk failed"))
	})

	It("diagnostic skip error", func() {
		cp := testCtx(
			&testReplicaCtx{id: 2, exists: true, generation: 1, conditions: []metav1.Condition{
				{Type: "DRBDConfigured", Status: metav1.ConditionFalse, Reason: "PendingJoin", ObservedGeneration: 1},
			}},
		)
		skip := func(_ *testReplicaCtx, _ uint8, cond *metav1.Condition) bool {
			return cond.Reason == "PendingJoin"
		}
		msg := generateProgressMessage(cp, 5, ConfirmResult{
			MustConfirm: idset.Of(2),
			Confirmed:   idset.IDSet(0),
		}, []string{"DRBDConfigured"}, skip)
		Expect(msg).NotTo(ContainSubstring("Errors"))
		Expect(msg).To(ContainSubstring("Waiting:"))
	})

	It("condition not False is skipped", func() {
		cp := testCtx(
			&testReplicaCtx{id: 2, exists: true, generation: 1, conditions: []metav1.Condition{
				{Type: "DRBDConfigured", Status: metav1.ConditionTrue, Reason: "OK", ObservedGeneration: 1},
			}},
		)
		msg := generateProgressMessage(cp, 5, ConfirmResult{
			MustConfirm: idset.Of(2),
			Confirmed:   idset.IDSet(0),
		}, []string{"DRBDConfigured"}, nil)
		Expect(msg).NotTo(ContainSubstring("Errors"))
	})

	It("condition with wrong ObservedGeneration is skipped", func() {
		cp := testCtx(
			&testReplicaCtx{id: 2, exists: true, generation: 5, conditions: []metav1.Condition{
				{Type: "DRBDConfigured", Status: metav1.ConditionFalse, Reason: "Stale", ObservedGeneration: 3},
			}},
		)
		msg := generateProgressMessage(cp, 5, ConfirmResult{
			MustConfirm: idset.Of(2),
			Confirmed:   idset.IDSet(0),
		}, []string{"DRBDConfigured"}, nil)
		Expect(msg).NotTo(ContainSubstring("Errors"))
	})

	It("condition with empty message", func() {
		cp := testCtx(
			&testReplicaCtx{id: 1, exists: true, generation: 1, conditions: []metav1.Condition{
				{Type: "DRBDConfigured", Status: metav1.ConditionFalse, Reason: "Unknown", ObservedGeneration: 1},
			}},
		)
		msg := generateProgressMessage(cp, 5, ConfirmResult{
			MustConfirm: idset.Of(1),
			Confirmed:   idset.IDSet(0),
		}, []string{"DRBDConfigured"}, nil)
		// Condition reason without trailing ": message" (empty message).
		Expect(msg).To(ContainSubstring("#1 DRBDConfigured/Unknown"))
		Expect(msg).NotTo(ContainSubstring("DRBDConfigured/Unknown:"))
	})

	It("two conditions on same replica", func() {
		cp := testCtx(
			&testReplicaCtx{id: 2, exists: true, generation: 1, conditions: []metav1.Condition{
				{Type: "DRBDConfigured", Status: metav1.ConditionFalse, Reason: "ConfigError", Message: "bad config", ObservedGeneration: 1},
				{Type: "Ready", Status: metav1.ConditionFalse, Reason: "NotReady", Message: "not synced", ObservedGeneration: 1},
			}},
		)
		msg := generateProgressMessage(cp, 5, ConfirmResult{
			MustConfirm: idset.Of(2),
			Confirmed:   idset.IDSet(0),
		}, []string{"DRBDConfigured", "Ready"}, nil)
		// Same replica, two conditions — separated by ", "
		Expect(msg).To(ContainSubstring("#2 DRBDConfigured/ConfigError: bad config, Ready/NotReady: not synced"))
	})

	It("two replicas with errors", func() {
		cp := testCtx(
			&testReplicaCtx{id: 1, exists: true, generation: 1, conditions: []metav1.Condition{
				{Type: "DRBDConfigured", Status: metav1.ConditionFalse, Reason: "ErrA", ObservedGeneration: 1},
			}},
			&testReplicaCtx{id: 3, exists: true, generation: 1, conditions: []metav1.Condition{
				{Type: "DRBDConfigured", Status: metav1.ConditionFalse, Reason: "ErrB", ObservedGeneration: 1},
			}},
		)
		msg := generateProgressMessage(cp, 5, ConfirmResult{
			MustConfirm: ids(1, 3),
			Confirmed:   idset.IDSet(0),
		}, []string{"DRBDConfigured"}, nil)
		// Two replicas with errors — separated by " | "
		Expect(msg).To(ContainSubstring("#1 DRBDConfigured/ErrA"))
		Expect(msg).To(ContainSubstring(" | #3 DRBDConfigured/ErrB"))
	})

	It("replica exists but has no matching conditions", func() {
		cp := testCtx(
			&testReplicaCtx{id: 5, exists: true, generation: 1}, // no conditions
		)
		msg := generateProgressMessage(cp, 5, ConfirmResult{
			MustConfirm: idset.Of(5),
			Confirmed:   idset.IDSet(0),
		}, []string{"DRBDConfigured"}, nil)
		Expect(msg).NotTo(ContainSubstring("Errors"))
		Expect(msg).To(ContainSubstring("Waiting:"))
	})

	It("zero replica context in provider reports not found", func() {
		cp := testCtx() // id 7 not in provider — zero value (*testReplicaCtx)(nil)
		msg := generateProgressMessage(cp, 5, ConfirmResult{
			MustConfirm: idset.Of(7),
			Confirmed:   idset.IDSet(0),
		}, []string{"DRBDConfigured"}, nil)
		Expect(msg).To(ContainSubstring("#7 replica not found"))
	})

	It("empty MustConfirm and Confirmed", func() {
		cp := testCtx()
		msg := generateProgressMessage(cp, 1, ConfirmResult{}, nil, nil)
		Expect(msg).To(Equal("0/0 replicas confirmed revision 1"))
	})
})

var _ = Describe("composeProgressMessage", func() {
	It("single-step plan", func() {
		msg := composeProgressMessage("Joining datamesh", "✦ → A", 0, 1, "3/4 replicas confirmed revision 7")
		Expect(msg).To(Equal("Joining datamesh: 3/4 replicas confirmed revision 7"))
	})

	It("multi-step plan step 1 of 3", func() {
		msg := composeProgressMessage("Adding diskful replica", "✦ → D∅", 0, 3, "2/4 replicas confirmed revision 12")
		Expect(msg).To(Equal("Adding diskful replica (step 1/3: ✦ → D∅): 2/4 replicas confirmed revision 12"))
	})

	It("multi-step plan step 2 of 3", func() {
		msg := composeProgressMessage("Adding diskful replica", "D∅ → D", 1, 3, "4/4 replicas confirmed revision 13")
		Expect(msg).To(Equal("Adding diskful replica (step 2/3: D∅ → D): 4/4 replicas confirmed revision 13"))
	})
})

var _ = Describe("composeBlocked", func() {
	It("guard reason", func() {
		msg := composeBlocked("Removing diskful replica", "would violate GMDR (ADR=1, need > 1)")
		Expect(msg).To(Equal("Removing diskful replica is blocked: would violate GMDR (ADR=1, need > 1)"))
	})

	It("tracker reason", func() {
		msg := composeBlocked("Adding diskful replica", "active Formation in progress")
		Expect(msg).To(Equal("Adding diskful replica is blocked: active Formation in progress"))
	})

	It("attachment guard reason", func() {
		msg := composeBlocked("Attaching volume", "volume is being deleted")
		Expect(msg).To(Equal("Attaching volume is blocked: volume is being deleted"))
	})
})

var _ = Describe("composeBlockedByActive", func() {
	It("active step with message", func() {
		step := &v1alpha1.ReplicatedVolumeDatameshTransitionStep{
			Name:    "✦ → A",
			Status:  v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
			Message: "3/4 confirmed",
		}
		msg := composeBlockedByActive("Leaving datamesh", "Joining datamesh", step)
		Expect(msg).To(Equal("Leaving datamesh: waiting for Joining datamesh to complete. 3/4 confirmed"))
	})

	It("active step without message", func() {
		step := &v1alpha1.ReplicatedVolumeDatameshTransitionStep{
			Name:   "✦ → A",
			Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
		}
		msg := composeBlockedByActive("Detaching", "Attaching", step)
		Expect(msg).To(Equal("Detaching: waiting for Attaching to complete"))
	})

	It("nil active step", func() {
		msg := composeBlockedByActive("Detaching", "Attaching", nil)
		Expect(msg).To(Equal("Detaching: waiting for Attaching to complete"))
	})
})
