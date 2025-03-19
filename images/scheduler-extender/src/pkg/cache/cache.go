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
	Lvgs            sync.Map // map[string]*LvgCache
	pvcLVGs         sync.Map // map[string][]string
	nodeLVGs        sync.Map // map[string][]string
	log             logger.Logger
	expiredDuration time.Duration
}

type LvgCache struct {
	Lvg       *snc.LVMVolumeGroup
	thickPVCs sync.Map // map[string]*pvcThickCache
	thinPools sync.Map // map[string]*thinPoolCache
}

type thinPoolCache struct {
	pvcs sync.Map // map[string]*pvcCache
}

type pvcThickCache struct {
	pvc          *v1.PersistentVolumeClaim
	selectedNode string
}

// GetAllLVG returns all the LVMVolumeGroups resources stored in the cache.
func (c *Cache) GetAllLVG() map[string]*snc.LVMVolumeGroup {
	lvgs := make(map[string]*snc.LVMVolumeGroup)
	c.Lvgs.Range(func(lvgName, lvgCh any) bool {
		if lvgCh.(*LvgCache).Lvg == nil {
			c.log.Error(fmt.Errorf("LVMVolumeGroup %s is not initialized", lvgName), "[GetAllLVG] an error occurs while iterating the LVMVolumeGroups")
			return true
		}

		lvgs[lvgName.(string)] = lvgCh.(*LvgCache).Lvg
		return true
	})

	return lvgs
}

// GetLVGThickReservedSpace returns a sum of reserved space by every thick PVC in the selected LVMVolumeGroup resource. If such LVMVolumeGroup resource is not stored, returns an error.
func (c *Cache) GetLVGThickReservedSpace(lvgName string) (int64, error) {
	lvg, found := c.Lvgs.Load(lvgName)
	if !found {
		c.log.Debug(fmt.Sprintf("[GetLVGThickReservedSpace] the LVMVolumeGroup %s was not found in the cache. Returns 0", lvgName))
		return 0, nil
	}

	var space int64
	lvg.(*LvgCache).thickPVCs.Range(func(_, pvcCh any) bool {
		space += pvcCh.(*pvcThickCache).pvc.Spec.Resources.Requests.Storage().Value()
		return true
	})

	return space, nil
}

// GetLVGThinReservedSpace returns a sum of reserved space by every thin PVC in the selected LVMVolumeGroup resource. If such LVMVolumeGroup resource is not stored, returns an error.
func (c *Cache) GetLVGThinReservedSpace(lvgName string, thinPoolName string) (int64, error) {
	lvgCh, found := c.Lvgs.Load(lvgName)
	if !found {
		c.log.Debug(fmt.Sprintf("[GetLVGThinReservedSpace] the LVMVolumeGroup %s was not found in the cache. Returns 0", lvgName))
		return 0, nil
	}

	thinPool, found := lvgCh.(*LvgCache).thinPools.Load(thinPoolName)
	if !found {
		c.log.Debug(fmt.Sprintf("[GetLVGThinReservedSpace] the Thin pool %s of the LVMVolumeGroup %s was not found in the cache. Returns 0", lvgName, thinPoolName))
		return 0, nil
	}

	var space int64
	thinPool.(*thinPoolCache).pvcs.Range(func(_, pvcCh any) bool {
		space += pvcCh.(*pvcThickCache).pvc.Spec.Resources.Requests.Storage().Value()
		return true
	})

	return space, nil
}
