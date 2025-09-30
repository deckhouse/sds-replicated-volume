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
	testRVName                = "testRVName"
	testRVRName               = "testRVRName"
	testLLVName               = "testLLVName"
	testNodeName              = "testNodeName"
	testSharedSecret          = "testSharedSecret"
	testVGName                = "testVGName"
	testActualVGNameOnTheNode = "testActualVGNameOnTheNode"
	testPortRng               = testPortRange{7000, 9000}
	testSize                  = int64(500 * 1024 * 1024)
	testSizeStr               = "500Mi"
)

type reconcileTestCase struct {
	name string

	existingRVRs []v1alpha2.ReplicatedVolumeReplica
	existingLLVs map[LLVPhysicalKey]*snc.LVMLogicalVolume

	replicaConfigs []testReplicaConfig
	rvName         *string
	size           *int64

	expectedAction ActionMatcher
	expectedErr    error
}

// TODO: Do not take ownership over llv, without special label/owner ref of controller,
// for new LLVs - always create it,
// during reconcile - manage (incl. deletion) all LLV with this label.
// Currently some LLVs may hang, when there's no diskful rvr in same LVG

var reconcileTestCases []reconcileTestCase = []reconcileTestCase{
	{
		name: "empty cluster - 1 replica - 1 create llv & wait llv & create rvr & wait rvr",
		replicaConfigs: []testReplicaConfig{
			{
				NodeName: testNodeName,
				Volume: &testVolumeConfig{
					VGName:                testVGName,
					ActualVgNameOnTheNode: testActualVGNameOnTheNode,
					LLVProps:              cluster.ThickVolumeProps{},
				},
			},
		},
		expectedAction: ActionsMatcher{
			CreateLVMLogicalVolumeMatcher{
				LLVSpec: snc.LVMLogicalVolumeSpec{
					ActualLVNameOnTheNode: testRVName,
					Type:                  "Thick",
					Size:                  testSizeStr,
					LVMVolumeGroupName:    testVGName,
					Thick:                 &snc.LVMLogicalVolumeThickSpec{},
				},
				OnMatch: func(action cluster.CreateLVMLogicalVolume) {
					action.LVMLogicalVolume.Name = testLLVName
				},
			},
			WaitLVMLogicalVolumeMatcher{LLVName: testLLVName},
			CreateReplicatedVolumeReplicaMatcher{
				RVRSpec: v1alpha2.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: testRVName,
					NodeName:             testNodeName,
					NodeAddress: v1alpha2.Address{
						IPv4: generateIPv4(testNodeName),
						Port: testPortRng.MinPort,
					},
					SharedSecret: testSharedSecret,
					Volumes: []v1alpha2.Volume{
						{
							Number: 0,
							Device: 0,
							Disk: fmt.Sprintf(
								"/dev/%s/%s",
								testActualVGNameOnTheNode, testRVName,
							),
						},
					},
				},
				OnMatch: func(action cluster.CreateReplicatedVolumeReplica) {
					action.ReplicatedVolumeReplica.Name = testRVRName
				},
			},
			WaitReplicatedVolumeReplicaMatcher{RVRName: testRVRName},
		},
	},
	{
		name: "existing LLV - 1 replica - patch llv & create rvr & wait rvr",
		existingLLVs: map[LLVPhysicalKey]*snc.LVMLogicalVolume{
			{nodeName: testNodeName, actualVGNameOnTheNode: testActualVGNameOnTheNode, actualLVNameOnTheNode: testRVName}: {
				ObjectMeta: v1.ObjectMeta{Name: testLLVName},
				Spec: snc.LVMLogicalVolumeSpec{
					ActualLVNameOnTheNode: testRVName,
					Size:                  testSizeStr,
					LVMVolumeGroupName:    testVGName,
					Thick:                 &snc.LVMLogicalVolumeThickSpec{},
					Type:                  "Thick",
				},
			},
		},
		replicaConfigs: []testReplicaConfig{
			{
				NodeName: testNodeName,
				Volume: &testVolumeConfig{
					VGName:                testVGName,
					ActualVgNameOnTheNode: testActualVGNameOnTheNode,
					LLVProps:              cluster.ThickVolumeProps{},
				},
			},
		},
		expectedAction: ActionsMatcher{
			LLVPatchMatcher{LLVName: testLLVName},
			CreateReplicatedVolumeReplicaMatcher{
				RVRSpec: v1alpha2.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: testRVName,
					NodeName:             testNodeName,
					NodeAddress: v1alpha2.Address{
						IPv4: generateIPv4(testNodeName),
						Port: testPortRng.MinPort,
					},
					SharedSecret: testSharedSecret,
					Volumes: []v1alpha2.Volume{
						{
							Number: 0,
							Device: 0,
							Disk: fmt.Sprintf(
								"/dev/%s/%s",
								testActualVGNameOnTheNode, testRVName,
							),
						},
					},
				},
				OnMatch: func(action cluster.CreateReplicatedVolumeReplica) {
					action.ReplicatedVolumeReplica.Name = testRVRName
				},
			},
			WaitReplicatedVolumeReplicaMatcher{RVRName: testRVRName},
		},
	},
	{
		name: "add 1 diskful and fix existing diskless - (parallel) create&wait llv + patch&wait rvr; then create&wait rvr",
		existingRVRs: []v1alpha2.ReplicatedVolumeReplica{
			{
				ObjectMeta: v1.ObjectMeta{Name: testRVRName},
				Spec: v1alpha2.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: testRVName,
					NodeName:             "node-b",
					NodeId:               1,
					NodeAddress: v1alpha2.Address{
						IPv4: "192.0.2.1", // wrong, will be fixed to generateIPv4("node-b")
						Port: testPortRng.MinPort,
					},
					SharedSecret: testSharedSecret,
					Volumes:      []v1alpha2.Volume{{Number: 0, Device: 0}}, // diskless
				},
			},
		},
		replicaConfigs: []testReplicaConfig{
			{ // diskful to add
				NodeName: "node-a",
				Volume: &testVolumeConfig{
					VGName:                testVGName,
					ActualVgNameOnTheNode: testActualVGNameOnTheNode,
					LLVProps:              cluster.ThickVolumeProps{},
				},
			},
			{ // diskless to fix
				NodeName: "node-b",
			},
		},
		expectedAction: ActionsMatcher{
			ParallelActionsMatcher{
				ActionsMatcher{
					CreateLVMLogicalVolumeMatcher{
						LLVSpec: snc.LVMLogicalVolumeSpec{
							ActualLVNameOnTheNode: testRVName,
							Type:                  "Thick",
							Size:                  testSizeStr,
							LVMVolumeGroupName:    testVGName,
							Thick:                 &snc.LVMLogicalVolumeThickSpec{},
						},
						OnMatch: func(action cluster.CreateLVMLogicalVolume) {
							action.LVMLogicalVolume.Name = testLLVName
						},
					},
					WaitLVMLogicalVolumeMatcher{LLVName: testLLVName},
				},
				ActionsMatcher{
					RVRPatchMatcher{RVRName: testRVRName},
					WaitReplicatedVolumeReplicaMatcher{RVRName: testRVRName},
				},
			},
			CreateReplicatedVolumeReplicaMatcher{
				RVRSpec: v1alpha2.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: testRVName,
					NodeName:             "node-a",
					NodeAddress: v1alpha2.Address{
						IPv4: generateIPv4("node-a"),
						Port: testPortRng.MinPort,
					},
					SharedSecret: testSharedSecret,
					Volumes: []v1alpha2.Volume{
						{
							Number: 0,
							Device: 0,
							Disk:   fmt.Sprintf("/dev/%s/%s", testActualVGNameOnTheNode, testRVName),
						},
					},
				},
				OnMatch: func(action cluster.CreateReplicatedVolumeReplica) {
					action.ReplicatedVolumeReplica.Name = testRVRName
				},
			},
			WaitReplicatedVolumeReplicaMatcher{RVRName: testRVRName},
		},
	},
	{
		name: "add 1 diskful and delete 1 orphan rvr - (parallel) create&wait llv; then create&wait rvr and delete orphan",
		existingRVRs: []v1alpha2.ReplicatedVolumeReplica{
			{
				ObjectMeta: v1.ObjectMeta{Name: testRVRName},
				Spec: v1alpha2.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: testRVName,
					NodeName:             "old-node",
					NodeId:               3,
					NodeAddress:          v1alpha2.Address{IPv4: generateIPv4("old-node"), Port: testPortRng.MinPort},
					SharedSecret:         testSharedSecret,
					Volumes: []v1alpha2.Volume{{
						Number: 0,
						Device: 0,
						Disk:   fmt.Sprintf("/dev/%s/%s", testActualVGNameOnTheNode, testRVName),
					}},
				},
			},
		},
		replicaConfigs: []testReplicaConfig{
			{
				NodeName: "node-a",
				Volume: &testVolumeConfig{
					VGName:                testVGName,
					ActualVgNameOnTheNode: testActualVGNameOnTheNode,
					LLVProps:              cluster.ThickVolumeProps{},
				},
			},
		},
		expectedAction: ActionsMatcher{
			CreateLVMLogicalVolumeMatcher{
				LLVSpec: snc.LVMLogicalVolumeSpec{
					ActualLVNameOnTheNode: testRVName,
					Type:                  "Thick",
					Size:                  testSizeStr,
					LVMVolumeGroupName:    testVGName,
					Thick:                 &snc.LVMLogicalVolumeThickSpec{},
				},
				OnMatch: func(action cluster.CreateLVMLogicalVolume) {
					action.LVMLogicalVolume.Name = testLLVName
				},
			},
			WaitLVMLogicalVolumeMatcher{LLVName: testLLVName},
			CreateReplicatedVolumeReplicaMatcher{
				RVRSpec: v1alpha2.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: testRVName,
					NodeName:             "node-a",
					NodeAddress:          v1alpha2.Address{IPv4: generateIPv4("node-a"), Port: testPortRng.MinPort},
					SharedSecret:         testSharedSecret,
					Volumes: []v1alpha2.Volume{{
						Number: 0,
						Device: 0,
						Disk:   fmt.Sprintf("/dev/%s/%s", testActualVGNameOnTheNode, testRVName),
					}},
				},
				OnMatch: func(action cluster.CreateReplicatedVolumeReplica) {
					action.ReplicatedVolumeReplica.Name = testRVRName
				},
			},
			WaitReplicatedVolumeReplicaMatcher{RVRName: testRVRName},
			DeleteReplicatedVolumeReplicaMatcher{RVRName: testRVRName},
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

func ifDefined[T any](p *T, def T) T {
	if p != nil {
		return *p
	}
	return def
}

func runClusterReconcileTestCase(t *testing.T, tc *reconcileTestCase) {
	// arrange
	rvrClient := NewMockRVRClient(tc.existingRVRs)
	llvClient := NewMockLLVClient(tc.existingLLVs)

	clr := cluster.New(
		t.Context(),
		rvrClient,
		rvrClient,
		testPortRng,
		llvClient,
		ifDefined(tc.rvName, testRVName),
		ifDefined(tc.size, testSize),
		testSharedSecret,
	)

	for _, rCfg := range tc.replicaConfigs {
		r := clr.AddReplica(rCfg.NodeName, generateIPv4(rCfg.NodeName), false, 0, 0)
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
	Volume   *testVolumeConfig
}

func generateIPv4(nodeName string) string {
	// generate private IP as a hash from [testReplicaConfig.NodeName]

	h := fnv.New32a()
	_, _ = h.Write([]byte(nodeName))
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
