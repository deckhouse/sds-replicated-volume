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

package nodelabelscount

import (
	"context"
	"fmt"
	"sort"

	"github.com/deckhouse/module-sdk/pkg"
	"github.com/deckhouse/module-sdk/pkg/registry"
)

const snapshotName = "nodes"

var _ = registry.RegisterFunc(
	&pkg.HookConfig{
		Kubernetes: []pkg.KubernetesConfig{
			{
				Name:       snapshotName,
				APIVersion: "v1",
				Kind:       "Node",
				JqFilter:   ".metadata",
			},
		},
	},
	countNodeLabels,
)

type nodeMetadata struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
}

func countNodeLabels(_ context.Context, input *pkg.HookInput) error {
	snapshots := input.Snapshots.Get(snapshotName)

	type nodeInfo struct {
		name   string
		labels int
	}

	var nodes []nodeInfo

	for _, snap := range snapshots {
		var meta nodeMetadata
		if err := snap.UnmarshalTo(&meta); err != nil {
			return fmt.Errorf("unmarshal node metadata: %w", err)
		}

		labelCount := len(meta.Labels)
		nodes = append(nodes, nodeInfo{name: meta.Name, labels: labelCount})

		input.Logger.Info("node labels count", "node", meta.Name, "labelCount", labelCount)
	}

	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].name < nodes[j].name
	})

	var result string
	for i, n := range nodes {
		if i > 0 {
			result += ","
		}
		result += fmt.Sprintf("%s:%d", n.name, n.labels)
	}

	input.Values.Set("sdsReplicatedVolume.internal.dataNodesChecksum", result)

	return nil
}
