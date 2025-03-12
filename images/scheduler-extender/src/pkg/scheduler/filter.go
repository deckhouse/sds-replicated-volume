package scheduler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"scheduler-extender/pkg/consts"
)

func (s *scheduler) filter(w http.ResponseWriter, r *http.Request) {
	s.log.Debug("[filter] starts the serving")

	var inputData ExtenderArgs
	reader := http.MaxBytesReader(w, r.Body, 10<<20)
	err := json.NewDecoder(reader).Decode(&inputData)
	if err != nil {
		s.log.Error(err, "[filter] unable to decode a request")
		http.Error(w, fmt.Sprintf("[filter] unable to decode a request: %s", err), http.StatusBadRequest)
		return
	}

	s.log.Trace(fmt.Sprintf("[filter] input data: %+v", inputData))

	if inputData.Pod == nil {
		s.log.Error(errors.New("no pod in the request"), "[filter] unable to get a Pod from the request")
		http.Error(w, fmt.Sprintf("[filter] unable to get a Pod from the request: %s", err), http.StatusBadRequest)
		return
	}

	nodeNames, err := getNodeNames(inputData)
	if err != nil {
		s.log.Error(err, "[filter] unable to get node names from the request")
		http.Error(w, fmt.Sprintf("[filter] unable to get node names from the request: %s", err), http.StatusBadRequest)
		return
	}

	s.log.Debug(fmt.Sprintf("[filter] starts the filtering for Pod %s/%s", inputData.Pod.Namespace, inputData.Pod.Name))
	s.log.Trace(fmt.Sprintf("[filter] Pod from the request: %+v", inputData.Pod))
	s.log.Trace(fmt.Sprintf("[filter] Node names from the request: %+v", nodeNames))

	s.log.Debug(fmt.Sprintf("[filter] Find out if the Pod %s/%s should be processed", inputData.Pod.Namespace, inputData.Pod.Name))
	shouldProcess, _, err := shouldProcessPod(s.ctx, s.client, s.log, inputData.Pod, consts.SdsReplicatedVolumeProvisioner)
	if err != nil {
		s.log.Error(err, "[filter] unable to check if the Pod should be processed")
		http.Error(w, fmt.Sprintf("[filter] unable to check if the Pod should be processed: %s", err), http.StatusBadRequest)
		return
	}

	if !shouldProcess {
		s.log.Debug(fmt.Sprintf("[filter] Pod %s/%s should not be processed. Return the same nodes", inputData.Pod.Namespace, inputData.Pod.Name))
		filteredNodes := &ExtenderFilterResult{
			NodeNames: &nodeNames,
		}
		s.log.Trace(fmt.Sprintf("[filter] filtered nodes: %+v", filteredNodes))

		w.Header().Set("content-type", "application/json")
		err = json.NewEncoder(w).Encode(filteredNodes)
		if err != nil {
			s.log.Error(err, "[filter] unable to encode a response")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		return
	}

	// returns a map of PVCs used by the given pod - pvcName: PvcStruct
	pvcs, err := getUsedPVC(s.ctx, s.client, s.log, inputData.Pod)
	if err != nil {
		s.log.Error(err, fmt.Sprintf("[filter] unable to get used PVC for a Pod %s in the namespace %s", inputData.Pod.Name, inputData.Pod.Namespace))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if len(pvcs) == 0 {
		s.log.Error(fmt.Errorf("no PVC was found for pod %s in namespace %s", inputData.Pod.Name, inputData.Pod.Namespace), fmt.Sprintf("[filter] unable to get used PVC for Pod %s", inputData.Pod.Name))
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// returns a map of StorageClasses used by the given PVCs - scName: scStruct
	scs, err := getStorageClassesUsedByPVCs(s.ctx, s.client, pvcs)
	if err != nil {
		s.log.Error(err, "[filter] unable to get StorageClasses from the PVC")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	for _, sc := range scs {
		s.log.Trace(fmt.Sprintf("[filter] Pod %s/%s uses StorageClass: %s", inputData.Pod.Namespace, inputData.Pod.Name, sc.Name))
	}

	// filters givemn pvcs, returns a map of those whic storage class'es provisioner is sds-replicated-volume - pvcName: PvcStruct
	managedPVCs := filterNotManagedPVC(s.log, pvcs, scs) //+
	for _, pvc := range managedPVCs {
		s.log.Trace(fmt.Sprintf("[filter] filtered managed PVC %s/%s", pvc.Namespace, pvc.Name))
	}

	if len(managedPVCs) == 0 {
		s.log.Warning(fmt.Sprintf("[filter] Pod %s/%s uses PVCs which are not managed by the module. Unable to filter and score the nodes", inputData.Pod.Namespace, inputData.Pod.Name))
		return
	}

	pvMap, err := getPersistentVolumes(s.ctx, s.client)
	if err != nil {
		s.log.Error(err, "[filter] unable to get PersistentVolumes")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	s.log.Debug(fmt.Sprintf("[filter] starts to extract PVC requested sizes for a Pod %s/%s", inputData.Pod.Namespace, inputData.Pod.Name))
	pvcRequests, err := extractRequestedSize(s.log, managedPVCs, scs, pvMap)
	if err != nil {
		s.log.Error(err, fmt.Sprintf("[filter] unable to extract request size for a Pod %s/%s", inputData.Pod.Namespace, inputData.Pod.Name))
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.log.Debug(fmt.Sprintf("[filter] successfully extracted the PVC requested sizes of a Pod %s/%s", inputData.Pod.Namespace, inputData.Pod.Name))

	pvcReplicatedSCs, err := filterOnlyReplicaredSC(s.ctx, s.client, scs)
	if err != nil {
		s.log.Error(err, "[filter] unable to filter only replicated SC")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// replicatedSP, err := getReplicatedStoragePools(s.ctx, s.client)
	// if err != nil {
	// 	s.log.Error(err, "[filter] unable to get replicated storage pools")
	// 	http.Error(w, "internal server error", http.StatusInternalServerError)
	// 	return
	// }

	// replicatedSC, err := getReplicatedStorageClasses(s.ctx, s.client)
	// if err != nil {
	// 	s.log.Error(err, "[filter] unable to get replicated storage classes")
	// 	http.Error(w, "internal server error", http.StatusInternalServerError)
	// 	return
	// }

	// lvgGroups, err := getlvmVolumeGroups(s.ctx, s.client)
	// if err != nil {
	// 	s.log.Error(err, "[filter] unable to get LVM volume groups")
	// 	http.Error(w, "internal server error", http.StatusInternalServerError)
	// 	return
	// }

	// nodesToLvgsMap, err := getNodeWithLvmVgsMap(s.ctx, s.client)
	// if err != nil {
	// 	s.log.Error(err, "[filter] unable to get nodes with LVM volume groups map")
	// 	http.Error(w, "internal server error", http.StatusInternalServerError)
	// 	return
	// }

	drbdResourceMap, err := getDRBDResourceMap(s.ctx, s.client)
	if err != nil {
		s.log.Error(err, "[filter] unable to get DRBD resource map")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	drbdNodesMap, err := getDRBDNodesMap(s.ctx, s.client)
	if err != nil {
		s.log.Error(err, "[filter] unable to get DRBD nodes map")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	s.log.Debug(fmt.Sprintf("[filter] starts to filter the nodes from the request for a Pod %s/%s", inputData.Pod.Namespace, inputData.Pod.Name))
	filteredNodes, err := filterNodes(
		s.log,
		s.cache,
		&nodeNames,
		inputData.Pod,
		managedPVCs,
		scs,
		pvcRequests,
		pvcReplicatedSCs,
		drbdResourceMap,
		drbdNodesMap,
	)
	// filteredNodes, err := filterNodes(
	// 	s.log,
	// 	&nodeNames,
	// 	inputData.Pod,
	// 	managedPVCs,
	// 	replicatedSP,
	// 	pvcReplicatedSCs,
	// 	replicatedSC,
	// 	pvcRequests,
	// 	lvgGroups,
	// 	nodesToLvgsMap,
	// 	drbdResourceMap,
	// 	drbdNodesMap,
	// 	nil, // TODO put cache here
	// )
	if err != nil {
		s.log.Error(err, "[filter] unable to filter the nodes")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.log.Debug(fmt.Sprintf("[filter] successfully filtered the nodes from the request for a Pod %s/%s", inputData.Pod.Namespace, inputData.Pod.Name))

	w.Header().Set("content-type", "application/json")
	err = json.NewEncoder(w).Encode(filteredNodes)
	if err != nil {
		s.log.Error(err, "[filter] unable to encode a response")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.log.Debug(fmt.Sprintf("[filter] ends the serving the request for a Pod %s/%s", inputData.Pod.Namespace, inputData.Pod.Name))
}
