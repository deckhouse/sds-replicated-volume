package cluster

import (
	"context"
	"testing"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// --- Mocks ---

type mockRVRClient struct {
	byRV map[string][]v1alpha2.ReplicatedVolumeReplica
}

func (m *mockRVRClient) ByReplicatedVolumeName(ctx context.Context, resourceName string) ([]v1alpha2.ReplicatedVolumeReplica, error) {
	return append([]v1alpha2.ReplicatedVolumeReplica(nil), m.byRV[resourceName]...), nil
}

type mockNodeRVRClient struct {
	byNode map[string][]v1alpha2.ReplicatedVolumeReplica
}

func (m *mockNodeRVRClient) ByNodeName(ctx context.Context, nodeName string) ([]v1alpha2.ReplicatedVolumeReplica, error) {
	return append([]v1alpha2.ReplicatedVolumeReplica(nil), m.byNode[nodeName]...), nil
}

type mockLLVClient struct {
	byKey map[string]*snc.LVMLogicalVolume
}

func llvKey(node, vg, lv string) string { return node + "/" + vg + "/" + lv }

func (m *mockLLVClient) ByActualNamesOnTheNode(ctx context.Context, nodeName, actualVGNameOnTheNode, actualLVNameOnTheNode string) (*snc.LVMLogicalVolume, error) {
	return m.byKey[llvKey(nodeName, actualVGNameOnTheNode, actualLVNameOnTheNode)], nil
}

type mockPortRange struct{ min, max uint }

func (m mockPortRange) PortMinMax() (uint, uint) { return m.min, m.max }

// --- Helpers ---

func flatten(actions Action, out *[]Action) {
	switch a := actions.(type) {
	case Actions:
		for _, sub := range a {
			flatten(sub, out)
		}
	case ParallelActions:
		for _, sub := range a {
			flatten(sub, out)
		}
	default:
		*out = append(*out, a)
	}
}

type expectedCounts struct {
	createRVR, waitRVR, deleteRVR           int
	createLLV, waitLLV, patchLLV, deleteLLV int
}

func countActions(all []Action) expectedCounts {
	var c expectedCounts
	for _, a := range all {
		switch a.(type) {
		case CreateReplicatedVolumeReplica:
			c.createRVR++
		case WaitReplicatedVolumeReplica:
			c.waitRVR++
		case DeleteReplicatedVolumeReplica:
			c.deleteRVR++
		case CreateLVMLogicalVolume:
			c.createLLV++
		case WaitLVMLogicalVolume:
			c.waitLLV++
		case LLVPatch:
			c.patchLLV++
		case DeleteLVMLogicalVolume:
			c.deleteLLV++
		}
	}
	return c
}

type replicaSpec struct {
	node    string
	ip      string
	primary bool
	vg      string // empty => diskless
}

// newRVR builds a minimal existing RVR used by mocks
func newRVR(name, rvName, node, ip string, nodeId uint, port uint, hasVol bool, vg, lv string, minor uint) v1alpha2.ReplicatedVolumeReplica {
	r := v1alpha2.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha2.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: rvName,
			NodeName:             node,
			NodeId:               nodeId,
			NodeAddress:          v1alpha2.Address{IPv4: ip, Port: port},
			SharedSecret:         "secret",
		},
	}
	if hasVol {
		v := v1alpha2.Volume{Number: 0, Device: minor}
		v.SetDisk(vg, lv)
		r.Spec.Volumes = []v1alpha2.Volume{v}
	}
	return r
}

func TestCluster_Reconcile_Table(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name     string
		rvName   string
		existing []v1alpha2.ReplicatedVolumeReplica
		llvs     map[string]*snc.LVMLogicalVolume
		replicas []replicaSpec
		expect   expectedCounts
	}{
		{
			name:     "one diskless replica, no existing",
			rvName:   "rv-a",
			replicas: []replicaSpec{{node: "n1", ip: "10.0.0.1", primary: true}},
			expect:   expectedCounts{createRVR: 1, waitRVR: 1},
		},
		{
			name:   "three replicas, two diskful create LLVs",
			rvName: "rv-b",
			replicas: []replicaSpec{
				{node: "n1", ip: "10.0.0.1", primary: true, vg: "vg-1"},
				{node: "n2", ip: "10.0.0.2", vg: "vg-1"},
				{node: "n3", ip: "10.0.0.3"}, // diskless
			},
			expect: expectedCounts{createLLV: 2, waitLLV: 2, createRVR: 3, waitRVR: 3},
		},
		{
			name:   "one existing diskful rvr recreated due to new peer, plus one new diskless",
			rvName: "rv-c",
			existing: []v1alpha2.ReplicatedVolumeReplica{
				newRVR("rvr-old", "rv-c", "n1", "10.0.0.1", 0, 2001, true, "vg-1", "rv-c", 1),
			},
			llvs: map[string]*snc.LVMLogicalVolume{
				llvKey("n1", "vg-1", "rv-c"): {ObjectMeta: metav1.ObjectMeta{Name: "llv-1"}, Spec: snc.LVMLogicalVolumeSpec{ActualLVNameOnTheNode: "rv-c", LVMVolumeGroupName: "vg-1", Size: "1Gi"}},
			},
			replicas: []replicaSpec{{node: "n1", ip: "10.0.0.1", primary: true, vg: "vg-1"}, {node: "n2", ip: "10.0.0.2"}},
			expect:   expectedCounts{createRVR: 2, waitRVR: 2},
		},
		{
			name:   "delete extra existing rvr and its llv",
			rvName: "rv-d",
			existing: []v1alpha2.ReplicatedVolumeReplica{
				newRVR("rvr-delete", "rv-d", "n2", "10.0.0.2", 1, 2002, true, "vg-1", "rv-d", 2),
			},
			llvs: map[string]*snc.LVMLogicalVolume{
				llvKey("n2", "vg-1", "rv-d"): {ObjectMeta: metav1.ObjectMeta{Name: "llv-del"}, Spec: snc.LVMLogicalVolumeSpec{ActualLVNameOnTheNode: "rv-d", LVMVolumeGroupName: "vg-1", Size: "1Gi"}},
			},
			replicas: []replicaSpec{{node: "n1", ip: "10.0.0.1", primary: true}},
			expect:   expectedCounts{createRVR: 1, waitRVR: 1, deleteRVR: 1, deleteLLV: 1},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Build mocks
			byRV := map[string][]v1alpha2.ReplicatedVolumeReplica{tc.rvName: tc.existing}
			byNode := map[string][]v1alpha2.ReplicatedVolumeReplica{}
			for i := range tc.existing {
				byNode[tc.existing[i].Spec.NodeName] = append(byNode[tc.existing[i].Spec.NodeName], tc.existing[i])
			}

			rvrCl := &mockRVRClient{byRV: byRV}
			nodeRVRCl := &mockNodeRVRClient{byNode: byNode}
			llvCl := &mockLLVClient{byKey: tc.llvs}
			pr := mockPortRange{min: 2000, max: 2005}

			clr := New(ctx, rvrCl, nodeRVRCl, pr, llvCl, tc.rvName, "secret")
			for id, rs := range tc.replicas {
				r := clr.AddReplica(rs.node, rs.ip, rs.primary, 0, 0)
				if rs.vg != "" {
					r.AddVolume(rs.vg)
				}
				_ = id
			}

			action, err := clr.Reconcile()
			if err != nil {
				t.Fatalf("Reconcile error: %v", err)
			}

			var flat []Action
			flatten(action, &flat)
			got := countActions(flat)
			if got != tc.expect {
				t.Fatalf("unexpected actions: got %+v, want %+v", got, tc.expect)
			}
		})
	}
}
