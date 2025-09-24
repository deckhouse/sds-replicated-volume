package cluster

import (
	"context"
	"fmt"
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

// --- Matchers ---

type Matcher interface{ Match(Action) error }

type Seq struct{ Elems []Matcher }

func (m Seq) Match(a Action) error {
	as, ok := a.(Actions)
	if !ok {
		return fmt.Errorf("expected Actions, got %T", a)
	}
	if len(as) != len(m.Elems) {
		return fmt.Errorf("actions len %d != expected %d", len(as), len(m.Elems))
	}
	for i := range m.Elems {
		if err := m.Elems[i].Match(as[i]); err != nil {
			return fmt.Errorf("seq[%d]: %w", i, err)
		}
	}
	return nil
}

type Par struct{ Elems []Matcher }

func (m Par) Match(a Action) error {
	pa, ok := a.(ParallelActions)
	if !ok {
		return fmt.Errorf("expected ParallelActions, got %T", a)
	}
	if len(pa) < len(m.Elems) {
		return fmt.Errorf("parallel len %d < expected %d", len(pa), len(m.Elems))
	}
	used := make([]bool, len(pa))
	for i := range m.Elems {
		found := false
		for j := range pa {
			if used[j] {
				continue
			}
			if err := m.Elems[i].Match(pa[j]); err == nil {
				used[j] = true
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("parallel: did not find match for elem %d", i)
		}
	}
	return nil
}

// OneOf matches if at least one of the alternatives matches
type OneOf struct{ Alts []Matcher }

func (o OneOf) Match(a Action) error {
	var errs []error
	for _, m := range o.Alts {
		if err := m.Match(a); err == nil {
			return nil
		} else {
			errs = append(errs, err)
		}
	}
	return fmt.Errorf("none matched: %v", errs)
}

type IsCreateRVR struct{}

type IsWaitRVR struct{}

type IsDeleteRVR struct{}

type IsCreateLLV struct{}

type IsWaitLLV struct{}

type IsPatchLLV struct{}

type IsDeleteLLV struct{}

func (IsCreateRVR) Match(a Action) error {
	if _, ok := a.(CreateReplicatedVolumeReplica); !ok {
		return fmt.Errorf("not CreateRVR: %T", a)
	}
	return nil
}
func (IsWaitRVR) Match(a Action) error {
	if _, ok := a.(WaitReplicatedVolumeReplica); !ok {
		return fmt.Errorf("not WaitRVR: %T", a)
	}
	return nil
}
func (IsDeleteRVR) Match(a Action) error {
	if _, ok := a.(DeleteReplicatedVolumeReplica); !ok {
		return fmt.Errorf("not DeleteRVR: %T", a)
	}
	return nil
}
func (IsCreateLLV) Match(a Action) error {
	if _, ok := a.(CreateLVMLogicalVolume); !ok {
		return fmt.Errorf("not CreateLLV: %T", a)
	}
	return nil
}
func (IsWaitLLV) Match(a Action) error {
	if _, ok := a.(WaitLVMLogicalVolume); !ok {
		return fmt.Errorf("not WaitLLV: %T", a)
	}
	return nil
}
func (IsPatchLLV) Match(a Action) error {
	if _, ok := a.(LLVPatch); !ok {
		return fmt.Errorf("not LLVPatch: %T", a)
	}
	return nil
}
func (IsDeleteLLV) Match(a Action) error {
	if _, ok := a.(DeleteLVMLogicalVolume); !ok {
		return fmt.Errorf("not DeleteLLV: %T", a)
	}
	return nil
}

// --- Test input helpers ---

type replicaSpec struct {
	node    string
	ip      string
	primary bool
	vg      string // empty => diskless
}

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

func mustMatch(t *testing.T, act Action, m Matcher) {
	t.Helper()
	if err := m.Match(act); err != nil {
		t.Fatalf("action does not match: %v", err)
	}
}

func TestCluster_Reconcile_Table(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name     string
		rvName   string
		existing []v1alpha2.ReplicatedVolumeReplica
		llvs     map[string]*snc.LVMLogicalVolume
		replicas []replicaSpec
		expect   Matcher
	}{
		{
			name:     "one diskless replica, no existing",
			rvName:   "rv-a",
			replicas: []replicaSpec{{node: "n1", ip: "10.0.0.1", primary: true}},
			expect:   Seq{Elems: []Matcher{IsCreateRVR{}, IsWaitRVR{}}},
		},
		{
			name:   "three replicas, two diskful create LLVs",
			rvName: "rv-b",
			replicas: []replicaSpec{
				{node: "n1", ip: "10.0.0.1", primary: true, vg: "vg-1"},
				{node: "n2", ip: "10.0.0.2", vg: "vg-1"},
				{node: "n3", ip: "10.0.0.3"}, // diskless
			},
			// Each diskful replica contributes Actions{ Actions{ CreateLLV, WaitLLV } }
			expect: Seq{Elems: []Matcher{
				Par{Elems: []Matcher{
					Seq{Elems: []Matcher{Seq{Elems: []Matcher{IsCreateLLV{}, IsWaitLLV{}}}}},
					Seq{Elems: []Matcher{Seq{Elems: []Matcher{IsCreateLLV{}, IsWaitLLV{}}}}},
				}},
				IsCreateRVR{}, IsWaitRVR{}, IsCreateRVR{}, IsWaitRVR{}, IsCreateRVR{}, IsWaitRVR{},
			}},
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
			// Existing diskful replica contributes either a create+wait or a patch wrapped in one Actions
			expect: Seq{Elems: []Matcher{
				Par{Elems: []Matcher{
					OneOf{Alts: []Matcher{
						Seq{Elems: []Matcher{Seq{Elems: []Matcher{IsCreateLLV{}, IsWaitLLV{}}}}},
						Seq{Elems: []Matcher{IsPatchLLV{}}},
					}},
				}},
				IsCreateRVR{}, IsWaitRVR{},
			}},
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
			// Expect: [CreateRVR, WaitRVR, Actions(DeleteRVR, maybe DeleteLLV)]
			expect: Seq{Elems: []Matcher{IsCreateRVR{}, IsWaitRVR{}, OneOf{Alts: []Matcher{Seq{Elems: []Matcher{IsDeleteRVR{}}}, Seq{Elems: []Matcher{IsDeleteRVR{}, IsDeleteLLV{}}}}}}},
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
			mustMatch(t, action, tc.expect)
		})
	}
}
