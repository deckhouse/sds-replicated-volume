package scheduler

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"scheduler-extender/pkg/cache"
	"scheduler-extender/pkg/consts"
	"scheduler-extender/pkg/logger"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func (s *scheduler) prioritize(w http.ResponseWriter, r *http.Request) {
	s.log.Debug("[prioritize] starts serving")
	var inputData ExtenderArgs
	reader := http.MaxBytesReader(w, r.Body, 10<<20)
	err := json.NewDecoder(reader).Decode(&inputData)
	if err != nil {
		s.log.Error(err, "[prioritize] unable to decode a request")
		http.Error(w, "Bad Request.", http.StatusBadRequest)
		return
	}
	s.log.Trace(fmt.Sprintf("[prioritize] input data: %+v", inputData))

	if inputData.Pod == nil {
		s.log.Error(errors.New("no pod in the request"), "[prioritize] unable to get a Pod from the request")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	nodeNames, err := getNodeNames(inputData)
	if err != nil {
		s.log.Error(err, "[prioritize] unable to get node names from the request")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	s.log.Debug(fmt.Sprintf("[prioritize] starts the prioritizeing for Pod %s/%s", inputData.Pod.Namespace, inputData.Pod.Name))
	s.log.Trace(fmt.Sprintf("[prioritize] Pod from the request: %+v", inputData.Pod))
	s.log.Trace(fmt.Sprintf("[prioritize] node names from the request: %v", nodeNames))

	s.log.Debug(fmt.Sprintf("[prioritize] Find out if the Pod %s/%s should be processed", inputData.Pod.Namespace, inputData.Pod.Name))
	shouldProcess, _, err := shouldProcessPod(s.ctx, s.client, s.log, inputData.Pod, consts.SdsReplicatedVolumeProvisioner)
	if err != nil {
		s.log.Error(err, "[prioritize] unable to check if the Pod should be processed")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !shouldProcess {
		s.log.Debug(fmt.Sprintf("[prioritize] Pod %s/%s should not be processed. Return the same nodes with 0 score", inputData.Pod.Namespace, inputData.Pod.Name))
		nodeScores := make([]HostPriority, 0, len(nodeNames))
		for _, nodeName := range nodeNames {
			nodeScores = append(nodeScores, HostPriority{
				Host:  nodeName,
				Score: 0,
			})
		}

		s.log.Trace(fmt.Sprintf("[prioritize] node scores: %+v", nodeScores))
		w.Header().Set("content-type", "application/json")
		err = json.NewEncoder(w).Encode(nodeScores)
		if err != nil {
			s.log.Error(err, fmt.Sprintf("[prioritize] unable to encode a response for a Pod %s/%s", inputData.Pod.Namespace, inputData.Pod.Name))
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	s.log.Debug(fmt.Sprintf("[prioritize] Pod %s/%s should be processed", inputData.Pod.Namespace, inputData.Pod.Name))

	pvcs, err := getUsedPVC(s.ctx, s.client, s.log, inputData.Pod)
	if err != nil {
		s.log.Error(err, fmt.Sprintf("[prioritize] unable to get PVC from the Pod %s/%s", inputData.Pod.Namespace, inputData.Pod.Name))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if len(pvcs) == 0 {
		s.log.Error(fmt.Errorf("no PVC was found for pod %s in namespace %s", inputData.Pod.Name, inputData.Pod.Namespace), fmt.Sprintf("[prioritize] unable to get used PVC for Pod %s", inputData.Pod.Name))
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	for _, pvc := range pvcs {
		s.log.Trace(fmt.Sprintf("[prioritize] Pod %s/%s uses PVC: %s", inputData.Pod.Namespace, inputData.Pod.Name, pvc.Name))
	}

	// all scs used by pod pvcs
	scs, err := getStorageClassesUsedByPVCs(s.ctx, s.client, pvcs)
	if err != nil {
		s.log.Error(err, fmt.Sprintf("[prioritize] unable to get StorageClasses from the PVC for Pod %s/%s", inputData.Pod.Namespace, inputData.Pod.Name))
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	for _, sc := range scs {
		s.log.Trace(fmt.Sprintf("[prioritize] Pod %s/%s uses Storage Class: %s", inputData.Pod.Namespace, inputData.Pod.Name, sc.Name))
	}

	// pvcs which sc provisioner is the right provisioner
	managedPVCs := filterNotManagedPVC(s.log, pvcs, scs)
	for _, pvc := range managedPVCs {
		s.log.Trace(fmt.Sprintf("[prioritize] prioritizeed managed PVC %s/%s", pvc.Namespace, pvc.Name))
	}

	pvMap, err := getPersistentVolumes(s.ctx, s.client)
	if err != nil {
		s.log.Error(err, "[filter] unable to get PersistentVolumes")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	s.log.Debug(fmt.Sprintf("[prioritize] starts to extract pvcRequests size for Pod %s/%s", inputData.Pod.Namespace, inputData.Pod.Name))
	pvcRequests, err := extractRequestedSize(s.log, managedPVCs, scs, pvMap)
	if err != nil {
		s.log.Error(err, fmt.Sprintf("[prioritize] unable to extract request size for Pod %s/%s", inputData.Pod.Namespace, inputData.Pod.Name))
		http.Error(w, "bad request", http.StatusBadRequest)
	}
	s.log.Debug(fmt.Sprintf("[prioritize] successfully extracted the pvcRequests size for Pod %s/%s", inputData.Pod.Namespace, inputData.Pod.Name))

	s.log.Debug(fmt.Sprintf("[prioritize] starts to score the nodes for Pod %s/%s", inputData.Pod.Namespace, inputData.Pod.Name))
	result, err := scoreNodes(s.log, s.cache, &nodeNames, managedPVCs, scs, pvcRequests, s.defaultDivisor)
	if err != nil {
		s.log.Error(err, fmt.Sprintf("[prioritize] unable to score nodes for Pod %s/%s", inputData.Pod.Namespace, inputData.Pod.Name))
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.log.Debug(fmt.Sprintf("[prioritize] successfully scored the nodes for Pod %s/%s", inputData.Pod.Namespace, inputData.Pod.Name))

	w.Header().Set("content-type", "application/json")
	err = json.NewEncoder(w).Encode(result)
	if err != nil {
		s.log.Error(err, fmt.Sprintf("[prioritize] unable to encode a response for a Pod %s/%s", inputData.Pod.Namespace, inputData.Pod.Name))
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
	s.log.Debug("[prioritize] ends serving")
}

func scoreNodes(
	log logger.Logger,
	schedulerCache *cache.Cache,
	nodeNames *[]string,
	pvcs map[string]*corev1.PersistentVolumeClaim,
	scs map[string]*v1.StorageClass,
	pvcRequests map[string]PVCRequest,
	divisor float64,
) ([]HostPriority, error) {
	lvgs := schedulerCache.GetAllLVG()
	scLVGs, err := GetSortedLVGsFromSC(scs)
	if err != nil {
		return nil, err
	}

	usedLVGs := RemoveUnusedLVGs(lvgs, scLVGs)
	for lvgName := range usedLVGs {
		log.Trace(fmt.Sprintf("[scoreNodes] used LVMVolumeGroup %s", lvgName))
	}

	nodeLVGs := SortLVGsByNodeName(usedLVGs)
	for n, ls := range nodeLVGs {
		for _, l := range ls {
			log.Trace(fmt.Sprintf("[scoreNodes] the LVMVolumeGroup %s belongs to node %s", l.Name, n))
		}
	}

	result := make([]HostPriority, 0, len(*nodeNames))
	for _, nodeName := range *nodeNames {
		lvgsFromNode := nodeLVGs[nodeName]
		var totalFreeSpaceLeftPercent int64

		for _, pvc := range pvcs {
			pvcReq := pvcRequests[pvc.Name]
			lvgsFromSC := scLVGs[*pvc.Spec.StorageClassName]
			commonLVG := findMatchedLVGs(lvgsFromNode, lvgsFromSC)
			if commonLVG == nil {
				log.Warning("unable to match Storage Class's LVMVolumeGroup with the node's one, Storage Class: %s, node: %s", *pvc.Spec.StorageClassName, nodeName)
				continue
			}
			log.Trace(fmt.Sprintf("[scoreNodes] LVMVolumeGroup %s is common for storage class %s and node %s", commonLVG.Name, *pvc.Spec.StorageClassName, nodeName))

			// TODO put in a separate func
			var freeSpace resource.Quantity
			lvg := lvgs[commonLVG.Name]
			switch pvcReq.DeviceType {
			case consts.Thick:
				freeSpace = lvg.Status.VGFree
				log.Trace(fmt.Sprintf("[scoreNodes] LVMVolumeGroup %s free Thick space before PVC reservation: %s", lvg.Name, freeSpace.String()))
				reserved, err := schedulerCache.GetLVGThickReservedSpace(lvg.Name)
				if err != nil {
					log.Error(err, fmt.Sprintf("[scoreNodes] unable to count reserved space for the LVMVolumeGroup %s", lvg.Name))
					continue
				}
				log.Trace(fmt.Sprintf("[scoreNodes] LVMVolumeGroup %s PVC Space reservation: %s", lvg.Name, resource.NewQuantity(reserved, resource.BinarySI)))
				spaceWithReserved := freeSpace.Value() - reserved
				freeSpace = *resource.NewQuantity(spaceWithReserved, resource.BinarySI)
				log.Trace(fmt.Sprintf("[scoreNodes] LVMVolumeGroup %s free Thick space after PVC reservation: %s", lvg.Name, freeSpace.String()))
			case consts.Thin:
				thinPool := findMatchedThinPool(lvg.Status.ThinPools, commonLVG.Thin.PoolName)
				if thinPool == nil {
					err = fmt.Errorf("unable to match Storage Class's ThinPools with the node's one, Storage Class: %s, node: %s", *pvc.Spec.StorageClassName, nodeName)
					log.Error(err, "[scoreNodes] an error occurs while searching for target LVMVolumeGroup")
				}

				freeSpace = thinPool.AvailableSpace
			}

			//TODO if PrefLocal handle negative freespace
			// TODO if preflocal and all replicas are on teh same node, score it at hightest
			log.Trace(fmt.Sprintf("[scoreNodes] LVMVolumeGroup %s total size: %s", lvg.Name, lvg.Status.VGSize.String()))
			totalFreeSpaceLeftPercent += getFreeSpaceLeftPercent(freeSpace.Value(), pvcReq.RequestedSize, lvg.Status.VGSize.Value())
		}

		averageFreeSpace := totalFreeSpaceLeftPercent / int64(len(pvcs))
		log.Trace(fmt.Sprintf("[scoreNodes] average free space left for the node: %s", nodeName))
		score := getNodeScore(averageFreeSpace, divisor)
		log.Trace(fmt.Sprintf("[scoreNodes] node %s has score %d with average free space left (after all PVC bounded), percent %d", nodeName, score, averageFreeSpace))

		result = append(result, HostPriority{
			Host:  nodeName,
			Score: score,
		})
	}

	return result, nil
}

func getFreeSpaceLeftPercent(freeSize, requestedSpace, totalSize int64) int64 {
	leftFreeSize := freeSize - requestedSpace
	fraction := float64(leftFreeSize) / float64(totalSize)
	percent := fraction * 100
	return int64(percent)
}

func getNodeScore(freeSpace int64, divisor float64) int {
	converted := int(math.Round(math.Log2(float64(freeSpace) / divisor)))
	switch {
	case converted < 1:
		return 1
	case converted > 10:
		return 10
	default:
		return converted
	}
}
