/*
Copyright 2024 Flant JSC

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

package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"sds-replicated-volume-controller/pkg/logger"
)

const (
	StorageClassVirtualizationAnnotationKey   = "virtualdisk.virtualization.deckhouse.io/access-mode"
	StorageClassVirtualizationAnnotationValue = "ReadWriteOnce"
)

func ReconcileControllerConfigMapEvent(ctx context.Context, cl client.Client, log logger.Logger, request reconcile.Request) (bool, error) {
	virtualizationEnabled, err := GetVirtualizationModuleEnabled(ctx, cl, log, request.NamespacedName)
	if err != nil {
		log.Error(err, "[ReconcileControllerConfigMapEvent] Failed to get virtualization module enabled")
		return true, err
	}
	log.Debug(fmt.Sprintf("[ReconcileControllerConfigMapEvent] Virtualization module enabled: %t", virtualizationEnabled))

	storageClassList, err := getStorageClassListForAnnotationsReconcile(ctx, cl, log, StorageClassProvisioner, virtualizationEnabled)
	if err != nil {
		log.Error(err, "[ReconcileControllerConfigMapEvent] Failed to get storage class list for annotations reconcile")
		return true, err
	}
	log.Debug("[ReconcileControllerConfigMapEvent] Successfully got storage class list for annotations reconcile")
	log.Trace(fmt.Sprintf("[ReconcileControllerConfigMapEvent] Storage class list for annotations reconcile: %+v", storageClassList))

	return reconcileStorageClassAnnotations(ctx, cl, log, storageClassList)
}

func GetVirtualizationModuleEnabled(ctx context.Context, cl client.Client, log logger.Logger, namespacedName client.ObjectKey) (bool, error) {
	configMap := &corev1.ConfigMap{}
	err := cl.Get(ctx, namespacedName, configMap)
	if err != nil {
		if !errors.IsNotFound(err) {
			return false, err
		}
		log.Trace(fmt.Sprintf("[GetVirtualizationModuleEnabled] ConfigMap %s/%s not found. Set virtualization module enabled to false", namespacedName.Namespace, namespacedName.Name))
		return false, nil
	}

	log.Trace(fmt.Sprintf("[GetVirtualizationModuleEnabled] ConfigMap %s/%s: %+v", namespacedName.Namespace, namespacedName.Name, configMap))
	virtualizationEnabledString, exists := configMap.Data[VirtualizationModuleEnabledKey]
	if !exists {
		return false, nil
	}

	return virtualizationEnabledString == "true", nil
}

func getStorageClassListForAnnotationsReconcile(ctx context.Context, cl client.Client, log logger.Logger, provisioner string, virtualizationEnabled bool) (*storagev1.StorageClassList, error) {
	storageClassesWithReplicatedVolumeProvisioner, err := getStorageClassListWithProvisioner(ctx, cl, log, provisioner)
	if err != nil {
		log.Error(err, fmt.Sprintf("[getStorageClassForAnnotationsReconcile] Failed to get storage classes with provisioner %s", provisioner))
		return nil, err
	}

	storageClassList := &storagev1.StorageClassList{}
	for _, storageClass := range storageClassesWithReplicatedVolumeProvisioner.Items {
		log.Trace(fmt.Sprintf("[getStorageClassForAnnotationsReconcile] Processing storage class %+v", storageClass))
		if storageClass.Parameters[StorageClassParamAllowRemoteVolumeAccessKey] == "false" {
			if storageClass.Annotations == nil {
				storageClass.Annotations = make(map[string]string)
			}

			value, exists := storageClass.Annotations[StorageClassVirtualizationAnnotationKey]

			if virtualizationEnabled {
				if value != StorageClassVirtualizationAnnotationValue {
					storageClass.Annotations[StorageClassVirtualizationAnnotationKey] = StorageClassVirtualizationAnnotationValue
					storageClassList.Items = append(storageClassList.Items, storageClass)
					log.Debug(fmt.Sprintf("[getStorageClassForAnnotationsReconcile] storage class %s has no annotation %s with value %s and virtualizationEnabled is true. Add the annotation with the proper value and add the storage class to the reconcile list.", storageClass.Name, StorageClassVirtualizationAnnotationKey, StorageClassVirtualizationAnnotationValue))
				}
			} else {
				if exists {
					delete(storageClass.Annotations, StorageClassVirtualizationAnnotationKey)
					if len(storageClass.Annotations) == 0 {
						storageClass.Annotations = nil
					}
					storageClassList.Items = append(storageClassList.Items, storageClass)
					log.Debug(fmt.Sprintf("[getStorageClassForAnnotationsReconcile] storage class %s has annotation %s and virtualizationEnabled is false. Remove the annotation and add the storage class to the reconcile list.", storageClass.Name, StorageClassVirtualizationAnnotationKey))
				}
			}
		}
	}

	return storageClassList, nil
}

func getStorageClassListWithProvisioner(ctx context.Context, cl client.Client, log logger.Logger, provisioner string) (*storagev1.StorageClassList, error) {
	storageClassList := &storagev1.StorageClassList{}
	err := cl.List(ctx, storageClassList)
	if err != nil {
		return nil, err
	}

	storageClassesWithProvisioner := &storagev1.StorageClassList{}
	for _, storageClass := range storageClassList.Items {
		log.Debug(fmt.Sprintf("[getStorageClassListWithProvisioner] process StorageClass %s with provisioner %s", storageClass.Name, provisioner))
		if storageClass.Provisioner == provisioner {
			log.Debug(fmt.Sprintf("[getStorageClassListWithProvisioner] StorageClass %s has provisioner %s and will be added to the list", storageClass.Name, provisioner))
			storageClassesWithProvisioner.Items = append(storageClassesWithProvisioner.Items, storageClass)
		}
	}

	return storageClassesWithProvisioner, nil
}

func reconcileStorageClassAnnotations(ctx context.Context, cl client.Client, log logger.Logger, storageClassList *storagev1.StorageClassList) (bool, error) {
	for _, storageClass := range storageClassList.Items {
		log.Debug(fmt.Sprintf("[reconcileStorageClassAnnotations] Update storage class %s", storageClass.Name))
		err := cl.Update(ctx, &storageClass)
		if err != nil {
			log.Error(err, fmt.Sprintf("[reconcileStorageClassAnnotations] Failed to update storage class %s", storageClass.Name))
			return true, err
		}
		log.Debug(fmt.Sprintf("[reconcileStorageClassAnnotations] Successfully updated storage class %s", storageClass.Name))
	}

	return false, nil
}
