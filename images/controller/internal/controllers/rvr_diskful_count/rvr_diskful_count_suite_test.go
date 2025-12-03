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

// HaveDiskfulReplicaCountReachedConditionCreated is a convenience matcher that checks if
// the DiskfulReplicaCountReached condition is True with ReasonCreatedRequiredNumberOfReplicas.
func HaveDiskfulReplicaCountReachedConditionCreated() OmegaMatcher {
	return HaveDiskfulReplicaCountReachedConditionWithReason(
		metav1.ConditionTrue,
		v1alpha3.ReasonCreatedRequiredNumberOfReplicas,
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
// the DiskfulReplicaCountReached condition is True with either ReasonCreatedRequiredNumberOfReplicas
// or ReasonRequiredNumberOfReplicasIsAvailable.
func HaveDiskfulReplicaCountReachedConditionCreatedOrAvailable() OmegaMatcher {
	return And(
		Not(BeNil()),
		HaveField("Status", Equal(metav1.ConditionTrue)),
		HaveField("Reason", BeElementOf(
			v1alpha3.ReasonCreatedRequiredNumberOfReplicas,
			v1alpha3.ReasonRequiredNumberOfReplicasIsAvailable,
		)),
	)
}

func Requeue() OmegaMatcher {
	return Not(Equal(reconcile.Result{}))
}

func RequestFor(o client.Object) reconcile.Request {
	return reconcile.Request{NamespacedName: client.ObjectKeyFromObject(o)}
}
