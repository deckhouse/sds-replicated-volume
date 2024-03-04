/*
Copyright 2023 Flant JSC

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
	"reflect"
	"sds-replicated-volume-controller/api/v1alpha1"
	sdsapi "sds-replicated-volume-controller/api/v1alpha1"
	"sds-replicated-volume-controller/pkg/controller"
	"sds-replicated-volume-controller/pkg/logger"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/strings/slices"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe(controller.ReplicatedStorageClassControllerName, func() {
	const (
		testNameSpace = "test_namespace"
	)

	var (
		ctx = context.Background()
		cl  = newFakeClient()
		log = logger.Logger{}

		validZones                    = []string{"first", "second", "third"}
		validSpecReplicatedSCTemplate = v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: testNameSpace,
			},
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				StoragePool:   "valid",
				ReclaimPolicy: controller.ReclaimPolicyRetain,
				Replication:   controller.ReplicationConsistencyAndAvailability,
				VolumeAccess:  controller.VolumeAccessLocal,
				Topology:      controller.TopologyTransZonal,
				Zones:         validZones,
			},
		}

		invalidValues               = []string{"first", "second"}
		invalidReplicatedSCTemplate = v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: testNameSpace,
			},
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				StoragePool:   "",
				ReclaimPolicy: "",
				Replication:   controller.ReplicationConsistencyAndAvailability,
				VolumeAccess:  controller.VolumeAccessLocal,
				Topology:      controller.TopologyTransZonal,
				Zones:         invalidValues,
			},
		}
	)

	It("GenerateStorageClassFromReplicatedStorageClass_Generates_expected_StorageClass", func() {
		var (
			testName                    = generateTestName()
			allowVolumeExpansion   bool = true
			volumeBindingMode           = storagev1.VolumeBindingMode("WaitForFirstConsumer")
			reclaimPolicy               = v1.PersistentVolumeReclaimPolicy(validSpecReplicatedSCTemplate.Spec.ReclaimPolicy)
			storageClassParameters      = map[string]string{
				controller.StorageClassStoragePoolKey:                     validSpecReplicatedSCTemplate.Spec.StoragePool,
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
			}

			expectedSC = &storagev1.StorageClass{
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

		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName

		actualSC := controller.GenerateStorageClassFromReplicatedStorageClass(&replicatedSC)
		Expect(actualSC).To(Equal(expectedSC))
	})

	It("GetStorageClass_Returns_storage_class_and_no_error", func() {
		testName := generateTestName()
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		storageClass := controller.GenerateStorageClassFromReplicatedStorageClass(&replicatedSC)

		err := cl.Create(ctx, storageClass)
		if err == nil {
			defer func() {
				if err = cl.Delete(ctx, storageClass); err != nil && !errors.IsNotFound(err) {
					fmt.Println(err.Error())
				}
			}()
		}
		Expect(err).NotTo(HaveOccurred())

		sc, err := controller.GetStorageClass(ctx, cl, testNameSpace, testName)
		Expect(err).NotTo(HaveOccurred())

		if Expect(sc).NotTo(BeNil()) {
			Expect(sc.Name).To(Equal(testName))
			Expect(sc.Namespace).To(Equal(testNameSpace))
		}
	})

	It("DeleteStorageClass_Deletes_needed_one_Returns_no_error", func() {
		testName := generateTestName()
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		storageClass := controller.GenerateStorageClassFromReplicatedStorageClass(&replicatedSC)

		err := cl.Create(ctx, storageClass)
		if err == nil {
			defer func() {
				if err = cl.Delete(ctx, storageClass); err != nil && !errors.IsNotFound(err) {
					fmt.Println(err.Error())
				}
			}()
		}
		Expect(err).NotTo(HaveOccurred())

		obj := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Name:      testName,
			Namespace: testNameSpace,
		}, obj)
		Expect(err).NotTo(HaveOccurred())
		Expect(obj.Name).To(Equal(testName))
		Expect(obj.Namespace).To(Equal(testNameSpace))

		err = controller.DeleteStorageClass(ctx, cl, testNameSpace, testName)
		Expect(err).NotTo(HaveOccurred())

		_, err = controller.GetStorageClass(ctx, cl, testName, testNameSpace)
		Expect(err).NotTo(BeNil())
		Expect(errors.IsNotFound(err)).To(BeTrue())
	})

	It("CreateStorageClass_Creates_one_Returns_no_error", func() {
		testName := generateTestName()
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		err := controller.CreateStorageClass(ctx, cl, &replicatedSC)
		if err == nil {
			defer func() {
				if err = controller.DeleteStorageClass(ctx, cl, testNameSpace, testName); err != nil {
					fmt.Println(err.Error())
				}
			}()
		}
		Expect(err).NotTo(HaveOccurred())

		sc, err := controller.GetStorageClass(ctx, cl, testNameSpace, testName)
		Expect(err).NotTo(HaveOccurred())
		if Expect(sc).NotTo(BeNil()) {
			Expect(sc.Name).To(Equal(testName))
			Expect(sc.Namespace).To(Equal(testNameSpace))
		}
	})

	It("UpdateReplicatedStorageClass_Updates_resource", func() {
		testName := generateTestName()
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		replicatedSC.Status.Phase = controller.Created

		err := cl.Create(ctx, &replicatedSC)
		if err == nil {
			defer func() {
				if err = cl.Delete(ctx, &replicatedSC); err != nil && !errors.IsNotFound(err) {
					fmt.Println(err.Error())
				}
			}()
		}
		Expect(err).NotTo(HaveOccurred())

		resources, err := getTestAPIStorageClasses(ctx, cl)
		Expect(err).NotTo(HaveOccurred())

		oldResource := resources[testName]
		Expect(oldResource.Name).To(Equal(testName))
		Expect(oldResource.Namespace).To(Equal(testNameSpace))
		Expect(oldResource.Status.Phase).To(Equal(controller.Created))

		oldResource.Status.Phase = controller.Failed
		updatedMessage := "new message"
		oldResource.Status.Reason = updatedMessage

		err = controller.UpdateReplicatedStorageClass(ctx, cl, &oldResource)
		Expect(err).NotTo(HaveOccurred())

		resources, err = getTestAPIStorageClasses(ctx, cl)
		Expect(err).NotTo(HaveOccurred())

		updatedResource := resources[testName]
		Expect(updatedResource.Name).To(Equal(testName))
		Expect(updatedResource.Namespace).To(Equal(testNameSpace))
		Expect(updatedResource.Status.Phase).To(Equal(controller.Failed))
		Expect(updatedResource.Status.Reason).To(Equal(updatedMessage))
	})

	It("RemoveString_removes_correct_one", func() {
		strs := [][]string{
			{
				"first", "second",
			},
			{
				"first",
			},
		}

		expected := [][]string{
			{"first"},
			{"first"},
		}

		strToRemove := "second"

		for variant := range strs {
			result := controller.RemoveString(strs[variant], strToRemove)
			Expect(result).To(Equal(expected[variant]))
		}
	})

	It("ReconcileReplicatedStorageClassEvent_Resource_exists_DeletionTimestamp_not_nil_Status_created_StorageClass_is_absent_Deletes_Resource_Successfully", func() {
		testName := generateTestName()
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		replicatedSC.Finalizers = []string{controller.ReplicatedStorageClassFinalizerName}
		replicatedSC.Status.Phase = controller.Created

		req := reconcile.Request{NamespacedName: types.NamespacedName{
			Namespace: testNameSpace,
			Name:      testName,
		}}

		err := cl.Create(ctx, &replicatedSC)
		if err == nil {
			defer func() {
				if err := cl.Delete(ctx, &replicatedSC); err != nil && !errors.IsNotFound(err) {
					fmt.Println(err)
				}
			}()
		}
		Expect(err).NotTo(HaveOccurred())

		err = cl.Delete(ctx, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		requeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, req, log)
		Expect(err).NotTo(HaveOccurred())
		Expect(requeue).To(BeFalse())

		resources, err := getTestAPIStorageClasses(ctx, cl)
		Expect(err).NotTo(HaveOccurred())
		Expect(reflect.ValueOf(resources[testName]).IsZero()).To(BeTrue())
	})

	It("ReconcileReplicatedStorageClassEvent_Resource_exists_DeletionTimestamp_not_nil_Status_created_StorageClass_exists_Deletes_resource_and_storage_class_successfully", func() {
		testName := generateTestName()
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		replicatedSC.Finalizers = []string{controller.ReplicatedStorageClassFinalizerName}
		replicatedSC.Status.Phase = controller.Created

		req := reconcile.Request{NamespacedName: types.NamespacedName{
			Namespace: testNameSpace,
			Name:      testName,
		}}

		err := cl.Create(ctx, &replicatedSC)
		if err == nil {
			defer func() {
				if err := cl.Delete(ctx, &replicatedSC); err != nil && !errors.IsNotFound(err) {
					fmt.Println(err)
				}
			}()
		}
		Expect(err).NotTo(HaveOccurred())

		err = controller.CreateStorageClass(ctx, cl, &replicatedSC)
		if err == nil {
			defer func() {
				if err = controller.DeleteStorageClass(ctx, cl, testNameSpace, testName); err != nil && !errors.IsNotFound(err) {
					fmt.Println(err.Error())
				}
			}()
		}
		Expect(err).NotTo(HaveOccurred())

		err = cl.Delete(ctx, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		requeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, req, log)
		Expect(err).NotTo(HaveOccurred())
		Expect(requeue).To(BeFalse())

		resources, err := getTestAPIStorageClasses(ctx, cl)
		Expect(err).NotTo(HaveOccurred())
		Expect(reflect.ValueOf(resources[testName]).IsZero()).To(BeTrue())

		_, err = controller.GetStorageClass(ctx, cl, testNameSpace, testName)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())
	})

	It("ReconcileReplicatedStorageClassEvent_Resource_exists_DeletionTimestamp_not_nil_Status_failed_StorageClass_exists_Does_NOT_delete_StorageClass_Deletes_resource", func() {
		testName := generateTestName()
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		replicatedSC.Finalizers = []string{controller.ReplicatedStorageClassFinalizerName}
		replicatedSC.Status.Phase = controller.Failed

		req := reconcile.Request{NamespacedName: types.NamespacedName{
			Namespace: testNameSpace,
			Name:      testName,
		}}

		err := cl.Create(ctx, &replicatedSC)
		if err == nil {
			defer func() {
				if err := cl.Delete(ctx, &replicatedSC); err != nil && !errors.IsNotFound(err) {
					fmt.Println(err.Error())
				}
			}()
		}

		err = controller.CreateStorageClass(ctx, cl, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		err = cl.Delete(ctx, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		requeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, req, log)
		Expect(err).NotTo(HaveOccurred())
		Expect(requeue).To(BeFalse())

		storageClass, err := controller.GetStorageClass(ctx, cl, testNameSpace, testName)
		Expect(err).NotTo(HaveOccurred())

		if Expect(storageClass).NotTo(BeNil()) {
			Expect(storageClass.Name).To(Equal(testName))
			Expect(storageClass.Namespace).To(Equal(testNameSpace))
		}

		resources, err := getTestAPIStorageClasses(ctx, cl)
		Expect(err).NotTo(HaveOccurred())

		Expect(reflect.ValueOf(resources[testName]).IsZero()).To(BeTrue())
	})

	It("ReconcileReplicatedStorageClassEvent_Resource_exists_DeletionTimestamp_is_nil_returns_false_no_error_Doesnt_delete_resource", func() {
		testName := generateTestName()
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		replicatedSC.Status.Phase = controller.Created

		req := reconcile.Request{NamespacedName: types.NamespacedName{
			Namespace: testNameSpace,
			Name:      testName,
		}}

		err := cl.Create(ctx, &replicatedSC)
		if err == nil {
			defer func() {
				if err := cl.Delete(ctx, &replicatedSC); err != nil && !errors.IsNotFound(err) {
					fmt.Println(err.Error())
				}
			}()
		}

		requeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, req, log)
		Expect(err).NotTo(HaveOccurred())
		Expect(requeue).To(BeFalse())

		resources, err := getTestAPIStorageClasses(ctx, cl)
		Expect(err).NotTo(HaveOccurred())

		Expect(resources[testName].Name).To(Equal(testName))
		Expect(resources[testName].Namespace).To(Equal(testNameSpace))
	})

	It("ReconcileReplicatedStorageClassEvent_Resource_does_not_exist_Returns_false_no_error", func() {
		testName := generateTestName()
		req := reconcile.Request{NamespacedName: types.NamespacedName{
			Namespace: testNameSpace,
			Name:      testName,
		}}

		resources, err := getTestAPIStorageClasses(ctx, cl)
		Expect(err).NotTo(HaveOccurred())
		Expect(reflect.ValueOf(resources[testName]).IsZero()).To(BeTrue())

		requeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, req, log)
		Expect(err).NotTo(HaveOccurred())
		Expect(requeue).To(BeFalse())
	})

	It("ValidateReplicatedStorageClass_Incorrect_spec_Returns_false_and_messages", func() {
		testName := generateTestName()
		replicatedSC := invalidReplicatedSCTemplate
		replicatedSC.Name = testName
		zones := map[string]struct{}{
			"first": {},
		}

		validation, mes := controller.ValidateReplicatedStorageClass(ctx, cl, &replicatedSC, zones)
		Expect(validation).Should(BeFalse())
		Expect(mes).To(Equal("Validation of ReplicatedStorageClass failed: StoragePool is empty; ReclaimPolicy is empty; Selected unacceptable amount of zones for replication type: ConsistencyAndAvailability; correct number of zones should be 3; "))
	})

	It("ValidateReplicatedStorageClass_new_replicatedSC_is_default_default_replicatedSC_is_already_exist_validation_failed", func() {
		testName := generateTestName()
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		replicatedSC.Spec.IsDefault = true

		err := cl.Create(ctx, &replicatedSC)
		if Expect(err).NotTo(HaveOccurred()) {
			defer func() {
				err = cl.Delete(ctx, &replicatedSC)
				if err != nil {
					fmt.Println(err.Error())
				}
			}()
		}

		newReplicatedSCName := generateTestName()
		dnewReplicatedSC := validSpecReplicatedSCTemplate
		dnewReplicatedSC.Name = newReplicatedSCName
		dnewReplicatedSC.Spec.IsDefault = true
		zones := map[string]struct{}{
			"first":  {},
			"second": {},
			"third":  {},
		}

		validation, msg := controller.ValidateReplicatedStorageClass(ctx, cl, &dnewReplicatedSC, zones)
		Expect(validation).Should(BeFalse())
		Expect(msg).To(Equal(fmt.Sprintf("Validation of ReplicatedStorageClass failed: Conflict with other default ReplicatedStorageClasses: %s; StorageClasses: ", replicatedSC.Name)))
	})

	It("ValidateReplicatedStorageClass_new_replicatedSC_is_default_default_replicatedSC_with_same_name_is_already_exist_validation_passed", func() {
		testName := generateTestName()
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		replicatedSC.Spec.IsDefault = true

		err := cl.Create(ctx, &replicatedSC)
		if Expect(err).NotTo(HaveOccurred()) {
			defer func() {
				err = cl.Delete(ctx, &replicatedSC)
				if err != nil {
					fmt.Println(err.Error())
				}
			}()
		}

		newReplicatedSCName := testName
		dnewReplicatedSC := validSpecReplicatedSCTemplate
		dnewReplicatedSC.Name = newReplicatedSCName
		dnewReplicatedSC.Spec.IsDefault = true
		zones := map[string]struct{}{
			"first":  {},
			"second": {},
			"third":  {},
		}

		validation, _ := controller.ValidateReplicatedStorageClass(ctx, cl, &dnewReplicatedSC, zones)
		Expect(validation).Should(BeTrue())
	})

	It("ValidateReplicatedStorageClass_new_replicatedSC_is_default_default_sc_is_already_exist_validation_failed", func() {
		sc := storagev1.StorageClass{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name: generateTestName(),
				Annotations: map[string]string{
					controller.DefaultStorageClassAnnotationKey: "true",
				},
			},
		}

		err := cl.Create(ctx, &sc)
		if Expect(err).NotTo(HaveOccurred()) {
			defer func() {
				err = cl.Delete(ctx, &sc)
				if err != nil {
					fmt.Println(err.Error())
				}
			}()
		}

		dnewReplicatedSC := validSpecReplicatedSCTemplate
		dnewReplicatedSC.Name = generateTestName()
		dnewReplicatedSC.Spec.IsDefault = true
		zones := map[string]struct{}{
			"first":  {},
			"second": {},
			"third":  {},
		}

		validation, msg := controller.ValidateReplicatedStorageClass(ctx, cl, &dnewReplicatedSC, zones)
		Expect(validation).Should(BeFalse())
		Expect(msg).To(Equal(fmt.Sprintf("Validation of ReplicatedStorageClass failed: Conflict with other default ReplicatedStorageClasses: ; StorageClasses: %s", sc.Name)))
	})

	It("ValidateReplicatedStorageClass_new_replicatedSC_is_default_default_sc_with_same_name_is_already_exist_validation_passed", func() {
		testName := generateTestName()
		sc := storagev1.StorageClass{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name: testName,
				Annotations: map[string]string{
					controller.DefaultStorageClassAnnotationKey: "true",
				},
			},
		}

		err := cl.Create(ctx, &sc)
		if Expect(err).NotTo(HaveOccurred()) {
			defer func() {
				err = cl.Delete(ctx, &sc)
				if err != nil {
					fmt.Println(err.Error())
				}
			}()
		}

		dnewReplicatedSC := validSpecReplicatedSCTemplate
		dnewReplicatedSC.Name = testName
		dnewReplicatedSC.Spec.IsDefault = true
		zones := map[string]struct{}{
			"first":  {},
			"second": {},
			"third":  {},
		}

		validation, _ := controller.ValidateReplicatedStorageClass(ctx, cl, &dnewReplicatedSC, zones)
		Expect(validation).Should(BeTrue())
	})

	It("ValidateReplicatedStorageClass_Correct_spec_Returns_true", func() {
		testName := generateTestName()
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		zones := map[string]struct{}{
			"first":  {},
			"second": {},
			"third":  {},
		}

		validation, _ := controller.ValidateReplicatedStorageClass(ctx, cl, &replicatedSC, zones)
		Expect(validation).Should(BeTrue())
	})

	It("GetClusterZones_nodes_in_zones_returns_correct_zones", func() {
		const (
			testZone = "zone1"
		)
		nodeInZone := v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "nodeInZone",
				Labels: map[string]string{controller.ZoneLabel: testZone},
			},
		}

		nodeNotInZone := v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "nodeNotInZone",
				Labels: map[string]string{"custom_label": ""},
			},
		}

		err := cl.Create(ctx, &nodeInZone)
		if err == nil {
			defer func() {
				if err := cl.Delete(ctx, &nodeInZone); err != nil && !errors.IsNotFound(err) {
					fmt.Println(err)
				}
			}()
		}
		Expect(err).NotTo(HaveOccurred())

		err = cl.Create(ctx, &nodeNotInZone)
		if err == nil {
			defer func() {
				if err := cl.Delete(ctx, &nodeNotInZone); err != nil && !errors.IsNotFound(err) {
					fmt.Println(err)
				}
			}()
		}
		Expect(err).NotTo(HaveOccurred())

		expectedZones := map[string]struct{}{
			testZone: {},
		}

		zones, err := controller.GetClusterZones(ctx, cl)
		Expect(err).NotTo(HaveOccurred())
		Expect(zones).To(Equal(expectedZones))
	})

	It("GetClusterZones_nodes_NOT_in_zones_returns_correct_zones", func() {
		nodeNotInZone1 := v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "nodeNotInZone1",
				Labels: map[string]string{"cus_lbl": "something"},
			},
		}

		nodeNotInZone2 := v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "nodeNotInZone2",
				Labels: map[string]string{"custom_label": ""},
			},
		}

		err := cl.Create(ctx, &nodeNotInZone1)
		if err == nil {
			defer func() {
				if err := cl.Delete(ctx, &nodeNotInZone1); err != nil && !errors.IsNotFound(err) {
					fmt.Println(err)
				}
			}()
		}
		Expect(err).NotTo(HaveOccurred())

		err = cl.Create(ctx, &nodeNotInZone2)
		if err == nil {
			defer func() {
				if err := cl.Delete(ctx, &nodeNotInZone2); err != nil && !errors.IsNotFound(err) {
					fmt.Println(err)
				}
			}()
		}
		Expect(err).NotTo(HaveOccurred())

		zones, err := controller.GetClusterZones(ctx, cl)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(zones)).To(Equal(0))
	})

	It("ReconcileReplicatedStorageClass_Validation_failed_Updates_status_to_failed_and_reason", func() {
		testName := generateTestName()
		replicatedSC := invalidReplicatedSCTemplate
		replicatedSC.Name = testName
		failedMessage := "Validation of ReplicatedStorageClass failed: StoragePool is empty; ReclaimPolicy is empty; Selected unacceptable amount of zones for replication type: ConsistencyAndAvailability; correct number of zones should be 3; "
		err := cl.Create(ctx, &replicatedSC)
		if err == nil {
			defer func() {
				if err := cl.Delete(ctx, &replicatedSC); err != nil && !errors.IsNotFound(err) {
					fmt.Println(err)
				}
			}()
		}
		Expect(err).NotTo(HaveOccurred())

		err = controller.ReconcileReplicatedStorageClass(ctx, cl, log, &replicatedSC)
		Expect(err).To(HaveOccurred())
		Expect(replicatedSC.Status.Phase).To(Equal(controller.Failed))
		Expect(replicatedSC.Status.Reason).To(Equal(failedMessage))

		resources, err := getTestAPIStorageClasses(ctx, cl)
		Expect(err).NotTo(HaveOccurred())

		resource := resources[testName]
		Expect(resource.Status.Phase).To(Equal(controller.Failed))
		Expect(resource.Status.Reason).To(Equal(failedMessage))
	})

	It("ReconcileReplicatedStorageClass_Validation_passed_StorageClass_not_found_Creates_one_Adds_finalizers_and_Returns_no_error", func() {
		testName := generateTestName()
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		replicatedSC.Finalizers = nil

		err := cl.Create(ctx, &replicatedSC)
		if err == nil {
			defer func() {
				if err := cl.Delete(ctx, &replicatedSC); err != nil && !errors.IsNotFound(err) {
					fmt.Println(err)
				}
			}()
		}
		Expect(err).NotTo(HaveOccurred())

		storageClass, err := controller.GetStorageClass(ctx, cl, testNameSpace, testName)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())
		Expect(storageClass).To(BeNil())

		err = controller.ReconcileReplicatedStorageClass(ctx, cl, log, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		resources, err := getTestAPIStorageClasses(ctx, cl)
		Expect(err).NotTo(HaveOccurred())

		resource := resources[testName]

		Expect(resource.Status.Phase).To(Equal(controller.Created))
		Expect(resource.Status.Reason).To(Equal("ReplicatedStorageClass and StorageClass are equal."))

		Expect(slices.Contains(resource.Finalizers, controller.ReplicatedStorageClassFinalizerName)).To(BeTrue())

		storageClass, err = controller.GetStorageClass(ctx, cl, testNameSpace, testName)
		Expect(err).NotTo(HaveOccurred())

		if Expect(storageClass).NotTo(BeNil()) {
			Expect(storageClass.Name).To(Equal(testName))
			Expect(storageClass.Namespace).To(Equal(testNameSpace))
		}
	})

	It("ReconcileReplicatedStorageClass_Validation_passed_StorageClass_founded_Resource_and_StorageClass_ARE_EQUAL_Resource.Status.Phase_equals_Created", func() {
		testName := generateTestName()
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		err := cl.Create(ctx, &replicatedSC)
		if err == nil {
			defer func() {
				if err := cl.Delete(ctx, &replicatedSC); err != nil {
					fmt.Println(err)
				}
			}()
		}
		Expect(err).NotTo(HaveOccurred())

		err = controller.CreateStorageClass(ctx, cl, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		err = controller.ReconcileReplicatedStorageClass(ctx, cl, log, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		resources, err := getTestAPIStorageClasses(ctx, cl)
		Expect(err).NotTo(HaveOccurred())

		resource := resources[testName]
		Expect(resource.Status.Phase).To(Equal(controller.Created))
		Expect(resource.Status.Reason).To(Equal("ReplicatedStorageClass and StorageClass are equal."))

		resFinalizers := strings.Join(resource.Finalizers, "")
		Expect(strings.Contains(resFinalizers, controller.ReplicatedStorageClassFinalizerName))

		storageClass, err := controller.GetStorageClass(ctx, cl, testNameSpace, testName)
		Expect(err).NotTo(HaveOccurred())

		if Expect(storageClass).NotTo(BeNil()) {
			Expect(storageClass.Name).To(Equal(testName))
			Expect(storageClass.Namespace).To(Equal(testNameSpace))
		}
	})

	It("ReconcileReplicatedStorageClass_Validation_passed_StorageClass_founded_Resource_and_StorageClass_ARE_NOT_EQUAL_Updates_resource_status_to_failed_and_reason", func() {
		testName := generateTestName()
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		replicatedSC.Status.Phase = controller.Created

		anotherReplicatedSC := validSpecReplicatedSCTemplate
		anotherReplicatedSC.Spec.ReclaimPolicy = "not-equal"
		anotherReplicatedSC.Name = testName

		err := cl.Create(ctx, &replicatedSC)
		if err == nil {
			defer func() {
				if err := cl.Delete(ctx, &replicatedSC); err != nil {
					fmt.Println(err)
				}
			}()
		}
		Expect(err).NotTo(HaveOccurred())

		err = controller.CreateStorageClass(ctx, cl, &anotherReplicatedSC)
		Expect(err).NotTo(HaveOccurred())

		err = controller.ReconcileReplicatedStorageClass(ctx, cl, log, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		resources, err := getTestAPIStorageClasses(ctx, cl)
		Expect(err).NotTo(HaveOccurred())

		resource := resources[testName]
		Expect(resource.Status.Phase).To(Equal(controller.Failed))

		storageClass, err := controller.GetStorageClass(ctx, cl, testNameSpace, testName)
		Expect(err).NotTo(HaveOccurred())

		if Expect(storageClass).NotTo(BeNil()) {
			Expect(storageClass.Name).To(Equal(testName))
			Expect(storageClass.Namespace).To(Equal(testNameSpace))
		}
	})

	It("CompareReplicatedStorageClassAndStorageClass_Resource_and_StorageClass_ARE_equal_Returns_true", func() {
		testName := generateTestName()
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		replicatedSC.Status.Phase = controller.Created
		storageClass := controller.GenerateStorageClassFromReplicatedStorageClass(&replicatedSC)

		equal, _ := controller.CompareReplicatedStorageClassAndStorageClass(&replicatedSC, storageClass)
		Expect(equal).To(BeTrue())
	})

	It("CompareReplicatedStorageClassAndStorageClass_Resource_and_StorageClass_ARE_NOT_equal_Returns_false_and_message", func() {
		var (
			diffRecPolicy v1.PersistentVolumeReclaimPolicy = "not-equal"
			diffVBM       storagev1.VolumeBindingMode      = "not-equal"
		)

		testName := generateTestName()
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		storageClass := &storagev1.StorageClass{
			Provisioner:       "not-equal",
			Parameters:        map[string]string{"not": "equal"},
			ReclaimPolicy:     &diffRecPolicy,
			VolumeBindingMode: &diffVBM,
		}

		equal, message := controller.CompareReplicatedStorageClassAndStorageClass(&replicatedSC, storageClass)
		Expect(equal).To(BeFalse())
		Expect(message).To(Equal("ReplicatedStorageClass and StorageClass are not equal: Parameters are not equal; Provisioner are not equal(ReplicatedStorageClass: linstor.csi.linbit.com, StorageClass: not-equal); ReclaimPolicy are not equal(ReplicatedStorageClass: Retain, StorageClass: not-equalVolumeBindingMode are not equal(ReplicatedStorageClass: WaitForFirstConsumer, StorageClass: not-equal); "))
	})

	It("LabelNodes_set_labels", func() {
		testName := generateTestName()
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		err := cl.Create(ctx, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		node := &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "node-1",
				Namespace: testNameSpace,
				Labels:    map[string]string{controller.ZoneLabel: "first"},
			},
		}

		err = cl.Create(ctx, node)
		if err == nil {
			defer func() {
				if err = cl.Delete(ctx, node); err != nil && !errors.IsNotFound(err) {
					fmt.Println(err.Error())
				}
			}()
		}

		// storageClassLabelKey := fmt.Sprintf("%s/%s", controller.StorageClassLabelKeyPrefix, replicatedSC.Name)
		// err = controller.LabelNodes(ctx, cl, storageClassLabelKey, replicatedSC.Spec.Zones)
		// Expect(err).NotTo(HaveOccurred())
		drbdNodeSelector := map[string]string{controller.DRBDNodeSelectorKey: ""}

		replicatedStorageClasses := sdsapi.ReplicatedStorageClassList{}
		err = cl.List(ctx, &replicatedStorageClasses)
		Expect(err).NotTo(HaveOccurred())

		err = controller.ReconcileKubernetesNodeLabels(ctx, cl, log, *node, replicatedStorageClasses, drbdNodeSelector, true)
		Expect(err).NotTo(HaveOccurred())

		updatedNode := &v1.Node{}
		err = cl.Get(ctx, client.ObjectKey{
			Name:      "node-1",
			Namespace: testNameSpace,
		}, updatedNode)
		Expect(err).NotTo(HaveOccurred())

		_, exist := updatedNode.Labels[fmt.Sprintf("class.storage.deckhouse.io/%s", replicatedSC.Name)]
		Expect(exist).To(BeTrue())
	})
})
