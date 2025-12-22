/*
Copyright 2025 Flant JSC

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

package rvrvolume_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gcustom"
	gomegatypes "github.com/onsi/gomega/types" // cspell:words gomegatypes
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func TestRvrVolume(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "RvrVolume Suite")
}

// Requeue returns a matcher that checks if reconcile result requires requeue
func Requeue() gomegatypes.GomegaMatcher {
	return Not(Equal(reconcile.Result{}))
}

// RequestFor creates a reconcile request for the given object
func RequestFor(object client.Object) reconcile.Request {
	return reconcile.Request{NamespacedName: client.ObjectKeyFromObject(object)}
}

// HaveLVMLogicalVolumeName returns a matcher that checks if RVR has the specified LLV name in status
func HaveLVMLogicalVolumeName(llvName string) gomegatypes.GomegaMatcher {
	if llvName == "" {
		return SatisfyAny(
			HaveField("Status", BeNil()),
			HaveField("Status.LVMLogicalVolumeName", BeEmpty()),
		)
	}
	return SatisfyAll(
		HaveField("Status", Not(BeNil())),
		HaveField("Status.LVMLogicalVolumeName", Equal(llvName)),
	)
}

// HaveNoLVMLogicalVolumeName returns a matcher that checks if RVR has no LLV name in status
func HaveNoLVMLogicalVolumeName() gomegatypes.GomegaMatcher {
	return HaveLVMLogicalVolumeName("")
}

// BeLLVPhase returns a matcher that checks if LLV has the specified phase
func BeLLVPhase(phase string) gomegatypes.GomegaMatcher {
	if phase == "" {
		return SatisfyAny(
			HaveField("Status", BeNil()),
			HaveField("Status.Phase", BeEmpty()),
		)
	}
	return SatisfyAll(
		HaveField("Status", Not(BeNil())),
		HaveField("Status.Phase", Equal(phase)),
	)
}

// HaveLLVWithOwnerReference returns a matcher that checks if LLV has owner reference to RVR
func HaveLLVWithOwnerReference(rvrName string) gomegatypes.GomegaMatcher {
	return gcustom.MakeMatcher(func(llv *snc.LVMLogicalVolume) (bool, error) {
		ownerRef := metav1.GetControllerOf(llv)
		if ownerRef == nil {
			return false, nil
		}
		return ownerRef.Kind == "ReplicatedVolumeReplica" && ownerRef.Name == rvrName, nil
	}).WithMessage("expected LLV to have owner reference to RVR " + rvrName)
}

// HaveFinalizer returns a matcher that checks if object has the specified finalizer
func HaveFinalizer(finalizerName string) gomegatypes.GomegaMatcher {
	return gcustom.MakeMatcher(func(obj client.Object) (bool, error) {
		for _, f := range obj.GetFinalizers() {
			if f == finalizerName {
				return true, nil
			}
		}
		return false, nil
	}).WithTemplate("Expected:\n{{.FormattedActual}}\n{{.To}} have finalizer:\n{{format .Data 1}}").WithTemplateData(finalizerName)
}

// NotHaveFinalizer returns a matcher that checks if object does not have the specified finalizer
func NotHaveFinalizer(finalizerName string) gomegatypes.GomegaMatcher {
	return gcustom.MakeMatcher(func(obj client.Object) (bool, error) {
		for _, f := range obj.GetFinalizers() {
			if f == finalizerName {
				return false, nil
			}
		}
		return true, nil
	}).WithMessage("expected object to not have finalizer " + finalizerName)
}

// BeDiskful returns a matcher that checks if RVR is diskful
func BeDiskful() gomegatypes.GomegaMatcher {
	return HaveField("Spec.Type", Equal(v1alpha1.ReplicaTypeDiskful))
}

// BeNonDiskful returns a matcher that checks if RVR is not diskful
func BeNonDiskful() gomegatypes.GomegaMatcher {
	return Not(BeDiskful())
}

// HaveDeletionTimestamp returns a matcher that checks if object has deletion timestamp
func HaveDeletionTimestamp() gomegatypes.GomegaMatcher {
	return HaveField("DeletionTimestamp", Not(BeNil()))
}

// NotHaveDeletionTimestamp returns a matcher that checks if object does not have deletion timestamp
func NotHaveDeletionTimestamp() gomegatypes.GomegaMatcher {
	return SatisfyAny(
		HaveField("DeletionTimestamp", BeNil()),
	)
}

// HaveBackingVolumeCreatedCondition returns a matcher that checks if RVR has BackingVolumeCreated condition
// with the specified status and reason.
func HaveBackingVolumeCreatedCondition(status metav1.ConditionStatus, reason string) gomegatypes.GomegaMatcher {
	return gcustom.MakeMatcher(func(rvr *v1alpha1.ReplicatedVolumeReplica) (bool, error) {
		if rvr.Status == nil || rvr.Status.Conditions == nil {
			return false, nil
		}
		for _, cond := range rvr.Status.Conditions {
			if cond.Type == v1alpha1.ConditionTypeBackingVolumeCreated {
				return cond.Status == status && cond.Reason == reason, nil
			}
		}
		return false, nil
	}).WithMessage("expected RVR to have BackingVolumeCreated condition with status " + string(status) + " and reason " + reason)
}

// HaveBackingVolumeCreatedConditionReady is a convenience matcher that checks if
// the BackingVolumeCreated condition is True with ReasonBackingVolumeReady.
func HaveBackingVolumeCreatedConditionReady() gomegatypes.GomegaMatcher {
	return HaveBackingVolumeCreatedCondition(metav1.ConditionTrue, v1alpha1.ReasonBackingVolumeReady)
}

// HaveBackingVolumeCreatedConditionNotReady is a convenience matcher that checks if
// the BackingVolumeCreated condition is False with ReasonBackingVolumeNotReady.
func HaveBackingVolumeCreatedConditionNotReady() gomegatypes.GomegaMatcher {
	return HaveBackingVolumeCreatedCondition(metav1.ConditionFalse, v1alpha1.ReasonBackingVolumeNotReady)
}

// HaveBackingVolumeCreatedConditionNotApplicable is a convenience matcher that checks if
// the BackingVolumeCreated condition is False with ReasonNotApplicable.
func HaveBackingVolumeCreatedConditionNotApplicable() gomegatypes.GomegaMatcher {
	return HaveBackingVolumeCreatedCondition(metav1.ConditionFalse, v1alpha1.ReasonNotApplicable)
}

// HaveBackingVolumeCreatedConditionCreationFailed is a convenience matcher that checks if
// the BackingVolumeCreated condition is False with ReasonBackingVolumeCreationFailed.
func HaveBackingVolumeCreatedConditionCreationFailed() gomegatypes.GomegaMatcher {
	return HaveBackingVolumeCreatedCondition(metav1.ConditionFalse, v1alpha1.ReasonBackingVolumeCreationFailed)
}

// HaveBackingVolumeCreatedConditionDeletionFailed is a convenience matcher that checks if
// the BackingVolumeCreated condition is True with ReasonBackingVolumeDeletionFailed.
func HaveBackingVolumeCreatedConditionDeletionFailed() gomegatypes.GomegaMatcher {
	return HaveBackingVolumeCreatedCondition(metav1.ConditionTrue, v1alpha1.ReasonBackingVolumeDeletionFailed)
}
