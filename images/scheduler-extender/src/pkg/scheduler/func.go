package scheduler

import (
	"context"
	"fmt"
	"scheduler-extender/pkg/cache"
	"scheduler-extender/pkg/consts"
	"scheduler-extender/pkg/logger"
	"sync"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	v1 "k8s.io/api/storage/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const (
	annotationBetaStorageProvisioner = "volume.beta.kubernetes.io/storage-provisioner"
	annotationStorageProvisioner     = "volume.kubernetes.io/storage-provisioner"
)

type nodeFilter func([]string, map[string]struct{}) ([]string, error)

func shouldProcessPod(ctx context.Context, cl client.Client, log logger.Logger, pod *corev1.Pod, targetProvisioner string) (bool, []corev1.Volume, error) {
	log.Trace(fmt.Sprintf("[ShouldProcessPod] targetProvisioner=%s, pod: %+v", targetProvisioner, pod))
	var discoveredProvisioner string
	shouldProcessPod := false
	targetProvisionerVolumes := make([]corev1.Volume, 0)

	pvcMap, err := getPersistentVolumeClaims(ctx, cl)
	if err != nil {
		return false, nil, fmt.Errorf("[ShouldProcessPod] error getting persistent volumes: %v", err)
	}

	for _, volume := range pod.Spec.Volumes {
		if volume.PersistentVolumeClaim == nil {
			log.Trace(fmt.Sprintf("[ShouldProcessPod] skip volume %s because it doesn't have PVC", volume.Name))
			continue
		}

		log.Trace(fmt.Sprintf("[ShouldProcessPod] process volume: %+v that has pvc: %+v", volume, volume.PersistentVolumeClaim))
		pvcName := volume.PersistentVolumeClaim.ClaimName
		pvc, found := pvcMap[pvcName]
		if !found {
			return false, nil, fmt.Errorf("[ShouldProcessPod] error getting PVC %s/%s: %v", pod.Namespace, pvcName, err)
		}

		log.Trace(fmt.Sprintf("[ShouldProcessPod] Successfully get PVC %s/%s: %+v", pod.Namespace, pvcName, pvc))

		discoveredProvisioner, err = getProvisionerFromPVC(ctx, cl, log, pvc)
		if err != nil {
			return false, nil, fmt.Errorf("[ShouldProcessPod] error getting provisioner from PVC %s/%s: %v", pod.Namespace, pvcName, err)
		}
		log.Trace(fmt.Sprintf("[ShouldProcessPod] discovered provisioner: %s", discoveredProvisioner))
		if discoveredProvisioner == targetProvisioner {
			log.Trace(fmt.Sprintf("[ShouldProcessPod] provisioner matches targetProvisioner %s. Pod: %s/%s", pod.Namespace, pod.Name, targetProvisioner))
			shouldProcessPod = true
			targetProvisionerVolumes = append(targetProvisionerVolumes, volume)
		}
		log.Trace(fmt.Sprintf("[ShouldProcessPod] provisioner %s doesn't match targetProvisioner %s. Skip volume %s.", discoveredProvisioner, targetProvisioner, volume.Name))
	}

	if shouldProcessPod {
		log.Trace(fmt.Sprintf("[ShouldProcessPod] targetProvisioner %s found in pod volumes. Pod: %s/%s. Volumes that match: %+v", targetProvisioner, pod.Namespace, pod.Name, targetProvisionerVolumes))
		return true, targetProvisionerVolumes, nil
	}

	log.Trace(fmt.Sprintf("[ShouldProcessPod] can't find targetProvisioner %s in pod volumes. Skip pod: %s/%s", targetProvisioner, pod.Namespace, pod.Name))
	return false, nil, nil
}

func getProvisionerFromPVC(ctx context.Context, cl client.Client, log logger.Logger, pvc *corev1.PersistentVolumeClaim) (string, error) {
	discoveredProvisioner := ""
	log.Trace(fmt.Sprintf("[getProvisionerFromPVC] check provisioner in pvc annotations: %+v", pvc.Annotations))

	discoveredProvisioner = pvc.Annotations[annotationStorageProvisioner]
	if discoveredProvisioner != "" {
		log.Trace(fmt.Sprintf("[getProvisionerFromPVC] discovered provisioner in pvc annotations: %s", discoveredProvisioner))
	} else {
		discoveredProvisioner = pvc.Annotations[annotationBetaStorageProvisioner]
		log.Trace(fmt.Sprintf("[getProvisionerFromPVC] discovered provisioner in beta pvc annotations: %s", discoveredProvisioner))
	}

	if discoveredProvisioner == "" && pvc.Spec.StorageClassName != nil && *pvc.Spec.StorageClassName != "" {
		log.Trace(fmt.Sprintf("[getProvisionerFromPVC] can't find provisioner in pvc annotations, check in storageClass with name: %s", *pvc.Spec.StorageClassName))

		storageClass := &storagev1.StorageClass{}
		if err := cl.Get(ctx, client.ObjectKey{Name: *pvc.Spec.StorageClassName}, storageClass); err != nil {
			if !k8serrors.IsNotFound(err) {
				return "", fmt.Errorf("[getProvisionerFromPVC] error getting StorageClass %s: %v", *pvc.Spec.StorageClassName, err)
			}
			log.Warning(fmt.Sprintf("[getProvisionerFromPVC] StorageClass %s for PVC %s/%s not found", *pvc.Spec.StorageClassName, pvc.Namespace, pvc.Name))
		}
		discoveredProvisioner = storageClass.Provisioner
		log.Trace(fmt.Sprintf("[getProvisionerFromPVC] discover provisioner %s in storageClass: %+v", discoveredProvisioner, storageClass))
	}

	if discoveredProvisioner == "" && pvc.Spec.VolumeName != "" {
		log.Trace(fmt.Sprintf("[getProvisionerFromPVC] can't find provisioner in pvc annotations and StorageClass, check in PV with name: %s", pvc.Spec.VolumeName))

		pv := &corev1.PersistentVolume{}
		if err := cl.Get(ctx, client.ObjectKey{Name: pvc.Spec.VolumeName}, pv); err != nil {
			if !k8serrors.IsNotFound(err) {
				return "", fmt.Errorf("[getProvisionerFromPVC] error getting PV %s for PVC %s/%s: %v", pvc.Spec.VolumeName, pvc.Namespace, pvc.Name, err)
			}
			log.Warning(fmt.Sprintf("[getProvisionerFromPVC] PV %s for PVC %s/%s not found", pvc.Spec.VolumeName, pvc.Namespace, pvc.Name))
		}

		if pv.Spec.CSI != nil {
			discoveredProvisioner = pv.Spec.CSI.Driver
		}

		log.Trace(fmt.Sprintf("[getProvisionerFromPVC] discover provisioner %s in PV: %+v", discoveredProvisioner, pv))
	}

	return discoveredProvisioner, nil
}

func getReplicatedStoragePools(ctx context.Context, cl client.Client) (map[string]*srv.ReplicatedStoragePool, error) {
	rsp := &srv.ReplicatedStoragePoolList{}
	err := cl.List(ctx, rsp)
	if err != nil {
		return nil, err
	}

	rpsMap := make(map[string]*srv.ReplicatedStoragePool, len(rsp.Items))
	for _, rp := range rsp.Items {
		rpsMap[rp.Name] = &rp
	}

	return rpsMap, nil
}

func getReplicatedStorageClasses(ctx context.Context, cl client.Client) (map[string]*srv.ReplicatedStorageClass, error) {
	rscs := &srv.ReplicatedStorageClassList{}
	err := cl.List(ctx, rscs)
	if err != nil {
		return nil, err
	}

	rscMap := make(map[string]*srv.ReplicatedStorageClass, len(rscs.Items))
	for _, rsc := range rscs.Items {
		rscMap[rsc.Name] = &rsc
	}

	return rscMap, nil
}

func getlvmVolumeGroups(ctx context.Context, cl client.Client) (map[string]*snc.LVMVolumeGroup, error) {
	lvmList := &snc.LVMVolumeGroupList{}
	err := cl.List(ctx, lvmList)
	if err != nil {
		return nil, err
	}

	lvmMap := make(map[string]*snc.LVMVolumeGroup, len(lvmList.Items))
	for _, lvm := range lvmList.Items {
		lvmMap[lvm.Name] = &lvm
	}

	return lvmMap, nil
}

func getNodeWithLvmVgsMap(ctx context.Context, cl client.Client) (map[string][]*snc.LVMVolumeGroup, error) {
	lvmList := &snc.LVMVolumeGroupList{}
	err := cl.List(ctx, lvmList)
	if err != nil {
		return nil, err
	}

	nodeToLvmMap := make(map[string][]*snc.LVMVolumeGroup, len(lvmList.Items))
	for _, lvm := range lvmList.Items {
		nodeToLvmMap[lvm.Spec.Local.NodeName] = append(nodeToLvmMap[lvm.Spec.Local.NodeName], &lvm)
	}

	return nodeToLvmMap, nil
}

func getDRBDResourceMap(ctx context.Context, cl client.Client) (map[string]*srv.DRBDResource, error) {
	drbdList := &srv.DRBDResourceList{}
	err := cl.List(ctx, drbdList)
	if err != nil {
		return nil, err
	}

	drbdMap := make(map[string]*srv.DRBDResource, len(drbdList.Items))
	for _, drbd := range drbdList.Items {
		drbdMap[drbd.Name] = &drbd
	}

	return drbdMap, nil
}

func getDRBDNodesMap(ctx context.Context, cl client.Client) (map[string]*srv.DRBDNode, error) {
	drbdNodes := &srv.DRBDNodeList{}
	err := cl.List(ctx, drbdNodes)
	if err != nil {
		return nil, err
	}

	drbdNodesMap := make(map[string]*srv.DRBDNode, len(drbdNodes.Items))
	for _, drbdNode := range drbdNodes.Items {
		drbdNodesMap[drbdNode.Name] = &drbdNode
	}

	return drbdNodesMap, nil
}

func getPersistentVolumeClaims(ctx context.Context, cl client.Client) (map[string]*corev1.PersistentVolumeClaim, error) {
	pvs := &corev1.PersistentVolumeClaimList{}
	err := cl.List(ctx, pvs)
	if err != nil {
		return nil, err
	}

	pvcMap := make(map[string]*corev1.PersistentVolumeClaim, len(pvs.Items))
	for _, pvc := range pvs.Items {
		pvcMap[pvc.Name] = &pvc
	}

	return pvcMap, nil
}

func getPersistentVolumes(ctx context.Context, cl client.Client) (map[string]*corev1.PersistentVolume, error) {
	pvs := &corev1.PersistentVolumeList{}
	err := cl.List(ctx, pvs)
	if err != nil {
		return nil, err
	}

	pvMap := make(map[string]*corev1.PersistentVolume, len(pvs.Items))
	for _, pv := range pvs.Items {
		pvMap[pv.Name] = &pv
	}

	return pvMap, nil
}

func getNodeNames(inputData ExtenderArgs) ([]string, error) {
	if inputData.NodeNames != nil && len(*inputData.NodeNames) > 0 {
		return *inputData.NodeNames, nil
	}

	if inputData.Nodes != nil && len(inputData.Nodes.Items) > 0 {
		nodeNames := make([]string, 0, len(inputData.Nodes.Items))
		for _, node := range inputData.Nodes.Items {
			nodeNames = append(nodeNames, node.Name)
		}
		return nodeNames, nil
	}

	return nil, fmt.Errorf("no nodes provided")
}

func filterNodes(
	log logger.Logger,
	schedulerCache *cache.Cache,
	nodeNames *[]string,
	pod *corev1.Pod,
	pvcs map[string]*corev1.PersistentVolumeClaim,
	scs map[string]*v1.StorageClass,
	pvcRequests map[string]PVCRequest,
	rscMap map[string]*srv.ReplicatedStorageClass,
	drbdResourceMap map[string]*srv.DRBDResource,
	drbdNodesMap map[string]*srv.DRBDNode,
) (*ExtenderFilterResult, error) {
	lvgs := schedulerCache.GetAllLVG()
	for _, lvg := range lvgs {
		log.Trace(fmt.Sprintf("[filterNodes] LVMVolumeGroup %s in the cache", lvg.Name))
	}

	log.Debug(fmt.Sprintf("[filterNodes] starts to get LVMVolumeGroups for Storage Classes for a Pod %s/%s", pod.Namespace, pod.Name))
	scLVGs, err := GetSortedLVGsFromSC(scs)
	if err != nil {
		return nil, err
	}

	usedLVGs := RemoveUnusedLVGs(lvgs, scLVGs)
	for _, lvg := range usedLVGs {
		log.Trace(fmt.Sprintf("[filterNodes] the LVMVolumeGroup %s is actually used. VG size: %s, allocatedSize: %s", lvg.Name, lvg.Status.VGSize.String(), lvg.Status.AllocatedSize.String()))
	}

	lvgsThickFree := getLVGThickFreeSpaces(usedLVGs)
	log.Trace(fmt.Sprintf("[filterNodes] for a Pod %s/%s current LVMVolumeGroups Thick FreeSpace on the node: %+v", pod.Namespace, pod.Name, lvgsThickFree))
	for lvgName, freeSpace := range lvgsThickFree {
		log.Trace(fmt.Sprintf("[filterNodes] current LVMVolumeGroup %s Thick free space %s", lvgName, resource.NewQuantity(freeSpace, resource.BinarySI)))
		reservedSpace, err := schedulerCache.GetLVGThickReservedSpace(lvgName)
		if err != nil {
			log.Error(err, fmt.Sprintf("[filterNodes] unable to count cache reserved space for the LVMVolumeGroup %s", lvgName))
			continue
		}
		log.Trace(fmt.Sprintf("[filterNodes] current LVMVolumeGroup %s reserved PVC space %s", lvgName, resource.NewQuantity(reservedSpace, resource.BinarySI)))
		lvgsThickFree[lvgName] -= reservedSpace
	}
	log.Trace(fmt.Sprintf("[filterNodes] for a Pod %s/%s current LVMVolumeGroups Thick FreeSpace with reserved PVC: %+v", pod.Namespace, pod.Name, lvgsThickFree))

	lvgsThinFree := getLVGThinFreeSpaces(usedLVGs)
	log.Trace(fmt.Sprintf("[filterNodes] for a Pod %s/%s current LVMVolumeGroups Thin FreeSpace on the node: %+v", pod.Namespace, pod.Name, lvgsThinFree))
	for lvgName, thinPools := range lvgsThinFree {
		for tpName, freeSpace := range thinPools {
			log.Trace(fmt.Sprintf("[filterNodes] current LVMVolumeGroup %s Thin Pool %s free space %s", lvgName, tpName, resource.NewQuantity(freeSpace, resource.BinarySI)))
			reservedSpace, err := schedulerCache.GetLVGThinReservedSpace(lvgName, tpName)
			if err != nil {
				log.Error(err, fmt.Sprintf("[filterNodes] unable to count cache reserved space for the Thin pool %s of the LVMVolumeGroup %s", tpName, lvgName))
				continue
			}
			log.Trace(fmt.Sprintf("[filterNodes] current LVMVolumeGroup %s Thin pool %s reserved PVC space %s", lvgName, tpName, resource.NewQuantity(reservedSpace, resource.BinarySI)))
			lvgsThinFree[lvgName][tpName] -= reservedSpace
		}
	}

	nodeLVGs := SortLVGsByNodeName(usedLVGs)
	for n, ls := range nodeLVGs {
		for _, l := range ls {
			log.Trace(fmt.Sprintf("[filterNodes] the LVMVolumeGroup %s belongs to node %s", l.Name, n))
		}
	}

	// these are the nodes which might store every PVC from the Pod
	commonNodes, err := getCommonNodesByStorageClasses(scs, nodeLVGs)
	if err != nil {
		log.Error(err, fmt.Sprintf("[filterNodes] unable to get common nodes for PVCs from the Pod %s/%s", pod.Namespace, pod.Name))
		return nil, err
	}

	type ResultWithError struct {
		nodeName string
		err      error
	}

	resCh := make(chan ResultWithError, len(*nodeNames))
	result := &ExtenderFilterResult{
		NodeNames:   &[]string{},
		FailedNodes: FailedNodesMap{},
	}

	var thickMapMtx sync.RWMutex
	var thinMapMtx sync.RWMutex
	var wg sync.WaitGroup
	wg.Add(len(*nodeNames))

	for _, nodeName := range *nodeNames {
		go func(nodeName string, log logger.Logger) {
			log.Debug(fmt.Sprintf("[filterNodes] Node %s is being filtered now", nodeName))
			log.Debug(fmt.Sprintf("[filterNodes]"))
			defer wg.Done()

			nodeLvgs := commonNodes[nodeName]

			for _, pvc := range pvcs {
				log.Debug(fmt.Sprintf("[filterNodes] pvc %s is being processed. node: %s", pvc.Name, nodeName))

				lvgsFromSC := scLVGs[*pvc.Spec.StorageClassName]
				replicatedStorageClass := rscMap[*pvc.Spec.StorageClassName]
				commonLVG := findMatchedLVG(nodeLvgs, lvgsFromSC)
				isDrbdDiskfulNode := isDrbdDiskfulNode(drbdResourceMap, pvc.Spec.VolumeName, nodeName)
				nodeHasEnoughSpace := nodeHasEnoughSpace(pvcRequests, lvgsThickFree, lvgsThinFree, commonLVG, pvc, lvgs, &thickMapMtx, &thinMapMtx)

				switch replicatedStorageClass.Spec.VolumeAccess {
				case "Local":
					log.Debug(fmt.Sprintf("[filterNodes] ReplicatedCS VolumeAccess %s, node %s, pvc %s", replicatedStorageClass.Spec.VolumeAccess, nodeName, pvc.Name))
					if pvc.Spec.VolumeName == "" {
						if commonLVG == nil {
							log.Debug(fmt.Sprintf("[filterNodes] node %s does not meet criteria: 0 copies of a volume storeed on it. pvc %s", nodeName, pvc.Name))
							resCh <- ResultWithError{
								nodeName: nodeName,
								err:      fmt.Errorf("node %s does not contain any lvgs from storage class %s", nodeName, replicatedStorageClass.Name),
							}
							return
						}

						if !nodeHasEnoughSpace {
							log.Debug(fmt.Sprintf("[filterNodes] node %s does not meet criteria: not enough free space. pvc %s", nodeName, pvc.Name))
							resCh <- ResultWithError{
								nodeName: nodeName,
								err:      fmt.Errorf("node does not have enough space in lvg %s for pvc %s/%s", commonLVG.Name, pvc.Namespace, pvc.Name),
							}
							return
						}
						break
					}

					if !isDrbdDiskfulNode {
						log.Debug(fmt.Sprintf("[filterNodes] node %s does not meet criteria: not a diskful DRBD node. pvc %s", nodeName, pvc.Name))
						resCh <- ResultWithError{
							nodeName: nodeName,
							err:      fmt.Errorf("node %s is not diskful for pv %s", nodeName, pvc.Spec.VolumeName),
						}
						return
					}

				case "EventuallyLocal":
					log.Debug(fmt.Sprintf("[filterNodes] ReplicatedCS VolumeAccess %s, node %s, pvc %s", replicatedStorageClass.Spec.VolumeAccess, nodeName, pvc.Name))

					if pvc.Spec.VolumeName == "" {
						if commonLVG == nil {
							log.Debug(fmt.Sprintf("[filterNodes] node %s does not meet criteria: 0 copies of a volume storeed on it. pvc %s", nodeName, pvc.Name))
							resCh <- ResultWithError{
								nodeName: nodeName,
								err:      fmt.Errorf("node %s does not contain any lvgs from storage class %s", nodeName, replicatedStorageClass.Name),
							}
							return
						}
						if !nodeHasEnoughSpace {
							log.Debug(fmt.Sprintf("[filterNodes] node %s does not meet criteria: not enough free space. pvc %s", nodeName, pvc.Name))
							resCh <- ResultWithError{
								nodeName: nodeName,
								err:      fmt.Errorf("node does not have enough space in lvg %s for pvc %s/%s", commonLVG.Name, pvc.Namespace, pvc.Name),
							}
							return
						}
						break
					}

					if isDrbdDiskfulNode {
						log.Debug(fmt.Sprintf("[filterNodes] node %s does not meet criteria: not a diskful DRBD node. pvc %s", nodeName, pvc.Name))
						resCh <- ResultWithError{
							nodeName: nodeName,
							err:      nil,
						}
						return
					}
					if commonLVG == nil {
						log.Debug(fmt.Sprintf("[filterNodes] node %s does not meet criteria: 0 copies of a volume storeed on it. pvc %s", nodeName, pvc.Name))
						resCh <- ResultWithError{
							nodeName: nodeName,
							err:      fmt.Errorf("node %s does not contain any lvgs from storage class %s", nodeName, replicatedStorageClass.Name),
						}
						return
					}
					if !nodeHasEnoughSpace {
						log.Debug(fmt.Sprintf("[filterNodes] node %s does not meet criteria: not enough free space. pvc %s", nodeName, pvc.Name))
						resCh <- ResultWithError{
							nodeName: nodeName,
							err:      fmt.Errorf("node does not have enough space in lvg %s for pvc %s/%s", commonLVG.Name, pvc.Namespace, pvc.Name),
						}
						return
					}

				case "PreferablyLocal":
					log.Debug(fmt.Sprintf("[filterNodes] node %s does not meet criteria: not enough free space. pvc %s", nodeName, pvc.Name))

					if pvc.Spec.VolumeName == "" {
						if !nodeHasEnoughSpace {
							resCh <- ResultWithError{
								nodeName: nodeName,
								err:      fmt.Errorf("node does not have enough space in lvg %s for pvc %s/%s", commonLVG.Name, pvc.Namespace, pvc.Name),
							}
							return
						}
					}
				}
			}

			if !isDrbdNode(nodeName, drbdNodesMap) {
				log.Debug(fmt.Sprintf("[filterNodes] node %s does not meet criteria: not a DRBD node", nodeName))
				resCh <- ResultWithError{
					nodeName: nodeName,
					err:      fmt.Errorf("node %s is not a drbd node", nodeName),
				}
				return
			}
			if !isOkNode(nodeName) {
				log.Debug(fmt.Sprintf("[filterNodes] node %s has problems nad will not be selected", nodeName))
				resCh <- ResultWithError{
					nodeName: nodeName,
					err:      fmt.Errorf("node %s is offline", nodeName),
				}
				return
			}

			log.Debug(fmt.Sprintf("[filterNodes] node %s is ok", nodeName))
			resCh <- ResultWithError{
				nodeName: nodeName,
				err:      nil,
			}
		}(nodeName, log)
	}

	wg.Wait()
	close(resCh)

	for res := range resCh {
		if res.err == nil {
			*result.NodeNames = append(*result.NodeNames, res.nodeName)
			continue
		}
		result.FailedNodes[res.nodeName] = res.err.Error()
	}
	return result, nil
}

func isDrbdDiskfulNode(drbdResourceMap map[string]*srv.DRBDResource, pvName string, nodeName string) bool {
	resource, found := drbdResourceMap[pvName]
	if !found {
		return false
	}

	for _, node := range resource.Spec.Peers {
		if node.NodeName == nodeName && !node.Diskless {
			return true
		}
	}

	return false
}

func isOkNode(_ string) bool {
	// TODO implement node online check
	return true
}

func filterOnlyReplicaredSC(ctx context.Context, cl client.Client, scs map[string]*v1.StorageClass) (map[string]*srv.ReplicatedStorageClass, error) {
	result := map[string]*srv.ReplicatedStorageClass{}

	rscList := &srv.ReplicatedStorageClassList{}
	err := cl.List(ctx, rscList)
	if err != nil {
		return nil, err
	}

	rscMap := make(map[string]*srv.ReplicatedStorageClass, len(rscList.Items))
	for _, rsc := range rscList.Items {
		rscMap[rsc.Name] = &rsc
	}

	for _, sc := range scs {
		if sc.Provisioner == consts.SdsReplicatedVolumeProvisioner {
			result[sc.Name] = rscMap[sc.Name]
		}
	}

	return result, nil
}

func isDrbdNode(targetNode string, drbdNodesMap map[string]*srv.DRBDNode) bool {
	_, ok := drbdNodesMap[targetNode]
	return ok
}

func nodeHasEnoughSpace(
	pvcRequests map[string]PVCRequest,
	lvgsThickFree map[string]int64,
	lvgsThinFree map[string]map[string]int64,
	commonLVG *LVMVolumeGroup,
	pvc *corev1.PersistentVolumeClaim,
	lvgMap map[string]*snc.LVMVolumeGroup,
	thickMapMtx *sync.RWMutex,
	thinMapMtx *sync.RWMutex,
) bool {
	nodeIsOk := true
	pvcReq := pvcRequests[pvc.Name]

	switch pvcReq.DeviceType {
	case consts.Thick:
		thickMapMtx.RLock()
		freeSpace := lvgsThickFree[commonLVG.Name]
		thickMapMtx.RUnlock()

		if freeSpace < pvcReq.RequestedSize {
			nodeIsOk = false
			break
		}

		thickMapMtx.Lock()
		lvgsThickFree[commonLVG.Name] -= pvcReq.RequestedSize
		thickMapMtx.Unlock()

	case consts.Thin:
		lvg := lvgMap[commonLVG.Name]

		targetThinPool := findMatchedThinPool(lvg.Status.ThinPools, commonLVG.Thin.PoolName)

		thinMapMtx.RLock()
		freeSpace := lvgsThinFree[lvg.Name][targetThinPool.Name]
		thinMapMtx.RUnlock()

		if freeSpace < pvcReq.RequestedSize {
			nodeIsOk = false
			break
		}

		thinMapMtx.Lock()
		lvgsThinFree[lvg.Name][targetThinPool.Name] -= pvcReq.RequestedSize
		thinMapMtx.Unlock()
	}

	return nodeIsOk
}

func findMatchedThinPool(thinPools []snc.LVMVolumeGroupThinPoolStatus, name string) *snc.LVMVolumeGroupThinPoolStatus {
	for _, tp := range thinPools {
		if tp.Name == name {
			return &tp
		}
	}

	return nil
}

// func findMatchedLVG(nodeLVGs []*snc.LVMVolumeGroup, scLVGs []srv.ReplicatedStoragePoolLVMVolumeGroups) *srv.ReplicatedStoragePoolLVMVolumeGroups {
// 	nodeLVGNames := make(map[string]struct{}, len(nodeLVGs))
// 	for _, lvg := range nodeLVGs {
// 		nodeLVGNames[lvg.Name] = struct{}{}
// 	}

// 	for _, lvg := range scLVGs {
// 		if _, match := nodeLVGNames[lvg.Name]; match {
// 			return &lvg
// 		}
// 	}

// 	return nil
// }

func findMatchedLVG(nodeLVGs []*snc.LVMVolumeGroup, scLVGs LVMVolumeGroups) *LVMVolumeGroup {
	nodeLVGNames := make(map[string]struct{}, len(nodeLVGs))
	for _, lvg := range nodeLVGs {
		nodeLVGNames[lvg.Name] = struct{}{}
	}

	for _, lvg := range scLVGs {
		if _, match := nodeLVGNames[lvg.Name]; match {
			return &lvg
		}
	}

	return nil
}

func getAllNodesWithLVGs(ctx context.Context, cl client.Client) (map[string]*snc.LVMVolumeGroup, error) {
	result := map[string]*snc.LVMVolumeGroup{}
	lvgs := &snc.LVMVolumeGroupList{}
	err := cl.List(ctx, lvgs)
	if err != nil {
		return nil, err
	}

	for _, lvg := range lvgs.Items {
		result[lvg.Spec.Local.NodeName] = &lvg
	}

	return result, nil
}

func getAllLvgsFromPod(pvcs map[string]*corev1.PersistentVolumeClaim, rscMap map[string]*srv.ReplicatedStorageClass, spMap map[string]*srv.ReplicatedStoragePool, lvgMap map[string]*snc.LVMVolumeGroup) map[string]*snc.LVMVolumeGroup {
	result := map[string]*snc.LVMVolumeGroup{}

	for _, pvc := range pvcs {
		scName := *pvc.Spec.StorageClassName
		sc, found := rscMap[scName]
		if !found {
			continue //TODO
		}

		sp := spMap[sc.Spec.StoragePool]

		for _, lvgGr := range sp.Spec.LVMVolumeGroups {
			result[lvgGr.Name] = lvgMap[lvgGr.Name]
		}
	}

	return result
}

func getLVGThinFreeSpaces(lvgs map[string]*snc.LVMVolumeGroup) map[string]map[string]int64 {
	result := make(map[string]map[string]int64, len(lvgs))

	for _, lvg := range lvgs {
		if result[lvg.Name] == nil {
			result[lvg.Name] = make(map[string]int64, len(lvg.Status.ThinPools))
		}

		for _, tp := range lvg.Status.ThinPools {
			result[lvg.Name][tp.Name] = tp.AvailableSpace.Value()
		}
	}

	return result
}

func getLVGThickFreeSpaces(lvgs map[string]*snc.LVMVolumeGroup) map[string]int64 {
	result := make(map[string]int64, len(lvgs))

	for _, lvg := range lvgs {
		result[lvg.Name] = lvg.Status.VGFree.Value()
	}

	return result
}

func filterDRBDNodes(nodes []string, sp *srv.ReplicatedStoragePool, lvmGrMap map[string]*snc.LVMVolumeGroup) []string {
	result := []string{}
	allowedNodes := map[string]struct{}{} // nodes which contain lvgs

	for _, lvmVolGr := range sp.Spec.LVMVolumeGroups {
		lvmGr, found := lvmGrMap[lvmVolGr.Name]
		if !found {
			continue
		}
		allowedNodes[lvmGr.Spec.Local.NodeName] = struct{}{}
	}

	for _, nodeName := range nodes {
		if _, allowed := allowedNodes[nodeName]; allowed {
			result = append(result, nodeName)
		}
	}

	return result
}

type PVCRequest struct {
	DeviceType    string
	RequestedSize int64
}

func extractRequestedSize(
	log logger.Logger,
	pvcs map[string]*corev1.PersistentVolumeClaim,
	scs map[string]*v1.StorageClass,
	pvs map[string]*corev1.PersistentVolume,
) (map[string]PVCRequest, error) {
	pvcRequests := make(map[string]PVCRequest, len(pvcs))
	for _, pvc := range pvcs {
		sc := scs[*pvc.Spec.StorageClassName]
		log.Debug(fmt.Sprintf("[extractRequestedSize] PVC %s/%s has status phase: %s", pvc.Namespace, pvc.Name, pvc.Status.Phase))
		switch pvc.Status.Phase {
		case corev1.ClaimPending:
			switch sc.Parameters[consts.LvmTypeParamKey] {
			case consts.Thick:
				pvcRequests[pvc.Name] = PVCRequest{
					DeviceType:    consts.Thick,
					RequestedSize: pvc.Spec.Resources.Requests.Storage().Value(),
				}
			case consts.Thin:
				pvcRequests[pvc.Name] = PVCRequest{
					DeviceType:    consts.Thin,
					RequestedSize: pvc.Spec.Resources.Requests.Storage().Value(),
				}
			}

		case corev1.ClaimBound:
			pv := pvs[pvc.Spec.VolumeName]
			switch sc.Parameters[consts.LvmTypeParamKey] {
			case consts.Thick:
				reqSize := pvc.Spec.Resources.Requests.Storage().Value() - pv.Spec.Capacity.Storage().Value()
				if reqSize < 0 {
					reqSize = 0
				}
				pvcRequests[pvc.Name] = PVCRequest{
					DeviceType:    consts.Thick,
					RequestedSize: reqSize,
				}
				// linstor affinity controller
			case consts.Thin:
				reqSize := pvc.Spec.Resources.Requests.Storage().Value() - pv.Spec.Capacity.Storage().Value()
				if reqSize < 0 {
					reqSize = 0
				}
				pvcRequests[pvc.Name] = PVCRequest{
					DeviceType:    consts.Thin,
					RequestedSize: pvc.Spec.Resources.Requests.Storage().Value() - pv.Spec.Capacity.Storage().Value(),
				}
			}
		}
	}

	for name, req := range pvcRequests {
		log.Trace(fmt.Sprintf("[extractRequestedSize] pvc %s has requested size: %d, device type: %s", name, req.RequestedSize, req.DeviceType))
	}

	return pvcRequests, nil
}

func getUsedPVC(ctx context.Context, cl client.Client, log logger.Logger, pod *corev1.Pod) (map[string]*corev1.PersistentVolumeClaim, error) {
	pvcMap, err := getAllPVCsFromNamespace(ctx, cl, pod.Namespace)
	if err != nil {
		log.Error(err, fmt.Sprintf("[getUsedPVC] unable to get all PVC for Pod %s in the namespace %s", pod.Name, pod.Namespace))
		return nil, err
	}

	for pvcName := range pvcMap {
		log.Trace(fmt.Sprintf("[getUsedPVC] PVC %s is in namespace %s", pod.Namespace, pvcName))
	}

	usedPvc := make(map[string]*corev1.PersistentVolumeClaim, len(pod.Spec.Volumes))
	for _, volume := range pod.Spec.Volumes {
		if volume.PersistentVolumeClaim != nil {
			log.Trace(fmt.Sprintf("[getUsedPVC] Pod %s/%s uses PVC %s", pod.Namespace, pod.Name, volume.PersistentVolumeClaim.ClaimName))
			pvc := pvcMap[volume.PersistentVolumeClaim.ClaimName]
			usedPvc[volume.PersistentVolumeClaim.ClaimName] = &pvc
		}
	}

	return usedPvc, err
}

func getAllPVCsFromNamespace(ctx context.Context, cl client.Client, namespace string) (map[string]corev1.PersistentVolumeClaim, error) {
	list := &corev1.PersistentVolumeClaimList{}
	err := cl.List(ctx, list, &client.ListOptions{Namespace: namespace})
	if err != nil {
		return nil, err
	}

	pvcs := make(map[string]corev1.PersistentVolumeClaim, len(list.Items))
	for _, pvc := range list.Items {
		pvcs[pvc.Name] = pvc
	}

	return pvcs, nil
}

func getStorageClassesUsedByPVCs(ctx context.Context, cl client.Client, pvcs map[string]*corev1.PersistentVolumeClaim) (map[string]*v1.StorageClass, error) {
	scs := &v1.StorageClassList{}
	err := cl.List(ctx, scs)
	if err != nil {
		return nil, err
	}

	scMap := make(map[string]v1.StorageClass, len(scs.Items))
	for _, sc := range scs.Items {
		scMap[sc.Name] = sc
	}

	result := make(map[string]*v1.StorageClass, len(pvcs))
	for _, pvc := range pvcs {
		if pvc.Spec.StorageClassName == nil {
			err = fmt.Errorf("no StorageClass specified for PVC %s", pvc.Name)
			return nil, err
		}

		scName := *pvc.Spec.StorageClassName
		if sc, match := scMap[scName]; match {
			result[sc.Name] = &sc
		}
	}

	return result, nil
}

func filterNotManagedPVC(log logger.Logger, pvcs map[string]*corev1.PersistentVolumeClaim, scs map[string]*v1.StorageClass) map[string]*corev1.PersistentVolumeClaim {
	filteredPVCs := make(map[string]*corev1.PersistentVolumeClaim, len(pvcs))
	for _, pvc := range pvcs {
		sc := scs[*pvc.Spec.StorageClassName]
		if sc.Provisioner != consts.SdsReplicatedVolumeProvisioner {
			log.Debug(fmt.Sprintf("[filterNotManagedPVC] filter out PVC %s/%s due to used Storage class %s is not managed by sds-replicated-volume-provisioner", pvc.Name, pvc.Namespace, sc.Name))
			continue
		}

		filteredPVCs[pvc.Name] = pvc
	}

	return filteredPVCs
}

func getSortedLVGsFromStorageClasses(replicatedSCs map[string]*srv.ReplicatedStorageClass, spMap map[string]*srv.ReplicatedStoragePool) (map[string][]srv.ReplicatedStoragePoolLVMVolumeGroups, error) {
	result := make(map[string][]srv.ReplicatedStoragePoolLVMVolumeGroups, len(replicatedSCs))

	for _, sc := range replicatedSCs {
		pool := spMap[sc.Spec.StoragePool]
		result[sc.Name] = pool.Spec.LVMVolumeGroups
	}

	return result, nil
}

func GetSortedLVGsFromSC(scs map[string]*v1.StorageClass) (map[string]LVMVolumeGroups, error) {
	result := make(map[string]LVMVolumeGroups, len(scs))

	for _, sc := range scs {
		lvgs, err := ExtractLVGsFromSC(sc)
		if err != nil {
			return nil, err
		}

		result[sc.Name] = append(result[sc.Name], lvgs...)
	}

	return result, nil
}

type LVMVolumeGroup struct {
	Name string `yaml:"name"`
	Thin struct {
		PoolName string `yaml:"poolName"`
	} `yaml:"Thin"`
}
type LVMVolumeGroups []LVMVolumeGroup

func ExtractLVGsFromSC(sc *v1.StorageClass) (LVMVolumeGroups, error) {
	var lvmVolumeGroups LVMVolumeGroups
	err := yaml.Unmarshal([]byte(sc.Parameters[consts.LVMVolumeGroupsParamKey]), &lvmVolumeGroups)
	if err != nil {
		return nil, err
	}
	return lvmVolumeGroups, nil
}

func RemoveUnusedLVGs(lvgs map[string]*snc.LVMVolumeGroup, scsLVGs map[string]LVMVolumeGroups) map[string]*snc.LVMVolumeGroup {
	result := make(map[string]*snc.LVMVolumeGroup, len(lvgs))
	usedLvgs := make(map[string]struct{}, len(lvgs))

	for _, scLvgs := range scsLVGs {
		for _, lvg := range scLvgs {
			usedLvgs[lvg.Name] = struct{}{}
		}
	}

	for _, lvg := range lvgs {
		if _, used := usedLvgs[lvg.Name]; used {
			result[lvg.Name] = lvg
		}
	}

	return result
}

func SortLVGsByNodeName(lvgs map[string]*snc.LVMVolumeGroup) map[string][]*snc.LVMVolumeGroup {
	sorted := make(map[string][]*snc.LVMVolumeGroup, len(lvgs))
	for _, lvg := range lvgs {
		for _, node := range lvg.Status.Nodes {
			sorted[node.Name] = append(sorted[node.Name], lvg)
		}
	}

	return sorted
}

func isOnSameNode(nodeLVGs []*snc.LVMVolumeGroup, scLVGs LVMVolumeGroups) bool {
	nodeLVGNames := make(map[string]struct{}, len(nodeLVGs))
	for _, lvg := range nodeLVGs {
		nodeLVGNames[lvg.Name] = struct{}{}
	}

	for _, lvg := range scLVGs {
		if _, found := nodeLVGNames[lvg.Name]; !found {
			return false
		}
	}

	return true
}

func findMatchedLVGs(nodeLVGs []*snc.LVMVolumeGroup, scLVGs LVMVolumeGroups) *LVMVolumeGroup {
	nodeLVGNames := make(map[string]struct{}, len(nodeLVGs))
	for _, lvg := range nodeLVGs {
		nodeLVGNames[lvg.Name] = struct{}{}
	}

	for _, lvg := range scLVGs {
		if _, match := nodeLVGNames[lvg.Name]; match {
			return &lvg
		}
	}

	return nil
}

func getCommonNodesByStorageClasses(scs map[string]*v1.StorageClass, nodesWithLVGs map[string][]*snc.LVMVolumeGroup) (map[string][]*snc.LVMVolumeGroup, error) {
	result := make(map[string][]*snc.LVMVolumeGroup, len(nodesWithLVGs))

	for nodeName, lvgs := range nodesWithLVGs {
		lvgNames := make(map[string]struct{}, len(lvgs))
		for _, l := range lvgs {
			lvgNames[l.Name] = struct{}{}
		}

		nodeIncludesLVG := true
		for _, sc := range scs {
			scLvgs, err := ExtractLVGsFromSC(sc)
			if err != nil {
				return nil, err
			}

			contains := false
			for _, lvg := range scLvgs {
				if _, exist := lvgNames[lvg.Name]; exist {
					contains = true
					break
				}
			}

			if !contains {
				nodeIncludesLVG = false
				break
			}
		}

		if nodeIncludesLVG {
			result[nodeName] = lvgs
		}
	}

	return result, nil
}
