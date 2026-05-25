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

package render

import (
	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/linstorviewer/index"
)

// NodeList formats node list output (linstor node list).
func NodeList(store *index.Store) string {
	rows := store.Nodes()
	data := make([][]string, 0, len(rows))
	for _, r := range rows {
		data = append(data, []string{r.Node, r.NodeType, r.Addresses, r.State})
	}
	return formatTable([]string{"Node", "NodeType", "Addresses", "State"}, data)
}

// StoragePoolList formats storage-pool list output.
func StoragePoolList(store *index.Store) string {
	rows := store.StoragePools()
	data := make([][]string, 0, len(rows))
	for _, r := range rows {
		data = append(data, []string{r.StoragePool, r.Node, r.Driver, r.PoolName, r.State})
	}
	return formatTable([]string{"StoragePool", "Node", "Driver", "PoolName", "State"}, data)
}

// VolumeList formats volume list output.
func VolumeList(store *index.Store) string {
	rows := store.Volumes()
	data := make([][]string, 0, len(rows))
	for _, r := range rows {
		data = append(data, []string{
			r.Node, r.Resource, r.StoragePool, r.VolNr, r.MinorNr, r.DeviceName, r.InUse, r.State,
		})
	}
	return formatTable([]string{
		"Node", "Resource", "StoragePool", "VolNr", "MinorNr", "DeviceName", "InUse", "State",
	}, data)
}
