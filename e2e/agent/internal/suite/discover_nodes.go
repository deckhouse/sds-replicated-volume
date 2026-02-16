package suite

import (
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DiscoverNodes Discovers existing Node objects from the cluster by name.
// nodeNames must be non-empty and contain no duplicates. Each name must match
// an existing cluster node.
func DiscoverNodes(
	e *envtesting.E,
	cl client.Client,
	nodeNames []string,
) []*corev1.Node {
	if len(nodeNames) == 0 {
		e.Fatal("require: expected nodeNames to be non-empty")
	}

	uniqueNodeNames := make(map[string]struct{}, len(nodeNames))
	for _, nodeName := range nodeNames {
		uniqueNodeNames[nodeName] = struct{}{}
	}
	if len(uniqueNodeNames) != len(nodeNames) {
		e.Fatal("require: expected nodeNames to have no duplicates")
	}

	nodeList := &corev1.NodeList{}

	if err := cl.List(e.Context(), nodeList); err != nil {
		e.Fatal(err)
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
		e.Fatalf("expected %d nodes %v, found %d: %v", len(nodeNames), nodeNames, len(found), found)
	}

	return res
}
