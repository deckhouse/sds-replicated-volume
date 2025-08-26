/*
Copyright 2024 Flant JSC

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

package discoverdatanodeschecksum

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/module-sdk/pkg"
	"github.com/deckhouse/module-sdk/pkg/registry"
	objectpatch "github.com/deckhouse/module-sdk/pkg/object-patch"
)

const (
	nodeSnapshotName = "nodes"
	moduleName       = "sdsReplicatedVolume"
	labelKey         = "storage.deckhouse.io/sds-replicated-volume-node"
	queue            = "/modules/sds-replicated-volume/node-label-change"
)

var _ = registry.RegisterFunc(
	&pkg.HookConfig{
		Kubernetes: []pkg.KubernetesConfig{
			{
				Name:       nodeSnapshotName,
				APIVersion: "v1",
				Kind:       "Node",
				JqFilter:   `{\"uid\": .metadata.uid}`,
				LabelSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						labelKey: "",
					},
				},
			},
		},
	},
	discoveryDataNodesChecksum,
)

func discoveryDataNodesChecksum(_ context.Context, input *pkg.HookInput) error {
	// snapshots := input.Snapshots.Get(nodeSnapshotName)
	// var uids []string
	// for _, snap := range snapshots {
	// 	b, err := json.Marshal(snap)
	// 	if err != nil {
	// 		continue
	// 	}
	// 	var snapMap map[string]interface{}
	// 	if err := json.Unmarshal(b, &snapMap); err != nil {
	// 		continue
	// 	}
	// 	filterResult, ok := snapMap["filterResult"].(map[string]interface{})
	// 	if !ok {
	// 		continue
	// 	}
	// 	uid, ok := filterResult["uid"].(string)
	// 	if !ok {
	// 		continue
	// 	}
	// 	uids = append(uids, uid)
	// }
	// sort.Strings(uids)
	// h := sha256.New()
	// h.Write([]byte(fmt.Sprintf("%v", uids)))
	// hash := hex.EncodeToString(h.Sum(nil))

	// input.Values.Set("sdsReplicatedVolume.internal.dataNodesChecksum", hash)
	// return nil


	uidList, err := objectpatch.UnmarshalToStruct[string](input.Snapshots, "nodes")
	if err != nil {
		return fmt.Errorf("failed to unmarshal node UIDs: %w", err)
	}

	sort.Strings(uidList)

	uidString := fmt.Sprintf("%v", uidList)
	hash := sha256.Sum256([]byte(uidString))
	hashString := fmt.Sprintf("%x", hash)

	input.Values.Set("sdsReplicatedVolume.internal.dataNodesChecksum", hashString)

	input.Logger.Info("computed data nodes checksum", 
		"nodeCount", len(uidList),
		"checksum", hashString)

	return nil
}
