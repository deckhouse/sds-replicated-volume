package cache

import (
	"fmt"
	"scheduler-extender/pkg/logger"
	"sync"
	"time"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
)

type Cache struct {
	lvgs            sync.Map // map[string]*lvgCache
	pvcLVGs         sync.Map // map[string][]string
	nodeLVGs        sync.Map // map[string][]string
	log             logger.Logger
	expiredDuration time.Duration
}

type lvgCache struct {
	lvg       *snc.LVMVolumeGroup
	thickPVCs sync.Map // map[string]*pvcCache
	thinPools sync.Map // map[string]*thinPoolCache
}

type thinPoolCache struct {
	pvcs sync.Map // map[string]*pvcCache
}

type pvcCache struct {
	pvc          *v1.PersistentVolumeClaim
	selectedNode string
}

// GetAllLVG returns all the LVMVolumeGroups resources stored in the cache.
func (c *Cache) GetAllLVG() map[string]*snc.LVMVolumeGroup {
	lvgs := make(map[string]*snc.LVMVolumeGroup)
	c.lvgs.Range(func(lvgName, lvgCh any) bool {
		if lvgCh.(*lvgCache).lvg == nil {
			c.log.Error(fmt.Errorf("LVMVolumeGroup %s is not initialized", lvgName), "[GetAllLVG] an error occurs while iterating the LVMVolumeGroups")
			return true
		}

		lvgs[lvgName.(string)] = lvgCh.(*lvgCache).lvg
		return true
	})

	return lvgs
}

// GetLVGThickReservedSpace returns a sum of reserved space by every thick PVC in the selected LVMVolumeGroup resource. If such LVMVolumeGroup resource is not stored, returns an error.
func (c *Cache) GetLVGThickReservedSpace(lvgName string) (int64, error) {
	lvg, found := c.lvgs.Load(lvgName)
	if !found {
		c.log.Debug(fmt.Sprintf("[GetLVGThickReservedSpace] the LVMVolumeGroup %s was not found in the cache. Returns 0", lvgName))
		return 0, nil
	}

	var space int64
	lvg.(*lvgCache).thickPVCs.Range(func(_, pvcCh any) bool {
		space += pvcCh.(*pvcCache).pvc.Spec.Resources.Requests.Storage().Value()
		return true
	})

	return space, nil
}

// GetLVGThinReservedSpace returns a sum of reserved space by every thin PVC in the selected LVMVolumeGroup resource. If such LVMVolumeGroup resource is not stored, returns an error.
func (c *Cache) GetLVGThinReservedSpace(lvgName string, thinPoolName string) (int64, error) {
	lvgCh, found := c.lvgs.Load(lvgName)
	if !found {
		c.log.Debug(fmt.Sprintf("[GetLVGThinReservedSpace] the LVMVolumeGroup %s was not found in the cache. Returns 0", lvgName))
		return 0, nil
	}

	thinPool, found := lvgCh.(*lvgCache).thinPools.Load(thinPoolName)
	if !found {
		c.log.Debug(fmt.Sprintf("[GetLVGThinReservedSpace] the Thin pool %s of the LVMVolumeGroup %s was not found in the cache. Returns 0", lvgName, thinPoolName))
		return 0, nil
	}

	var space int64
	thinPool.(*thinPoolCache).pvcs.Range(func(_, pvcCh any) bool {
		space += pvcCh.(*pvcCache).pvc.Spec.Resources.Requests.Storage().Value()
		return true
	})

	return space, nil
}
