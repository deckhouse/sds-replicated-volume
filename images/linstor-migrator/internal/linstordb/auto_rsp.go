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
	"fmt"
	"sort"
	"strings"

	sncv1alpha1 "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	srvv1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

const (
	storDriverStorPoolNameKey = "StorDriver/StorPoolName"
	linstorDriverLVM          = "LVM"
	linstorDriverLVMThin      = "LVM_THIN"
)

// AutoRSPBuildResult holds spec fields for a migration ReplicatedStoragePool.
type AutoRSPBuildResult struct {
	Type            srvv1alpha1.ReplicatedStoragePoolType
	LVMVolumeGroups []srvv1alpha1.ReplicatedStoragePoolLVMVolumeGroups
}

func normalizeStorPoolKey(nodeName, poolName string) string {
	return strings.ToLower(strings.TrimSpace(nodeName)) + "\x00" + strings.ToLower(strings.TrimSpace(poolName))
}

// parseStorPoolConfInstance extracts LINSTOR node and pool names from PropsContainers.props_instance
// when it references /STORPOOLCONF/<node>/<pool>.
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
	// Pool name is a single segment in observed clusters; join defensively if more segments appear.
	return nodeName, strings.Join(parts[2:], "/"), true
}

// parseStorPoolNameProp parses StorDriver/StorPoolName (VG for thick, VG/thinpool for thin).
func parseStorPoolNameProp(prop string) (vgName, thinPoolName string, err error) {
	prop = strings.TrimSpace(prop)
	if prop == "" {
		return "", "", fmt.Errorf("empty StorDriver/StorPoolName value")
	}
	segs := strings.Split(prop, "/")
	if len(segs) == 1 {
		return segs[0], "", nil
	}
	if len(segs) == 2 {
		return segs[0], segs[1], nil
	}
	return "", "", fmt.Errorf("unexpected StorDriver/StorPoolName format %q", prop)
}

func thinPoolExistsInLVG(lvg sncv1alpha1.LVMVolumeGroup, thinName string) bool {
	for _, tp := range lvg.Spec.ThinPools {
		if strings.EqualFold(tp.Name, thinName) {
			return true
		}
	}
	return false
}

// BuildAutoReplicatedStoragePoolSpec builds RSP type and lvmVolumeGroups for a LINSTOR storage pool name
// using NodeStorPool + PropsContainers + cluster LVMVolumeGroup objects.
func (db *LinstorDB) BuildAutoReplicatedStoragePoolSpec(
	linstorPoolName string,
	lvgs map[string]sncv1alpha1.LVMVolumeGroup,
) (AutoRSPBuildResult, error) {
	if linstorPoolName == "" {
		return AutoRSPBuildResult{}, fmt.Errorf("linstor pool name is empty")
	}
	var (
		out          AutoRSPBuildResult
		poolType     srvv1alpha1.ReplicatedStoragePoolType
		poolTypeSet  bool
		byLVGName    = make(map[string]srvv1alpha1.ReplicatedStoragePoolLVMVolumeGroups)
		matchedNodes int
	)
	for _, nsp := range db.LVMNodeStorPools {
		if !strings.EqualFold(nsp.Spec.PoolName, linstorPoolName) {
			continue
		}
		matchedNodes++
		propVal := db.StorPoolDriverStorPoolName[normalizeStorPoolKey(nsp.Spec.NodeName, nsp.Spec.PoolName)]
		if propVal == "" {
			return AutoRSPBuildResult{}, fmt.Errorf("no StorDriver/StorPoolName for node %q pool %q", nsp.Spec.NodeName, nsp.Spec.PoolName)
		}
		vgOnNode, thinFromProp, err := parseStorPoolNameProp(propVal)
		if err != nil {
			return AutoRSPBuildResult{}, fmt.Errorf("node %q pool %q: %w", nsp.Spec.NodeName, nsp.Spec.PoolName, err)
		}
		var wantType srvv1alpha1.ReplicatedStoragePoolType
		switch strings.ToUpper(strings.TrimSpace(nsp.Spec.DriverName)) {
		case linstorDriverLVM:
			wantType = srvv1alpha1.ReplicatedStoragePoolTypeLVM
			if thinFromProp != "" {
				return AutoRSPBuildResult{}, fmt.Errorf("node %q pool %q: LVM driver but thin path in StorPoolName %q", nsp.Spec.NodeName, nsp.Spec.PoolName, propVal)
			}
		case linstorDriverLVMThin:
			wantType = srvv1alpha1.ReplicatedStoragePoolTypeLVMThin
			if thinFromProp == "" {
				return AutoRSPBuildResult{}, fmt.Errorf("node %q pool %q: LVM_THIN driver but missing thin pool in StorPoolName %q", nsp.Spec.NodeName, nsp.Spec.PoolName, propVal)
			}
		default:
			continue
		}
		if !poolTypeSet {
			poolType = wantType
			poolTypeSet = true
		} else if poolType != wantType {
			return AutoRSPBuildResult{}, fmt.Errorf("mixed LVM and LVM_THIN NodeStorPool for pool %q", linstorPoolName)
		}
		var found *sncv1alpha1.LVMVolumeGroup
		for i := range lvgs {
			lvg := lvgs[i]
			if !strings.EqualFold(lvg.Spec.Local.NodeName, nsp.Spec.NodeName) {
				continue
			}
			if strings.EqualFold(lvg.Spec.ActualVGNameOnTheNode, vgOnNode) {
				found = &lvg
				break
			}
		}
		if found == nil {
			return AutoRSPBuildResult{}, fmt.Errorf("no LVMVolumeGroup for node %q VG %q (pool %q)", nsp.Spec.NodeName, vgOnNode, linstorPoolName)
		}
		entry := srvv1alpha1.ReplicatedStoragePoolLVMVolumeGroups{Name: found.Name}
		if wantType == srvv1alpha1.ReplicatedStoragePoolTypeLVMThin {
			if !thinPoolExistsInLVG(*found, thinFromProp) {
				return AutoRSPBuildResult{}, fmt.Errorf("LVMVolumeGroup %q has no thin pool %q", found.Name, thinFromProp)
			}
			entry.ThinPoolName = thinFromProp
		}
		byLVGName[found.Name] = entry
	}
	if matchedNodes == 0 {
		return AutoRSPBuildResult{}, fmt.Errorf("no LVM/LVM_THIN NodeStorPool for pool %q", linstorPoolName)
	}
	if len(byLVGName) == 0 {
		return AutoRSPBuildResult{}, fmt.Errorf("no LVMVolumeGroups resolved for pool %q", linstorPoolName)
	}
	names := make([]string, 0, len(byLVGName))
	for n := range byLVGName {
		names = append(names, n)
	}
	sort.Strings(names)
	out.Type = poolType
	out.LVMVolumeGroups = make([]srvv1alpha1.ReplicatedStoragePoolLVMVolumeGroups, 0, len(names))
	for _, n := range names {
		out.LVMVolumeGroups = append(out.LVMVolumeGroups, byLVGName[n])
	}
	return out, nil
}
