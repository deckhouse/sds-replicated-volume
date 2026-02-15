package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/etesting"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/deckhouse/sds-replicated-volume/e2e/agent/internal/suite"
)

func TestDRBDResource(t *testing.T) {
	e := etesting.New(t)

	// Discover K8s clients.
	cl := DiscoverClient(e)
	cs := DiscoverClientset(e)

	// Start agent pod log monitoring.
	podsLogs := SetupPodsLogWatcher(e, cl, cs)
	SetupErrorLogsWatcher(e, podsLogs)

	e.Run("R1", func(e *etesting.E) {
		var opts ClusterOptions
		e.Options(&opts)
		node := opts.Nodes[0]
		testID := e.TestID()
		drbdrName := fmt.Sprintf("e2e-drbdr-%s-%s", testID, node.Name)
		llvName := drbdrName
		size := resource.MustParse("100Mi")

		SetupResource(e, cl, &v1alpha1.DRBDResource{
			ObjectMeta: metav1.ObjectMeta{Name: drbdrName},
			Spec: v1alpha1.DRBDResourceSpec{
				NodeName:       node.Name,
				State:          v1alpha1.DRBDResourceStateUp,
				SystemNetworks: []string{"Internal"},
				NodeID:         0,
				Role:           v1alpha1.DRBDRoleSecondary,
				Type:           v1alpha1.DRBDResourceTypeDiskless,
			},
		})
		drbdr := waitDRBDRConfigured(e, cl, drbdrName)
		if len(drbdr.Status.Addresses) == 0 {
			e.Fatalf("DRBDResource %q has no addresses after configured", drbdrName)
		}

		SetupResource(e, cl, &snc.LVMLogicalVolume{
			ObjectMeta: metav1.ObjectMeta{Name: llvName},
			Spec: snc.LVMLogicalVolumeSpec{
				ActualLVNameOnTheNode: llvName,
				Type:                  "Thick",
				Size:                  "100Mi",
				LVMVolumeGroupName:    node.LVGName,
			},
		})
		waitLLVCreated(e, cl, llvName)

		SetupResourcePatch(e, cl, client.ObjectKey{Name: drbdrName}, func(d *v1alpha1.DRBDResource) {
			d.Spec.Type = v1alpha1.DRBDResourceTypeDiskful
			d.Spec.LVMLogicalVolumeName = llvName
			d.Spec.Size = &size
		})
		waitDRBDRConfigured(e, cl, drbdrName)
	})

	e.Run("R2", func(e *etesting.E) {
		var opts ClusterOptions
		e.Options(&opts)
		if len(opts.Nodes) < 2 {
			e.Fatalf("R2 requires at least 2 nodes, got %d", len(opts.Nodes))
		}
		node0 := opts.Nodes[0]
		node1 := opts.Nodes[1]
		testID := e.TestID()
		drbdrName0 := fmt.Sprintf("e2e-drbdr-%s-%s", testID, node0.Name)
		drbdrName1 := fmt.Sprintf("e2e-drbdr-%s-%s", testID, node1.Name)
		llvName0 := drbdrName0
		llvName1 := drbdrName1
		size := resource.MustParse("100Mi")

		// Create DRBDResources (Diskless).
		SetupResource(e, cl, &v1alpha1.DRBDResource{
			ObjectMeta: metav1.ObjectMeta{Name: drbdrName0},
			Spec: v1alpha1.DRBDResourceSpec{
				NodeName:       node0.Name,
				State:          v1alpha1.DRBDResourceStateUp,
				SystemNetworks: []string{"Internal"},
				NodeID:         0,
				Role:           v1alpha1.DRBDRoleSecondary,
				Type:           v1alpha1.DRBDResourceTypeDiskless,
			},
		})
		drbdr0 := waitDRBDRConfigured(e, cl, drbdrName0)
		if len(drbdr0.Status.Addresses) == 0 {
			e.Fatalf("DRBDResource %q has no addresses after configured", drbdrName0)
		}

		SetupResource(e, cl, &v1alpha1.DRBDResource{
			ObjectMeta: metav1.ObjectMeta{Name: drbdrName1},
			Spec: v1alpha1.DRBDResourceSpec{
				NodeName:       node1.Name,
				State:          v1alpha1.DRBDResourceStateUp,
				SystemNetworks: []string{"Internal"},
				NodeID:         1,
				Role:           v1alpha1.DRBDRoleSecondary,
				Type:           v1alpha1.DRBDResourceTypeDiskless,
			},
		})
		drbdr1 := waitDRBDRConfigured(e, cl, drbdrName1)
		if len(drbdr1.Status.Addresses) == 0 {
			e.Fatalf("DRBDResource %q has no addresses after configured", drbdrName1)
		}

		// Create LLVs.
		SetupResource(e, cl, &snc.LVMLogicalVolume{
			ObjectMeta: metav1.ObjectMeta{Name: llvName0},
			Spec: snc.LVMLogicalVolumeSpec{
				ActualLVNameOnTheNode: llvName0,
				Type:                  "Thick",
				Size:                  "100Mi",
				LVMVolumeGroupName:    node0.LVGName,
			},
		})
		waitLLVCreated(e, cl, llvName0)

		SetupResource(e, cl, &snc.LVMLogicalVolume{
			ObjectMeta: metav1.ObjectMeta{Name: llvName1},
			Spec: snc.LVMLogicalVolumeSpec{
				ActualLVNameOnTheNode: llvName1,
				Type:                  "Thick",
				Size:                  "100Mi",
				LVMVolumeGroupName:    node1.LVGName,
			},
		})
		waitLLVCreated(e, cl, llvName1)

		// Patch DRBDResources to Diskful.
		SetupResourcePatch(e, cl, client.ObjectKey{Name: drbdrName0}, func(d *v1alpha1.DRBDResource) {
			d.Spec.Type = v1alpha1.DRBDResourceTypeDiskful
			d.Spec.LVMLogicalVolumeName = llvName0
			d.Spec.Size = &size
		})
		waitDRBDRConfigured(e, cl, drbdrName0)

		SetupResourcePatch(e, cl, client.ObjectKey{Name: drbdrName1}, func(d *v1alpha1.DRBDResource) {
			d.Spec.Type = v1alpha1.DRBDResourceTypeDiskful
			d.Spec.LVMLogicalVolumeName = llvName1
			d.Spec.Size = &size
		})
		waitDRBDRConfigured(e, cl, drbdrName1)

		// Fetch addresses for peering.
		fresh0 := &v1alpha1.DRBDResource{}
		if err := cl.Get(e.Context(), client.ObjectKey{Name: drbdrName0}, fresh0); err != nil {
			e.Fatalf("getting DRBDResource %q for peering: %v", drbdrName0, err)
		}
		fresh1 := &v1alpha1.DRBDResource{}
		if err := cl.Get(e.Context(), client.ObjectKey{Name: drbdrName1}, fresh1); err != nil {
			e.Fatalf("getting DRBDResource %q for peering: %v", drbdrName1, err)
		}

		sharedSecret := "e2e-test-shared-secret"

		// Patch peers.
		SetupResourcePatch(e, cl, client.ObjectKey{Name: drbdrName0}, func(d *v1alpha1.DRBDResource) {
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
		SetupResourcePatch(e, cl, client.ObjectKey{Name: drbdrName1}, func(d *v1alpha1.DRBDResource) {
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

		waitDRBDRConfigured(e, cl, drbdrName0)
		waitDRBDRConfigured(e, cl, drbdrName1)
	})

	// e.Run("R3", func(e *etesting.E) {
	// 	SetupResource(e, cl, &v1alpha1.DRBDResource{})
	// 	waitDRBDRConfigured(e, cl, "TODO")

	// 	SetupResource(e, cl, &v1alpha1.DRBDResource{})
	// 	waitDRBDRConfigured(e, cl, "TODO")

	// 	SetupResource(e, cl, &v1alpha1.DRBDResource{})
	// 	waitDRBDRConfigured(e, cl, "TODO")
	// })

	// e.Run("R4", func(e *etesting.E) {
	// 	SetupResource(e, cl, &v1alpha1.DRBDResource{})
	// 	waitDRBDRConfigured(e, cl, "TODO")

	// 	SetupResource(e, cl, &v1alpha1.DRBDResource{})
	// 	waitDRBDRConfigured(e, cl, "TODO")

	// 	SetupResource(e, cl, &v1alpha1.DRBDResource{})
	// 	waitDRBDRConfigured(e, cl, "TODO")

	// 	SetupResource(e, cl, &v1alpha1.DRBDResource{})
	// 	waitDRBDRConfigured(e, cl, "TODO")
	// })
}

func waitDRBDRConfigured(e *etesting.E, cl client.Client, drbdrName string) *v1alpha1.DRBDResource {
	waitCtx, cancel := context.WithTimeout(e.Context(), 120*time.Second)
	defer cancel()

	w := SetupResourceWatcher(waitCtx, e, cl,
		types.NamespacedName{Name: drbdrName},
		func() client.ObjectList { return &v1alpha1.DRBDResourceList{} },
	)

	for event := range w.Chan(waitCtx) {
		drbdr, ok := event.Object.(*v1alpha1.DRBDResource)
		if !ok {
			continue
		}
		for _, cond := range drbdr.Status.Conditions {
			if cond.Type != v1alpha1.DRBDResourceCondConfiguredType {
				continue
			}
			if cond.ObservedGeneration < drbdr.Generation {
				break
			}
			if cond.Status == metav1.ConditionTrue {
				return drbdr
			}
			break
		}
	}

	e.Fatalf("timed out waiting for DRBDResource %q to be configured", drbdrName)
	return nil
}

func waitLLVCreated(e *etesting.E, cl client.Client, llvName string) {
	waitCtx, cancel := context.WithTimeout(e.Context(), 120*time.Second)
	defer cancel()

	w := SetupResourceWatcher(waitCtx, e, cl,
		types.NamespacedName{Name: llvName},
		func() client.ObjectList { return &snc.LVMLogicalVolumeList{} },
	)

	for event := range w.Chan(waitCtx) {
		llv, ok := event.Object.(*snc.LVMLogicalVolume)
		if !ok {
			continue
		}
		if llv.Status != nil && llv.Status.Phase == snc.PhaseCreated {
			return
		}
	}

	e.Fatalf("timed out waiting for LVMLogicalVolume %q to reach phase Created", llvName)
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
