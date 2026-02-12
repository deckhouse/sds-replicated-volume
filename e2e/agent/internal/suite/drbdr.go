package suite

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DRBDResourceInfo holds metadata about a created DRBDResource.
type DRBDResourceInfo struct {
	Name     string
	NodeName string
	NodeID   uint8
	LLVName  string
}

// SetupDRBDResources Provides DRBDResource objects created for each node.
// Each resource is type=Diskful, size=100Mi, systemNetworks=["Internal"],
// state=Up, role=Secondary, with no peers. Resources are polled until
// status.addresses is populated and condition Configured=True.
// Cleaned up after test.
func SetupDRBDResources(
	t *testing.T,
	cl client.Client,
	testID string,
	nodeNames []string,
	llvInfos []LLVInfo,
) []DRBDResourceInfo {
	// Require
	if len(nodeNames) == 0 {
		t.Fatal("expected nodeNames to be non-empty")
	}
	if len(llvInfos) != len(nodeNames) {
		t.Fatalf("expected llvInfos (%d) to match nodeNames (%d)",
			len(llvInfos), len(nodeNames))
	}

	// Build LLV lookup by node name
	llvByNode := make(map[string]string, len(llvInfos))
	for _, llv := range llvInfos {
		llvByNode[llv.NodeName] = llv.LLVName
	}

	size := resource.MustParse("100Mi")
	result := make([]DRBDResourceInfo, 0, len(nodeNames))

	for i, nodeName := range nodeNames {
		llvName, ok := llvByNode[nodeName]
		if !ok {
			t.Fatalf("no LLV found for node %q", nodeName)
		}

		nodeID := uint8(i)
		drbdrName := fmt.Sprintf("e2e-drbdr-%s-%s", testID, nodeName)

		drbdr := &v1alpha1.DRBDResource{
			ObjectMeta: metav1.ObjectMeta{
				Name: drbdrName,
			},
			Spec: v1alpha1.DRBDResourceSpec{
				NodeName:             nodeName,
				State:                v1alpha1.DRBDResourceStateUp,
				SystemNetworks:       []string{"Internal"},
				Size:                 &size,
				NodeID:               nodeID,
				Role:                 v1alpha1.DRBDRoleSecondary,
				Type:                 v1alpha1.DRBDResourceTypeDiskful,
				LVMLogicalVolumeName: llvName,
			},
		}

		// Discover: check if already exists
		existing := &v1alpha1.DRBDResource{}
		err := cl.Get(t.Context(), client.ObjectKey{Name: drbdrName}, existing)
		if err == nil {
			if existing.Spec.NodeName != nodeName {
				t.Fatalf("DRBDResource %q already exists but on node %q, expected %q",
					drbdrName, existing.Spec.NodeName, nodeName)
			}
		} else {
			// Arrange: create
			if err := cl.Create(t.Context(), drbdr); err != nil {
				t.Fatalf("creating DRBDResource %q: %v", drbdrName, err)
			}
		}

		// Cleanup
		t.Cleanup(func() {
			cleanupDRBDResource(t, cl, drbdrName)
		})

		result = append(result, DRBDResourceInfo{
			Name:     drbdrName,
			NodeName: nodeName,
			NodeID:   nodeID,
			LLVName:  llvName,
		})
	}

	// Assert: wait for addresses and Configured=True on all resources
	for _, info := range result {
		waitForDRBDResourceAddresses(t, cl, info.Name)
		waitForDRBDResourceCondition(t, cl, info.Name,
			v1alpha1.DRBDResourceCondConfiguredType, metav1.ConditionTrue)
	}

	return result
}

// SetupDRBDResourcesPeered Provides peered DRBDResources. Each DRBDResource is
// patched to add all other resources as peers with protocol=C,
// sharedSecretAlg=DummyForTest. Peer paths use status.addresses from the remote
// resource. All resources are polled until condition Configured=True.
func SetupDRBDResourcesPeered(
	t *testing.T,
	cl client.Client,
	drbdrInfos []DRBDResourceInfo,
) {
	if len(drbdrInfos) < 2 {
		t.Fatal("expected at least 2 DRBDResources for peering")
	}

	// Fetch current status.addresses for all resources
	type resourceWithAddresses struct {
		info      DRBDResourceInfo
		addresses []v1alpha1.DRBDResourceAddressStatus
	}
	resources := make([]resourceWithAddresses, 0, len(drbdrInfos))
	for _, info := range drbdrInfos {
		drbdr := &v1alpha1.DRBDResource{}
		if err := cl.Get(t.Context(), client.ObjectKey{Name: info.Name}, drbdr); err != nil {
			t.Fatalf("getting DRBDResource %q for peering: %v", info.Name, err)
		}
		if !hasValidAddresses(drbdr) {
			t.Fatalf("DRBDResource %q has no valid addresses for peering", info.Name)
		}
		resources = append(resources, resourceWithAddresses{
			info:      info,
			addresses: drbdr.Status.Addresses,
		})
	}

	// Generate a shared secret for the peer connections
	sharedSecret := generateSharedSecret()

	// Arrange: patch each resource with peers pointing to all others
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
				Name:            peerRes.info.NodeName,
				Type:            v1alpha1.DRBDResourceTypeDiskful,
				NodeID:          peerRes.info.NodeID,
				Protocol:        v1alpha1.DRBDProtocolC,
				SharedSecret:    sharedSecret,
				SharedSecretAlg: v1alpha1.SharedSecretAlgDummyForTest,
				Paths:           paths,
			})
		}

		drbdr := &v1alpha1.DRBDResource{}
		if err := cl.Get(t.Context(), client.ObjectKey{Name: res.info.Name}, drbdr); err != nil {
			t.Fatalf("getting DRBDResource %q for peer patch: %v", res.info.Name, err)
		}

		drbdr.Spec.Peers = peers

		if err := cl.Update(t.Context(), drbdr); err != nil {
			t.Fatalf("updating DRBDResource %q peers: %v", res.info.Name, err)
		}
	}

	// Cleanup: remove peers
	t.Cleanup(func() {
		for _, res := range resources {
			drbdr := &v1alpha1.DRBDResource{}
			if err := cl.Get(t.Context(), client.ObjectKey{Name: res.info.Name}, drbdr); err != nil {
				t.Errorf("getting DRBDResource %q for peer cleanup: %v", res.info.Name, err)
				continue
			}

			drbdr.Spec.Peers = nil

			if err := cl.Update(t.Context(), drbdr); err != nil {
				t.Errorf("removing peers from DRBDResource %q: %v", res.info.Name, err)
			}
		}
	})

	// Assert: wait for Configured=True on all resources
	for _, res := range resources {
		waitForDRBDResourceCondition(t, cl, res.info.Name,
			v1alpha1.DRBDResourceCondConfiguredType, metav1.ConditionTrue)
	}
}

// cleanupDRBDResource deletes a DRBDResource and removes any agent finalizers
// to allow garbage collection.
func cleanupDRBDResource(t *testing.T, cl client.Client, name string) {
	t.Helper()

	drbdr := &v1alpha1.DRBDResource{}
	if err := cl.Get(t.Context(), client.ObjectKey{Name: name}, drbdr); err != nil {
		t.Errorf("getting DRBDResource %q for cleanup: %v", name, err)
		return
	}

	// Set state to Down so the agent brings down the DRBD resource
	drbdr.Spec.State = v1alpha1.DRBDResourceStateDown
	if err := cl.Update(t.Context(), drbdr); err != nil {
		t.Errorf("setting DRBDResource %q state to Down: %v", name, err)
	}

	// Wait for agent to remove its finalizer
	waitForDRBDResourceNoFinalizer(t, cl, name, v1alpha1.AgentFinalizer)

	// Delete
	if err := cl.Delete(t.Context(), drbdr); err != nil {
		t.Errorf("deleting DRBDResource %q: %v", name, err)
	}
}

// waitForDRBDResourceNoFinalizer polls a DRBDResource until the specified
// finalizer is removed.
func waitForDRBDResourceNoFinalizer(
	t *testing.T,
	cl client.Client,
	name string,
	finalizer string,
) {
	t.Helper()

	deadline := defaultTimeout
	poll(t, deadline, func() bool {
		drbdr := &v1alpha1.DRBDResource{}
		if err := cl.Get(t.Context(), client.ObjectKey{Name: name}, drbdr); err != nil {
			return false
		}
		for _, f := range drbdr.Finalizers {
			if f == finalizer {
				return false
			}
		}
		return true
	}, "DRBDResource %q to have finalizer %q removed", name, finalizer)
}

// poll calls check repeatedly until it returns true or the timeout expires.
func poll(t *testing.T, timeout time.Duration, check func() bool, msgFmt string, args ...any) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		if check() {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("timed out waiting for "+msgFmt, args...)
			return
		}
		time.Sleep(defaultPollInterval)
	}
}

func generateSharedSecret() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to a static string if crypto/rand fails
		return "e2e-test-shared-secret"
	}
	return hex.EncodeToString(b)
}
