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

	sncv1alpha1 "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	srvlinstor "github.com/deckhouse/sds-replicated-volume/api/linstor"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	kubecl "sigs.k8s.io/controller-runtime/pkg/client"
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

	// NodeNetInterfaces stores all LINSTOR node network interfaces.
	NodeNetInterfaces *srvlinstor.NodeNetInterfacesList

	// LayerDrbdResourceDefinitions stores LINSTOR layer DRBD resource definitions.
	// Key: resource name in lowercase.
	LayerDrbdResourceDefinitions map[string]srvlinstor.LayerDrbdResourceDefinitions

	// LayerDrbdVolumeDefinitions stores LINSTOR layer DRBD volume definitions.
	// Key: resource name in lowercase.
	LayerDrbdVolumeDefinitions map[string]srvlinstor.LayerDrbdVolumeDefinitions
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

	db.NodeNetInterfaces = &srvlinstor.NodeNetInterfacesList{}
	if err := kClient.List(ctx, db.NodeNetInterfaces); err != nil {
		return nil, fmt.Errorf("failed to list LINSTOR NodeNetInterfaces: %w", err)
	}

	layerDrbdResourceDefinitions := &srvlinstor.LayerDrbdResourceDefinitionsList{}
	if err := kClient.List(ctx, layerDrbdResourceDefinitions); err != nil {
		return nil, fmt.Errorf("failed to list LINSTOR LayerDrbdResourceDefinitions: %w", err)
	}
	db.LayerDrbdResourceDefinitions = make(map[string]srvlinstor.LayerDrbdResourceDefinitions, len(layerDrbdResourceDefinitions.Items))
	for _, ldrd := range layerDrbdResourceDefinitions.Items {
		db.LayerDrbdResourceDefinitions[strings.ToLower(ldrd.Spec.ResourceName)] = ldrd
	}

	layerDrbdVolumeDefinitions := &srvlinstor.LayerDrbdVolumeDefinitionsList{}
	if err := kClient.List(ctx, layerDrbdVolumeDefinitions); err != nil {
		return nil, fmt.Errorf("failed to list LINSTOR LayerDrbdVolumeDefinitions: %w", err)
	}
	db.LayerDrbdVolumeDefinitions = make(map[string]srvlinstor.LayerDrbdVolumeDefinitions, len(layerDrbdVolumeDefinitions.Items))
	for _, ldvd := range layerDrbdVolumeDefinitions.Items {
		db.LayerDrbdVolumeDefinitions[strings.ToLower(ldvd.Spec.ResourceName)] = ldvd
	}

	return db, nil
}

// GetPVSize determines the volume size from PV capacity or LINSTOR VolumeDefinitions.
func (db *LinstorDB) GetPVSize(pv corev1.PersistentVolume) (resource.Quantity, error) {
	if pv.Spec.Capacity != nil {
		if qty, ok := pv.Spec.Capacity[corev1.ResourceStorage]; ok && !qty.IsZero() {
			return qty, nil
		}
	}

	vd, ok := db.VolumeDefinitions[strings.ToLower(pv.Name)]
	if ok && vd.Spec.VlmSize > 0 {
		// VlmSize is in KiB, convert to bytes.
		sizeBytes := int64(vd.Spec.VlmSize) * 1024
		return *resource.NewQuantity(sizeBytes, resource.BinarySI), nil
	}

	return resource.Quantity{}, fmt.Errorf("unable to determine size for PV %s", pv.Name)
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
func (db *LinstorDB) GetNodeID(pvName string, nodeName string) (int, error) {
	layerResourceID := -1
	for _, lri := range db.LayerResourcesIds.Items {
		if lri.Spec.LayerResourceKind == "DRBD" &&
			strings.EqualFold(lri.Spec.NodeName, nodeName) &&
			strings.EqualFold(lri.Spec.ResourceName, pvName) {
			layerResourceID = lri.Spec.LayerResourceID
			break
		}
	}
	if layerResourceID == -1 {
		return -1, fmt.Errorf("DRBD layer resource ID not found for resource %q on node %q", pvName, nodeName)
	}

	ldr, ok := db.LayerDrbdResources[layerResourceID]
	if !ok {
		return -1, fmt.Errorf("LayerDrbdResource not found for layer resource ID %d", layerResourceID)
	}

	return ldr.Spec.NodeID, nil
}

// GetNodeIPv4 returns the IPv4 address for the given node name.
func (db *LinstorDB) GetNodeIPv4(nodeName string) (string, error) {
	for _, nni := range db.NodeNetInterfaces.Items {
		if strings.EqualFold(nni.Spec.NodeName, nodeName) {
			return nni.Spec.InetAddress, nil
		}
	}
	return "", fmt.Errorf("node IPv4 not found for %q", nodeName)
}

// GetDRBDPort returns the DRBD TCP port for the given resource.
func (db *LinstorDB) GetDRBDPort(pvName string) (int, error) {
	ldrd, ok := db.LayerDrbdResourceDefinitions[strings.ToLower(pvName)]
	if !ok {
		return 0, fmt.Errorf("LayerDrbdResourceDefinition not found for %q", pvName)
	}
	return ldrd.Spec.TCPPort, nil
}

// GetSharedSecret returns the DRBD shared secret for the given resource.
func (db *LinstorDB) GetSharedSecret(pvName string) (string, error) {
	ldrd, ok := db.LayerDrbdResourceDefinitions[strings.ToLower(pvName)]
	if !ok {
		return "", fmt.Errorf("LayerDrbdResourceDefinition not found for %q", pvName)
	}
	return ldrd.Spec.Secret, nil
}

// GetDRBDMinor returns the DRBD minor number for the given resource.
func (db *LinstorDB) GetDRBDMinor(pvName string) (int, error) {
	ldvd, ok := db.LayerDrbdVolumeDefinitions[strings.ToLower(pvName)]
	if !ok {
		return 0, fmt.Errorf("LayerDrbdVolumeDefinition not found for %q", pvName)
	}
	return ldvd.Spec.VlmMinorNr, nil
}

// GetReplicaCount returns the replica count for the given resource.
func (db *LinstorDB) GetReplicaCount(pvName string) (byte, error) {
	rd, ok := db.ResourceDefinitions[strings.ToLower(pvName)]
	if !ok {
		return 0, fmt.Errorf("LINSTOR ResourceDefinition not found for %q", pvName)
	}

	rg, ok := db.ResourceGroups[rd.Spec.ResourceGroupName]
	if !ok {
		return 0, fmt.Errorf("LINSTOR ResourceGroup %q not found", rd.Spec.ResourceGroupName)
	}

	if rg.Spec.ReplicaCount == 0 {
		return 0, fmt.Errorf("replica count is 0 for ResourceGroup %q", rg.Spec.ResourceGroupName)
	}

	return byte(rg.Spec.ReplicaCount), nil
}

// GetLVMVolumeGroupNameAndThinPoolName finds the LVMVolumeGroup matching the
// given node from the pool's LVMVolumeGroups map and returns its name and thin pool name.
func GetLVMVolumeGroupNameAndThinPoolName(nodeName string, poolLVMVolumeGroups map[string]sncv1alpha1.LVMVolumeGroup) (string, string, error) {
	for _, lvg := range poolLVMVolumeGroups {
		if strings.EqualFold(lvg.Spec.Local.NodeName, nodeName) {
			thinPoolName := ""
			if len(lvg.Spec.ThinPools) > 0 {
				thinPoolName = lvg.Spec.ThinPools[0].Name
			}
			return lvg.Name, thinPoolName, nil
		}
	}
	return "", "", fmt.Errorf("LVMVolumeGroup not found for node %q", nodeName)
}
