/*
Copyright 2026 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package suite

import (
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/kubetesting"
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

	lvgNames := make([]string, len(opts.Nodes))
	for i, n := range opts.Nodes {
		lvgNames[i] = n.LVGName
	}
	lvgs := kubetesting.DiscoverResources[snc.LVMVolumeGroup](e, cl, lvgNames, nil)

	nodes := make([]ClusterNode, len(opts.Nodes))
	for i, lvg := range lvgs {
		expected := opts.Nodes[i]
		if lvg.Spec.Local.NodeName != expected.Name {
			e.Fatalf("LVG %q is on node %q, expected %q",
				lvg.Name, lvg.Spec.Local.NodeName, expected.Name)
		}
		if lvg.Status.Phase != "Ready" {
			e.Fatalf("LVG %q on node %q: phase is %q, expected Ready",
				lvg.Name, expected.Name, lvg.Status.Phase)
		}
		if lvg.Status.VGFree.Cmp(allocateSize) < 0 {
			e.Fatalf("LVG %q on node %q: vgFree %s < allocateSize %s",
				lvg.Name, expected.Name,
				lvg.Status.VGFree.String(), allocateSize.String())
		}
		nodes[i] = ClusterNode{
			Name: expected.Name,
			LVG:  lvg,
		}
	}

	return &Cluster{
		AllocateSize: allocateSize,
		Nodes:        nodes,
	}
}
