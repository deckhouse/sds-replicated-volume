package suite

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func SetupExistingNodes(
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

	return res
}

func SetupExistingLVG(
	t *testing.T,
	cl client.Client,
	nodeNames []string,
) {
	// nodes := SymbolSelectedNodes.Require()

	// _ = nodes
	// slices.Sort(nodes)

	// SymbolSelectedLVGs.Provide(func(nodeName string) string { return "lvg-0" })
}
