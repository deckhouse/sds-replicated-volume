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

// Package index builds lookup tables from LINSTOR CR documents for CLI-style listings.
package index

import (
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	kindNodes                      = "Nodes"
	kindNodeNetInterfaces          = "NodeNetInterfaces"
	kindNodeStorPool               = "NodeStorPool"
	kindStorPoolDefinitions        = "StorPoolDefinitions"
	kindPropsContainers            = "PropsContainers"
	kindVolumes                    = "Volumes"
	kindLayerResourceIds           = "LayerResourceIds"
	kindLayerStorageVolumes        = "LayerStorageVolumes"
	kindLayerDrbdVolumeDefinitions = "LayerDrbdVolumeDefinitions"

	storDriverStorPoolNameKey = "StorDriver/StorPoolName"
	layerResourceKindStorage  = "STORAGE"
)

// Store holds derived indexes from a crs.gz snapshot.
type Store struct {
	// nodeDSP maps LINSTOR node_name (any case) to display name (node_dsp_name).
	nodeDSP map[string]string
	// poolDSP maps LINSTOR pool_name (any case) to display name (pool_dsp_name).
	poolDSP map[string]string
	// storPoolPhys maps normalized node+pool to StorDriver/StorPoolName value.
	storPoolPhys map[string]string
	// storageVolByLRID maps layer_resource_id to storage pool display name on that volume.
	storageVolByLRID map[int]storageVolInfo
	// drbdMinor maps resource_name+vlm_nr (lowercase) to minor number.
	drbdMinor map[string]int

	nodes        []NodeRow
	storagePools []StoragePoolRow
	volumes      []VolumeRow
}

type storageVolInfo struct {
	storPoolDSP string
}

// NodeRow is one line of node list output.
type NodeRow struct {
	Node      string
	NodeType  string
	Addresses string
	State     string
}

// StoragePoolRow is one line of storage-pool list output.
type StoragePoolRow struct {
	StoragePool string
	Node        string
	Driver      string
	PoolName    string
	State       string
}

// VolumeRow is one line of volume list output.
type VolumeRow struct {
	Node        string
	Resource    string
	StoragePool string
	VolNr       string
	MinorNr     string
	DeviceName  string
	InUse       string
	State       string
}

// Build constructs indexes and pre-rendered row slices from backup documents.
func Build(docs []*unstructured.Unstructured) (*Store, error) {
	s := &Store{
		nodeDSP:          make(map[string]string),
		poolDSP:          make(map[string]string),
		storPoolPhys:     make(map[string]string),
		storageVolByLRID: make(map[int]storageVolInfo),
		drbdMinor:        make(map[string]int),
	}

	byKind := make(map[string][]*unstructured.Unstructured)
	for _, d := range docs {
		byKind[d.GetKind()] = append(byKind[d.GetKind()], d)
	}

	for _, u := range byKind[kindNodes] {
		name := specString(u, "node_name")
		dsp := specString(u, "node_dsp_name")
		if name == "" {
			continue
		}
		if dsp == "" {
			dsp = strings.ToLower(name)
		}
		s.nodeDSP[normalizeKey(name)] = dsp
	}

	for _, u := range byKind[kindStorPoolDefinitions] {
		name := specString(u, "pool_name")
		dsp := specString(u, "pool_dsp_name")
		if name == "" {
			continue
		}
		if dsp == "" {
			dsp = name
		}
		s.poolDSP[normalizeKey(name)] = dsp
	}

	for _, u := range byKind[kindPropsContainers] {
		if specString(u, "prop_key") != storDriverStorPoolNameKey {
			continue
		}
		nodeName, poolName, ok := parseStorPoolConfInstance(specString(u, "props_instance"))
		if !ok {
			continue
		}
		s.storPoolPhys[normalizeStorPoolKey(nodeName, poolName)] = specString(u, "prop_value")
	}

	for _, u := range byKind[kindLayerStorageVolumes] {
		lrid, _, _ := unstructured.NestedInt64(u.Object, "spec", "layer_resource_id")
		s.storageVolByLRID[int(lrid)] = storageVolInfo{
			storPoolDSP: s.poolDisplay(specString(u, "stor_pool_name")),
		}
	}

	for _, u := range byKind[kindLayerDrbdVolumeDefinitions] {
		res := specString(u, "resource_name")
		vlmNr, _, _ := unstructured.NestedInt64(u.Object, "spec", "vlm_nr")
		minor, _, _ := unstructured.NestedInt64(u.Object, "spec", "vlm_minor_nr")
		key := volumeKey(res, int(vlmNr))
		s.drbdMinor[key] = int(minor)
	}

	s.buildNodes(byKind[kindNodes], byKind[kindNodeNetInterfaces])
	s.buildStoragePools(byKind[kindNodeStorPool])
	s.buildVolumes(byKind[kindVolumes], byKind[kindLayerResourceIds])

	return s, nil
}

func (s *Store) buildNodes(nodes, netIfaces []*unstructured.Unstructured) {
	addrByNode := make(map[string][]string)
	for _, u := range netIfaces {
		node := specString(u, "node_name")
		ip := specString(u, "inet_address")
		port, _, _ := unstructured.NestedInt64(u.Object, "spec", "stlt_conn_port")
		encr := specString(u, "stlt_conn_encr_type")
		if node == "" || ip == "" {
			continue
		}
		addr := fmt.Sprintf("%s:%d", ip, port)
		if encr != "" {
			addr += " (" + encr + ")"
		}
		addrByNode[normalizeKey(node)] = append(addrByNode[normalizeKey(node)], addr)
	}

	for _, u := range nodes {
		name := specString(u, "node_name")
		dsp := s.nodeDisplay(name)
		nodeType := nodeTypeDisplay(u)
		addrs := strings.Join(addrByNode[normalizeKey(name)], ", ")
		if addrs == "" {
			addrs = dash
		}
		s.nodes = append(s.nodes, NodeRow{
			Node:      dsp,
			NodeType:  nodeType,
			Addresses: addrs,
			State:     dash,
		})
	}

	sort.Slice(s.nodes, func(i, j int) bool {
		return s.nodes[i].Node < s.nodes[j].Node
	})
}

func (s *Store) buildStoragePools(pools []*unstructured.Unstructured) {
	for _, u := range pools {
		nodeName := specString(u, "node_name")
		poolName := specString(u, "pool_name")
		driver := specString(u, "driver_name")
		phys := s.storPoolPhys[normalizeStorPoolKey(nodeName, poolName)]
		if phys == "" {
			phys = dash
		}
		s.storagePools = append(s.storagePools, StoragePoolRow{
			StoragePool: s.poolDisplay(poolName),
			Node:        s.nodeDisplay(nodeName),
			Driver:      orDash(driver),
			PoolName:    phys,
			State:       dash,
		})
	}

	sort.Slice(s.storagePools, func(i, j int) bool {
		if s.storagePools[i].StoragePool != s.storagePools[j].StoragePool {
			return s.storagePools[i].StoragePool < s.storagePools[j].StoragePool
		}
		return s.storagePools[i].Node < s.storagePools[j].Node
	})
}

func (s *Store) buildVolumes(volumes, layerIDs []*unstructured.Unstructured) {
	storageLRID := make(map[string]int)
	for _, u := range layerIDs {
		if !strings.EqualFold(specString(u, "layer_resource_kind"), layerResourceKindStorage) {
			continue
		}
		res := specString(u, "resource_name")
		node := specString(u, "node_name")
		vlmNr, _, _ := unstructured.NestedInt64(u.Object, "spec", "vlm_nr")
		lrid, _, _ := unstructured.NestedInt64(u.Object, "spec", "layer_resource_id")
		storageLRID[volumeInstanceKey(node, res, int(vlmNr))] = int(lrid)
	}

	for _, u := range volumes {
		nodeName := specString(u, "node_name")
		resName := specString(u, "resource_name")
		vlmNr, _, _ := unstructured.NestedInt64(u.Object, "spec", "vlm_nr")

		pool := dash
		if lrid, ok := storageLRID[volumeInstanceKey(nodeName, resName, int(vlmNr))]; ok {
			if info, ok := s.storageVolByLRID[lrid]; ok && info.storPoolDSP != "" {
				pool = info.storPoolDSP
			}
		}

		minor := dash
		device := dash
		if m, ok := s.drbdMinor[volumeKey(resName, int(vlmNr))]; ok && m > 0 {
			minor = fmt.Sprintf("%d", m)
			device = fmt.Sprintf("/dev/drbd%d", m)
		}

		s.volumes = append(s.volumes, VolumeRow{
			Node:        s.nodeDisplay(nodeName),
			Resource:    strings.ToLower(resName),
			StoragePool: pool,
			VolNr:       fmt.Sprintf("%d", vlmNr),
			MinorNr:     minor,
			DeviceName:  device,
			InUse:       dash,
			State:       dash,
		})
	}

	sort.Slice(s.volumes, func(i, j int) bool {
		if s.volumes[i].Resource != s.volumes[j].Resource {
			return s.volumes[i].Resource < s.volumes[j].Resource
		}
		if s.volumes[i].Node != s.volumes[j].Node {
			return s.volumes[i].Node < s.volumes[j].Node
		}
		return s.volumes[i].VolNr < s.volumes[j].VolNr
	})
}

func (s *Store) nodeDisplay(nodeName string) string {
	if dsp, ok := s.nodeDSP[normalizeKey(nodeName)]; ok && dsp != "" {
		return dsp
	}
	if nodeName == "" {
		return dash
	}
	return strings.ToLower(nodeName)
}

func (s *Store) poolDisplay(poolName string) string {
	if dsp, ok := s.poolDSP[normalizeKey(poolName)]; ok && dsp != "" {
		return dsp
	}
	if poolName == "" {
		return dash
	}
	return poolName
}

// Nodes returns node list rows.
func (s *Store) Nodes() []NodeRow { return s.nodes }

// StoragePools returns storage pool list rows.
func (s *Store) StoragePools() []StoragePoolRow { return s.storagePools }

// Volumes returns volume list rows.
func (s *Store) Volumes() []VolumeRow { return s.volumes }

const dash = "-"

func specString(u *unstructured.Unstructured, key string) string {
	v, _, _ := unstructured.NestedString(u.Object, "spec", key)
	return v
}

func normalizeKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func normalizeStorPoolKey(nodeName, poolName string) string {
	return normalizeKey(nodeName) + "\x00" + normalizeKey(poolName)
}

func parseStorPoolConfInstance(propsInstance string) (nodeName, poolName string, ok bool) {
	parts := strings.Split(strings.Trim(propsInstance, "/"), "/")
	if len(parts) < 3 {
		return "", "", false
	}
	if !strings.EqualFold(parts[0], "STORPOOLCONF") {
		return "", "", false
	}
	nodeName = parts[1]
	if len(parts) == 3 {
		return nodeName, parts[2], true
	}
	return nodeName, strings.Join(parts[2:], "/"), true
}

func volumeKey(resourceName string, vlmNr int) string {
	return normalizeKey(resourceName) + "\x00" + fmt.Sprintf("%d", vlmNr)
}

func volumeInstanceKey(nodeName, resourceName string, vlmNr int) string {
	return normalizeKey(nodeName) + "\x00" + volumeKey(resourceName, vlmNr)
}

func nodeTypeDisplay(u *unstructured.Unstructured) string {
	t, _, _ := unstructured.NestedInt64(u.Object, "spec", "node_type")
	switch t {
	case 1:
		return "CONTROLLER"
	case 2:
		return "SATELLITE"
	case 3:
		return "COMBINED"
	default:
		if t == 0 {
			return dash
		}
		return fmt.Sprintf("%d", t)
	}
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return dash
	}
	return s
}
