package cluster_test

import (
	"fmt"
	"hash/fnv"
	"log/slog"
	"testing"

	"github.com/deckhouse/sds-common-lib/utils"
	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	cluster "github.com/deckhouse/sds-replicated-volume/images/controller/internal/reconcile/rv/cluster"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type LLVPhysicalKey struct {
	nodeName, actualLVNameOnTheNode string
}

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
	testSizeSmallStr          = "200Mi"
)

type reconcileTestCase struct {
	existingRVRs []v1alpha2.ReplicatedVolumeReplica
	existingLLVs map[LLVPhysicalKey]*snc.LVMLogicalVolume

	replicaConfigs []testReplicaConfig
	rvName         *string

	expectedAction ActionMatcher
	expectedErr    error
}

func TestClusterReconcile(t *testing.T) {
	t.Run("empty cluster - 1 replica - 1 create llv & create rvr",
		func(t *testing.T) {
			runClusterReconcileTestCase(t, &reconcileTestCase{
				replicaConfigs: []testReplicaConfig{
					{
						NodeName: testNodeName,
						Volume: &testVolumeConfig{
							VGName:                testVGName,
							ActualVgNameOnTheNode: testActualVGNameOnTheNode,
						},
					},
				},
				expectedAction: ActionsMatcher{
					CreateLLVMatcher{
						LLVSpec: snc.LVMLogicalVolumeSpec{
							ActualLVNameOnTheNode: testRVName,
							Type:                  "Thick",
							Size:                  testSizeStr,
							LVMVolumeGroupName:    testVGName,
							Thick:                 &snc.LVMLogicalVolumeThickSpec{Contiguous: utils.Ptr(true)},
						},
					},
					CreateRVRMatcher{
						RVRSpec: v1alpha2.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: testRVName,
							NodeName:             testNodeName,
							NodeAddress: v1alpha2.Address{
								IPv4: generateIPv4(testNodeName),
								Port: testPortRng.MinPort,
							},
							SharedSecret: testSharedSecret,
							Quorum:       1,
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
					},
				},
			})
		},
	)

	t.Run("existing small LLV - 1 replica - resize llv & create rvr",
		func(t *testing.T) {
			runClusterReconcileTestCase(t, &reconcileTestCase{
				existingLLVs: map[LLVPhysicalKey]*snc.LVMLogicalVolume{
					{nodeName: testNodeName, actualLVNameOnTheNode: testRVName}: {
						ObjectMeta: v1.ObjectMeta{Name: testLLVName},
						Spec: snc.LVMLogicalVolumeSpec{
							ActualLVNameOnTheNode: testRVName,
							Size:                  testSizeSmallStr,
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
						},
					},
				},
				expectedAction: ActionsMatcher{
					PatchLLVMatcher{
						LLVName: testLLVName,
						LLVSpec: snc.LVMLogicalVolumeSpec{
							ActualLVNameOnTheNode: testRVName,
							Size:                  testSizeStr,
							LVMVolumeGroupName:    testVGName,
							Type:                  "Thick",
							Thick:                 &snc.LVMLogicalVolumeThickSpec{Contiguous: utils.Ptr(true)},
						},
					},
					CreateRVRMatcher{
						RVRSpec: v1alpha2.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: testRVName,
							NodeName:             testNodeName,
							NodeAddress: v1alpha2.Address{
								IPv4: generateIPv4(testNodeName),
								Port: testPortRng.MinPort,
							},
							SharedSecret: testSharedSecret,
							Quorum:       1,
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
					},
				},
			})
		},
	)

	t.Run("add 1 diskful and fix existing diskless - (parallel) create llv + patch rvr; then create rvr",
		func(t *testing.T) {
			runClusterReconcileTestCase(t, &reconcileTestCase{
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
						Status: &v1alpha2.ReplicatedVolumeReplicaStatus{
							DRBD: &v1alpha2.DRBDStatus{
								Devices: []v1alpha2.DeviceStatus{
									{Size: int(testSize)},
								},
							},
						},
					},
				},
				replicaConfigs: []testReplicaConfig{
					{ // diskful to add
						NodeName: "node-a",
						Volume: &testVolumeConfig{
							VGName:                testVGName,
							ActualVgNameOnTheNode: testActualVGNameOnTheNode,
						},
					},
					{ // diskless to fix
						NodeName: "node-b",
					},
				},
				expectedAction: ActionsMatcher{
					PatchRVRMatcher{RVRName: testRVRName},
					CreateLLVMatcher{
						LLVSpec: snc.LVMLogicalVolumeSpec{
							ActualLVNameOnTheNode: testRVName,
							Type:                  "Thick",
							Size:                  testSizeStr,
							LVMVolumeGroupName:    testVGName,
							Thick:                 &snc.LVMLogicalVolumeThickSpec{Contiguous: utils.Ptr(true)},
						},
					},
					CreateRVRMatcher{
						RVRSpec: v1alpha2.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: testRVName,
							NodeName:             "node-a",
							NodeAddress: v1alpha2.Address{
								IPv4: generateIPv4("node-a"),
								Port: testPortRng.MinPort,
							},
							SharedSecret: testSharedSecret,
							Quorum:       2,
							Peers: map[string]v1alpha2.Peer{
								"node-b": {
									NodeId:       1,
									Address:      v1alpha2.Address{IPv4: generateIPv4("node-b"), Port: testPortRng.MinPort},
									Diskless:     true,
									SharedSecret: "testSharedSecret",
								},
							},
							Volumes: []v1alpha2.Volume{
								{
									Number: 0,
									Device: 0,
									Disk:   fmt.Sprintf("/dev/%s/%s", testActualVGNameOnTheNode, testRVName),
								},
							},
						},
					},
				},
			})
		},
	)

	t.Run("add 1 diskful and delete 1 orphan rvr - (parallel) create llv; then create rvr and delete orphan",
		func(t *testing.T) {
			runClusterReconcileTestCase(t, &reconcileTestCase{
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
						},
					},
				},
				expectedAction: ActionsMatcher{
					CreateLLVMatcher{
						LLVSpec: snc.LVMLogicalVolumeSpec{
							ActualLVNameOnTheNode: testRVName,
							Type:                  "Thick",
							Size:                  testSizeStr,
							LVMVolumeGroupName:    testVGName,
							Thick: &snc.LVMLogicalVolumeThickSpec{
								Contiguous: utils.Ptr(true),
							},
						},
					},
					CreateRVRMatcher{
						RVRSpec: v1alpha2.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: testRVName,
							NodeName:             "node-a",
							NodeAddress:          v1alpha2.Address{IPv4: generateIPv4("node-a"), Port: testPortRng.MinPort},
							SharedSecret:         testSharedSecret,
							Volumes: []v1alpha2.Volume{
								{
									Number: 0,
									Device: 0,
									Disk:   fmt.Sprintf("/dev/%s/%s", testActualVGNameOnTheNode, testRVName),
								},
							},
							Quorum: 1,
						},
					},
					DeleteRVRMatcher{RVRName: testRVRName},
				},
			})
		},
	)
}

func ifDefined[T any](p *T, def T) T {
	if p != nil {
		return *p
	}
	return def
}

func runClusterReconcileTestCase(t *testing.T, tc *reconcileTestCase) {
	// arrange
	rv := &v1alpha2.ReplicatedVolume{
		ObjectMeta: v1.ObjectMeta{Name: ifDefined(tc.rvName, testRVName)},
		Spec: v1alpha2.ReplicatedVolumeSpec{
			Replicas:     byte(len(tc.replicaConfigs)),
			SharedSecret: testSharedSecret,
			Size:         *resource.NewQuantity(testSize, resource.BinarySI),
			LVM: v1alpha2.LVMSpec{
				Type: "Thick",
				LVMVolumeGroups: []v1alpha2.LVGRef{
					{Name: testVGName},
				},
			},
		},
	}
	rvAdapter, err := cluster.NewRVAdapter(rv)
	if err != nil {
		t.Fatalf("rv adapter: %v", err)
	}
	var rvNodes []cluster.RVNodeAdapter
	var nodeMgrs []cluster.NodeManager
	for _, rCfg := range tc.replicaConfigs {
		var lvg *snc.LVMVolumeGroup
		if rCfg.Volume != nil {
			lvg = &snc.LVMVolumeGroup{
				ObjectMeta: v1.ObjectMeta{Name: rCfg.Volume.VGName},
				Spec: snc.LVMVolumeGroupSpec{
					Local:                 snc.LVMVolumeGroupLocalSpec{NodeName: rCfg.NodeName},
					ActualVGNameOnTheNode: rCfg.Volume.ActualVgNameOnTheNode,
				},
			}
		}
		node := &corev1.Node{
			ObjectMeta: v1.ObjectMeta{Name: rCfg.NodeName},
			Status: corev1.NodeStatus{
				Addresses: []corev1.NodeAddress{
					{Type: corev1.NodeHostName, Address: rCfg.NodeName},
					{Type: corev1.NodeInternalIP, Address: generateIPv4(rCfg.NodeName)},
				},
			},
		}
		rvNode, err := cluster.NewRVNodeAdapter(rvAdapter, node, lvg)
		if err != nil {
			t.Fatalf("rv node adapter: %v", err)
		}
		rvNodes = append(rvNodes, rvNode)
		nodeMgrs = append(nodeMgrs, cluster.NewNodeManager(testPortRng, rCfg.NodeName))
	}
	clr, err := cluster.NewCluster(slog.Default(), rvAdapter, rvNodes, nodeMgrs)
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	for i := range tc.existingRVRs {
		ra, err := cluster.NewRVRAdapter(&tc.existingRVRs[i])
		if err != nil {
			t.Fatalf("rvrAdapter: %v", err)
		}
		if err := clr.AddExistingRVR(ra); err != nil {
			t.Fatalf("addExistingRVR: %v", err)
		}
	}
	for _, llv := range tc.existingLLVs {
		la, err := cluster.NewLLVAdapter(llv)
		if err != nil {
			t.Fatalf("llvAdapter: %v", err)
		}
		if err := clr.AddExistingLLV(la); err != nil {
			t.Fatalf("addExistingLLV: %v", err)
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
}

type testPortRange struct {
	MinPort, MaxPort uint
}

func (r testPortRange) PortMinMax() (uint, uint) {
	return r.MinPort, r.MaxPort
}

var _ cluster.DRBDPortRange = testPortRange{}
