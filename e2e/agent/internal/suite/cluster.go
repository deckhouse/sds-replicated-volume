package suite

import (
	"testing"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DiscoverNodes Discovers existing Node objects from the cluster by name.
// nodeNames must be non-empty and contain no duplicates. Each name must match
// an existing cluster node.
func DiscoverNodes(
	t *testing.T,
	cl client.Client,
	nodeNames []string,
) []*corev1.Node {
	if len(nodeNames) == 0 {
		t.Fatal("expected selectedNodes to be non-empty")
	}

	uniqueNodeNames := make(map[string]struct{}, len(nodeNames))
	for _, nodeName := range nodeNames {
		uniqueNodeNames[nodeName] = struct{}{}
	}
	if len(uniqueNodeNames) != len(nodeNames) {
		t.Fatal("expected selectedNodes to have no duplicates")
	}

	nodeList := &corev1.NodeList{}

	if err := cl.List(t.Context(), nodeList); err != nil {
		t.Fatal(err)
	}

	res := make([]*corev1.Node, 0, len(nodeNames))

	for i := range nodeList.Items {
		node := &nodeList.Items[i]
		if _, ok := uniqueNodeNames[node.Name]; ok {
			res = append(res, node)
		}
	}

	if len(res) != len(nodeNames) {
		found := make([]string, 0, len(res))
		for _, n := range res {
			found = append(found, n.Name)
		}
		t.Fatalf("expected %d nodes %v, found %d: %v", len(nodeNames), nodeNames, len(found), found)
	}

	return res
}

// LVGInfo holds a discovered LVMVolumeGroup and its associated node name.
type LVGInfo struct {
	NodeName string
	LVGName  string
	LVG      *snc.LVMVolumeGroup
}

// DiscoverLVGs Discovers existing LVMVolumeGroup objects for the given node configs.
// Each node config specifies a node name and LVG name. The LVG must exist
// in the cluster and its spec.local.nodeName must match the expected node.
func DiscoverLVGs(
	t *testing.T,
	cl client.Client,
	nodeConfigs []NodeConfig,
) []LVGInfo {
	if len(nodeConfigs) == 0 {
		t.Fatal("expected nodeConfigs to be non-empty")
	}

	result := make([]LVGInfo, 0, len(nodeConfigs))

	for _, nc := range nodeConfigs {
		lvg := &snc.LVMVolumeGroup{}
		if err := cl.Get(t.Context(), client.ObjectKey{Name: nc.LVGName}, lvg); err != nil {
			t.Fatalf("getting LVG %q for node %q: %v", nc.LVGName, nc.Name, err)
		}

		if lvg.Spec.Local.NodeName != nc.Name {
			t.Fatalf("LVG %q is on node %q, expected %q",
				nc.LVGName, lvg.Spec.Local.NodeName, nc.Name)
		}

		result = append(result, LVGInfo{
			NodeName: nc.Name,
			LVGName:  nc.LVGName,
			LVG:      lvg,
		})
	}

	return result
}
