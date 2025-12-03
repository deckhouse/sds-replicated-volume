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

package rvrdiskfulcount_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha3 "github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

func TestRvrDiskfulCount(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "RvrDiskfulCount Suite")
}

// HaveDiskfulReplicaCountReachedConditionWithReason is a matcher that checks if a ReplicatedVolume
// has the DiskfulReplicaCountReached condition with the specified status and reason.
func HaveDiskfulReplicaCountReachedConditionWithReason(status metav1.ConditionStatus, reason string) OmegaMatcher {
	return And(
		Not(BeNil()),
		HaveField("Status", Equal(status)),
		HaveField("Reason", Equal(reason)),
	)
}

// HaveDiskfulReplicaCountReachedConditionAvailable is a convenience matcher that checks if
// the DiskfulReplicaCountReached condition is True with ReasonRequiredNumberOfReplicasIsAvailable.
func HaveDiskfulReplicaCountReachedConditionAvailable() OmegaMatcher {
	return HaveDiskfulReplicaCountReachedConditionWithReason(
		metav1.ConditionTrue,
		v1alpha3.ReasonRequiredNumberOfReplicasIsAvailable,
	)
}

// HaveDiskfulReplicaCountReachedConditionFirstReplicaBeingCreated is a convenience matcher that checks if
// the DiskfulReplicaCountReached condition is False with ReasonFirstReplicaIsBeingCreated.
func HaveDiskfulReplicaCountReachedConditionFirstReplicaBeingCreated() OmegaMatcher {
	return HaveDiskfulReplicaCountReachedConditionWithReason(
		metav1.ConditionFalse,
		v1alpha3.ReasonFirstReplicaIsBeingCreated,
	)
}

// HaveDiskfulReplicaCountReachedConditionCreatedOrAvailable is a convenience matcher that checks if
// the DiskfulReplicaCountReached condition is True with ReasonRequiredNumberOfReplicasIsAvailable.
func HaveDiskfulReplicaCountReachedConditionCreatedOrAvailable() OmegaMatcher {
	return HaveDiskfulReplicaCountReachedConditionAvailable()
}

func Requeue() OmegaMatcher {
	return Not(Equal(reconcile.Result{}))
}

func RequestFor(o client.Object) reconcile.Request {
	return reconcile.Request{NamespacedName: client.ObjectKeyFromObject(o)}
}
