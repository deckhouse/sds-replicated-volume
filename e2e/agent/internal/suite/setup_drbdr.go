package suite

import (
	"fmt"
	"time"

	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/etesting"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DRBDResourcesOptions holds the config section for SetupDRBDResources.
type DRBDResourcesOptions struct {
	Nodes []NodeLVG `json:"nodes"`
}

// SetupDRBDResources Provides DRBDResource objects for each configured node.
// It reads the "DRBDResourcesOptions" config section, discovers nodes and LVGs,
// creates LLVs and DRBDResources. Each resource is type=Diskful, size=100Mi,
// systemNetworks=["Internal"], state=Up, role=Secondary, with no peers.
// Resources are polled until status.addresses is populated and condition
// Configured=True. Cleaned up after test.
func SetupDRBDResources(
	e *etesting.E,
	cl client.Client,
) []*v1alpha1.DRBDResource {
	// Require: read and validate config.
	var opts DRBDResourcesOptions
	e.Options(&opts)

	if len(opts.Nodes) < 2 {
		e.Fatalf("require: need at least 2 nodes, got %d", len(opts.Nodes))
	}
	for i, n := range opts.Nodes {
		if n.Name == "" {
			e.Fatalf("require: nodes[%d].name must not be empty", i)
		}
		if n.LVGName == "" {
			e.Fatalf("require: nodes[%d].lvgName must not be empty", i)
		}
	}

	// Discover existing nodes and LVGs.
	nodeNames := make([]string, len(opts.Nodes))
	for i, n := range opts.Nodes {
		nodeNames[i] = n.Name
	}
	_ = DiscoverNodes(e, cl, nodeNames)
	lvgs := DiscoverLVGs(e, cl, opts.Nodes)

	// Create LLVs.
	llvs := SetupLLVs(e, cl, lvgs)

	// Create DRBDResources.
	testID := e.TestID()
	size := resource.MustParse("100Mi")
	result := make([]*v1alpha1.DRBDResource, 0, len(opts.Nodes))

	for i, nodeName := range nodeNames {
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
				LVMLogicalVolumeName: llvs[i].Name,
			},
		}

		// Discover: check if already exists.
		existing := &v1alpha1.DRBDResource{}
		err := cl.Get(e.Context(), client.ObjectKey{Name: drbdrName}, existing)
		if err == nil {
			if existing.Spec.NodeName != nodeName {
				e.Fatalf("DRBDResource %q already exists but on node %q, expected %q",
					drbdrName, existing.Spec.NodeName, nodeName)
			}
		} else {
			// Arrange: create.
			if err := cl.Create(e.Context(), drbdr); err != nil {
				e.Fatalf("creating DRBDResource %q: %v", drbdrName, err)
			}
		}

		// Cleanup.
		e.Cleanup(func() {
			cleanupDRBDResource(e, cl, drbdrName)
		})

		result = append(result, drbdr)
	}

	// Assert: wait for addresses and Configured=True on all resources.
	for _, drbdr := range result {
		waitForDRBDResourceAddresses(e, cl, drbdr.Name)
		waitForDRBDResourceCondition(e, cl, drbdr.Name,
			v1alpha1.DRBDResourceCondConfiguredType, metav1.ConditionTrue)
	}

	return result
}

// cleanupDRBDResource deletes a DRBDResource and waits for the agent to remove
// its finalizer before deleting.
func cleanupDRBDResource(e *etesting.E, cl client.Client, name string) {
	drbdr := &v1alpha1.DRBDResource{}
	if err := cl.Get(e.Context(), client.ObjectKey{Name: name}, drbdr); err != nil {
		e.Errorf("getting DRBDResource %q for cleanup: %v", name, err)
		return
	}

	// Set state to Down so the agent brings down the DRBD resource.
	drbdr.Spec.State = v1alpha1.DRBDResourceStateDown
	if err := cl.Update(e.Context(), drbdr); err != nil {
		e.Errorf("setting DRBDResource %q state to Down: %v", name, err)
	}

	// Wait for agent to remove its finalizer.
	waitForDRBDResourceNoFinalizer(e, cl, name, v1alpha1.AgentFinalizer)

	// Delete.
	if err := cl.Delete(e.Context(), drbdr); err != nil {
		e.Errorf("deleting DRBDResource %q: %v", name, err)
	}
}

// waitForDRBDResourceNoFinalizer polls a DRBDResource until the specified
// finalizer is removed.
func waitForDRBDResourceNoFinalizer(
	e *etesting.E,
	cl client.Client,
	name string,
	finalizer string,
) {
	deadline := defaultTimeout
	poll(e, deadline, func() bool {
		drbdr := &v1alpha1.DRBDResource{}
		if err := cl.Get(e.Context(), client.ObjectKey{Name: name}, drbdr); err != nil {
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
func poll(e *etesting.E, timeout time.Duration, check func() bool, msgFmt string, args ...any) {
	deadline := time.Now().Add(timeout)
	for {
		if check() {
			return
		}
		if time.Now().After(deadline) {
			e.Errorf("timed out waiting for "+msgFmt, args...)
			return
		}
		time.Sleep(defaultPollInterval)
	}
}
