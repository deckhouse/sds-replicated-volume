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

package controller_test

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"sds-replicated-volume-controller/config"
	"sds-replicated-volume-controller/pkg/controller"
	"sds-replicated-volume-controller/pkg/logger"
)

var _ = Describe(controller.StorageClassAnnotationsCtrlName, func() {

	const (
		testNameSpace = "test-namespace"
		testName      = "test-name"
	)

	var (
		ctx = context.Background()
		cl  = newFakeClient()
		log = logger.Logger{}

		validCFG, _ = config.NewConfig()

		allowVolumeExpansion   bool = true
		volumeBindingMode           = storagev1.VolumeBindingWaitForFirstConsumer
		reclaimPolicy               = corev1.PersistentVolumeReclaimPolicy(controller.ReclaimPolicyRetain)
		storageClassParameters      = map[string]string{
			controller.StorageClassStoragePoolKey:                     "test-sp",
			controller.StorageClassParamFSTypeKey:                     controller.FsTypeExt4,
			controller.StorageClassParamPlacementPolicyKey:            controller.PlacementPolicyAutoPlaceTopology,
			controller.StorageClassParamNetProtocolKey:                controller.NetProtocolC,
			controller.StorageClassParamNetRRConflictKey:              controller.RrConflictRetryConnect,
			controller.StorageClassParamAutoQuorumKey:                 controller.SuspendIo,
			controller.StorageClassParamAutoAddQuorumTieBreakerKey:    "true",
			controller.StorageClassParamOnNoQuorumKey:                 controller.SuspendIo,
			controller.StorageClassParamOnNoDataAccessibleKey:         controller.SuspendIo,
			controller.StorageClassParamOnSuspendedPrimaryOutdatedKey: controller.PrimaryOutdatedForceSecondary,
			controller.StorageClassPlacementCountKey:                  "3",
			controller.StorageClassAutoEvictMinReplicaCountKey:        "3",
			controller.StorageClassParamReplicasOnSameKey:             fmt.Sprintf("class.storage.deckhouse.io/%s", testName),
			controller.StorageClassParamReplicasOnDifferentKey:        controller.ZoneLabel,
			controller.StorageClassParamAllowRemoteVolumeAccessKey:    "false",
			controller.QuorumMinimumRedundancyWithPrefixSCKey:         "2",
		}

		validStorageClassResource = &storagev1.StorageClass{
			TypeMeta: metav1.TypeMeta{
				Kind:       controller.StorageClassKind,
				APIVersion: controller.StorageClassAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:            testName,
				Namespace:       testNameSpace,
				OwnerReferences: nil,
				Finalizers:      nil,
				ManagedFields:   nil,
				Labels: map[string]string{
					"storage.deckhouse.io/managed-by": "sds-replicated-volume",
				},
			},
			Parameters:           storageClassParameters,
			ReclaimPolicy:        &reclaimPolicy,
			AllowVolumeExpansion: &allowVolumeExpansion,
			VolumeBindingMode:    &volumeBindingMode,
			Provisioner:          controller.StorageClassProvisioner,
		}
	)

	It("ReconcileControllerConfigMapEvent_ConfigMap_does_not_exist_StorageClass_local_without_annotations_exist", func() {
		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: validCFG.ControllerNamespace,
				Name:      controller.ControllerConfigMapName,
			},
		}
		configMap, err := getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())
		Expect(configMap).NotTo(BeNil())
		Expect(configMap.Name).To(Equal(""))

		virtualizationEnabled, err := controller.GetVirtualizationModuleEnabled(ctx, cl, log, request.NamespacedName)
		Expect(err).NotTo(HaveOccurred())
		Expect(virtualizationEnabled).To(BeFalse())

		scResource := validStorageClassResource.DeepCopy()
		err = cl.Create(ctx, scResource)
		Expect(err).NotTo(HaveOccurred())

		storageClass, err := getSC(ctx, cl, scResource.Name, scResource.Namespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(storageClass).NotTo(BeNil())
		Expect(storageClass.Name).To(Equal(scResource.Name))
		Expect(storageClass.Namespace).To(Equal(scResource.Namespace))
		Expect(storageClass.Annotations).To(BeNil())
		Expect(storageClass.Parameters[controller.StorageClassParamAllowRemoteVolumeAccessKey]).To(Equal("false"))

		shouldRequeue, err := controller.ReconcileControllerConfigMapEvent(ctx, cl, log, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		storageClass, err = getSC(ctx, cl, scResource.Name, scResource.Namespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(storageClass).NotTo(BeNil())
		Expect(storageClass.Name).To(Equal(scResource.Name))
		Expect(storageClass.Namespace).To(Equal(scResource.Namespace))
		Expect(storageClass.Annotations).To(BeNil())
		Expect(storageClass.Parameters[controller.StorageClassParamAllowRemoteVolumeAccessKey]).To(Equal("false"))

		// Cleanup
		err = cl.Delete(ctx, storageClass)
		Expect(err).NotTo(HaveOccurred())

		_, err = getSC(ctx, cl, scResource.Name, scResource.Namespace)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())
	})

	It("ReconcileControllerConfigMapEvent_ConfigMap_does_not_exist_StorageClass_local_with_default_annotation_exist", func() {
		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: validCFG.ControllerNamespace,
				Name:      controller.ControllerConfigMapName,
			},
		}
		configMap, err := getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())
		Expect(configMap).NotTo(BeNil())
		Expect(configMap.Name).To(Equal(""))

		virtualizationEnabled, err := controller.GetVirtualizationModuleEnabled(ctx, cl, log, request.NamespacedName)
		Expect(err).NotTo(HaveOccurred())
		Expect(virtualizationEnabled).To(BeFalse())

		scResource := validStorageClassResource.DeepCopy()
		scResource.Annotations = map[string]string{controller.DefaultStorageClassAnnotationKey: "true"}
		err = cl.Create(ctx, scResource)
		Expect(err).NotTo(HaveOccurred())

		storageClass, err := getSC(ctx, cl, scResource.Name, scResource.Namespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(storageClass).NotTo(BeNil())
		Expect(storageClass.Name).To(Equal(scResource.Name))
		Expect(storageClass.Namespace).To(Equal(scResource.Namespace))
		Expect(storageClass.Annotations).NotTo(BeNil())
		Expect(len(storageClass.Annotations)).To(Equal(1))
		Expect(storageClass.Annotations[controller.DefaultStorageClassAnnotationKey]).To(Equal("true"))
		Expect(storageClass.Parameters[controller.StorageClassParamAllowRemoteVolumeAccessKey]).To(Equal("false"))

		shouldRequeue, err := controller.ReconcileControllerConfigMapEvent(ctx, cl, log, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		storageClass, err = getSC(ctx, cl, scResource.Name, scResource.Namespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(storageClass).NotTo(BeNil())
		Expect(storageClass.Name).To(Equal(scResource.Name))
		Expect(storageClass.Namespace).To(Equal(scResource.Namespace))
		Expect(storageClass.Annotations).NotTo(BeNil())
		Expect(len(storageClass.Annotations)).To(Equal(1))
		Expect(storageClass.Annotations[controller.DefaultStorageClassAnnotationKey]).To(Equal("true"))
		Expect(storageClass.Parameters[controller.StorageClassParamAllowRemoteVolumeAccessKey]).To(Equal("false"))

		// Cleanup
		err = cl.Delete(ctx, storageClass)
		Expect(err).NotTo(HaveOccurred())

		_, err = getSC(ctx, cl, scResource.Name, scResource.Namespace)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())

	})

	It("ReconcileControllerConfigMapEvent_ConfigMap_exist_without_data_StorageClass_local_without_annotations_exist", func() {
		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: validCFG.ControllerNamespace,
				Name:      controller.ControllerConfigMapName,
			},
		}

		err := cl.Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: request.Namespace,
				Name:      request.Name,
			},
		})
		Expect(err).NotTo(HaveOccurred())

		configMap, err := getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(configMap).NotTo(BeNil())
		Expect(configMap.Name).To(Equal(controller.ControllerConfigMapName))
		Expect(configMap.Namespace).To(Equal(validCFG.ControllerNamespace))
		Expect(configMap.Data).To(BeNil())

		virtualizationEnabled, err := controller.GetVirtualizationModuleEnabled(ctx, cl, log, request.NamespacedName)
		Expect(err).NotTo(HaveOccurred())
		Expect(virtualizationEnabled).To(BeFalse())

		scResource := validStorageClassResource.DeepCopy()
		err = cl.Create(ctx, scResource)
		Expect(err).NotTo(HaveOccurred())

		storageClass, err := getSC(ctx, cl, scResource.Name, scResource.Namespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(storageClass).NotTo(BeNil())
		Expect(storageClass.Name).To(Equal(scResource.Name))
		Expect(storageClass.Namespace).To(Equal(scResource.Namespace))
		Expect(storageClass.Annotations).To(BeNil())
		Expect(storageClass.Parameters[controller.StorageClassParamAllowRemoteVolumeAccessKey]).To(Equal("false"))

		shouldRequeue, err := controller.ReconcileControllerConfigMapEvent(ctx, cl, log, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		storageClass, err = getSC(ctx, cl, scResource.Name, scResource.Namespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(storageClass).NotTo(BeNil())
		Expect(storageClass.Name).To(Equal(scResource.Name))
		Expect(storageClass.Namespace).To(Equal(scResource.Namespace))
		Expect(storageClass.Annotations).To(BeNil())
		Expect(storageClass.Parameters[controller.StorageClassParamAllowRemoteVolumeAccessKey]).To(Equal("false"))

		// Cleanup
		err = cl.Delete(ctx, storageClass)
		Expect(err).NotTo(HaveOccurred())

		_, err = getSC(ctx, cl, scResource.Name, scResource.Namespace)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())

		err = cl.Delete(ctx, configMap)
		Expect(err).NotTo(HaveOccurred())

		_, err = getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())
	})

	It("ReconcileControllerConfigMapEvent_ConfigMap_exist_with_virtualization_key_and_virtualization_value_is_false_StorageClass_local_without_annotations_exist", func() {
		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: validCFG.ControllerNamespace,
				Name:      controller.ControllerConfigMapName,
			},
		}

		err := cl.Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: request.Namespace,
				Name:      request.Name,
			},
			Data: map[string]string{controller.VirtualizationModuleEnabledKey: "false"},
		})
		Expect(err).NotTo(HaveOccurred())

		configMap, err := getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(configMap).NotTo(BeNil())
		Expect(configMap.Name).To(Equal(controller.ControllerConfigMapName))
		Expect(configMap.Namespace).To(Equal(validCFG.ControllerNamespace))
		Expect(configMap.Data).NotTo(BeNil())
		Expect(configMap.Data[controller.VirtualizationModuleEnabledKey]).To(Equal("false"))

		virtualizationEnabled, err := controller.GetVirtualizationModuleEnabled(ctx, cl, log, request.NamespacedName)
		Expect(err).NotTo(HaveOccurred())
		Expect(virtualizationEnabled).To(BeFalse())

		scResource := validStorageClassResource.DeepCopy()
		err = cl.Create(ctx, scResource)
		Expect(err).NotTo(HaveOccurred())

		storageClass, err := getSC(ctx, cl, scResource.Name, scResource.Namespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(storageClass).NotTo(BeNil())
		Expect(storageClass.Name).To(Equal(scResource.Name))
		Expect(storageClass.Namespace).To(Equal(scResource.Namespace))
		Expect(storageClass.Annotations).To(BeNil())
		Expect(storageClass.Parameters[controller.StorageClassParamAllowRemoteVolumeAccessKey]).To(Equal("false"))

		shouldRequeue, err := controller.ReconcileControllerConfigMapEvent(ctx, cl, log, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		storageClass, err = getSC(ctx, cl, scResource.Name, scResource.Namespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(storageClass).NotTo(BeNil())
		Expect(storageClass.Name).To(Equal(scResource.Name))
		Expect(storageClass.Namespace).To(Equal(scResource.Namespace))
		Expect(storageClass.Annotations).To(BeNil())
		Expect(storageClass.Parameters[controller.StorageClassParamAllowRemoteVolumeAccessKey]).To(Equal("false"))

		// Cleanup
		err = cl.Delete(ctx, storageClass)
		Expect(err).NotTo(HaveOccurred())

		_, err = getSC(ctx, cl, scResource.Name, scResource.Namespace)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())

		err = cl.Delete(ctx, configMap)
		Expect(err).NotTo(HaveOccurred())

		_, err = getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())
	})

	It("ReconcileControllerConfigMapEvent_ConfigMap_exist_with_virtualization_key_and_virtualization_value_is_true_StorageClass_local_without_annotations_exist", func() {
		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: validCFG.ControllerNamespace,
				Name:      controller.ControllerConfigMapName,
			},
		}

		err := cl.Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: request.Namespace,
				Name:      request.Name,
			},
			Data: map[string]string{controller.VirtualizationModuleEnabledKey: "true"},
		})
		Expect(err).NotTo(HaveOccurred())

		configMap, err := getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(configMap).NotTo(BeNil())
		Expect(configMap.Name).To(Equal(controller.ControllerConfigMapName))
		Expect(configMap.Namespace).To(Equal(validCFG.ControllerNamespace))
		Expect(configMap.Data).NotTo(BeNil())
		Expect(configMap.Data[controller.VirtualizationModuleEnabledKey]).To(Equal("true"))

		virtualizationEnabled, err := controller.GetVirtualizationModuleEnabled(ctx, cl, log, request.NamespacedName)
		Expect(err).NotTo(HaveOccurred())
		Expect(virtualizationEnabled).To(BeTrue())

		scResource := validStorageClassResource.DeepCopy()
		err = cl.Create(ctx, scResource)
		Expect(err).NotTo(HaveOccurred())

		storageClass, err := getSC(ctx, cl, scResource.Name, scResource.Namespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(storageClass).NotTo(BeNil())
		Expect(storageClass.Name).To(Equal(scResource.Name))
		Expect(storageClass.Namespace).To(Equal(scResource.Namespace))
		Expect(storageClass.Annotations).To(BeNil())
		Expect(storageClass.Parameters[controller.StorageClassParamAllowRemoteVolumeAccessKey]).To(Equal("false"))

		shouldRequeue, err := controller.ReconcileControllerConfigMapEvent(ctx, cl, log, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		storageClass, err = getSC(ctx, cl, scResource.Name, scResource.Namespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(storageClass).NotTo(BeNil())
		Expect(storageClass.Name).To(Equal(scResource.Name))
		Expect(storageClass.Namespace).To(Equal(scResource.Namespace))
		Expect(storageClass.Annotations).NotTo(BeNil())
		Expect(len(storageClass.Annotations)).To(Equal(1))
		Expect(storageClass.Annotations[controller.StorageClassVirtualizationAnnotationKey]).To(Equal(controller.StorageClassVirtualizationAnnotationValue))
		Expect(storageClass.Parameters[controller.StorageClassParamAllowRemoteVolumeAccessKey]).To(Equal("false"))

		// Cleanup
		err = cl.Delete(ctx, storageClass)
		Expect(err).NotTo(HaveOccurred())

		_, err = getSC(ctx, cl, scResource.Name, scResource.Namespace)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())

		err = cl.Delete(ctx, configMap)
		Expect(err).NotTo(HaveOccurred())

		_, err = getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())
	})

	It("ReconcileControllerConfigMapEvent_ConfigMap_exist_with_virtualization_key_and_virtualization_value_is_true_StorageClass_local_with_default_annotation_exist", func() {
		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: validCFG.ControllerNamespace,
				Name:      controller.ControllerConfigMapName,
			},
		}

		err := cl.Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: request.Namespace,
				Name:      request.Name,
			},
			Data: map[string]string{controller.VirtualizationModuleEnabledKey: "true"},
		})
		Expect(err).NotTo(HaveOccurred())

		configMap, err := getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(configMap).NotTo(BeNil())
		Expect(configMap.Name).To(Equal(controller.ControllerConfigMapName))
		Expect(configMap.Namespace).To(Equal(validCFG.ControllerNamespace))
		Expect(configMap.Data).NotTo(BeNil())
		Expect(configMap.Data[controller.VirtualizationModuleEnabledKey]).To(Equal("true"))

		virtualizationEnabled, err := controller.GetVirtualizationModuleEnabled(ctx, cl, log, request.NamespacedName)
		Expect(err).NotTo(HaveOccurred())
		Expect(virtualizationEnabled).To(BeTrue())

		scResource := validStorageClassResource.DeepCopy()
		scResource.Annotations = map[string]string{controller.DefaultStorageClassAnnotationKey: "true"}
		err = cl.Create(ctx, scResource)
		Expect(err).NotTo(HaveOccurred())

		storageClass, err := getSC(ctx, cl, scResource.Name, scResource.Namespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(storageClass).NotTo(BeNil())
		Expect(storageClass.Name).To(Equal(scResource.Name))
		Expect(storageClass.Namespace).To(Equal(scResource.Namespace))
		Expect(storageClass.Annotations).NotTo(BeNil())
		Expect(len(storageClass.Annotations)).To(Equal(1))
		Expect(storageClass.Annotations[controller.DefaultStorageClassAnnotationKey]).To(Equal("true"))
		Expect(storageClass.Parameters[controller.StorageClassParamAllowRemoteVolumeAccessKey]).To(Equal("false"))

		shouldRequeue, err := controller.ReconcileControllerConfigMapEvent(ctx, cl, log, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		storageClass, err = getSC(ctx, cl, scResource.Name, scResource.Namespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(storageClass).NotTo(BeNil())
		Expect(storageClass.Name).To(Equal(scResource.Name))
		Expect(storageClass.Namespace).To(Equal(scResource.Namespace))
		Expect(storageClass.Annotations).NotTo(BeNil())
		Expect(len(storageClass.Annotations)).To(Equal(2))
		Expect(storageClass.Annotations[controller.DefaultStorageClassAnnotationKey]).To(Equal("true"))
		Expect(storageClass.Annotations[controller.StorageClassVirtualizationAnnotationKey]).To(Equal(controller.StorageClassVirtualizationAnnotationValue))
		Expect(storageClass.Parameters[controller.StorageClassParamAllowRemoteVolumeAccessKey]).To(Equal("false"))
	})

	It("ReconcileControllerConfigMapEvent_ConfigMap_exist_with_virtualization_key_and_virtualization_value_is_change_from_true_to_false_StorageClass_local_with_default_and_virtualization_annotations_exist", func() {
		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: validCFG.ControllerNamespace,
				Name:      controller.ControllerConfigMapName,
			},
		}

		configMap, err := getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(configMap).NotTo(BeNil())
		Expect(configMap.Name).To(Equal(controller.ControllerConfigMapName))
		Expect(configMap.Namespace).To(Equal(validCFG.ControllerNamespace))
		Expect(configMap.Data).NotTo(BeNil())
		Expect(configMap.Data[controller.VirtualizationModuleEnabledKey]).To(Equal("true"))

		configMap.Data[controller.VirtualizationModuleEnabledKey] = "false"
		err = cl.Update(ctx, configMap)
		Expect(err).NotTo(HaveOccurred())

		configMap, err = getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(configMap).NotTo(BeNil())
		Expect(configMap.Name).To(Equal(controller.ControllerConfigMapName))
		Expect(configMap.Namespace).To(Equal(validCFG.ControllerNamespace))
		Expect(configMap.Data).NotTo(BeNil())
		Expect(configMap.Data[controller.VirtualizationModuleEnabledKey]).To(Equal("false"))

		virtualizationEnabled, err := controller.GetVirtualizationModuleEnabled(ctx, cl, log, request.NamespacedName)
		Expect(err).NotTo(HaveOccurred())
		Expect(virtualizationEnabled).To(BeFalse())

		storageClass, err := getSC(ctx, cl, validStorageClassResource.Name, validStorageClassResource.Namespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(storageClass).NotTo(BeNil())
		Expect(storageClass.Name).To(Equal(validStorageClassResource.Name))
		Expect(storageClass.Namespace).To(Equal(validStorageClassResource.Namespace))
		Expect(storageClass.Annotations).NotTo(BeNil())
		Expect(len(storageClass.Annotations)).To(Equal(2))
		Expect(storageClass.Annotations[controller.DefaultStorageClassAnnotationKey]).To(Equal("true"))
		Expect(storageClass.Annotations[controller.StorageClassVirtualizationAnnotationKey]).To(Equal(controller.StorageClassVirtualizationAnnotationValue))
		Expect(storageClass.Parameters[controller.StorageClassParamAllowRemoteVolumeAccessKey]).To(Equal("false"))

		shouldRequeue, err := controller.ReconcileControllerConfigMapEvent(ctx, cl, log, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		storageClass, err = getSC(ctx, cl, validStorageClassResource.Name, validStorageClassResource.Namespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(storageClass).NotTo(BeNil())
		Expect(storageClass.Name).To(Equal(validStorageClassResource.Name))
		Expect(storageClass.Namespace).To(Equal(validStorageClassResource.Namespace))
		Expect(storageClass.Annotations).NotTo(BeNil())
		Expect(len(storageClass.Annotations)).To(Equal(1))
		Expect(storageClass.Annotations[controller.DefaultStorageClassAnnotationKey]).To(Equal("true"))
		Expect(storageClass.Parameters[controller.StorageClassParamAllowRemoteVolumeAccessKey]).To(Equal("false"))

		// Cleanup
		err = cl.Delete(ctx, storageClass)
		Expect(err).NotTo(HaveOccurred())

		_, err = getSC(ctx, cl, validStorageClassResource.Name, validStorageClassResource.Namespace)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())

		err = cl.Delete(ctx, configMap)
		Expect(err).NotTo(HaveOccurred())

		_, err = getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())
	})

	It("ReconcileControllerConfigMapEvent_ConfigMap_exist_with_virtualization_key_and_virtualization_value_is_true_StorageClass_not_local_without_annotations_exist", func() {
		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: validCFG.ControllerNamespace,
				Name:      controller.ControllerConfigMapName,
			},
		}

		err := cl.Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: request.Namespace,
				Name:      request.Name,
			},
			Data: map[string]string{controller.VirtualizationModuleEnabledKey: "true"},
		})
		Expect(err).NotTo(HaveOccurred())

		configMap, err := getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(configMap).NotTo(BeNil())
		Expect(configMap.Name).To(Equal(controller.ControllerConfigMapName))
		Expect(configMap.Namespace).To(Equal(validCFG.ControllerNamespace))
		Expect(configMap.Data).NotTo(BeNil())
		Expect(configMap.Data[controller.VirtualizationModuleEnabledKey]).To(Equal("true"))

		virtualizationEnabled, err := controller.GetVirtualizationModuleEnabled(ctx, cl, log, request.NamespacedName)
		Expect(err).NotTo(HaveOccurred())
		Expect(virtualizationEnabled).To(BeTrue())

		scResource := validStorageClassResource.DeepCopy()
		scResource.Parameters[controller.StorageClassParamAllowRemoteVolumeAccessKey] = "true"
		err = cl.Create(ctx, scResource)
		Expect(err).NotTo(HaveOccurred())

		storageClass, err := getSC(ctx, cl, scResource.Name, scResource.Namespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(storageClass).NotTo(BeNil())
		Expect(storageClass.Name).To(Equal(scResource.Name))
		Expect(storageClass.Namespace).To(Equal(scResource.Namespace))
		Expect(storageClass.Annotations).To(BeNil())
		Expect(storageClass.Parameters[controller.StorageClassParamAllowRemoteVolumeAccessKey]).To(Equal("true"))

		shouldRequeue, err := controller.ReconcileControllerConfigMapEvent(ctx, cl, log, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		storageClass, err = getSC(ctx, cl, scResource.Name, scResource.Namespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(storageClass).NotTo(BeNil())
		Expect(storageClass.Name).To(Equal(scResource.Name))
		Expect(storageClass.Namespace).To(Equal(scResource.Namespace))
		Expect(storageClass.Annotations).To(BeNil())
		Expect(storageClass.Parameters[controller.StorageClassParamAllowRemoteVolumeAccessKey]).To(Equal("true"))

		// Cleanup
		err = cl.Delete(ctx, storageClass)
		Expect(err).NotTo(HaveOccurred())

		_, err = getSC(ctx, cl, scResource.Name, scResource.Namespace)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())

		err = cl.Delete(ctx, configMap)
		Expect(err).NotTo(HaveOccurred())

		_, err = getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())
	})

	It("ReconcileControllerConfigMapEvent_ConfigMap_exist_with_virtualization_key_and_virtualization_value_is_true_StorageClass_not_local_with_default_annotation_exist", func() {
		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: validCFG.ControllerNamespace,
				Name:      controller.ControllerConfigMapName,
			},
		}

		err := cl.Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: request.Namespace,
				Name:      request.Name,
			},
			Data: map[string]string{controller.VirtualizationModuleEnabledKey: "true"},
		})
		Expect(err).NotTo(HaveOccurred())

		configMap, err := getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(configMap).NotTo(BeNil())
		Expect(configMap.Name).To(Equal(controller.ControllerConfigMapName))
		Expect(configMap.Namespace).To(Equal(validCFG.ControllerNamespace))
		Expect(configMap.Data).NotTo(BeNil())
		Expect(configMap.Data[controller.VirtualizationModuleEnabledKey]).To(Equal("true"))

		virtualizationEnabled, err := controller.GetVirtualizationModuleEnabled(ctx, cl, log, request.NamespacedName)
		Expect(err).NotTo(HaveOccurred())
		Expect(virtualizationEnabled).To(BeTrue())

		scResource := validStorageClassResource.DeepCopy()
		scResource.Annotations = map[string]string{controller.DefaultStorageClassAnnotationKey: "true"}
		scResource.Parameters[controller.StorageClassParamAllowRemoteVolumeAccessKey] = "true"
		err = cl.Create(ctx, scResource)
		Expect(err).NotTo(HaveOccurred())

		storageClass, err := getSC(ctx, cl, scResource.Name, scResource.Namespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(storageClass).NotTo(BeNil())
		Expect(storageClass.Name).To(Equal(scResource.Name))
		Expect(storageClass.Namespace).To(Equal(scResource.Namespace))
		Expect(storageClass.Annotations).NotTo(BeNil())
		Expect(len(storageClass.Annotations)).To(Equal(1))
		Expect(storageClass.Annotations[controller.DefaultStorageClassAnnotationKey]).To(Equal("true"))
		Expect(storageClass.Parameters[controller.StorageClassParamAllowRemoteVolumeAccessKey]).To(Equal("true"))

		shouldRequeue, err := controller.ReconcileControllerConfigMapEvent(ctx, cl, log, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		storageClass, err = getSC(ctx, cl, scResource.Name, scResource.Namespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(storageClass).NotTo(BeNil())
		Expect(storageClass.Name).To(Equal(scResource.Name))
		Expect(storageClass.Namespace).To(Equal(scResource.Namespace))
		Expect(storageClass.Annotations).NotTo(BeNil())
		Expect(len(storageClass.Annotations)).To(Equal(1))
		Expect(storageClass.Annotations[controller.DefaultStorageClassAnnotationKey]).To(Equal("true"))
		Expect(storageClass.Parameters[controller.StorageClassParamAllowRemoteVolumeAccessKey]).To(Equal("true"))

		// Cleanup
		err = cl.Delete(ctx, storageClass)
		Expect(err).NotTo(HaveOccurred())

		_, err = getSC(ctx, cl, scResource.Name, scResource.Namespace)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())

		err = cl.Delete(ctx, configMap)
		Expect(err).NotTo(HaveOccurred())

		_, err = getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())
	})

	It("ReconcileControllerConfigMapEvent_ConfigMap_exist_with_virtualization_key_and_virtualization_value_is_true_StorageClass_not_replicated_but_local_with_default_annotation_exist", func() {
		anotherProvisioner := "another.provisioner"

		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: validCFG.ControllerNamespace,
				Name:      controller.ControllerConfigMapName,
			},
		}

		err := cl.Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: request.Namespace,
				Name:      request.Name,
			},
			Data: map[string]string{controller.VirtualizationModuleEnabledKey: "true"},
		})
		Expect(err).NotTo(HaveOccurred())

		configMap, err := getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(configMap).NotTo(BeNil())
		Expect(configMap.Name).To(Equal(controller.ControllerConfigMapName))
		Expect(configMap.Namespace).To(Equal(validCFG.ControllerNamespace))
		Expect(configMap.Data).NotTo(BeNil())
		Expect(configMap.Data[controller.VirtualizationModuleEnabledKey]).To(Equal("true"))

		virtualizationEnabled, err := controller.GetVirtualizationModuleEnabled(ctx, cl, log, request.NamespacedName)
		Expect(err).NotTo(HaveOccurred())
		Expect(virtualizationEnabled).To(BeTrue())

		scResource := validStorageClassResource.DeepCopy()
		scResource.Annotations = map[string]string{controller.DefaultStorageClassAnnotationKey: "true"}
		scResource.Parameters[controller.StorageClassParamAllowRemoteVolumeAccessKey] = "false"
		scResource.Provisioner = anotherProvisioner
		err = cl.Create(ctx, scResource)
		Expect(err).NotTo(HaveOccurred())

		storageClass, err := getSC(ctx, cl, scResource.Name, scResource.Namespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(storageClass).NotTo(BeNil())
		Expect(storageClass.Name).To(Equal(scResource.Name))
		Expect(storageClass.Namespace).To(Equal(scResource.Namespace))
		Expect(storageClass.Annotations).NotTo(BeNil())
		Expect(len(storageClass.Annotations)).To(Equal(1))
		Expect(storageClass.Annotations[controller.DefaultStorageClassAnnotationKey]).To(Equal("true"))
		Expect(storageClass.Parameters[controller.StorageClassParamAllowRemoteVolumeAccessKey]).To(Equal("false"))
		Expect(storageClass.Provisioner).To(Equal(anotherProvisioner))

		shouldRequeue, err := controller.ReconcileControllerConfigMapEvent(ctx, cl, log, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		storageClass, err = getSC(ctx, cl, scResource.Name, scResource.Namespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(storageClass).NotTo(BeNil())
		Expect(storageClass.Name).To(Equal(scResource.Name))
		Expect(storageClass.Namespace).To(Equal(scResource.Namespace))
		Expect(storageClass.Annotations).NotTo(BeNil())
		Expect(len(storageClass.Annotations)).To(Equal(1))
		Expect(storageClass.Annotations[controller.DefaultStorageClassAnnotationKey]).To(Equal("true"))
		Expect(storageClass.Parameters[controller.StorageClassParamAllowRemoteVolumeAccessKey]).To(Equal("false"))
		Expect(storageClass.Provisioner).To(Equal(anotherProvisioner))

		// Cleanup
		err = cl.Delete(ctx, storageClass)
		Expect(err).NotTo(HaveOccurred())

		_, err = getSC(ctx, cl, scResource.Name, scResource.Namespace)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())

		err = cl.Delete(ctx, configMap)
		Expect(err).NotTo(HaveOccurred())

		_, err = getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())
	})
})
