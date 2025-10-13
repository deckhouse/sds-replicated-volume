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

package utils

import (
	"context"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"gopkg.in/yaml.v2"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-local-volume/images/sds-local-volume-csi/internal"
	"github.com/deckhouse/sds-local-volume/images/sds-local-volume-csi/pkg/logger"
	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
)

const (
	LLVStatusCreated            = "Created"
	LLVSStatusCreated           = "Created"
	LLVStatusFailed             = "Failed"
	LLVSStatusFailed            = "Failed"
	LLVTypeThin                 = "Thin"
	KubernetesAPIRequestLimit   = 3
	KubernetesAPIRequestTimeout = 1
	SDSLocalVolumeCSIFinalizer  = "storage.deckhouse.io/sds-local-volume-csi"
)

func CreateLVMLogicalVolumeSnapshot(
	ctx context.Context,
	kc client.Client,
	log *logger.Logger,
	traceID, name string,
	lvmLogicalVolumeSnapshotSpec snc.LVMLogicalVolumeSnapshotSpec,
) (*snc.LVMLogicalVolumeSnapshot, error) {
	llvs := &snc.LVMLogicalVolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			OwnerReferences: []metav1.OwnerReference{},
			Finalizers:      []string{SDSLocalVolumeCSIFinalizer},
		},
		Spec: lvmLogicalVolumeSnapshotSpec,
	}

	log.Trace(fmt.Sprintf("[CreateLVMLogicalVolumeSnapshot][traceID:%s][volumeID:%s] LVMLogicalVolumeSnapshot: %+v", traceID, name, llvs))

	return llvs, kc.Create(ctx, llvs)
}

func DeleteLVMLogicalVolumeSnapshot(ctx context.Context, kc client.Client, log *logger.Logger, traceID, lvmLogicalVolumeSnapshotName string) error {
	var err error

	log.Trace(fmt.Sprintf("[DeleteLVMLogicalVolumeSnapshot][traceID:%s][volumeID:%s] Trying to find LVMLogicalVolumeSnapshot", traceID, lvmLogicalVolumeSnapshotName))
	llvs, err := GetLVMLogicalVolumeSnapshot(ctx, kc, lvmLogicalVolumeSnapshotName, "")
	if err != nil {
		return fmt.Errorf("get LVMLogicalVolumeSnapshot %s: %w", lvmLogicalVolumeSnapshotName, err)
	}

	log.Trace(fmt.Sprintf("[DeleteLVMLogicalVolumeSnapshot][traceID:%s][volumeID:%s] LVMLogicalVolumeSnapshot found: %+v (status: %+v)", traceID, lvmLogicalVolumeSnapshotName, llvs, llvs.Status))
	log.Trace(fmt.Sprintf("[DeleteLVMLogicalVolumeSnapshot][traceID:%s][volumeID:%s] Removing finalizer %s if exists", traceID, lvmLogicalVolumeSnapshotName, SDSLocalVolumeCSIFinalizer))

	removed, err := removeLLVSFinalizerIfExist(ctx, kc, log, llvs, SDSLocalVolumeCSIFinalizer)
	if err != nil {
		return fmt.Errorf("remove finalizers from DeleteLVMLogicalVolumeSnapshot %s: %w", lvmLogicalVolumeSnapshotName, err)
	}
	if removed {
		log.Trace(fmt.Sprintf("[DeleteLVMLogicalVolumeSnapshot][traceID:%s][volumeID:%s] finalizer %s removed from LVMLogicalVolumeSnapshot %s", traceID, lvmLogicalVolumeSnapshotName, SDSLocalVolumeCSIFinalizer, lvmLogicalVolumeSnapshotName))
	} else {
		log.Warning(fmt.Sprintf("[DeleteLVMLogicalVolumeSnapshot][traceID:%s][volumeID:%s] finalizer %s not found in LVMLogicalVolumeSnapshot %s", traceID, lvmLogicalVolumeSnapshotName, SDSLocalVolumeCSIFinalizer, lvmLogicalVolumeSnapshotName))
	}

	log.Trace(fmt.Sprintf("[DeleteLVMLogicalVolumeSnapshot][traceID:%s][volumeID:%s] Trying to delete LVMLogicalVolumeSnapshot", traceID, lvmLogicalVolumeSnapshotName))
	err = kc.Delete(ctx, llvs)
	return err
}

func removeLLVSFinalizerIfExist(ctx context.Context, kc client.Client, log *logger.Logger, llvs *snc.LVMLogicalVolumeSnapshot, finalizer string) (bool, error) {
	for attempt := 0; attempt < KubernetesAPIRequestLimit; attempt++ {
		removed := false
		for i, val := range llvs.Finalizers {
			if val == finalizer {
				llvs.Finalizers = slices.Delete(llvs.Finalizers, i, i+1)
				removed = true
				break
			}
		}

		if !removed {
			return false, nil
		}

		log.Trace(fmt.Sprintf("[removeLLVSFinalizerIfExist] removing finalizer %s from LVMLogicalVolumeSnapshot %s", finalizer, llvs.Name))
		err := kc.Update(ctx, llvs)
		if err == nil {
			return true, nil
		}

		if !kerrors.IsConflict(err) {
			return false, fmt.Errorf("[removeLLVSFinalizerIfExist] error updating LVMLogicalVolumeSnapshot %s: %w", llvs.Name, err)
		}

		if attempt < KubernetesAPIRequestLimit-1 {
			log.Trace(fmt.Sprintf("[removeLLVSFinalizerIfExist] conflict while updating LVMLogicalVolumeSnapshot %s, retrying...", llvs.Name))
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			default:
				time.Sleep(KubernetesAPIRequestTimeout * time.Second)
				freshLLVS, getErr := GetLVMLogicalVolumeSnapshot(ctx, kc, llvs.Name, "")
				if getErr != nil {
					return false, fmt.Errorf("[removeLLVSFinalizerIfExist] error getting LVMLogicalVolumeSnapshot %s after update conflict: %w", llvs.Name, getErr)
				}
				// Update the llvs struct with fresh data (without changing pointers because we need the new resource version outside of this function)
				*llvs = *freshLLVS
			}
		}
	}

	return false, fmt.Errorf("after %d attempts of removing finalizer %s from LVMLogicalVolumeSnapshot %s, last error: %w", KubernetesAPIRequestLimit, finalizer, llvs.Name, nil)
}

func WaitForLLVSStatusUpdate(
	ctx context.Context,
	kc client.Client,
	log *logger.Logger,
	traceID string,
	lvmLogicalVolumeSnapshotName string,
) (int, error) {
	var attemptCounter int
	log.Info(fmt.Sprintf("[WaitForLLVSStatusUpdate][traceID:%s][volumeID:%s] Waiting for LVM Logical Volume Snapshot status update", traceID, lvmLogicalVolumeSnapshotName))
	for {
		attemptCounter++
		select {
		case <-ctx.Done():
			log.Warning(fmt.Sprintf("[WaitForLLVSStatusUpdate][traceID:%s][volumeID:%s] context done. Failed to wait for LVM Logical Volume Snapshot status update", traceID, lvmLogicalVolumeSnapshotName))
			return attemptCounter, ctx.Err()
		default:
			time.Sleep(500 * time.Millisecond)
		}

		llvs, err := GetLVMLogicalVolumeSnapshot(ctx, kc, lvmLogicalVolumeSnapshotName, "")
		if err != nil {
			return attemptCounter, err
		}

		if attemptCounter%10 == 0 {
			log.Info(fmt.Sprintf("[WaitForLLVSStatusUpdate][traceID:%s][volumeID:%s] Attempt: %d,LVM Logical Volume Snapshot: %+v", traceID, lvmLogicalVolumeSnapshotName, attemptCounter, llvs))
		}

		if llvs.Status != nil {
			log.Trace(fmt.Sprintf("[WaitForLLVSStatusUpdate][traceID:%s][volumeID:%s] Attempt %d, LVM Logical Volume Snapshot status: %+v, full LVMLogicalVolumeSnapshot resource: %+v", traceID, lvmLogicalVolumeSnapshotName, attemptCounter, llvs.Status, llvs))

			if llvs.DeletionTimestamp != nil {
				return attemptCounter, fmt.Errorf("failed to create LVM logical volume snapshot on node for LVMLogicalVolumeSnapshot %s, reason: LVMLogicalVolumeSnapshot is being deleted", lvmLogicalVolumeSnapshotName)
			}

			if llvs.Status.Phase == LLVSStatusFailed {
				return attemptCounter, fmt.Errorf("failed to create LVM logical volume on node for LVMLogicalVolumeSnapshot %s, reason: %s", lvmLogicalVolumeSnapshotName, llvs.Status.Reason)
			}

			if llvs.Status.Phase == LLVSStatusCreated {
				log.Trace(fmt.Sprintf("[WaitForLLVSStatusUpdate][traceID:%s][volumeID:%s] Attempt %d, LVM Logical Volume Snapshot created but size does not match the requested size yet. Waiting...", traceID, lvmLogicalVolumeSnapshotName, attemptCounter))
				return attemptCounter, nil
			}
			log.Trace(fmt.Sprintf("[WaitForLLVSStatusUpdate][traceID:%s][volumeID:%s] Attempt %d, LVM Logical Volume Snapshot status is not 'Created' yet. Waiting...", traceID, lvmLogicalVolumeSnapshotName, attemptCounter))
		}
	}
}

func GetLVMLogicalVolumeSnapshot(ctx context.Context, kc client.Client, lvmLogicalVolumeSnapshotName, namespace string) (*snc.LVMLogicalVolumeSnapshot, error) {
	var llvs snc.LVMLogicalVolumeSnapshot

	err := kc.Get(ctx, client.ObjectKey{
		Name:      lvmLogicalVolumeSnapshotName,
		Namespace: namespace,
	}, &llvs)

	return &llvs, err
}

// GetLSCBeforeLLVDelete removed for replicated-volume flow

func CreateLVMLogicalVolume(ctx context.Context, kc client.Client, log *logger.Logger, traceID, name string, lvmLogicalVolumeSpec snc.LVMLogicalVolumeSpec) (*snc.LVMLogicalVolume, error) {
	var err error
	llv := &snc.LVMLogicalVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			OwnerReferences: []metav1.OwnerReference{},
			Finalizers:      []string{SDSLocalVolumeCSIFinalizer},
		},
		Spec: lvmLogicalVolumeSpec,
	}

	log.Trace(fmt.Sprintf("[CreateLVMLogicalVolume][traceID:%s][volumeID:%s] LVMLogicalVolume: %+v", traceID, name, llv))

	err = kc.Create(ctx, llv)
	return llv, err
}

func DeleteLVMLogicalVolume(ctx context.Context, kc client.Client, log *logger.Logger, traceID, lvmLogicalVolumeName, volumeCleanup string) error {
	var err error

	log.Trace(fmt.Sprintf("[DeleteLVMLogicalVolume][traceID:%s][volumeID:%s] Trying to find LVMLogicalVolume", traceID, lvmLogicalVolumeName))
	llv, err := GetLVMLogicalVolume(ctx, kc, lvmLogicalVolumeName, "")
	if err != nil {
		return fmt.Errorf("get LVMLogicalVolume %s: %w", lvmLogicalVolumeName, err)
	}

	if volumeCleanup != "" {
		llv.Spec.VolumeCleanup = &volumeCleanup
	} else {
		llv.Spec.VolumeCleanup = nil
	}
	err = kc.Update(ctx, llv)
	if err != nil {
		return fmt.Errorf("update LVMLogicalVolume %s: %w", lvmLogicalVolumeName, err)
	}

	log.Trace(fmt.Sprintf("[DeleteLVMLogicalVolume][traceID:%s][volumeID:%s] LVMLogicalVolume found: %+v (status: %+v)", traceID, lvmLogicalVolumeName, llv, llv.Status))
	log.Trace(fmt.Sprintf("[DeleteLVMLogicalVolume][traceID:%s][volumeID:%s] Removing finalizer %s if exists", traceID, lvmLogicalVolumeName, SDSLocalVolumeCSIFinalizer))

	removed, err := removeLLVFinalizerIfExist(ctx, kc, log, llv, SDSLocalVolumeCSIFinalizer)
	if err != nil {
		return fmt.Errorf("remove finalizers from LVMLogicalVolume %s: %w", lvmLogicalVolumeName, err)
	}
	if removed {
		log.Trace(fmt.Sprintf("[DeleteLVMLogicalVolume][traceID:%s][volumeID:%s] finalizer %s removed from LVMLogicalVolume %s", traceID, lvmLogicalVolumeName, SDSLocalVolumeCSIFinalizer, lvmLogicalVolumeName))
	} else {
		log.Warning(fmt.Sprintf("[DeleteLVMLogicalVolume][traceID:%s][volumeID:%s] finalizer %s not found in LVMLogicalVolume %s", traceID, lvmLogicalVolumeName, SDSLocalVolumeCSIFinalizer, lvmLogicalVolumeName))
	}

	log.Trace(fmt.Sprintf("[DeleteLVMLogicalVolume][traceID:%s][volumeID:%s] Trying to delete LVMLogicalVolume", traceID, lvmLogicalVolumeName))
	err = kc.Delete(ctx, llv)
	return err
}

func WaitForStatusUpdate(ctx context.Context, kc client.Client, log *logger.Logger, traceID, lvmLogicalVolumeName, namespace string, llvSize, delta resource.Quantity) (int, error) {
	var attemptCounter int
	sizeEquals := false
	log.Info(fmt.Sprintf("[WaitForStatusUpdate][traceID:%s][volumeID:%s] Waiting for LVM Logical Volume status update", traceID, lvmLogicalVolumeName))
	for {
		attemptCounter++
		select {
		case <-ctx.Done():
			log.Warning(fmt.Sprintf("[WaitForStatusUpdate][traceID:%s][volumeID:%s] context done. Failed to wait for LVM Logical Volume status update", traceID, lvmLogicalVolumeName))
			return attemptCounter, ctx.Err()
		default:
			time.Sleep(500 * time.Millisecond)
		}

		llv, err := GetLVMLogicalVolume(ctx, kc, lvmLogicalVolumeName, namespace)
		if err != nil {
			return attemptCounter, err
		}

		if attemptCounter%10 == 0 {
			log.Info(fmt.Sprintf("[WaitForStatusUpdate][traceID:%s][volumeID:%s] Attempt: %d,LVM Logical Volume: %+v; delta=%s; sizeEquals=%t", traceID, lvmLogicalVolumeName, attemptCounter, llv, delta.String(), sizeEquals))
		}

		if llv.Status != nil {
			log.Trace(fmt.Sprintf("[WaitForStatusUpdate][traceID:%s][volumeID:%s] Attempt %d, LVM Logical Volume status: %+v, full LVMLogicalVolume resource: %+v", traceID, lvmLogicalVolumeName, attemptCounter, llv.Status, llv))
			sizeEquals = AreSizesEqualWithinDelta(llvSize, llv.Status.ActualSize, delta)

			if llv.DeletionTimestamp != nil {
				return attemptCounter, fmt.Errorf("failed to create LVM logical volume on node for LVMLogicalVolume %s, reason: LVMLogicalVolume is being deleted", lvmLogicalVolumeName)
			}

			if llv.Status.Phase == LLVStatusFailed {
				return attemptCounter, fmt.Errorf("failed to create LVM logical volume on node for LVMLogicalVolume %s, reason: %s", lvmLogicalVolumeName, llv.Status.Reason)
			}

			if llv.Status.Phase == LLVStatusCreated {
				if sizeEquals {
					return attemptCounter, nil
				}
				log.Trace(fmt.Sprintf("[WaitForStatusUpdate][traceID:%s][volumeID:%s] Attempt %d, LVM Logical Volume created but size does not match the requested size yet. Waiting...", traceID, lvmLogicalVolumeName, attemptCounter))
			} else {
				log.Trace(fmt.Sprintf("[WaitForStatusUpdate][traceID:%s][volumeID:%s] Attempt %d, LVM Logical Volume status is not 'Created' yet. Waiting...", traceID, lvmLogicalVolumeName, attemptCounter))
			}
		}
	}
}

func GetLVMLogicalVolume(ctx context.Context, kc client.Client, lvmLogicalVolumeName, namespace string) (*snc.LVMLogicalVolume, error) {
	var llv snc.LVMLogicalVolume

	err := kc.Get(ctx, client.ObjectKey{
		Name:      lvmLogicalVolumeName,
		Namespace: namespace,
	}, &llv)

	return &llv, err
}

func AreSizesEqualWithinDelta(leftSize, rightSize, allowedDelta resource.Quantity) bool {
	leftSizeFloat := float64(leftSize.Value())
	rightSizeFloat := float64(rightSize.Value())

	return math.Abs(leftSizeFloat-rightSizeFloat) < float64(allowedDelta.Value())
}

func GetNodeWithMaxFreeSpace(lvgs []snc.LVMVolumeGroup, storageClassLVGParametersMap map[string]string, lvmType string) (nodeName string, freeSpace resource.Quantity, err error) {
	var maxFreeSpace int64
	for _, lvg := range lvgs {
		switch lvmType {
		case internal.LVMTypeThick:
			freeSpace = lvg.Status.VGFree
		case internal.LVMTypeThin:
			thinPoolName, ok := storageClassLVGParametersMap[lvg.Name]
			if !ok {
				return "", freeSpace, fmt.Errorf("thin pool name for lvg %s not found in storage class parameters: %+v", lvg.Name, storageClassLVGParametersMap)
			}
			freeSpace, err = GetLVMThinPoolFreeSpace(lvg, thinPoolName)
			if err != nil {
				return "", freeSpace, fmt.Errorf("get free space for thin pool %s in lvg %s: %w", thinPoolName, lvg.Name, err)
			}
		}

		if freeSpace.Value() > maxFreeSpace {
			nodeName = lvg.Status.Nodes[0].Name
			maxFreeSpace = freeSpace.Value()
		}
	}

	return nodeName, *resource.NewQuantity(maxFreeSpace, resource.BinarySI), nil
}

func GetLVMVolumeGroup(ctx context.Context, kc client.Client, lvgName string) (*snc.LVMVolumeGroup, error) {
	lvg := &snc.LVMVolumeGroup{}

	if err := kc.Get(
		ctx,
		client.ObjectKey{Name: lvgName, Namespace: ""},
		lvg,
	); err != nil {
		return nil, err
	}

	return lvg, nil
}

func GetLVMVolumeGroupFreeSpace(lvg snc.LVMVolumeGroup) (vgFreeSpace resource.Quantity) {
	vgFreeSpace = lvg.Status.VGSize
	vgFreeSpace.Sub(lvg.Status.AllocatedSize)
	return vgFreeSpace
}

func GetLVMThinPoolFreeSpace(lvg snc.LVMVolumeGroup, thinPoolName string) (thinPoolFreeSpace resource.Quantity, err error) {
	var storagePoolThinPool *snc.LVMVolumeGroupThinPoolStatus
	for _, thinPool := range lvg.Status.ThinPools {
		if thinPool.Name == thinPoolName {
			storagePoolThinPool = &thinPool
			break
		}
	}

	if storagePoolThinPool == nil {
		return thinPoolFreeSpace, fmt.Errorf("[GetLVMThinPoolFreeSpace] thin pool %s not found in lvg %+v", thinPoolName, lvg)
	}

	return storagePoolThinPool.AvailableSpace, nil
}

func ExpandLVMLogicalVolume(ctx context.Context, kc client.Client, llv *snc.LVMLogicalVolume, newSize string) error {
	llv.Spec.Size = newSize
	return kc.Update(ctx, llv)
}

func GetStorageClassLVGsAndParameters(
	ctx context.Context,
	kc client.Client,
	log *logger.Logger,
	storageClassLVGParametersString string,
) (storageClassLVGs []snc.LVMVolumeGroup, storageClassLVGParametersMap map[string]string, err error) {
	var storageClassLVGParametersList LVMVolumeGroups
	err = yaml.Unmarshal([]byte(storageClassLVGParametersString), &storageClassLVGParametersList)
	if err != nil {
		log.Error(err, "unmarshal yaml lvmVolumeGroup")
		return nil, nil, err
	}

	storageClassLVGParametersMap = make(map[string]string, len(storageClassLVGParametersList))
	for _, v := range storageClassLVGParametersList {
		storageClassLVGParametersMap[v.Name] = v.Thin.PoolName
	}
	log.Info(fmt.Sprintf("[GetStorageClassLVGs] StorageClass LVM volume groups parameters map: %+v", storageClassLVGParametersMap))

	lvgs, err := GetLVGList(ctx, kc)
	if err != nil {
		return nil, nil, err
	}

	for _, lvg := range lvgs.Items {
		log.Trace(fmt.Sprintf("[GetStorageClassLVGs] process lvg: %+v", lvg))

		_, ok := storageClassLVGParametersMap[lvg.Name]
		if ok {
			log.Info(fmt.Sprintf("[GetStorageClassLVGs] found lvg from storage class: %s", lvg.Name))
			log.Info(fmt.Sprintf("[GetStorageClassLVGs] lvg.Status.Nodes[0].Name: %s", lvg.Status.Nodes[0].Name))
			storageClassLVGs = append(storageClassLVGs, lvg)
		} else {
			log.Trace(fmt.Sprintf("[GetStorageClassLVGs] skip lvg: %s", lvg.Name))
		}
	}

	return storageClassLVGs, storageClassLVGParametersMap, nil
}

func GetLVGList(ctx context.Context, kc client.Client) (*snc.LVMVolumeGroupList, error) {
	listLvgs := &snc.LVMVolumeGroupList{}
	return listLvgs, kc.List(ctx, listLvgs)
}

func GetLLVSpec(
	log *logger.Logger,
	lvName string,
	selectedLVG snc.LVMVolumeGroup,
	storageClassLVGParametersMap map[string]string,
	lvmType string,
	llvSize resource.Quantity,
	contiguous bool,
	source *snc.LVMLogicalVolumeSource,
	volumeCleanup string,
) snc.LVMLogicalVolumeSpec {
	lvmLogicalVolumeSpec := snc.LVMLogicalVolumeSpec{
		ActualLVNameOnTheNode: lvName,
		Type:                  lvmType,
		Size:                  llvSize.String(),
		LVMVolumeGroupName:    selectedLVG.Name,
		Source:                source,
	}

	switch lvmType {
	case internal.LVMTypeThin:
		lvmLogicalVolumeSpec.Thin = &snc.LVMLogicalVolumeThinSpec{
			PoolName: storageClassLVGParametersMap[selectedLVG.Name],
		}
		log.Info(fmt.Sprintf("[GetLLVSpec] Thin pool name: %s", lvmLogicalVolumeSpec.Thin.PoolName))
	case internal.LVMTypeThick:
		if contiguous {
			lvmLogicalVolumeSpec.Thick = &snc.LVMLogicalVolumeThickSpec{
				Contiguous: &contiguous,
			}
		}

		log.Info(fmt.Sprintf("[GetLLVSpec] Thick contiguous: %t", contiguous))
	}

	if volumeCleanup != "" {
		lvmLogicalVolumeSpec.VolumeCleanup = &volumeCleanup
	}

	log.Info(fmt.Sprintf("[GetLLVSpec] volumeCleanup: %s", volumeCleanup))

	return lvmLogicalVolumeSpec
}

func SelectLVG(storageClassLVGs []snc.LVMVolumeGroup, nodeName string) (*snc.LVMVolumeGroup, error) {
	for i := 0; i < len(storageClassLVGs); i++ {
		if storageClassLVGs[i].Status.Nodes[0].Name == nodeName {
			return &storageClassLVGs[i], nil
		}
	}
	return nil, fmt.Errorf("[SelectLVG] no LVMVolumeGroup found for node %s", nodeName)
}

func SelectLVGByName(storageClassLVGs []snc.LVMVolumeGroup, name string) (*snc.LVMVolumeGroup, error) {
	for i := 0; i < len(storageClassLVGs); i++ {
		if storageClassLVGs[i].Name == name {
			return &storageClassLVGs[i], nil
		}
	}
	return nil, fmt.Errorf("[SelectLVG] no LVMVolumeGroup found with name %s", name)
}

func SelectLVGByActualNameOnTheNode(storageClassLVGs []snc.LVMVolumeGroup, nodeName string, actualNameOnTheNode string) (*snc.LVMVolumeGroup, error) {
	for i := 0; i < len(storageClassLVGs); i++ {
		if storageClassLVGs[i].Spec.Local.NodeName == nodeName &&
			storageClassLVGs[i].Spec.ActualVGNameOnTheNode == actualNameOnTheNode {
			return &storageClassLVGs[i], nil
		}
	}
	return nil, fmt.Errorf("[SelectLVG] no LVMVolumeGroup found with actualNameOnTheNode %s on node %s", actualNameOnTheNode, nodeName)
}

func removeLLVFinalizerIfExist(ctx context.Context, kc client.Client, log *logger.Logger, llv *snc.LVMLogicalVolume, finalizer string) (bool, error) {
	for attempt := 0; attempt < KubernetesAPIRequestLimit; attempt++ {
		removed := false
		for i, val := range llv.Finalizers {
			if val == finalizer {
				llv.Finalizers = slices.Delete(llv.Finalizers, i, i+1)
				removed = true
				break
			}
		}

		if !removed {
			return false, nil
		}

		log.Trace(fmt.Sprintf("[removeLLVFinalizerIfExist] removing finalizer %s from LVMLogicalVolume %s", finalizer, llv.Name))
		err := kc.Update(ctx, llv)
		if err == nil {
			return true, nil
		}

		if !kerrors.IsConflict(err) {
			return false, fmt.Errorf("[removeLLVFinalizerIfExist] error updating LVMLogicalVolume %s: %w", llv.Name, err)
		}

		if attempt < KubernetesAPIRequestLimit-1 {
			log.Trace(fmt.Sprintf("[removeLLVFinalizerIfExist] conflict while updating LVMLogicalVolume %s, retrying...", llv.Name))
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			default:
				time.Sleep(KubernetesAPIRequestTimeout * time.Second)
				freshLLV, getErr := GetLVMLogicalVolume(ctx, kc, llv.Name, "")
				if getErr != nil {
					return false, fmt.Errorf("[removeLLVFinalizerIfExist] error getting LVMLogicalVolume %s after update conflict: %w", llv.Name, getErr)
				}
				// Update the llv struct with fresh data (without changing pointers because we need the new resource version outside of this function)
				*llv = *freshLLV
			}
		}
	}

	return false, fmt.Errorf("after %d attempts of removing finalizer %s from LVMLogicalVolume %s, last error: %w", KubernetesAPIRequestLimit, finalizer, llv.Name, nil)
}

func IsContiguous(request *csi.CreateVolumeRequest, lvmType string) bool {
	if lvmType == internal.LVMTypeThin {
		return false
	}

	val, exist := request.Parameters[internal.LVMVThickContiguousParamKey]
	if exist {
		return val == "true"
	}

	return false
}
