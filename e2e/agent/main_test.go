package agent

import (
	"testing"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	csync "github.com/deckhouse/sds-replicated-volume/lib/go/common/sync"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/deckhouse/sds-replicated-volume/e2e/agent/internal/suite"
)

func TestDRBDResource(t *testing.T) {
	e := envtesting.New(t)

	var testID TestId
	e.Options(&testID)

	var timeouts Timeouts
	e.Options(&timeouts)

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
		SetupDisklessToDiskfulReplica(e, cl, timeouts, testID.ResourceName("0"), cluster.Nodes[0], 0, cluster.AllocateSize)
	})

	e.Run("R2", func(e envtesting.E) {
		if len(cluster.Nodes) < 2 {
			e.Fatalf("R2 requires at least 2 nodes, got %d", len(cluster.Nodes))
		}
		node0, node1 := cluster.Nodes[0], cluster.Nodes[1]
		name0, name1 := testID.ResourceName("0"), testID.ResourceName("1")

		r0 := SetupDisklessToDiskfulReplica(e, cl, timeouts, name0, node0, 0, cluster.AllocateSize)
		r1 := SetupDisklessToDiskfulReplica(e, cl, timeouts, name1, node1, 1, cluster.AllocateSize)

		// Fetch fresh addresses for peering.
		fresh0 := &v1alpha1.DRBDResource{}
		if err := cl.Get(e.Context(), client.ObjectKey{Name: name0}, fresh0); err != nil {
			e.Fatalf("getting DRBDResource %q for peering: %v", name0, err)
		}
		fresh1 := &v1alpha1.DRBDResource{}
		if err := cl.Get(e.Context(), client.ObjectKey{Name: name1}, fresh1); err != nil {
			e.Fatalf("getting DRBDResource %q for peering: %v", name1, err)
		}

		sharedSecret := "e2e-test-shared-secret"

		// Patch peers.
		SetupResourcePatch(e, cl, client.ObjectKey{Name: name0}, func(d *v1alpha1.DRBDResource) {
			d.Spec.Peers = []v1alpha1.DRBDResourcePeer{{
				Name:            node1.Name,
				Type:            v1alpha1.DRBDResourceTypeDiskful,
				NodeID:          1,
				Protocol:        v1alpha1.DRBDProtocolC,
				SharedSecret:    sharedSecret,
				SharedSecretAlg: v1alpha1.SharedSecretAlgDummyForTest,
				Paths:           addressesToPaths(fresh1.Status.Addresses),
			}}
		})
		SetupResourcePatch(e, cl, client.ObjectKey{Name: name1}, func(d *v1alpha1.DRBDResource) {
			d.Spec.Peers = []v1alpha1.DRBDResourcePeer{{
				Name:            node0.Name,
				Type:            v1alpha1.DRBDResourceTypeDiskful,
				NodeID:          0,
				Protocol:        v1alpha1.DRBDProtocolC,
				SharedSecret:    sharedSecret,
				SharedSecretAlg: v1alpha1.SharedSecretAlgDummyForTest,
				Paths:           addressesToPaths(fresh0.Status.Addresses),
			}}
		})

		event0, ok := csync.WaitWithTimeout(e.Context(), timeouts.DRBDRConfiguredDuration(), r0.DRBDRCh, isDRBDRTerminal)
		if !ok {
			e.Fatalf("DRBDResource %q did not reach terminal state after peering", name0)
		}
		assertDRBDRConfigured(e, event0.Object.(*v1alpha1.DRBDResource))

		event1, ok := csync.WaitWithTimeout(e.Context(), timeouts.DRBDRConfiguredDuration(), r1.DRBDRCh, isDRBDRTerminal)
		if !ok {
			e.Fatalf("DRBDResource %q did not reach terminal state after peering", name1)
		}
		assertDRBDRConfigured(e, event1.Object.(*v1alpha1.DRBDResource))
	})

	// e.Run("R3", func(e envtesting.E) {
	// })

	// e.Run("R4", func(e envtesting.E) {
	// })
}

// Replica holds state returned by SetupDisklessToDiskfulReplica for further
// use (e.g. peering in multi-replica tests).
type Replica struct {
	Name    string
	DRBDRCh <-chan watch.Event
}

// SetupDisklessToDiskfulReplica creates a full single-node replica: diskless
// DRBDResource -> wait configured -> LLV -> wait created -> patch to diskful
// -> wait configured. Returns a Replica with the live watcher channel for
// subsequent waits (e.g. peering).
func SetupDisklessToDiskfulReplica(
	e envtesting.E,
	cl client.WithWatch,
	timeouts Timeouts,
	name string,
	node ClusterNode,
	nodeID uint8,
	size resource.Quantity,
) *Replica {
	// Watch before create to not miss events.
	drbdrCh := SetupResourceWatcher(e, cl, types.NamespacedName{Name: name}, &v1alpha1.DRBDResourceList{})
	drbdrCh = SetupWatchLog(e, drbdrCh)

	llvCh := SetupResourceWatcher(e, cl, types.NamespacedName{Name: name}, &snc.LVMLogicalVolumeList{})
	llvCh = SetupWatchLog(e, llvCh)

	// Create DRBDResource (Diskless).
	SetupResource(e, cl, newDRBDResourceDiskless(name, node.Name, nodeID))
	event, ok := csync.WaitWithTimeout(e.Context(), timeouts.DRBDRConfiguredDuration(), drbdrCh, isDRBDRTerminal)
	if !ok {
		e.Fatalf("DRBDResource %q did not reach terminal state", name)
	}
	drbdr := event.Object.(*v1alpha1.DRBDResource)
	assertDRBDRConfigured(e, drbdr)
	if len(drbdr.Status.Addresses) == 0 {
		e.Fatalf("DRBDResource %q has no addresses after configured", name)
	}

	// Create LLV.
	SetupResource(e, cl, newLLV(name, size, node.LVG.Name))
	_, ok = csync.WaitWithTimeout(e.Context(), timeouts.LLVCreatedDuration(), llvCh, isLLVCreated)
	if !ok {
		e.Fatalf("LVMLogicalVolume %q did not reach phase Created", name)
	}

	// Patch DRBDResource to Diskful.
	SetupResourcePatch(e, cl, client.ObjectKey{Name: name}, changeDRBDResourceToDiskful(name, size))
	event, ok = csync.WaitWithTimeout(e.Context(), timeouts.DRBDRConfiguredDuration(), drbdrCh, isDRBDRTerminal)
	if !ok {
		e.Fatalf("DRBDResource %q did not reach terminal state after diskful patch", name)
	}
	assertDRBDRConfigured(e, event.Object.(*v1alpha1.DRBDResource))

	return &Replica{Name: name, DRBDRCh: drbdrCh}
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
func isDRBDRTerminal(event watch.Event) bool {
	drbdr, ok := event.Object.(*v1alpha1.DRBDResource)
	if !ok {
		return false
	}
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

func isLLVCreated(event watch.Event) bool {
	llv, ok := event.Object.(*snc.LVMLogicalVolume)
	if !ok {
		return false
	}
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
