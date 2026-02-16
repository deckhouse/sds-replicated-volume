package suite

import (
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NodeLVG maps a node name to its LVMVolumeGroup name for config purposes.
type NodeLVG struct {
	Name    string `json:"name"`
	LVGName string `json:"lvgName"`
}

// DiscoverLVGs Discovers existing LVMVolumeGroup objects for the given
// node-LVG pairs. Each LVG must exist and its spec.local.nodeName must match
// the expected node. Returns LVGs in the same order as nodeLVGs.
func DiscoverLVGs(
	e *envtesting.E,
	cl client.Client,
	nodeLVGs []NodeLVG,
) []*snc.LVMVolumeGroup {
	if len(nodeLVGs) == 0 {
		e.Fatal("require: expected nodeLVGs to be non-empty")
	}

	result := make([]*snc.LVMVolumeGroup, 0, len(nodeLVGs))

	for _, nl := range nodeLVGs {
		lvg := &snc.LVMVolumeGroup{}
		if err := cl.Get(e.Context(), client.ObjectKey{Name: nl.LVGName}, lvg); err != nil {
			e.Fatalf("getting LVG %q for node %q: %v", nl.LVGName, nl.Name, err)
		}

		if lvg.Spec.Local.NodeName != nl.Name {
			e.Fatalf("LVG %q is on node %q, expected %q",
				nl.LVGName, lvg.Spec.Local.NodeName, nl.Name)
		}

		result = append(result, lvg)
	}

	return result
}
