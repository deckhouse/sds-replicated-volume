package rvrstatusconfigaddress_test

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	gomegatypes "github.com/onsi/gomega/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

func TestRvrStatusConfigAddress(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "RvrStatusConfigAddress Suite")
}

// makeReady sets up an RVR to be in ready state by initializing Status and DRBD.Config with NodeId and Address
func makeReady(rvr *v1alpha3.ReplicatedVolumeReplica, nodeID uint, address v1alpha3.Address) {
	if rvr.Status == nil {
		rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{}
	}

	if rvr.Status.DRBD == nil {
		rvr.Status.DRBD = &v1alpha3.DRBD{}
	}

	if rvr.Status.DRBD.Config == nil {
		rvr.Status.DRBD.Config = &v1alpha3.DRBDConfig{}
	}

	rvr.Status.DRBD.Config.NodeId = &nodeID
	rvr.Status.DRBD.Config.Address = &address
}

// BeReady returns a matcher that checks if an RVR is in ready state (has NodeName, NodeId, and Address)
func BeReady() gomegatypes.GomegaMatcher {
	return SatisfyAll(
		HaveField("Spec.NodeName", Not(BeEmpty())),
		HaveField("Status.DRBD.Config.NodeId", Not(BeNil())),
		HaveField("Status.DRBD.Config.Address", Not(BeNil())),
	)
}

func Requeue() gomegatypes.GomegaMatcher {
	return Not(Equal(reconcile.Result{}))
}

func RequestFor(object client.Object) reconcile.Request {
	return reconcile.Request{NamespacedName: client.ObjectKeyFromObject(object)}
}

// ExpectEnqueueNodeForRequest checks that handler returns a single request for the given node name.
func ExpectEnqueueNodeForRequest(handler func(context.Context, client.Object) []reconcile.Request, ctx context.Context, obj client.Object, nodeName string) {
	Expect(handler(ctx, obj)).To(SatisfyAll(
		HaveLen(1),
		ContainElement(SatisfyAll(
			HaveField("NamespacedName.Name", Equal(nodeName)),
			HaveField("NamespacedName.Namespace", BeEmpty()),
		)),
	))
}
