package suite

import (
	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Cluster holds the discovered cluster state validated against ClusterOptions.
type Cluster struct {
	AllocateSize resource.Quantity
	Nodes        []ClusterNode
}

// ClusterNode holds the discovered state for a single node.
type ClusterNode struct {
	Name string
	LVG  *snc.LVMVolumeGroup
}

// DiscoverCluster reads ClusterOptions from the config, discovers every node's
// LVG, and validates that each LVG is Ready and has enough free space for
// AllocateSize.
func DiscoverCluster(e envtesting.E, cl client.Client) *Cluster {
	var opts ClusterOptions
	e.Options(&opts)

	allocateSize := resource.MustParse(opts.AllocateSize)

	nodeLVGs := make([]NodeLVG, len(opts.Nodes))
	for i, n := range opts.Nodes {
		nodeLVGs[i] = NodeLVG{Name: n.Name, LVGName: n.LVGName}
	}
	lvgs := DiscoverLVGs(e, cl, nodeLVGs)

	nodes := make([]ClusterNode, len(opts.Nodes))
	for i, lvg := range lvgs {
		if lvg.Status.Phase != "Ready" {
			e.Fatalf("LVG %q on node %q: phase is %q, expected Ready",
				lvg.Name, opts.Nodes[i].Name, lvg.Status.Phase)
		}
		if lvg.Status.VGFree.Cmp(allocateSize) < 0 {
			e.Fatalf("LVG %q on node %q: vgFree %s < allocateSize %s",
				lvg.Name, opts.Nodes[i].Name,
				lvg.Status.VGFree.String(), allocateSize.String())
		}
		nodes[i] = ClusterNode{
			Name: opts.Nodes[i].Name,
			LVG:  lvg,
		}
	}

	return &Cluster{
		AllocateSize: allocateSize,
		Nodes:        nodes,
	}
}
