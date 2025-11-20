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

	"gopkg.in/yaml.v2"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	v1alpha2 "github.com/deckhouse/sds-replicated-volume/api/v1alpha2old"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/logger"
	"k8s.io/apimachinery/pkg/api/meta"
)

const (
	KubernetesAPIRequestLimit       = 3
	KubernetesAPIRequestTimeout     = 1
	SDSReplicatedVolumeCSIFinalizer = "storage.deckhouse.io/sds-replicated-volume-csi"
)

func AreSizesEqualWithinDelta(leftSize, rightSize, allowedDelta resource.Quantity) bool {
	leftSizeFloat := float64(leftSize.Value())
	rightSizeFloat := float64(rightSize.Value())

	return math.Abs(leftSizeFloat-rightSizeFloat) < float64(allowedDelta.Value())
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

// StoragePoolInfo contains information extracted from ReplicatedStoragePool
type StoragePoolInfo struct {
	LVMVolumeGroups []snc.LVMVolumeGroup
	LVGToThinPool   map[string]string // maps LVMVolumeGroup name to ThinPool name
	LVMType         string            // "Thick" or "Thin"
}

// GetReplicatedStoragePool retrieves ReplicatedStoragePool by name
func GetReplicatedStoragePool(
	ctx context.Context,
	kc client.Client,
	storagePoolName string,
) (*srv.ReplicatedStoragePool, error) {
	rsp := &srv.ReplicatedStoragePool{}
	err := kc.Get(ctx, client.ObjectKey{Name: storagePoolName}, rsp)
	if err != nil {
		return nil, fmt.Errorf("failed to get ReplicatedStoragePool %s: %w", storagePoolName, err)
	}
	return rsp, nil
}

// GetLVMTypeFromStoragePool extracts LVM type from ReplicatedStoragePool
// Returns "Thick" for "LVM" and "Thin" for "LVMThin"
func GetLVMTypeFromStoragePool(rsp *srv.ReplicatedStoragePool) string {
	switch rsp.Spec.Type {
	case "LVMThin":
		return "Thin"
	case "LVM":
		return "Thick"
	default:
		return "Thick" // default fallback
	}
}

// GetLVGToThinPoolMap creates a map from LVMVolumeGroup name to ThinPool name
// from ReplicatedStoragePool spec
func GetLVGToThinPoolMap(rsp *srv.ReplicatedStoragePool) map[string]string {
	lvgToThinPool := make(map[string]string, len(rsp.Spec.LVMVolumeGroups))
	for _, rspLVG := range rsp.Spec.LVMVolumeGroups {
		lvgToThinPool[rspLVG.Name] = rspLVG.ThinPoolName
	}
	return lvgToThinPool
}

// GetStoragePoolInfo gets all information needed from ReplicatedStoragePool
func GetStoragePoolInfo(
	ctx context.Context,
	kc client.Client,
	log *logger.Logger,
	storagePoolName string,
) (*StoragePoolInfo, error) {
	// Get ReplicatedStoragePool
	rsp, err := GetReplicatedStoragePool(ctx, kc, storagePoolName)
	if err != nil {
		log.Error(err, fmt.Sprintf("failed to get ReplicatedStoragePool: %s", storagePoolName))
		return nil, err
	}

	// Extract LVM type
	lvmType := GetLVMTypeFromStoragePool(rsp)
	log.Info(fmt.Sprintf("[GetStoragePoolInfo] StoragePool %s LVM type: %s", storagePoolName, lvmType))

	// Extract LVG to ThinPool mapping
	lvgToThinPool := GetLVGToThinPoolMap(rsp)
	log.Info(fmt.Sprintf("[GetStoragePoolInfo] StoragePool %s LVG to ThinPool map: %+v", storagePoolName, lvgToThinPool))

	// Build set of LVG names from StoragePool
	lvgNamesSet := make(map[string]struct{}, len(rsp.Spec.LVMVolumeGroups))
	for _, rspLVG := range rsp.Spec.LVMVolumeGroups {
		lvgNamesSet[rspLVG.Name] = struct{}{}
	}

	// Get all LVMVolumeGroups from cluster and filter by names from StoragePool
	allLVGs, err := GetLVGList(ctx, kc)
	if err != nil {
		return nil, fmt.Errorf("failed to get LVMVolumeGroups list: %w", err)
	}

	lvmVolumeGroups := make([]snc.LVMVolumeGroup, 0)
	for _, lvg := range allLVGs.Items {
		log.Trace(fmt.Sprintf("[GetStoragePoolInfo] process lvg: %+v", lvg))

		if _, ok := lvgNamesSet[lvg.Name]; ok {
			log.Info(fmt.Sprintf("[GetStoragePoolInfo] found lvg from StoragePool: %s", lvg.Name))
			lvmVolumeGroups = append(lvmVolumeGroups, lvg)
		} else {
			log.Trace(fmt.Sprintf("[GetStoragePoolInfo] skip lvg: %s (not in StoragePool)", lvg.Name))
		}
	}

	return &StoragePoolInfo{
		LVMVolumeGroups: lvmVolumeGroups,
		LVGToThinPool:   lvgToThinPool,
		LVMType:         lvmType,
	}, nil
}

// CreateReplicatedVolume creates a ReplicatedVolume resource
func CreateReplicatedVolume(
	ctx context.Context,
	kc client.Client,
	log *logger.Logger,
	traceID, name string,
	rvSpec v1alpha2.ReplicatedVolumeSpec,
) (*v1alpha2.ReplicatedVolume, error) {
	rv := &v1alpha2.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			OwnerReferences: []metav1.OwnerReference{},
			Finalizers:      []string{SDSReplicatedVolumeCSIFinalizer},
		},
		Spec: rvSpec,
	}

	log.Trace(fmt.Sprintf("[CreateReplicatedVolume][traceID:%s][volumeID:%s] ReplicatedVolume: %+v", traceID, name, rv))

	err := kc.Create(ctx, rv)
	return rv, err
}

// GetReplicatedVolume gets a ReplicatedVolume resource
func GetReplicatedVolume(ctx context.Context, kc client.Client, name string) (*v1alpha2.ReplicatedVolume, error) {
	rv := &v1alpha2.ReplicatedVolume{}
	err := kc.Get(ctx, client.ObjectKey{Name: name}, rv)
	return rv, err
}

// WaitForReplicatedVolumeReady waits for ReplicatedVolume to become ready
func WaitForReplicatedVolumeReady(
	ctx context.Context,
	kc client.Client,
	log *logger.Logger,
	traceID, name string,
) (int, error) {
	var attemptCounter int
	log.Info(fmt.Sprintf("[WaitForReplicatedVolumeReady][traceID:%s][volumeID:%s] Waiting for ReplicatedVolume to become ready", traceID, name))
	for {
		attemptCounter++
		select {
		case <-ctx.Done():
			log.Warning(fmt.Sprintf("[WaitForReplicatedVolumeReady][traceID:%s][volumeID:%s] context done. Failed to wait for ReplicatedVolume", traceID, name))
			return attemptCounter, ctx.Err()
		default:
			time.Sleep(500 * time.Millisecond)
		}

		rv, err := GetReplicatedVolume(ctx, kc, name)
		if err != nil {
			return attemptCounter, err
		}

		if attemptCounter%10 == 0 {
			log.Info(fmt.Sprintf("[WaitForReplicatedVolumeReady][traceID:%s][volumeID:%s] Attempt: %d, ReplicatedVolume: %+v", traceID, name, attemptCounter, rv))
		}

		if rv.DeletionTimestamp != nil {
			return attemptCounter, fmt.Errorf("failed to create ReplicatedVolume %s, reason: ReplicatedVolume is being deleted", name)
		}

		if rv.Status != nil {
			readyCond := meta.FindStatusCondition(rv.Status.Conditions, v1alpha2.ConditionTypeReady)
			if readyCond != nil && readyCond.Status == metav1.ConditionTrue {
				log.Info(fmt.Sprintf("[WaitForReplicatedVolumeReady][traceID:%s][volumeID:%s] ReplicatedVolume is ready", traceID, name))
				return attemptCounter, nil
			}
			log.Trace(fmt.Sprintf("[WaitForReplicatedVolumeReady][traceID:%s][volumeID:%s] Attempt %d, ReplicatedVolume not ready yet. Waiting...", traceID, name, attemptCounter))
		}
	}
}

// DeleteReplicatedVolume deletes a ReplicatedVolume resource
func DeleteReplicatedVolume(ctx context.Context, kc client.Client, log *logger.Logger, traceID, name string) error {
	log.Trace(fmt.Sprintf("[DeleteReplicatedVolume][traceID:%s][volumeID:%s] Trying to find ReplicatedVolume", traceID, name))
	rv, err := GetReplicatedVolume(ctx, kc, name)
	if err != nil {
		if kerrors.IsNotFound(err) {
			log.Info(fmt.Sprintf("[DeleteReplicatedVolume][traceID:%s][volumeID:%s] ReplicatedVolume not found, already deleted", traceID, name))
			return nil
		}
		return fmt.Errorf("get ReplicatedVolume %s: %w", name, err)
	}

	log.Trace(fmt.Sprintf("[DeleteReplicatedVolume][traceID:%s][volumeID:%s] ReplicatedVolume found: %+v", traceID, name, rv))
	log.Trace(fmt.Sprintf("[DeleteReplicatedVolume][traceID:%s][volumeID:%s] Removing finalizer %s if exists", traceID, name, SDSReplicatedVolumeCSIFinalizer))

	removed, err := removeRVFinalizerIfExist(ctx, kc, log, rv, SDSReplicatedVolumeCSIFinalizer)
	if err != nil {
		return fmt.Errorf("remove finalizers from ReplicatedVolume %s: %w", name, err)
	}
	if removed {
		log.Trace(fmt.Sprintf("[DeleteReplicatedVolume][traceID:%s][volumeID:%s] finalizer %s removed from ReplicatedVolume %s", traceID, name, SDSReplicatedVolumeCSIFinalizer, name))
	} else {
		log.Warning(fmt.Sprintf("[DeleteReplicatedVolume][traceID:%s][volumeID:%s] finalizer %s not found in ReplicatedVolume %s", traceID, name, SDSReplicatedVolumeCSIFinalizer, name))
	}

	log.Trace(fmt.Sprintf("[DeleteReplicatedVolume][traceID:%s][volumeID:%s] Trying to delete ReplicatedVolume", traceID, name))
	err = kc.Delete(ctx, rv)
	return err
}

func removeRVFinalizerIfExist(ctx context.Context, kc client.Client, log *logger.Logger, rv *v1alpha2.ReplicatedVolume, finalizer string) (bool, error) {
	for attempt := 0; attempt < KubernetesAPIRequestLimit; attempt++ {
		removed := false
		for i, val := range rv.Finalizers {
			if val == finalizer {
				rv.Finalizers = slices.Delete(rv.Finalizers, i, i+1)
				removed = true
				break
			}
		}

		if !removed {
			return false, nil
		}

		log.Trace(fmt.Sprintf("[removeRVFinalizerIfExist] removing finalizer %s from ReplicatedVolume %s", finalizer, rv.Name))
		err := kc.Update(ctx, rv)
		if err == nil {
			return true, nil
		}

		if !kerrors.IsConflict(err) {
			return false, fmt.Errorf("[removeRVFinalizerIfExist] error updating ReplicatedVolume %s: %w", rv.Name, err)
		}

		if attempt < KubernetesAPIRequestLimit-1 {
			log.Trace(fmt.Sprintf("[removeRVFinalizerIfExist] conflict while updating ReplicatedVolume %s, retrying...", rv.Name))
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			default:
				time.Sleep(KubernetesAPIRequestTimeout * time.Second)
				freshRV, getErr := GetReplicatedVolume(ctx, kc, rv.Name)
				if getErr != nil {
					return false, fmt.Errorf("[removeRVFinalizerIfExist] error getting ReplicatedVolume %s after update conflict: %w", rv.Name, getErr)
				}
				*rv = *freshRV
			}
		}
	}

	return false, fmt.Errorf("after %d attempts of removing finalizer %s from ReplicatedVolume %s, last error: %w", KubernetesAPIRequestLimit, finalizer, rv.Name, nil)
}

// GetReplicatedVolumeReplicaForNode gets ReplicatedVolumeReplica for a specific node
func GetReplicatedVolumeReplicaForNode(ctx context.Context, kc client.Client, volumeName, nodeName string) (*v1alpha2.ReplicatedVolumeReplica, error) {
	rvrList := &v1alpha2.ReplicatedVolumeReplicaList{}
	err := kc.List(
		ctx,
		rvrList,
		client.MatchingFields{"spec.replicatedVolumeName": volumeName},
		client.MatchingFields{"spec.nodeName": nodeName},
	)
	if err != nil {
		return nil, err
	}

	for i := range rvrList.Items {
		if rvrList.Items[i].Spec.NodeName == nodeName {
			return &rvrList.Items[i], nil
		}
	}

	return nil, fmt.Errorf("ReplicatedVolumeReplica not found for volume %s on node %s", volumeName, nodeName)
}

// GetDRBDDevicePath gets DRBD device path from ReplicatedVolumeReplica status
func GetDRBDDevicePath(rvr *v1alpha2.ReplicatedVolumeReplica) (string, error) {
	if rvr.Status == nil || rvr.Status.DRBD == nil || len(rvr.Status.DRBD.Devices) == 0 {
		return "", fmt.Errorf("DRBD status not available or no devices found")
	}

	minor := rvr.Status.DRBD.Devices[0].Minor
	return fmt.Sprintf("/dev/drbd%d", minor), nil
}

// ExpandReplicatedVolume expands a ReplicatedVolume
func ExpandReplicatedVolume(ctx context.Context, kc client.Client, rv *v1alpha2.ReplicatedVolume, newSize resource.Quantity) error {
	rv.Spec.Size = newSize
	return kc.Update(ctx, rv)
}

// BuildReplicatedVolumeSpec builds ReplicatedVolumeSpec from parameters
func BuildReplicatedVolumeSpec(
	size resource.Quantity,
	lvmType string,
	volumeGroups []v1alpha2.LVGRef,
	replicas byte,
	topology string,
	volumeAccess string,
	sharedSecret string,
	publishRequested []string,
	zones []string,
) v1alpha2.ReplicatedVolumeSpec {
	return v1alpha2.ReplicatedVolumeSpec{
		Size:             size,
		Replicas:         replicas,
		SharedSecret:     sharedSecret,
		Topology:         topology,
		VolumeAccess:     volumeAccess,
		PublishRequested: publishRequested,
		Zones:            zones,
		LVM: v1alpha2.LVMSpec{
			Type:            lvmType,
			LVMVolumeGroups: volumeGroups,
		},
	}
}

// AddPublishRequested adds a node name to publishRequested array if not already present
func AddPublishRequested(ctx context.Context, kc client.Client, log *logger.Logger, traceID, volumeName, nodeName string) error {
	for attempt := 0; attempt < KubernetesAPIRequestLimit; attempt++ {
		rv, err := GetReplicatedVolume(ctx, kc, volumeName)
		if err != nil {
			return fmt.Errorf("get ReplicatedVolume %s: %w", volumeName, err)
		}

		// Check if node is already in publishRequested
		for _, existingNode := range rv.Spec.PublishRequested {
			if existingNode == nodeName {
				log.Info(fmt.Sprintf("[AddPublishRequested][traceID:%s][volumeID:%s][node:%s] Node already in publishRequested", traceID, volumeName, nodeName))
				return nil
			}
		}

		// Check if we can add more nodes (max 2)
		if len(rv.Spec.PublishRequested) >= 2 {
			return fmt.Errorf("cannot add node %s to publishRequested: maximum of 2 nodes already present", nodeName)
		}

		// Add node to publishRequested
		rv.Spec.PublishRequested = append(rv.Spec.PublishRequested, nodeName)

		log.Info(fmt.Sprintf("[AddPublishRequested][traceID:%s][volumeID:%s][node:%s] Adding node to publishRequested", traceID, volumeName, nodeName))
		err = kc.Update(ctx, rv)
		if err == nil {
			return nil
		}

		if !kerrors.IsConflict(err) {
			return fmt.Errorf("error updating ReplicatedVolume %s: %w", volumeName, err)
		}

		if attempt < KubernetesAPIRequestLimit-1 {
			log.Trace(fmt.Sprintf("[AddPublishRequested][traceID:%s][volumeID:%s][node:%s] Conflict while updating, retrying...", traceID, volumeName, nodeName))
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				time.Sleep(KubernetesAPIRequestTimeout * time.Second)
			}
		}
	}

	return fmt.Errorf("failed to add node %s to publishRequested after %d attempts", nodeName, KubernetesAPIRequestLimit)
}

// RemovePublishRequested removes a node name from publishRequested array
func RemovePublishRequested(ctx context.Context, kc client.Client, log *logger.Logger, traceID, volumeName, nodeName string) error {
	for attempt := 0; attempt < KubernetesAPIRequestLimit; attempt++ {
		rv, err := GetReplicatedVolume(ctx, kc, volumeName)
		if err != nil {
			if kerrors.IsNotFound(err) {
				log.Info(fmt.Sprintf("[RemovePublishRequested][traceID:%s][volumeID:%s][node:%s] ReplicatedVolume not found, assuming already removed", traceID, volumeName, nodeName))
				return nil
			}
			return fmt.Errorf("get ReplicatedVolume %s: %w", volumeName, err)
		}

		// Check if node is in publishRequested
		found := false
		for i, existingNode := range rv.Spec.PublishRequested {
			if existingNode == nodeName {
				rv.Spec.PublishRequested = slices.Delete(rv.Spec.PublishRequested, i, i+1)
				found = true
				break
			}
		}

		if !found {
			log.Info(fmt.Sprintf("[RemovePublishRequested][traceID:%s][volumeID:%s][node:%s] Node not in publishRequested, nothing to remove", traceID, volumeName, nodeName))
			return nil
		}

		log.Info(fmt.Sprintf("[RemovePublishRequested][traceID:%s][volumeID:%s][node:%s] Removing node from publishRequested", traceID, volumeName, nodeName))
		err = kc.Update(ctx, rv)
		if err == nil {
			return nil
		}

		if !kerrors.IsConflict(err) {
			return fmt.Errorf("error updating ReplicatedVolume %s: %w", volumeName, err)
		}

		if attempt < KubernetesAPIRequestLimit-1 {
			log.Trace(fmt.Sprintf("[RemovePublishRequested][traceID:%s][volumeID:%s][node:%s] Conflict while updating, retrying...", traceID, volumeName, nodeName))
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				time.Sleep(KubernetesAPIRequestTimeout * time.Second)
			}
		}
	}

	return fmt.Errorf("failed to remove node %s from publishRequested after %d attempts", nodeName, KubernetesAPIRequestLimit)
}

// WaitForPublishProvided waits for a node name to appear in publishProvided status
func WaitForPublishProvided(
	ctx context.Context,
	kc client.Client,
	log *logger.Logger,
	traceID, volumeName, nodeName string,
) error {
	var attemptCounter int
	log.Info(fmt.Sprintf("[WaitForPublishProvided][traceID:%s][volumeID:%s][node:%s] Waiting for node to appear in publishProvided", traceID, volumeName, nodeName))
	for {
		attemptCounter++
		select {
		case <-ctx.Done():
			log.Warning(fmt.Sprintf("[WaitForPublishProvided][traceID:%s][volumeID:%s][node:%s] context done", traceID, volumeName, nodeName))
			return ctx.Err()
		default:
			time.Sleep(500 * time.Millisecond)
		}

		rv, err := GetReplicatedVolume(ctx, kc, volumeName)
		if err != nil {
			if kerrors.IsNotFound(err) {
				return fmt.Errorf("ReplicatedVolume %s not found", volumeName)
			}
			return err
		}

		if rv.Status != nil {
			if attemptCounter%10 == 0 {
				log.Info(fmt.Sprintf("[WaitForPublishProvided][traceID:%s][volumeID:%s][node:%s] Attempt: %d, publishProvided: %v", traceID, volumeName, nodeName, attemptCounter, rv.Status.PublishProvided))
			}

			// Check if node is in publishProvided
			for _, publishedNode := range rv.Status.PublishProvided {
				if publishedNode == nodeName {
					log.Info(fmt.Sprintf("[WaitForPublishProvided][traceID:%s][volumeID:%s][node:%s] Node is now in publishProvided", traceID, volumeName, nodeName))
					return nil
				}
			}
		} else {
			if attemptCounter%10 == 0 {
				log.Info(fmt.Sprintf("[WaitForPublishProvided][traceID:%s][volumeID:%s][node:%s] Attempt: %d, status is nil", traceID, volumeName, nodeName, attemptCounter))
			}
		}

		log.Trace(fmt.Sprintf("[WaitForPublishProvided][traceID:%s][volumeID:%s][node:%s] Attempt %d, node not in publishProvided yet. Waiting...", traceID, volumeName, nodeName, attemptCounter))
	}
}

// WaitForPublishRemoved waits for a node name to disappear from publishProvided status
func WaitForPublishRemoved(
	ctx context.Context,
	kc client.Client,
	log *logger.Logger,
	traceID, volumeName, nodeName string,
) error {
	var attemptCounter int
	log.Info(fmt.Sprintf("[WaitForPublishRemoved][traceID:%s][volumeID:%s][node:%s] Waiting for node to disappear from publishProvided", traceID, volumeName, nodeName))
	for {
		attemptCounter++
		select {
		case <-ctx.Done():
			log.Warning(fmt.Sprintf("[WaitForPublishRemoved][traceID:%s][volumeID:%s][node:%s] context done", traceID, volumeName, nodeName))
			return ctx.Err()
		default:
			time.Sleep(500 * time.Millisecond)
		}

		rv, err := GetReplicatedVolume(ctx, kc, volumeName)
		if err != nil {
			if kerrors.IsNotFound(err) {
				// Volume deleted, consider it as removed
				log.Info(fmt.Sprintf("[WaitForPublishRemoved][traceID:%s][volumeID:%s][node:%s] ReplicatedVolume not found, considering node as removed", traceID, volumeName, nodeName))
				return nil
			}
			return err
		}

		if rv.Status != nil {
			if attemptCounter%10 == 0 {
				log.Info(fmt.Sprintf("[WaitForPublishRemoved][traceID:%s][volumeID:%s][node:%s] Attempt: %d, publishProvided: %v", traceID, volumeName, nodeName, attemptCounter, rv.Status.PublishProvided))
			}

			// Check if node is NOT in publishProvided
			found := false
			for _, publishedNode := range rv.Status.PublishProvided {
				if publishedNode == nodeName {
					found = true
					break
				}
			}

			if !found {
				log.Info(fmt.Sprintf("[WaitForPublishRemoved][traceID:%s][volumeID:%s][node:%s] Node is no longer in publishProvided", traceID, volumeName, nodeName))
				return nil
			}
		} else {
			if attemptCounter%10 == 0 {
				log.Info(fmt.Sprintf("[WaitForPublishRemoved][traceID:%s][volumeID:%s][node:%s] Attempt: %d, status is nil, considering node as removed", traceID, volumeName, nodeName, attemptCounter))
			}
			// If status is nil, consider node as removed
			return nil
		}

		log.Trace(fmt.Sprintf("[WaitForPublishRemoved][traceID:%s][volumeID:%s][node:%s] Attempt %d, node still in publishProvided. Waiting...", traceID, volumeName, nodeName, attemptCounter))
	}
}
