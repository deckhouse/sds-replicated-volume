/*
Copyright 2025 Flant JSC

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

package linstordb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	kubecl "sigs.k8s.io/controller-runtime/pkg/client"

	srvlinstor "github.com/deckhouse/sds-replicated-volume/api/linstor"
)

// LINSTOR resource flags.
const (
	ResourceFlagDiskful    = 0
	ResourceFlagDiskless   = 260
	ResourceFlagTieBreaker = 388
)

// LinstorDB holds all LINSTOR data fetched from Kubernetes CRDs at startup.
// This avoids making additional API requests during migration.
type LinstorDB struct {
	// VolumeDefinitions stores LINSTOR volume definitions.
	// Key: resource name in lowercase.
	VolumeDefinitions map[string]srvlinstor.VolumeDefinitions

	// ResourceDefinitions stores LINSTOR resource definitions.
	// Key: resource name in lowercase.
	ResourceDefinitions map[string]srvlinstor.ResourceDefinitions

	// ResourceGroups stores LINSTOR resource groups.
	// Key: resource group name.
	ResourceGroups map[string]srvlinstor.ResourceGroups

	// Resources stores LINSTOR resources grouped by resource name.
	// Key: resource name in lowercase.
	// Value: slice of resources (multiple resources per name on different nodes).
	Resources map[string][]srvlinstor.Resources

	// LayerResourcesIds stores all LINSTOR layer resource identifiers.
	LayerResourcesIds *srvlinstor.LayerResourceIdsList

	// LayerDrbdResources stores LINSTOR layer DRBD resources.
	// Key: layer resource identifier.
	LayerDrbdResources map[int]srvlinstor.LayerDrbdResources

	// LayerDrbdResourceDefinitions stores LINSTOR layer DRBD resource definitions.
	// Key: resource name in lowercase.
	LayerDrbdResourceDefinitions map[string]srvlinstor.LayerDrbdResourceDefinitions

	// StorPoolDriverStorPoolName maps normalized "node\x00pool" (lowercase) to StorDriver/StorPoolName (VG or VG/thin).
	StorPoolDriverStorPoolName map[string]string

	// LVMNodeStorPools lists NodeStorPool entries with driver LVM or LVM_THIN only.
	LVMNodeStorPools []srvlinstor.NodeStorPool
}

// Init fetches all required LINSTOR data from Kubernetes and returns a populated LinstorDB.
func Init(ctx context.Context, kClient kubecl.Client) (*LinstorDB, error) {
	db := &LinstorDB{}

	volumeDefinitions := &srvlinstor.VolumeDefinitionsList{}
	if err := kClient.List(ctx, volumeDefinitions); err != nil {
		return nil, fmt.Errorf("failed to list LINSTOR VolumeDefinitions: %w", err)
	}
	db.VolumeDefinitions = make(map[string]srvlinstor.VolumeDefinitions, len(volumeDefinitions.Items))
	for _, vd := range volumeDefinitions.Items {
		db.VolumeDefinitions[strings.ToLower(vd.Spec.ResourceName)] = vd
	}

	resourceDefinitions := &srvlinstor.ResourceDefinitionsList{}
	if err := kClient.List(ctx, resourceDefinitions); err != nil {
		return nil, fmt.Errorf("failed to list LINSTOR ResourceDefinitions: %w", err)
	}
	db.ResourceDefinitions = make(map[string]srvlinstor.ResourceDefinitions, len(resourceDefinitions.Items))
	for _, rd := range resourceDefinitions.Items {
		db.ResourceDefinitions[strings.ToLower(rd.Spec.ResourceName)] = rd
	}

	resourceGroups := &srvlinstor.ResourceGroupsList{}
	if err := kClient.List(ctx, resourceGroups); err != nil {
		return nil, fmt.Errorf("failed to list LINSTOR ResourceGroups: %w", err)
	}
	db.ResourceGroups = make(map[string]srvlinstor.ResourceGroups, len(resourceGroups.Items))
	for _, rg := range resourceGroups.Items {
		db.ResourceGroups[rg.Spec.ResourceGroupName] = rg
	}

	resources := &srvlinstor.ResourcesList{}
	if err := kClient.List(ctx, resources); err != nil {
		return nil, fmt.Errorf("failed to list LINSTOR Resources: %w", err)
	}
	db.Resources = make(map[string][]srvlinstor.Resources, len(resources.Items))
	for _, r := range resources.Items {
		key := strings.ToLower(r.Spec.ResourceName)
		db.Resources[key] = append(db.Resources[key], r)
	}

	db.LayerResourcesIds = &srvlinstor.LayerResourceIdsList{}
	if err := kClient.List(ctx, db.LayerResourcesIds); err != nil {
		return nil, fmt.Errorf("failed to list LINSTOR LayerResourceIds: %w", err)
	}

	layerDrbdResources := &srvlinstor.LayerDrbdResourcesList{}
	if err := kClient.List(ctx, layerDrbdResources); err != nil {
		return nil, fmt.Errorf("failed to list LINSTOR LayerDrbdResources: %w", err)
	}
	db.LayerDrbdResources = make(map[int]srvlinstor.LayerDrbdResources, len(layerDrbdResources.Items))
	for _, ldr := range layerDrbdResources.Items {
		db.LayerDrbdResources[ldr.Spec.LayerResourceID] = ldr
	}

	layerDrbdResourceDefinitions := &srvlinstor.LayerDrbdResourceDefinitionsList{}
	if err := kClient.List(ctx, layerDrbdResourceDefinitions); err != nil {
		return nil, fmt.Errorf("failed to list LINSTOR LayerDrbdResourceDefinitions: %w", err)
	}
	db.LayerDrbdResourceDefinitions = make(map[string]srvlinstor.LayerDrbdResourceDefinitions, len(layerDrbdResourceDefinitions.Items))
	for _, ldrd := range layerDrbdResourceDefinitions.Items {
		db.LayerDrbdResourceDefinitions[strings.ToLower(ldrd.Spec.ResourceName)] = ldrd
	}

	propsContainers := &srvlinstor.PropsContainersList{}
	if err := kClient.List(ctx, propsContainers); err != nil {
		return nil, fmt.Errorf("failed to list LINSTOR PropsContainers: %w", err)
	}
	db.StorPoolDriverStorPoolName = make(map[string]string, len(propsContainers.Items))
	for _, pc := range propsContainers.Items {
		if pc.Spec.PropKey != storDriverStorPoolNameKey {
			continue
		}
		nodeName, poolName, ok := parseStorPoolConfInstance(pc.Spec.PropsInstance)
		if !ok {
			continue
		}
		k := normalizeStorPoolKey(nodeName, poolName)
		db.StorPoolDriverStorPoolName[k] = pc.Spec.PropValue
	}

	nodeStorPools := &srvlinstor.NodeStorPoolList{}
	if err := kClient.List(ctx, nodeStorPools); err != nil {
		return nil, fmt.Errorf("failed to list LINSTOR NodeStorPool: %w", err)
	}
	for _, nsp := range nodeStorPools.Items {
		switch strings.ToUpper(strings.TrimSpace(nsp.Spec.DriverName)) {
		case linstorDriverLVM, linstorDriverLVMThin:
			db.LVMNodeStorPools = append(db.LVMNodeStorPools, nsp)
		default:
		}
	}

	return db, nil
}

// GetSize determines the volume size from PV capacity or LINSTOR VolumeDefinitions.
// pv may be nil, in which case resName is used to look up the size from VolumeDefinitions.
func (db *LinstorDB) GetSize(pv *corev1.PersistentVolume, resName string) (resource.Quantity, error) {
	if pv != nil && pv.Spec.Capacity != nil {
		if qty, ok := pv.Spec.Capacity[corev1.ResourceStorage]; ok && !qty.IsZero() {
			return qty, nil
		}
	}

	vd, ok := db.VolumeDefinitions[strings.ToLower(resName)]
	if ok && vd.Spec.VlmSize > 0 {
		// VlmSize is in KiB, convert to bytes.
		sizeBytes := int64(vd.Spec.VlmSize) * 1024
		return *resource.NewQuantity(sizeBytes, resource.BinarySI), nil
	}

	return resource.Quantity{}, fmt.Errorf("unable to determine size for resource %s", resName)
}

// GetPoolName returns the storage pool name for the given PV from LINSTOR metadata.
func (db *LinstorDB) GetPoolName(pvName string) (string, error) {
	rd, ok := db.ResourceDefinitions[strings.ToLower(pvName)]
	if !ok {
		return "", fmt.Errorf("LINSTOR ResourceDefinition not found for %s", pvName)
	}

	rg, ok := db.ResourceGroups[rd.Spec.ResourceGroupName]
	if !ok {
		return "", fmt.Errorf("LINSTOR ResourceGroup %q not found", rd.Spec.ResourceGroupName)
	}

	if rg.Spec.PoolName == "" {
		return "", fmt.Errorf("LINSTOR pool name is empty for ResourceGroup %q", rg.Spec.ResourceGroupName)
	}

	var poolNames []string
	if err := json.Unmarshal([]byte(rg.Spec.PoolName), &poolNames); err != nil {
		return "", fmt.Errorf("failed to unmarshal LINSTOR pool name: %w", err)
	}
	if len(poolNames) == 0 {
		return "", fmt.Errorf("LINSTOR pool name list is empty for ResourceGroup %q", rg.Spec.ResourceGroupName)
	}

	return poolNames[0], nil
}

// GetNodeID returns the DRBD node ID for a given resource on a given node.
func (db *LinstorDB) GetNodeID(resName string, nodeName string) (int, error) {
	layerResourceID := -1
	for _, lri := range db.LayerResourcesIds.Items {
		if lri.Spec.LayerResourceKind == "DRBD" &&
			strings.EqualFold(lri.Spec.NodeName, nodeName) &&
			strings.EqualFold(lri.Spec.ResourceName, resName) {
			layerResourceID = lri.Spec.LayerResourceID
			break
		}
	}
	if layerResourceID == -1 {
		return -1, fmt.Errorf("DRBD layer resource ID not found for resource %q on node %q", resName, nodeName)
	}

	ldr, ok := db.LayerDrbdResources[layerResourceID]
	if !ok {
		return -1, fmt.Errorf("LayerDrbdResource not found for layer resource ID %d", layerResourceID)
	}

	return ldr.Spec.NodeID, nil
}

// GetSharedSecret returns the DRBD shared secret for the given resource.
func (db *LinstorDB) GetSharedSecret(pvName string) (string, error) {
	ldrd, ok := db.LayerDrbdResourceDefinitions[strings.ToLower(pvName)]
	if !ok {
		return "", fmt.Errorf("LayerDrbdResourceDefinition not found for %q", pvName)
	}
	return ldrd.Spec.Secret, nil
}
