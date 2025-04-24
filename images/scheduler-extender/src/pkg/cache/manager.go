package cache

import (
	"context"
	"errors"
	"fmt"
	"scheduler-extender/pkg/logger"
	"sync"
	"time"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	nameSpace     = "d8-sds-replicated-volume"
	configMapName = "sheduler-extender-cache"
)

type CacheManager struct {
	cache     *Cache
	mu        *sync.Mutex
	log       logger.Logger
	mrg       manager.Manager
	isUpdated bool
}

func NewCacheManager(c *Cache, mu *sync.Mutex, log *logger.Logger) *CacheManager {
	return &CacheManager{
		cache: c,
		mu:    mu,
	}
}

func (cm *CacheManager) RunCleaner(ctx context.Context, pvcCheckInterval time.Duration) {
	t := time.NewTicker(pvcCheckInterval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			cm.log.Info("[CacheManager] Cleaner gracefully stops it's work")
			return
		case <-t.C:
			cm.log.Info("[CacheManager] Starting pvc cleanup")
			cm.mu.Lock()
			cm.cache.clearBoundExpiredPVC(pvcCheckInterval)
			cm.isUpdated = true
			cm.mu.Unlock()
			cm.log.Info("[CacheManager] pvc cleanup has finished")
		}
	}
}

func (cm *CacheManager) RunSaver(ctx context.Context, cacheCheckInterval, configMapUpdateTimeout time.Duration) {
	t := time.NewTicker(cacheCheckInterval * time.Second)

	for {
		select {
		case <-ctx.Done():
			cm.log.Info("[CacheManager] Saver gracefully stops it's work")
			return
		case <-t.C:
			if !cm.isUpdated {
				continue
			}

			cm.mu.Lock()
			cacheStr := cm.cache.String()
			cm.mu.Unlock()
			if cacheStr == "" {
				cm.log.Warning("[CacheManager] Cache returned an empty data string. Skipping iteration")
				continue
			}

			cwt, cancel := context.WithTimeout(ctx, configMapUpdateTimeout*time.Second)
			defer cancel()
			err := cm.SaveOrUpdate(cwt, cm.mrg, cacheStr)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					cm.log.Error(err, "[CacheManager] cache saving process timed out")
				} else {
					cm.log.Error(err, "[CacheManager] cache saving process failed")
				}
			}
			cm.isUpdated = false
		}
	}
}

func (cm *CacheManager) SaveOrUpdate(ctx context.Context, mrg manager.Manager, data string) error {
	cfgMap := &v1.ConfigMap{}
	err := cm.mrg.GetClient().Get(ctx, client.ObjectKey{Name: configMapName, Namespace: nameSpace}, cfgMap)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			cfgMap.ObjectMeta = metav1.ObjectMeta{
				Name:      configMapName,
				Namespace: nameSpace,
			}
			cfgMap.Data = map[string]string{
				"cache": data,
			}
			return cm.mrg.GetClient().Create(ctx, cfgMap)
		}
		return err
	}

	cfgMap.Data["cache"] = data
	return cm.mrg.GetClient().Update(ctx, cfgMap)
}

func (cm *CacheManager) GetAllLVG() map[string]*snc.LVMVolumeGroup {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	return cm.cache.GetAllLVG()
}

func (cm *CacheManager) GetLVGThickReservedSpace(lvgName string) (int64, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	return cm.cache.GetLVGThickReservedSpace(lvgName)
}

func (cm *CacheManager) GetLVGThinReservedSpace(lvgName string, thinPoolName string) (int64, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	return cm.cache.GetLVGThinReservedSpace(lvgName, thinPoolName)
}

func (cm *CacheManager) RemovePVCFromTheCache(pvc *v1.PersistentVolumeClaim) {
	cm.mu.Lock()
	defer func() {
		cm.isUpdated = true
		cm.mu.Unlock()
	}()

	cm.cache.RemovePVCFromTheCache(pvc)
}

func (cm *CacheManager) GetLVGNamesForPVC(pvc *v1.PersistentVolumeClaim) []string {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	return cm.cache.GetLVGNamesForPVC(pvc)
}

func (cm *CacheManager) GetLVGNamesByNodeName(nodeName string) []string {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	return cm.cache.GetLVGNamesByNodeName(nodeName)
}

func (cm *CacheManager) UpdateThickPVC(lvgName string, pvc *v1.PersistentVolumeClaim, provisioner string) error {
	cm.mu.Lock()
	defer func() {
		cm.isUpdated = true
		cm.mu.Unlock()
	}()

	return cm.cache.UpdateThickPVC(lvgName, pvc, provisioner)
}

func (cm *CacheManager) AddThickPVC(lvgName string, pvc *v1.PersistentVolumeClaim, provisioner string) error {
	cm.mu.Lock()
	defer func() {
		cm.isUpdated = true
		cm.mu.Unlock()
	}()

	return cm.cache.AddThickPVC(lvgName, pvc, provisioner)
}

func (cm *CacheManager) UpdateThinPVC(lvgName, thinPoolName string, pvc *v1.PersistentVolumeClaim, provisioner string) error {
	cm.mu.Lock()
	defer func() {
		cm.isUpdated = true
		cm.mu.Unlock()
	}()

	return cm.cache.UpdateThinPVC(lvgName, thinPoolName, pvc, provisioner)
}

func (cm *CacheManager) PrintTheCacheLog() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.log.Cache("*******************CACHE BEGIN*******************")
	cm.log.Cache("[LVMVolumeGroups BEGIN]")
	for lvgName, lvgCh := range cm.cache.storage.Lvgs {
		cm.log.Cache(fmt.Sprintf("[%s]", lvgName))

		for pvcName, pvcCh := range lvgCh.ThickPVCs {
			cm.log.Cache(fmt.Sprintf("      THICK PVC %s, selected node: %s", pvcName, pvcCh.SelectedNode))
		}

		for thinPoolName, thinPool := range lvgCh.ThinPools {
			for pvcName, pvcCh := range thinPool {
				cm.log.Cache(fmt.Sprintf("      THIN POOL %s PVC %s, selected node: %s", thinPoolName, pvcName, pvcCh.SelectedNode))
			}
		}
	}
	cm.log.Cache("[LVMVolumeGroups ENDS]")

	cm.log.Cache("[PVC and LVG BEGINS]")
	for pvcName, lvgs := range cm.cache.storage.PvcLVGs {
		cm.log.Cache(fmt.Sprintf("[PVC: %s]", pvcName))

		for _, lvgName := range lvgs {
			cm.log.Cache(fmt.Sprintf("      LVMVolumeGroup: %s", lvgName))
		}
	}
	cm.log.Cache("[PVC and LVG ENDS]")

	cm.log.Cache("[Node and LVG BEGINS]")
	for nodeName, lvgs := range cm.cache.storage.NodeLVGs {
		cm.log.Cache(fmt.Sprintf("[Node: %s]", nodeName))

		for _, lvgName := range lvgs {
			cm.log.Cache(fmt.Sprintf("      LVMVolumeGroup name: %s", lvgName))
		}
	}
	cm.log.Cache("[Node and LVG ENDS]")

	cm.log.Cache("*******************CACHE END*******************")
}

func (cm *CacheManager) TryGetLVG(name string) *snc.LVMVolumeGroup {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	return cm.cache.TryGetLVG(name)
}

func (cm *CacheManager) UpdateLVG(lvg *snc.LVMVolumeGroup) error {
	cm.mu.Lock()
	defer func() {
		cm.isUpdated = true
		cm.mu.Unlock()
	}()

	return cm.cache.UpdateLVG(lvg)
}

func (cm *CacheManager) AddLVG(lvg *snc.LVMVolumeGroup) {
	cm.mu.Lock()
	defer func() {
		cm.isUpdated = true
		cm.mu.Unlock()
	}()

	cm.cache.AddLVG(lvg)
}

func (cm *CacheManager) DeleteLVG(lvgName string) {
	cm.mu.Lock()
	defer func() {
		cm.isUpdated = true
		cm.mu.Unlock()
	}()

	cm.cache.DeleteLVG(lvgName)
}

func (cm *CacheManager) GetAllPVCForLVG(lvgName string) ([]*v1.PersistentVolumeClaim, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	return cm.cache.GetAllPVCForLVG(lvgName)
}

func (cm *CacheManager) RemoveSpaceReservationForPVCWithSelectedNode(pvc *v1.PersistentVolumeClaim, deviceType string) error {
	cm.mu.Lock()
	defer func() {
		cm.isUpdated = true
		cm.mu.Unlock()
	}()

	return cm.cache.RemoveSpaceReservationForPVCWithSelectedNode(pvc, deviceType)
}
