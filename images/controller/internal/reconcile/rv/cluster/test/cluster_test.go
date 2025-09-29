package clustertest

import (
	"fmt"
	"hash/fnv"
	"testing"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/reconcile/rv/cluster"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	testRVName           = "testRVName"
	testRVNameIrrelevant = "testRVNameIrrelevant"
	testRVRName          = "testRVRName"
	testRVRName2         = "testRVRName2"
	testRVRName3         = "testRVRName3"
	testNodeName         = "testNodeName"
	testSharedSecret     = "testSharedSecret"
	testPortRng          = testPortRange{7000, 9000}
)

type reconcileTestCase struct {
	name string

	existingRVRs []v1alpha2.ReplicatedVolumeReplica
	existingLLVs map[LLVPhysicalKey]*snc.LVMLogicalVolume

	replicaConfigs []testReplicaConfig
	size           int64

	expectedAction ActionMatcher
	expectedErr    error
}

var reconcileTestCases []reconcileTestCase = []reconcileTestCase{
	{
		name: "empty cluster - 0 replicas - no actions",
	},
	{
		name: "empty cluster - 1 diskless replicas - 1 create&wait action",
		replicaConfigs: []testReplicaConfig{
			{
				NodeName: testNodeName,
			},
		},
		expectedAction: ActionsMatcher{
			CreateReplicatedVolumeReplicaMatcher{}, // TODO
		},
	},
	{
		name: "1 rvr - 0 replicas - delete action",
		existingRVRs: []v1alpha2.ReplicatedVolumeReplica{
			{
				ObjectMeta: v1.ObjectMeta{
					Name: testRVRName,
				},
				Spec: v1alpha2.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: testRVName,
				},
			},
		},
		expectedAction: DeleteReplicatedVolumeReplicaMatcher{RVRName: testRVRName},
	},
	{
		name: "2 rvrs - 0 replicas - 2 delete actions",
		existingRVRs: []v1alpha2.ReplicatedVolumeReplica{
			{
				ObjectMeta: v1.ObjectMeta{
					Name: testRVRName,
				},
				Spec: v1alpha2.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: testRVName,
				},
			},
			{
				ObjectMeta: v1.ObjectMeta{
					Name: testRVRName2,
				},
				Spec: v1alpha2.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: testRVName,
				},
			},
		},
		expectedAction: ParallelActionsMatcher{
			DeleteReplicatedVolumeReplicaMatcher{RVRName: testRVRName},
			DeleteReplicatedVolumeReplicaMatcher{RVRName: testRVRName2},
		},
	},
	{
		name: "3 rvrs (1 irrelevant) - 0 replicas - 2 delete actions",
		existingRVRs: []v1alpha2.ReplicatedVolumeReplica{
			{
				ObjectMeta: v1.ObjectMeta{
					Name: testRVRName,
				},
				Spec: v1alpha2.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: testRVName,
				},
			},
			{
				ObjectMeta: v1.ObjectMeta{
					Name: testRVRName2,
				},
				Spec: v1alpha2.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: testRVName,
				},
			},
			{
				ObjectMeta: v1.ObjectMeta{
					Name: testRVRName3,
				},
				Spec: v1alpha2.ReplicatedVolumeReplicaSpec{
					// irrelevant rv name
					ReplicatedVolumeName: testRVNameIrrelevant,
				},
			},
		},
		expectedAction: ParallelActionsMatcher{
			DeleteReplicatedVolumeReplicaMatcher{RVRName: testRVRName},
			DeleteReplicatedVolumeReplicaMatcher{RVRName: testRVRName2},
		},
	},
}

func TestClusterReconcile(t *testing.T) {
	for i := range reconcileTestCases {
		tc := &reconcileTestCases[i]
		t.Run(
			tc.name,
			func(t *testing.T) { runClusterReconcileTestCase(t, tc) },
		)
	}
}

func runClusterReconcileTestCase(t *testing.T, tc *reconcileTestCase) {
	// arrange
	rvrClient := NewMockRVRClient(tc.existingRVRs)
	llvClient := NewMockLLVClient(tc.existingLLVs)

	clr := cluster.New(t.Context(), rvrClient, rvrClient, testPortRng, llvClient, testRVName, tc.size, testSharedSecret)

	for _, rCfg := range tc.replicaConfigs {
		r := clr.AddReplica(rCfg.NodeName, rCfg.GenIPv4(), false, 0, 0)
		if rCfg.Volume != nil {
			r.AddVolume(rCfg.Volume.VGName, rCfg.Volume.ActualVgNameOnTheNode, rCfg.Volume.LLVProps)
		}
	}

	// act
	action, err := clr.Reconcile()

	// assert
	if tc.expectedErr != err {
		t.Errorf("expected reconile error '%v', got '%v'", tc.expectedErr, err)
	}

	if action == nil && tc.expectedAction != nil {
		t.Errorf("expected '%T', got no actions", tc.expectedAction)
	} else if action != nil && tc.expectedAction == nil {
		t.Errorf("expected no actions, got '%T'", action)
	} else if tc.expectedAction != nil {
		err := tc.expectedAction.Match(action)
		if err != nil {
			t.Error(err)
		}
	}
}

type testReplicaConfig struct {
	NodeName string
	IPv4     string
	Volume   *testVolumeConfig
}

func (cfg testReplicaConfig) GenIPv4() string {
	if cfg.IPv4 != "" {
		return cfg.IPv4
	}

	// generate private IP as a hash from [testReplicaConfig.NodeName]

	h := fnv.New32a()
	_, _ = h.Write([]byte(cfg.NodeName))
	v := h.Sum32()

	o2 := byte(v >> 16)
	o3 := byte(v >> 8)
	o4 := byte(v)

	// avoid .0 and .255 for host octet
	if o4 == 0 || o4 == 255 {
		o4 = 1 + o4%253
	}
	return fmt.Sprintf("10.%d.%d.%d", o2, o3, o4)

}

type testVolumeConfig struct {
	VGName                string
	ActualVgNameOnTheNode string
	LLVProps              cluster.LLVProps
}

type testPortRange struct {
	MinPort, MaxPort uint
}

func (r testPortRange) PortMinMax() (uint, uint) {
	return r.MinPort, r.MaxPort
}

var _ cluster.DRBDPortRange = testPortRange{}
