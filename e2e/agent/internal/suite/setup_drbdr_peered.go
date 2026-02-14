package suite

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/etesting"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// SetupDRBDResourcesPeered Provides peered DRBDResources. Each DRBDResource is
// patched to add all other resources as peers with protocol=C,
// sharedSecretAlg=DummyForTest. Peer paths use status.addresses from the remote
// resource. All resources are polled until condition Configured=True.
func SetupDRBDResourcesPeered(
	e *etesting.E,
	cl client.Client,
	drbdrs []*v1alpha1.DRBDResource,
) {
	if len(drbdrs) < 2 {
		e.Fatal("require: expected at least 2 DRBDResources for peering")
	}

	// Fetch current status.addresses for all resources.
	type resourceWithAddresses struct {
		name      string
		nodeName  string
		nodeID    uint8
		addresses []v1alpha1.DRBDResourceAddressStatus
	}
	resources := make([]resourceWithAddresses, 0, len(drbdrs))
	for _, dr := range drbdrs {
		drbdr := &v1alpha1.DRBDResource{}
		if err := cl.Get(e.Context(), client.ObjectKey{Name: dr.Name}, drbdr); err != nil {
			e.Fatalf("getting DRBDResource %q for peering: %v", dr.Name, err)
		}
		if !hasValidAddresses(drbdr) {
			e.Fatalf("DRBDResource %q has no valid addresses for peering", dr.Name)
		}
		resources = append(resources, resourceWithAddresses{
			name:      drbdr.Name,
			nodeName:  drbdr.Spec.NodeName,
			nodeID:    drbdr.Spec.NodeID,
			addresses: drbdr.Status.Addresses,
		})
	}

	// Generate a shared secret for the peer connections.
	sharedSecret := generateSharedSecret()

	// Arrange: patch each resource with peers pointing to all others.
	for i, res := range resources {
		peers := make([]v1alpha1.DRBDResourcePeer, 0, len(resources)-1)

		for j, peerRes := range resources {
			if i == j {
				continue
			}

			paths := make([]v1alpha1.DRBDResourcePath, 0, len(peerRes.addresses))
			for _, addr := range peerRes.addresses {
				paths = append(paths, v1alpha1.DRBDResourcePath{
					SystemNetworkName: addr.SystemNetworkName,
					Address:           addr.Address,
				})
			}

			peers = append(peers, v1alpha1.DRBDResourcePeer{
				Name:            peerRes.nodeName,
				Type:            v1alpha1.DRBDResourceTypeDiskful,
				NodeID:          peerRes.nodeID,
				Protocol:        v1alpha1.DRBDProtocolC,
				SharedSecret:    sharedSecret,
				SharedSecretAlg: v1alpha1.SharedSecretAlgDummyForTest,
				Paths:           paths,
			})
		}

		drbdr := &v1alpha1.DRBDResource{}
		if err := cl.Get(e.Context(), client.ObjectKey{Name: res.name}, drbdr); err != nil {
			e.Fatalf("getting DRBDResource %q for peer patch: %v", res.name, err)
		}

		drbdr.Spec.Peers = peers

		if err := cl.Update(e.Context(), drbdr); err != nil {
			e.Fatalf("updating DRBDResource %q peers: %v", res.name, err)
		}
	}

	// Cleanup: remove peers.
	e.Cleanup(func() {
		for _, res := range resources {
			drbdr := &v1alpha1.DRBDResource{}
			if err := cl.Get(e.Context(), client.ObjectKey{Name: res.name}, drbdr); err != nil {
				e.Errorf("getting DRBDResource %q for peer cleanup: %v", res.name, err)
				continue
			}

			drbdr.Spec.Peers = nil

			if err := cl.Update(e.Context(), drbdr); err != nil {
				e.Errorf("removing peers from DRBDResource %q: %v", res.name, err)
			}
		}
	})

	// Assert: wait for Configured=True on all resources.
	for _, res := range resources {
		waitForDRBDResourceCondition(e, cl, res.name,
			v1alpha1.DRBDResourceCondConfiguredType, metav1.ConditionTrue)
	}
}

func generateSharedSecret() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to a static string if crypto/rand fails.
		return "e2e-test-shared-secret"
	}
	return hex.EncodeToString(b)
}
