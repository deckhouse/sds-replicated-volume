package agent

import (
	"testing"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/deckhouse/sds-replicated-volume/e2e/agent/internal/suite"
)

func TestDRBDResource(t *testing.T) {
	e := envtesting.New(t)

	// Discover K8s clients.
	cl := DiscoverClient(e)
	cs := DiscoverClientset(e)

	// Start agent pod log monitoring.
	var podLogOpts PodLogMonitorOptions
	e.Options(&podLogOpts)
	podsLogs := SetupPodsLogWatcher(e, cl, cs, podLogOpts)
	SetupErrorLogsWatcher(e, podsLogs)

	// Discover and validate cluster (LVGs ready, enough free space).
	cluster := DiscoverCluster(e, cl)

	e.Run("R1", func(e envtesting.E) {
		var testID TestId
		e.Options(&testID)
		SetupDisklessToDiskfulReplica(e, cl, testID.ResourceName("0"), cluster.Nodes[0], 0, cluster.AllocateSize)
	})

	e.Run("R2", func(e envtesting.E) {
		if len(cluster.Nodes) < 2 {
			e.Fatalf("R2 requires at least 2 nodes, got %d", len(cluster.Nodes))
		}

		var testID TestId
		e.Options(&testID)

		drbdr0, _ := SetupDisklessToDiskfulReplica(e, cl, testID.ResourceName("0"), cluster.Nodes[0], 0, cluster.AllocateSize)
		drbdr1, _ := SetupDisklessToDiskfulReplica(e, cl, testID.ResourceName("1"), cluster.Nodes[1], 1, cluster.AllocateSize)

		SetupPeering(e, cl, drbdr0, drbdr1)
	})

	// e.Run("R3", func(e envtesting.E) {
	// })

	// e.Run("R4", func(e envtesting.E) {
	// })
}

// SetupDisklessToDiskfulReplica creates a full single-node replica: diskless
// DRBDResource -> wait configured -> LLV -> wait created -> patch to diskful
// -> wait configured. Returns the configured DRBDResource and LLV.
func SetupDisklessToDiskfulReplica(
	e envtesting.E,
	cl client.WithWatch,
	name string,
	node ClusterNode,
	nodeID uint8,
	size resource.Quantity,
) (*v1alpha1.DRBDResource, *snc.LVMLogicalVolume) {
	var timeouts Timeouts
	e.Options(&timeouts)

	// Create DRBDResource (Diskless), wait for configured.
	drbdr := SetupResource(
		e.ScopeWithTimeout(timeouts.DRBDRConfiguredDuration()),
		cl,
		newDRBDResourceDiskless(name, node.Name, nodeID),
		isDRBDRTerminal,
	)
	assertDRBDRConfigured(e, drbdr)
	if len(drbdr.Status.Addresses) == 0 {
		e.Fatalf("DRBDResource %q has no addresses after configured", name)
	}

	// Create LLV, wait for created.
	llv := SetupResource(
		e.ScopeWithTimeout(timeouts.LLVCreatedDuration()),
		cl,
		newLLV(name, size, node.LVG.Name),
		isLLVCreated,
	)

	// Patch DRBDResource to Diskful, wait for configured.
	drbdr = SetupResourcePatch(
		e.ScopeWithTimeout(timeouts.DRBDRConfiguredDuration()),
		cl,
		client.ObjectKey{Name: name},
		changeDRBDResourceToDiskful(name, size),
		isDRBDRTerminal,
	)
	assertDRBDRConfigured(e, drbdr)

	return drbdr, llv
}

// SetupPeering links two DRBDResources as peers of each other.
// Returns the updated resources after peering is configured.
func SetupPeering(
	e envtesting.E,
	cl client.WithWatch,
	drbdr0, drbdr1 *v1alpha1.DRBDResource,
) (*v1alpha1.DRBDResource, *v1alpha1.DRBDResource) {
	var timeouts Timeouts
	e.Options(&timeouts)

	sharedSecret := "e2e-test-shared-secret"

	drbdr0 = SetupResourcePatch(
		e.ScopeWithTimeout(timeouts.DRBDRConfiguredDuration()),
		cl,
		client.ObjectKey{Name: drbdr0.Name},
		func(d *v1alpha1.DRBDResource) {
			d.Spec.Peers = []v1alpha1.DRBDResourcePeer{{
				Name:            drbdr1.Spec.NodeName,
				Type:            drbdr1.Spec.Type,
				NodeID:          drbdr1.Spec.NodeID,
				Protocol:        v1alpha1.DRBDProtocolC,
				SharedSecret:    sharedSecret,
				SharedSecretAlg: v1alpha1.SharedSecretAlgDummyForTest,
				Paths:           addressesToPaths(drbdr1.Status.Addresses),
			}}
		},
		isDRBDRTerminal,
	)
	assertDRBDRConfigured(e, drbdr0)
	assertDRBDRHasPeer(e, drbdr0, drbdr1.Spec.NodeName, drbdr1.Spec.NodeID)

	drbdr1 = SetupResourcePatch(
		e.ScopeWithTimeout(timeouts.DRBDRConfiguredDuration()),
		cl,
		client.ObjectKey{Name: drbdr1.Name},
		func(d *v1alpha1.DRBDResource) {
			d.Spec.Peers = []v1alpha1.DRBDResourcePeer{{
				Name:            drbdr0.Spec.NodeName,
				Type:            drbdr0.Spec.Type,
				NodeID:          drbdr0.Spec.NodeID,
				Protocol:        v1alpha1.DRBDProtocolC,
				SharedSecret:    sharedSecret,
				SharedSecretAlg: v1alpha1.SharedSecretAlgDummyForTest,
				Paths:           addressesToPaths(drbdr0.Status.Addresses),
			}}
		},
		isDRBDRTerminal,
	)
	assertDRBDRConfigured(e, drbdr1)
	assertDRBDRHasPeer(e, drbdr1, drbdr0.Spec.NodeName, drbdr0.Spec.NodeID)

	return drbdr0, drbdr1
}

func assertDRBDRHasPeer(e envtesting.E, drbdr *v1alpha1.DRBDResource, peerName string, peerNodeID uint8) {
	for _, peer := range drbdr.Status.Peers {
		if peer.Name == peerName && peer.NodeID == uint(peerNodeID) {
			return
		}
	}
	e.Fatalf("DRBDResource %q has no peer with name %q and nodeID %d in status", drbdr.Name, peerName, peerNodeID)
}

func newDRBDResourceDiskless(name, nodeName string, nodeID uint8) *v1alpha1.DRBDResource {
	return &v1alpha1.DRBDResource{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.DRBDResourceSpec{
			NodeName:       nodeName,
			State:          v1alpha1.DRBDResourceStateUp,
			SystemNetworks: []string{"Internal"},
			NodeID:         nodeID,
			Role:           v1alpha1.DRBDRoleSecondary,
			Type:           v1alpha1.DRBDResourceTypeDiskless,
		},
	}
}

func newLLV(name string, size resource.Quantity, lvgName string) *snc.LVMLogicalVolume {
	return &snc.LVMLogicalVolume{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: snc.LVMLogicalVolumeSpec{
			ActualLVNameOnTheNode: name,
			Type:                  "Thick",
			Size:                  size.String(),
			LVMVolumeGroupName:    lvgName,
		},
	}
}

func changeDRBDResourceToDiskful(llvName string, size resource.Quantity) func(*v1alpha1.DRBDResource) {
	return func(d *v1alpha1.DRBDResource) {
		d.Spec.Type = v1alpha1.DRBDResourceTypeDiskful
		d.Spec.LVMLogicalVolumeName = llvName
		d.Spec.Size = &size
	}
}

// isDRBDRTerminal returns true when the Configured condition has reached a
// terminal state (Status=True or Status=False) for the current generation.
func isDRBDRTerminal(drbdr *v1alpha1.DRBDResource) bool {
	for _, cond := range drbdr.Status.Conditions {
		if cond.Type != v1alpha1.DRBDResourceCondConfiguredType {
			continue
		}
		if cond.ObservedGeneration < drbdr.Generation {
			return false
		}
		return cond.Status == metav1.ConditionTrue || cond.Status == metav1.ConditionFalse
	}
	return false
}

// assertDRBDRConfigured fails the test if the DRBDResource Configured
// condition is not True.
func assertDRBDRConfigured(e envtesting.E, drbdr *v1alpha1.DRBDResource) {
	for _, cond := range drbdr.Status.Conditions {
		if cond.Type != v1alpha1.DRBDResourceCondConfiguredType {
			continue
		}
		if cond.Status != metav1.ConditionTrue {
			e.Fatalf("DRBDResource %q Configured condition is %s (reason: %s, message: %s)",
				drbdr.Name, cond.Status, cond.Reason, cond.Message)
		}
		return
	}
	e.Fatalf("DRBDResource %q has no Configured condition", drbdr.Name)
}

func isLLVCreated(llv *snc.LVMLogicalVolume) bool {
	return llv.Status != nil && llv.Status.Phase == snc.PhaseCreated
}

func addressesToPaths(addrs []v1alpha1.DRBDResourceAddressStatus) []v1alpha1.DRBDResourcePath {
	paths := make([]v1alpha1.DRBDResourcePath, 0, len(addrs))
	for _, addr := range addrs {
		paths = append(paths, v1alpha1.DRBDResourcePath{
			SystemNetworkName: addr.SystemNetworkName,
			Address:           addr.Address,
		})
	}
	return paths
}
