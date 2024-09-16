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
	"slices"
	"strings"

	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"sds-replicated-volume-controller/config"
	"sds-replicated-volume-controller/pkg/controller"
	"sds-replicated-volume-controller/pkg/logger"
)

var _ = Describe(controller.ReplicatedStorageClassControllerName, func() {

	var (
		ctx         = context.Background()
		cl          = newFakeClient()
		log         = logger.Logger{}
		validCFG, _ = config.NewConfig()

		validZones                    = []string{"first", "second", "third"}
		validSpecReplicatedSCTemplate = srv.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: testNamespaceConst,
			},
			Spec: srv.ReplicatedStorageClassSpec{
				StoragePool:   "valid",
				ReclaimPolicy: controller.ReclaimPolicyRetain,
				Replication:   controller.ReplicationConsistencyAndAvailability,
				VolumeAccess:  controller.VolumeAccessLocal,
				Topology:      controller.TopologyTransZonal,
				Zones:         validZones,
			},
		}

		invalidValues               = []string{"first", "second"}
		invalidReplicatedSCTemplate = srv.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: testNamespaceConst,
			},
			Spec: srv.ReplicatedStorageClassSpec{
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
			volumeBindingMode           = storagev1.VolumeBindingWaitForFirstConsumer
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
				controller.QuorumMinimumRedundancyWithPrefixSCKey:         "2",
			}

			expectedSC = &storagev1.StorageClass{
				TypeMeta: metav1.TypeMeta{
					Kind:       controller.StorageClassKind,
					APIVersion: controller.StorageClassAPIVersion,
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:            testName,
					Namespace:       testNamespaceConst,
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

		virtualizationEnabled := false
		actualSC := controller.GetNewStorageClass(&replicatedSC, virtualizationEnabled)
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

		sc, err := controller.GetStorageClass(ctx, cl, testNamespaceConst, testName)
		Expect(err).NotTo(HaveOccurred())
		Expect(sc).NotTo(BeNil())
		Expect(sc.Name).To(Equal(testName))
		Expect(sc.Namespace).To(Equal(testNamespaceConst))
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
			Namespace: testNamespaceConst,
		}, obj)
		Expect(err).NotTo(HaveOccurred())
		Expect(obj.Name).To(Equal(testName))
		Expect(obj.Namespace).To(Equal(testNamespaceConst))

		err = controller.DeleteStorageClass(ctx, cl, storageClass)
		Expect(err).NotTo(HaveOccurred())

		sc, err := controller.GetStorageClass(ctx, cl, testName, testNamespaceConst)
		Expect(err).NotTo(BeNil())
		Expect(errors.IsNotFound(err)).To(BeTrue())
		Expect(sc).To(BeNil())
	})

	It("CreateStorageClass_Creates_one_Returns_no_error", func() {
		testName := generateTestName()
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		virtualizationEnabled := false
		sc := controller.GetNewStorageClass(&replicatedSC, virtualizationEnabled)
		err := controller.CreateStorageClass(ctx, cl, sc)
		if err == nil {
			defer func() {
				if err = controller.DeleteStorageClass(ctx, cl, sc); err != nil {
					fmt.Println(err.Error())
				}
			}()
		}
		Expect(err).NotTo(HaveOccurred())

		sc, err = controller.GetStorageClass(ctx, cl, testNamespaceConst, testName)
		Expect(err).NotTo(HaveOccurred())
		Expect(sc).NotTo(BeNil())
		Expect(sc.Name).To(Equal(testName))
		Expect(sc.Namespace).To(Equal(testNamespaceConst))
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
		Expect(oldResource.Namespace).To(Equal(testNamespaceConst))
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
		Expect(updatedResource.Namespace).To(Equal(testNamespaceConst))
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

		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: testNamespaceConst,
				Name:      testName,
			},
		}

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

		replicatedSCafterDelete := srv.ReplicatedStorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Name:      testName,
			Namespace: testNamespaceConst,
		}, &replicatedSCafterDelete)
		Expect(err).NotTo(HaveOccurred())
		Expect(replicatedSCafterDelete.Name).To(Equal(testName))
		Expect(replicatedSCafterDelete.Finalizers).To(ContainElement(controller.ReplicatedStorageClassFinalizerName))
		Expect(replicatedSCafterDelete.ObjectMeta.DeletionTimestamp).NotTo(BeNil())

		requeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
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

		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: testNamespaceConst,
				Name:      testName,
			},
		}

		err := cl.Create(ctx, &replicatedSC)
		if err == nil {
			defer func() {
				if err := cl.Delete(ctx, &replicatedSC); err != nil && !errors.IsNotFound(err) {
					fmt.Println(err)
				}
			}()
		}
		Expect(err).NotTo(HaveOccurred())

		virtualizationEnabled := false
		scTemplate := controller.GetNewStorageClass(&replicatedSC, virtualizationEnabled)
		err = controller.CreateStorageClass(ctx, cl, scTemplate)
		if err == nil {
			defer func() {
				if err = controller.DeleteStorageClass(ctx, cl, scTemplate); err != nil && !errors.IsNotFound(err) {
					fmt.Println(err.Error())
				}
			}()
		}
		Expect(err).NotTo(HaveOccurred())

		err = cl.Delete(ctx, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		replicatedSCafterDelete := srv.ReplicatedStorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Name:      testName,
			Namespace: testNamespaceConst,
		}, &replicatedSCafterDelete)
		Expect(err).NotTo(HaveOccurred())
		Expect(replicatedSCafterDelete.Name).To(Equal(testName))
		Expect(replicatedSCafterDelete.Finalizers).To(ContainElement(controller.ReplicatedStorageClassFinalizerName))
		Expect(replicatedSCafterDelete.ObjectMeta.DeletionTimestamp).NotTo(BeNil())

		requeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(requeue).To(BeFalse())

		resources, err := getTestAPIStorageClasses(ctx, cl)
		Expect(err).NotTo(HaveOccurred())
		Expect(reflect.ValueOf(resources[testName]).IsZero()).To(BeTrue())

		sc, err := controller.GetStorageClass(ctx, cl, testNamespaceConst, testName)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())
		Expect(sc).To(BeNil())
	})

	It("ReconcileReplicatedStorageClassEvent_Resource_exists_DeletionTimestamp_not_nil_Status_failed_StorageClass_exists_Does_NOT_delete_StorageClass_Deletes_resource", func() {
		testName := generateTestName()
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		replicatedSC.Finalizers = []string{controller.ReplicatedStorageClassFinalizerName}
		replicatedSC.Status.Phase = controller.Failed

		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: testNamespaceConst,
				Name:      testName,
			},
		}

		err := cl.Create(ctx, &replicatedSC)
		if err == nil {
			defer func() {
				if err := cl.Delete(ctx, &replicatedSC); err != nil && !errors.IsNotFound(err) {
					fmt.Println(err.Error())
				}
			}()
		}

		virtualizationEnabled := false
		sc := controller.GetNewStorageClass(&replicatedSC, virtualizationEnabled)
		err = controller.CreateStorageClass(ctx, cl, sc)
		Expect(err).NotTo(HaveOccurred())

		err = cl.Delete(ctx, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		replicatedSCafterDelete := srv.ReplicatedStorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Name:      testName,
			Namespace: testNamespaceConst,
		}, &replicatedSCafterDelete)
		Expect(err).NotTo(HaveOccurred())
		Expect(replicatedSCafterDelete.Name).To(Equal(testName))
		Expect(replicatedSCafterDelete.Finalizers).To(ContainElement(controller.ReplicatedStorageClassFinalizerName))
		Expect(replicatedSCafterDelete.ObjectMeta.DeletionTimestamp).NotTo(BeNil())

		requeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(requeue).To(BeFalse())

		storageClass, err := controller.GetStorageClass(ctx, cl, testNamespaceConst, testName)
		Expect(err).NotTo(HaveOccurred())
		Expect(storageClass).NotTo(BeNil())
		Expect(storageClass.Name).To(Equal(testName))
		Expect(storageClass.Namespace).To(Equal(testNamespaceConst))

		resources, err := getTestAPIStorageClasses(ctx, cl)
		Expect(err).NotTo(HaveOccurred())

		Expect(reflect.ValueOf(resources[testName]).IsZero()).To(BeTrue())
	})

	It("ReconcileReplicatedStorageClassEvent_Resource_exists_DeletionTimestamp_is_nil_returns_false_no_error_Doesnt_delete_resource", func() {
		testName := generateTestName()
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		replicatedSC.Status.Phase = controller.Created

		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: testNamespaceConst,
				Name:      testName,
			},
		}

		err := cl.Create(ctx, &replicatedSC)
		if err == nil {
			defer func() {
				if err := cl.Delete(ctx, &replicatedSC); err != nil && !errors.IsNotFound(err) {
					fmt.Println(err.Error())
				}
			}()
		}

		requeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(requeue).To(BeFalse())

		resources, err := getTestAPIStorageClasses(ctx, cl)
		Expect(err).NotTo(HaveOccurred())

		Expect(resources[testName].Name).To(Equal(testName))
		Expect(resources[testName].Namespace).To(Equal(testNamespaceConst))
	})

	It("ReconcileReplicatedStorageClassEvent_Resource_does_not_exist_Returns_false_no_error", func() {
		testName := generateTestName()
		req := reconcile.Request{NamespacedName: types.NamespacedName{
			Namespace: testNamespaceConst,
			Name:      testName,
		}}

		_, err := controller.GetReplicatedStorageClass(ctx, cl, req.Namespace, req.Name)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())

		resources, err := getTestAPIStorageClasses(ctx, cl)
		Expect(err).NotTo(HaveOccurred())
		Expect(reflect.ValueOf(resources[testName]).IsZero()).To(BeTrue())
	})

	It("ValidateReplicatedStorageClass_Incorrect_spec_Returns_false_and_messages", func() {
		testName := generateTestName()
		replicatedSC := invalidReplicatedSCTemplate
		replicatedSC.Name = testName
		zones := map[string]struct{}{
			"first": {},
		}

		validation, mes := controller.ValidateReplicatedStorageClass(&replicatedSC, zones)
		Expect(validation).Should(BeFalse())
		Expect(mes).To(Equal("Validation of ReplicatedStorageClass failed: StoragePool is empty; ReclaimPolicy is empty; Selected unacceptable amount of zones for replication type: ConsistencyAndAvailability; correct number of zones should be 3; "))
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

		validation, _ := controller.ValidateReplicatedStorageClass(&replicatedSC, zones)
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

		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: testNamespaceConst,
				Name:      testName,
			},
		}

		failedMessage := fmt.Sprintf("[ReconcileReplicatedStorageClass] Validation of ReplicatedStorageClass %s failed for the following reason: Validation of ReplicatedStorageClass failed: StoragePool is empty; ReclaimPolicy is empty; Selected unacceptable amount of zones for replication type: ConsistencyAndAvailability; correct number of zones should be 3; ", testName)

		err := cl.Create(ctx, &replicatedSC)
		if err == nil {
			defer func() {
				if err := cl.Delete(ctx, &replicatedSC); err != nil && !errors.IsNotFound(err) {
					fmt.Println(err)
				}
			}()
		}
		Expect(err).NotTo(HaveOccurred())

		replicatedSC = srv.ReplicatedStorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Name:      testName,
			Namespace: testNamespaceConst,
		}, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())
		Expect(replicatedSC.Name).To(Equal(testName))
		Expect(replicatedSC.Finalizers).To(BeNil())
		Expect(replicatedSC.Spec.StoragePool).To(Equal(""))

		shouldRequeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).To(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		replicatedSC = srv.ReplicatedStorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Name:      testName,
			Namespace: testNamespaceConst,
		}, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())
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

		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: testNamespaceConst,
				Name:      testName,
			},
		}

		err := cl.Create(ctx, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		replicatedSC = getAndValidateNotReconciledRSC(ctx, cl, testName)

		storageClass, err := controller.GetStorageClass(ctx, cl, testNamespaceConst, testName)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())
		Expect(storageClass).To(BeNil())

		shouldRequeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		resources, err := getTestAPIStorageClasses(ctx, cl)
		Expect(err).NotTo(HaveOccurred())

		resource := resources[testName]

		Expect(resource.Status.Phase).To(Equal(controller.Created))
		Expect(resource.Status.Reason).To(Equal("ReplicatedStorageClass and StorageClass are equal."))

		Expect(slices.Contains(resource.Finalizers, controller.ReplicatedStorageClassFinalizerName)).To(BeTrue())

		storageClass, err = controller.GetStorageClass(ctx, cl, testNamespaceConst, testName)
		Expect(err).NotTo(HaveOccurred())
		Expect(storageClass).NotTo(BeNil())
		Expect(storageClass.Name).To(Equal(testName))
		Expect(storageClass.Namespace).To(Equal(testNamespaceConst))

		err = cl.Delete(ctx, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		replicatedSC = getAndValidateReconciledRSC(ctx, cl, testName)
		Expect(replicatedSC.DeletionTimestamp).NotTo(BeNil())

		shouldRequeue, err = controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		_, err = getRSC(ctx, cl, testName)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())

		_, err = getSC(ctx, cl, testName, testNamespaceConst)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())
	})

	It("ReconcileReplicatedStorageClass_Validation_passed_StorageClass_already_exists_Resource_and_StorageClass_ARE_EQUAL_Resource.Status.Phase_equals_Created", func() {
		testName := generateTestName()
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: testNamespaceConst,
				Name:      testName,
			},
		}

		err := cl.Create(ctx, &replicatedSC)
		if err == nil {
			defer func() {
				if err := cl.Delete(ctx, &replicatedSC); err != nil {
					fmt.Println(err)
				}
			}()
		}
		Expect(err).NotTo(HaveOccurred())

		virtualizationEnabled := false
		sc := controller.GetNewStorageClass(&replicatedSC, virtualizationEnabled)
		err = controller.CreateStorageClass(ctx, cl, sc)
		Expect(err).NotTo(HaveOccurred())

		shouldRequeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		resources, err := getTestAPIStorageClasses(ctx, cl)
		Expect(err).NotTo(HaveOccurred())

		resource := resources[testName]
		Expect(resource.Status.Phase).To(Equal(controller.Created))
		Expect(resource.Status.Reason).To(Equal("ReplicatedStorageClass and StorageClass are equal."))

		resFinalizers := strings.Join(resource.Finalizers, "")
		Expect(strings.Contains(resFinalizers, controller.ReplicatedStorageClassFinalizerName))

		storageClass, err := controller.GetStorageClass(ctx, cl, testNamespaceConst, testName)
		Expect(err).NotTo(HaveOccurred())
		Expect(storageClass).NotTo(BeNil())
		Expect(storageClass.Name).To(Equal(testName))
		Expect(storageClass.Namespace).To(Equal(testNamespaceConst))
	})

	It("ReconcileReplicatedStorageClass_Validation_passed_StorageClass_founded_Resource_and_StorageClass_ARE_NOT_EQUAL_Updates_resource_status_to_failed_and_reason", func() {
		testName := generateTestName()
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		replicatedSC.Status.Phase = controller.Created

		anotherReplicatedSC := validSpecReplicatedSCTemplate
		anotherReplicatedSC.Spec.ReclaimPolicy = "not-equal"
		anotherReplicatedSC.Name = testName

		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: testNamespaceConst,
				Name:      testName,
			},
		}

		failedMessage := "[ReconcileReplicatedStorageClass] error updateStorageClassIfNeeded: [recreateStorageClassIfNeeded] The StorageClass cannot be recreated because its parameters are not equal: Old StorageClass and New StorageClass are not equal: ReclaimPolicy are not equal (Old StorageClass: Retain, New StorageClass: not-equal"
		err := cl.Create(ctx, &replicatedSC)
		if err == nil {
			defer func() {
				if err := cl.Delete(ctx, &replicatedSC); err != nil {
					fmt.Println(err)
				}
			}()
		}
		Expect(err).NotTo(HaveOccurred())

		virtualizationEnabled := false
		anotherSC := controller.GetNewStorageClass(&anotherReplicatedSC, virtualizationEnabled)
		err = controller.CreateStorageClass(ctx, cl, anotherSC)
		Expect(err).NotTo(HaveOccurred())

		shouldRequeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).To(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())
		Expect(err.Error()).To(Equal(failedMessage))

		replicatedSCafterReconcile := srv.ReplicatedStorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Name:      testName,
			Namespace: testNamespaceConst,
		}, &replicatedSCafterReconcile)
		Expect(err).NotTo(HaveOccurred())
		Expect(replicatedSCafterReconcile.Name).To(Equal(testName))
		Expect(replicatedSCafterReconcile.Status.Phase).To(Equal(controller.Failed))

		storageClass, err := controller.GetStorageClass(ctx, cl, testNamespaceConst, testName)
		Expect(err).NotTo(HaveOccurred())
		Expect(storageClass).NotTo(BeNil())
		Expect(storageClass.Name).To(Equal(testName))
		Expect(storageClass.Namespace).To(Equal(testNamespaceConst))
	})

	It("CompareReplicatedStorageClassAndStorageClass_Resource_and_StorageClass_ARE_equal_Returns_true", func() {
		testName := generateTestName()
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		replicatedSC.Status.Phase = controller.Created
		storageClass := controller.GenerateStorageClassFromReplicatedStorageClass(&replicatedSC)

		equal, _ := controller.CompareStorageClasses(storageClass, storageClass)
		Expect(equal).To(BeTrue())
	})

	It("CompareReplicatedStorageClassAndStorageClass_Resource_and_StorageClass_ARE_NOT_equal_Returns_false_and_message", func() {
		var (
			diffRecPolicy v1.PersistentVolumeReclaimPolicy = "not-equal"
			diffVBM       storagev1.VolumeBindingMode      = "not-equal"
		)

		storageClass1 := &storagev1.StorageClass{
			Provisioner:       "first",
			Parameters:        map[string]string{"not": "equal"},
			ReclaimPolicy:     &diffRecPolicy,
			VolumeBindingMode: &diffVBM,
		}

		storageClass2 := &storagev1.StorageClass{
			Provisioner:       "second",
			Parameters:        map[string]string{"not": "equal"},
			ReclaimPolicy:     &diffRecPolicy,
			VolumeBindingMode: &diffVBM,
		}

		equal, message := controller.CompareStorageClasses(storageClass1, storageClass2)
		Expect(equal).To(BeFalse())
		Expect(message).NotTo(Equal(""))
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
				Namespace: testNamespaceConst,
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
		drbdNodeSelector := map[string]string{controller.SdsReplicatedVolumeNodeSelectorKey: ""}

		replicatedStorageClasses := srv.ReplicatedStorageClassList{}
		err = cl.List(ctx, &replicatedStorageClasses)
		Expect(err).NotTo(HaveOccurred())

		err = controller.ReconcileKubernetesNodeLabels(ctx, cl, log, *node, replicatedStorageClasses, drbdNodeSelector, true)
		Expect(err).NotTo(HaveOccurred())

		updatedNode := &v1.Node{}
		err = cl.Get(ctx, client.ObjectKey{
			Name:      "node-1",
			Namespace: testNamespaceConst,
		}, updatedNode)
		Expect(err).NotTo(HaveOccurred())

		_, exist := updatedNode.Labels[fmt.Sprintf("class.storage.deckhouse.io/%s", replicatedSC.Name)]
		Expect(exist).To(BeTrue())
	})

	// Annotation tests
	It("ReconcileReplicatedStorageClass_new_with_valid_config_VolumeAccessPreferablyLocal_ConfigMap_does_not_exist", func() {
		testName := testNameForAnnotationTests
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		replicatedSC.Spec.VolumeAccess = controller.VolumeAccessPreferablyLocal

		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: testNamespaceConst,
				Name:      testName,
			},
		}

		err := cl.Create(ctx, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		replicatedSC = getAndValidateNotReconciledRSC(ctx, cl, testName)

		_, err = getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())

		shouldRequeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		replicatedSC = getAndValidateReconciledRSC(ctx, cl, testName)
		Expect(replicatedSC.Status.Phase).To(Equal(controller.Created))

		storageClass := getAndValidateSC(ctx, cl, replicatedSC)
		Expect(storageClass.Annotations).To(BeNil())

		// Cleanup
		err = cl.Delete(ctx, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		replicatedSC = getAndValidateReconciledRSC(ctx, cl, testName)
		Expect(replicatedSC.DeletionTimestamp).NotTo(BeNil())

		shouldRequeue, err = controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		_, err = getRSC(ctx, cl, testName)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())

		_, err = getSC(ctx, cl, testName, testNamespaceConst)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())
	})

	It("ReconcileReplicatedStorageClass_new_with_valid_config_VolumeAccessLocal_ConfigMap_does_not_exist", func() {
		testName := testNameForAnnotationTests
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		replicatedSC.Spec.VolumeAccess = controller.VolumeAccessLocal

		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: testNamespaceConst,
				Name:      testName,
			},
		}

		err := cl.Create(ctx, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		replicatedSC = getAndValidateNotReconciledRSC(ctx, cl, testName)

		_, err = getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())

		shouldRequeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		replicatedSC = getAndValidateReconciledRSC(ctx, cl, testName)
		Expect(replicatedSC.Status.Phase).To(Equal(controller.Created))

		storageClass := getAndValidateSC(ctx, cl, replicatedSC)
		Expect(storageClass.Annotations).To(BeNil())

		// Cleanup
		err = cl.Delete(ctx, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		replicatedSC = getAndValidateReconciledRSC(ctx, cl, testName)
		Expect(replicatedSC.DeletionTimestamp).NotTo(BeNil())

		shouldRequeue, err = controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		_, err = getRSC(ctx, cl, testName)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())

		_, err = getSC(ctx, cl, testName, testNamespaceConst)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())
	})

	It("ReconcileReplicatedStorageClass_new_with_valid_config_VolumeAccessPreferablyLocal_ConfigMap_exist_without_data", func() {
		testName := testNameForAnnotationTests
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		replicatedSC.Spec.VolumeAccess = controller.VolumeAccessPreferablyLocal

		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: testNamespaceConst,
				Name:      testName,
			},
		}

		err := createConfigMap(ctx, cl, validCFG.ControllerNamespace, map[string]string{})
		Expect(err).NotTo(HaveOccurred())

		configMap, err := getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(configMap).NotTo(BeNil())
		Expect(configMap.Name).To(Equal(controller.ControllerConfigMapName))
		Expect(configMap.Namespace).To(Equal(validCFG.ControllerNamespace))
		Expect(configMap.Data).To(BeNil())

		err = cl.Create(ctx, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		replicatedSC = getAndValidateNotReconciledRSC(ctx, cl, testName)

		shouldRequeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		replicatedSC = getAndValidateReconciledRSC(ctx, cl, testName)
		Expect(replicatedSC.Status.Phase).To(Equal(controller.Created))

		storageClass := getAndValidateSC(ctx, cl, replicatedSC)
		Expect(storageClass.Annotations).To(BeNil())

		// Cleanup
		err = cl.Delete(ctx, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		replicatedSC = getAndValidateReconciledRSC(ctx, cl, testName)
		Expect(replicatedSC.DeletionTimestamp).NotTo(BeNil())

		shouldRequeue, err = controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		_, err = getRSC(ctx, cl, testName)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())

		_, err = getSC(ctx, cl, testName, testNamespaceConst)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())

		err = cl.Delete(ctx, configMap)
		Expect(err).NotTo(HaveOccurred())

		_, err = getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())
	})

	It("ReconcileReplicatedStorageClass_new_with_valid_config_VolumeAccessLocal_ConfigMap_exist_without_data", func() {
		testName := testNameForAnnotationTests
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		replicatedSC.Spec.VolumeAccess = controller.VolumeAccessLocal

		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: testNamespaceConst,
				Name:      testName,
			},
		}

		err := createConfigMap(ctx, cl, validCFG.ControllerNamespace, map[string]string{})
		Expect(err).NotTo(HaveOccurred())

		configMap, err := getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(configMap).NotTo(BeNil())
		Expect(configMap.Name).To(Equal(controller.ControllerConfigMapName))
		Expect(configMap.Namespace).To(Equal(validCFG.ControllerNamespace))
		Expect(configMap.Data).To(BeNil())

		err = cl.Create(ctx, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		replicatedSC = getAndValidateNotReconciledRSC(ctx, cl, testName)

		shouldRequeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		replicatedSC = getAndValidateReconciledRSC(ctx, cl, testName)
		Expect(replicatedSC.Status.Phase).To(Equal(controller.Created))

		storageClass := getAndValidateSC(ctx, cl, replicatedSC)
		Expect(storageClass.Annotations).To(BeNil())

		// Cleanup
		err = cl.Delete(ctx, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		replicatedSC = getAndValidateReconciledRSC(ctx, cl, testName)
		Expect(replicatedSC.DeletionTimestamp).NotTo(BeNil())

		shouldRequeue, err = controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		_, err = getRSC(ctx, cl, testName)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())

		_, err = getSC(ctx, cl, testName, testNamespaceConst)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())

		err = cl.Delete(ctx, configMap)
		Expect(err).NotTo(HaveOccurred())

		_, err = getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())
	})

	It("ReconcileReplicatedStorageClass_new_with_valid_config_VolumeAccessPreferablyLocal_ConfigMap_exist_with_virtualization_key_and_virtualization_value_is_false", func() {
		testName := testNameForAnnotationTests
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		replicatedSC.Spec.VolumeAccess = controller.VolumeAccessPreferablyLocal

		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: testNamespaceConst,
				Name:      testName,
			},
		}

		err := createConfigMap(ctx, cl, validCFG.ControllerNamespace, map[string]string{controller.VirtualizationModuleEnabledKey: "false"})
		Expect(err).NotTo(HaveOccurred())

		configMap, err := getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(configMap).NotTo(BeNil())
		Expect(configMap.Name).To(Equal(controller.ControllerConfigMapName))
		Expect(configMap.Namespace).To(Equal(validCFG.ControllerNamespace))
		Expect(configMap.Data).NotTo(BeNil())
		Expect(configMap.Data[controller.VirtualizationModuleEnabledKey]).To(Equal("false"))

		err = cl.Create(ctx, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		replicatedSC = getAndValidateNotReconciledRSC(ctx, cl, testName)

		shouldRequeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		replicatedSC = getAndValidateReconciledRSC(ctx, cl, testName)
		Expect(replicatedSC.Status.Phase).To(Equal(controller.Created))

		storageClass := getAndValidateSC(ctx, cl, replicatedSC)
		Expect(storageClass.Annotations).To(BeNil())
	})

	It("ReconcileReplicatedStorageClass_already_exists_with_valid_config_VolumeAccessPreferablyLocal_ConfigMap_exist_with_virtualization_key_and_virtualization_value_updated_from_false_to_true", func() {
		testName := testNameForAnnotationTests

		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: testNamespaceConst,
				Name:      testName,
			},
		}

		configMap, err := getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(configMap).NotTo(BeNil())
		Expect(configMap.Name).To(Equal(controller.ControllerConfigMapName))
		Expect(configMap.Namespace).To(Equal(validCFG.ControllerNamespace))
		Expect(configMap.Data).NotTo(BeNil())
		Expect(configMap.Data[controller.VirtualizationModuleEnabledKey]).To(Equal("false"))

		configMap.Data[controller.VirtualizationModuleEnabledKey] = "true"
		err = cl.Update(ctx, configMap)
		Expect(err).NotTo(HaveOccurred())

		configMap, err = getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(configMap).NotTo(BeNil())
		Expect(configMap.Name).To(Equal(controller.ControllerConfigMapName))
		Expect(configMap.Namespace).To(Equal(validCFG.ControllerNamespace))
		Expect(configMap.Data).NotTo(BeNil())
		Expect(configMap.Data[controller.VirtualizationModuleEnabledKey]).To(Equal("true"))

		replicatedSC := getAndValidateReconciledRSC(ctx, cl, testName)
		Expect(replicatedSC.Spec.VolumeAccess).To(Equal(controller.VolumeAccessPreferablyLocal))
		Expect(replicatedSC.Status.Phase).To(Equal(controller.Created))

		shouldRequeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		replicatedSC = getAndValidateReconciledRSC(ctx, cl, testName)
		Expect(replicatedSC.Status.Phase).To(Equal(controller.Created))

		storageClass := getAndValidateSC(ctx, cl, replicatedSC)
		Expect(storageClass.Annotations).To(BeNil())

		// Cleanup
		err = cl.Delete(ctx, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		replicatedSC = getAndValidateReconciledRSC(ctx, cl, testName)
		Expect(replicatedSC.DeletionTimestamp).NotTo(BeNil())

		shouldRequeue, err = controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		_, err = getRSC(ctx, cl, testName)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())

		_, err = getSC(ctx, cl, testName, testNamespaceConst)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())

		err = cl.Delete(ctx, configMap)
		Expect(err).NotTo(HaveOccurred())

		_, err = getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())
	})

	It("ReconcileReplicatedStorageClass_new_with_valid_config_VolumeAccessLocal_ConfigMap_exist_with_virtualization_key_and_virtualization_value_is_false", func() {
		testName := testNameForAnnotationTests
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		replicatedSC.Spec.VolumeAccess = controller.VolumeAccessLocal

		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: testNamespaceConst,
				Name:      testName,
			},
		}

		err := createConfigMap(ctx, cl, validCFG.ControllerNamespace, map[string]string{controller.VirtualizationModuleEnabledKey: "false"})
		Expect(err).NotTo(HaveOccurred())

		configMap, err := getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(configMap).NotTo(BeNil())
		Expect(configMap.Name).To(Equal(controller.ControllerConfigMapName))
		Expect(configMap.Namespace).To(Equal(validCFG.ControllerNamespace))
		Expect(configMap.Data).NotTo(BeNil())
		Expect(configMap.Data[controller.VirtualizationModuleEnabledKey]).To(Equal("false"))

		err = cl.Create(ctx, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		replicatedSC = getAndValidateNotReconciledRSC(ctx, cl, testName)

		shouldRequeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		replicatedSC = getAndValidateReconciledRSC(ctx, cl, testName)
		Expect(replicatedSC.Status.Phase).To(Equal(controller.Created))

		storageClass := getAndValidateSC(ctx, cl, replicatedSC)
		Expect(storageClass.Annotations).To(BeNil())

	})

	It("ReconcileReplicatedStorageClass_already_exists_with_valid_config_VolumeAccessLocal_ConfigMap_exist_with_virtualization_key_and_virtualization_value_updated_from_false_to_true", func() {
		testName := testNameForAnnotationTests

		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: testNamespaceConst,
				Name:      testName,
			},
		}

		configMap, err := getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(configMap).NotTo(BeNil())
		Expect(configMap.Name).To(Equal(controller.ControllerConfigMapName))
		Expect(configMap.Namespace).To(Equal(validCFG.ControllerNamespace))
		Expect(configMap.Data).NotTo(BeNil())
		Expect(configMap.Data[controller.VirtualizationModuleEnabledKey]).To(Equal("false"))

		configMap.Data[controller.VirtualizationModuleEnabledKey] = "true"
		err = cl.Update(ctx, configMap)
		Expect(err).NotTo(HaveOccurred())

		configMap, err = getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(configMap).NotTo(BeNil())
		Expect(configMap.Name).To(Equal(controller.ControllerConfigMapName))
		Expect(configMap.Namespace).To(Equal(validCFG.ControllerNamespace))
		Expect(configMap.Data).NotTo(BeNil())
		Expect(configMap.Data[controller.VirtualizationModuleEnabledKey]).To(Equal("true"))

		replicatedSC := getAndValidateReconciledRSC(ctx, cl, testName)
		Expect(replicatedSC.Spec.VolumeAccess).To(Equal(controller.VolumeAccessLocal))
		Expect(replicatedSC.Status.Phase).To(Equal(controller.Created))

		shouldRequeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		replicatedSC = getAndValidateReconciledRSC(ctx, cl, testName)
		Expect(replicatedSC.Status.Phase).To(Equal(controller.Created))

		storageClass := getAndValidateSC(ctx, cl, replicatedSC)
		Expect(storageClass.Annotations).NotTo(BeNil())
		Expect(storageClass.Annotations[controller.StorageClassVirtualizationAnnotationKey]).To(Equal(controller.StorageClassVirtualizationAnnotationValue))

		// Cleanup
		err = cl.Delete(ctx, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		replicatedSC = getAndValidateReconciledRSC(ctx, cl, testName)
		Expect(replicatedSC.DeletionTimestamp).NotTo(BeNil())

		shouldRequeue, err = controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		_, err = getRSC(ctx, cl, testName)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())

		_, err = getSC(ctx, cl, testName, testNamespaceConst)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())

		err = cl.Delete(ctx, configMap)
		Expect(err).NotTo(HaveOccurred())

		_, err = getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())
	})

	It("ReconcileReplicatedStorageClass_new_with_valid_config_VolumeAccessPreferablyLocal_ConfigMap_exist_with_virtualization_key_and_virtualization_value_is_true", func() {
		testName := testNameForAnnotationTests
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		replicatedSC.Spec.VolumeAccess = controller.VolumeAccessPreferablyLocal

		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: testNamespaceConst,
				Name:      testName,
			},
		}

		err := createConfigMap(ctx, cl, validCFG.ControllerNamespace, map[string]string{controller.VirtualizationModuleEnabledKey: "true"})
		Expect(err).NotTo(HaveOccurred())

		configMap, err := getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(configMap).NotTo(BeNil())
		Expect(configMap.Name).To(Equal(controller.ControllerConfigMapName))
		Expect(configMap.Namespace).To(Equal(validCFG.ControllerNamespace))
		Expect(configMap.Data).NotTo(BeNil())
		Expect(configMap.Data[controller.VirtualizationModuleEnabledKey]).To(Equal("true"))

		err = cl.Create(ctx, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		replicatedSC = getAndValidateNotReconciledRSC(ctx, cl, testName)

		shouldRequeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		replicatedSC = getAndValidateReconciledRSC(ctx, cl, testName)
		Expect(replicatedSC.Status.Phase).To(Equal(controller.Created))

		storageClass := getAndValidateSC(ctx, cl, replicatedSC)
		Expect(storageClass.Annotations).To(BeNil())
	})

	It("ReconcileReplicatedStorageClass_already_exists_with_valid_config_VolumeAccessPreferablyLocal_ConfigMap_exist_with_virtualization_key_and_virtualization_value_updated_from_true_to_false", func() {
		testName := testNameForAnnotationTests

		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: testNamespaceConst,
				Name:      testName,
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

		replicatedSC := getAndValidateReconciledRSC(ctx, cl, testName)
		Expect(replicatedSC.Spec.VolumeAccess).To(Equal(controller.VolumeAccessPreferablyLocal))
		Expect(replicatedSC.Status.Phase).To(Equal(controller.Created))

		shouldRequeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		replicatedSC = getAndValidateReconciledRSC(ctx, cl, testName)
		Expect(replicatedSC.Status.Phase).To(Equal(controller.Created))

		storageClass := getAndValidateSC(ctx, cl, replicatedSC)
		Expect(storageClass.Annotations).To(BeNil())

		// Cleanup
		err = cl.Delete(ctx, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		replicatedSC = getAndValidateReconciledRSC(ctx, cl, testName)
		Expect(replicatedSC.DeletionTimestamp).NotTo(BeNil())

		shouldRequeue, err = controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		_, err = getRSC(ctx, cl, testName)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())

		_, err = getSC(ctx, cl, testName, testNamespaceConst)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())

		err = cl.Delete(ctx, configMap)
		Expect(err).NotTo(HaveOccurred())

		_, err = getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())
	})

	It("ReconcileReplicatedStorageClass_new_with_valid_config_VolumeAccessLocal_ConfigMap_exist_with_virtualization_key_and_virtualization_value_is_true", func() {
		testName := testNameForAnnotationTests
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		replicatedSC.Spec.VolumeAccess = controller.VolumeAccessLocal

		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: testNamespaceConst,
				Name:      testName,
			},
		}

		err := createConfigMap(ctx, cl, validCFG.ControllerNamespace, map[string]string{controller.VirtualizationModuleEnabledKey: "true"})
		Expect(err).NotTo(HaveOccurred())

		configMap, err := getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(configMap).NotTo(BeNil())
		Expect(configMap.Name).To(Equal(controller.ControllerConfigMapName))
		Expect(configMap.Namespace).To(Equal(validCFG.ControllerNamespace))
		Expect(configMap.Data).NotTo(BeNil())
		Expect(configMap.Data[controller.VirtualizationModuleEnabledKey]).To(Equal("true"))

		err = cl.Create(ctx, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		replicatedSC = getAndValidateNotReconciledRSC(ctx, cl, testName)

		virtualizationEnabled, err := controller.GetVirtualizationModuleEnabled(ctx, cl, log, types.NamespacedName{Name: controller.ControllerConfigMapName, Namespace: validCFG.ControllerNamespace})
		Expect(err).NotTo(HaveOccurred())
		Expect(virtualizationEnabled).To(BeTrue())

		scResource := controller.GetNewStorageClass(&replicatedSC, virtualizationEnabled)
		Expect(scResource).NotTo(BeNil())
		Expect(scResource.Annotations).NotTo(BeNil())
		Expect(scResource.Annotations[controller.StorageClassVirtualizationAnnotationKey]).To(Equal(controller.StorageClassVirtualizationAnnotationValue))

		shouldRequeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		replicatedSC = getAndValidateReconciledRSC(ctx, cl, testName)
		Expect(replicatedSC.Status.Phase).To(Equal(controller.Created))

		storageClass := getAndValidateSC(ctx, cl, replicatedSC)
		Expect(storageClass.Annotations).NotTo(BeNil())
		Expect(storageClass.Annotations[controller.StorageClassVirtualizationAnnotationKey]).To(Equal(controller.StorageClassVirtualizationAnnotationValue))
	})

	It("ReconcileReplicatedStorageClass_already_exists_with_valid_config_VolumeAccessLocal_ConfigMap_exist_with_virtualization_key_and_virtualization_value_updated_from_true_to_false", func() {
		testName := testNameForAnnotationTests

		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: testNamespaceConst,
				Name:      testName,
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

		replicatedSC := getAndValidateReconciledRSC(ctx, cl, testName)
		Expect(replicatedSC.Spec.VolumeAccess).To(Equal(controller.VolumeAccessLocal))
		Expect(replicatedSC.Status.Phase).To(Equal(controller.Created))

		virtualizationEnabled, err := controller.GetVirtualizationModuleEnabled(ctx, cl, log, types.NamespacedName{Name: controller.ControllerConfigMapName, Namespace: validCFG.ControllerNamespace})
		Expect(err).NotTo(HaveOccurred())
		Expect(virtualizationEnabled).To(BeFalse())

		storageClass := getAndValidateSC(ctx, cl, replicatedSC)
		Expect(storageClass.Annotations).NotTo(BeNil())
		Expect(storageClass.Annotations[controller.StorageClassVirtualizationAnnotationKey]).To(Equal(controller.StorageClassVirtualizationAnnotationValue))

		scResourceAfterUpdate := controller.GetUpdatedStorageClass(&replicatedSC, storageClass, virtualizationEnabled)
		Expect(scResourceAfterUpdate).NotTo(BeNil())
		Expect(scResourceAfterUpdate.Annotations).To(BeNil())

		shouldRequeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		replicatedSC = getAndValidateReconciledRSC(ctx, cl, testName)
		Expect(replicatedSC.Status.Phase).To(Equal(controller.Created))

		storageClass = getAndValidateSC(ctx, cl, replicatedSC)
		Expect(storageClass.Annotations).To(BeNil())

		// Cleanup
		err = cl.Delete(ctx, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		replicatedSC = getAndValidateReconciledRSC(ctx, cl, testName)
		Expect(replicatedSC.DeletionTimestamp).NotTo(BeNil())

		shouldRequeue, err = controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		_, err = getRSC(ctx, cl, testName)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())

		_, err = getSC(ctx, cl, testName, testNamespaceConst)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())

		err = cl.Delete(ctx, configMap)
		Expect(err).NotTo(HaveOccurred())

		_, err = getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())
	})

	It("ReconcileReplicatedStorageClass_new_with_valid_config_VolumeAccessLocal_StorageClass_already_exists_with_default_annotation_only_ConfigMap_exist_with_virtualization_key_and_virtualization_value_is_true", func() {
		testName := testNameForAnnotationTests
		replicatedSC := validSpecReplicatedSCTemplate
		replicatedSC.Name = testName
		replicatedSC.Spec.VolumeAccess = controller.VolumeAccessLocal

		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: testNamespaceConst,
				Name:      testName,
			},
		}

		storageClassResource := controller.GetNewStorageClass(&replicatedSC, false)
		Expect(storageClassResource).NotTo(BeNil())
		Expect(storageClassResource.Annotations).To(BeNil())
		Expect(storageClassResource.Name).To(Equal(replicatedSC.Name))
		Expect(storageClassResource.Namespace).To(Equal(replicatedSC.Namespace))
		Expect(storageClassResource.Provisioner).To(Equal(controller.StorageClassProvisioner))

		// add default annotation
		storageClassResource.Annotations = map[string]string{controller.DefaultStorageClassAnnotationKey: "true"}

		err := cl.Create(ctx, storageClassResource)
		Expect(err).NotTo(HaveOccurred())

		storageClass := getAndValidateSC(ctx, cl, replicatedSC)
		Expect(storageClass.Annotations).NotTo(BeNil())
		Expect(len(storageClass.Annotations)).To(Equal(1))
		Expect(storageClass.Annotations[controller.DefaultStorageClassAnnotationKey]).To(Equal("true"))

		err = createConfigMap(ctx, cl, validCFG.ControllerNamespace, map[string]string{controller.VirtualizationModuleEnabledKey: "true"})
		Expect(err).NotTo(HaveOccurred())

		configMap, err := getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(configMap).NotTo(BeNil())
		Expect(configMap.Name).To(Equal(controller.ControllerConfigMapName))
		Expect(configMap.Namespace).To(Equal(validCFG.ControllerNamespace))
		Expect(configMap.Data).NotTo(BeNil())
		Expect(configMap.Data[controller.VirtualizationModuleEnabledKey]).To(Equal("true"))

		err = cl.Create(ctx, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		replicatedSC = getAndValidateNotReconciledRSC(ctx, cl, testName)

		virtualizationEnabled, err := controller.GetVirtualizationModuleEnabled(ctx, cl, log, types.NamespacedName{Name: controller.ControllerConfigMapName, Namespace: validCFG.ControllerNamespace})
		Expect(err).NotTo(HaveOccurred())
		Expect(virtualizationEnabled).To(BeTrue())

		scResource := controller.GetUpdatedStorageClass(&replicatedSC, storageClass, virtualizationEnabled)
		Expect(scResource).NotTo(BeNil())
		Expect(scResource.Annotations).NotTo(BeNil())
		Expect(len(scResource.Annotations)).To(Equal(2))
		Expect(scResource.Annotations[controller.DefaultStorageClassAnnotationKey]).To(Equal("true"))
		Expect(scResource.Annotations[controller.StorageClassVirtualizationAnnotationKey]).To(Equal(controller.StorageClassVirtualizationAnnotationValue))

		shouldRequeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		replicatedSC = getAndValidateReconciledRSC(ctx, cl, testName)
		Expect(replicatedSC.Status.Phase).To(Equal(controller.Created))

		storageClass = getAndValidateSC(ctx, cl, replicatedSC)
		Expect(storageClass.Annotations).NotTo(BeNil())
		Expect(storageClass.Annotations[controller.DefaultStorageClassAnnotationKey]).To(Equal("true"))
		Expect(len(storageClass.Annotations)).To(Equal(2))
		Expect(storageClass.Annotations[controller.StorageClassVirtualizationAnnotationKey]).To(Equal(controller.StorageClassVirtualizationAnnotationValue))
	})

	It("ReconcileReplicatedStorageClass_already_exists_with_valid_config_VolumeAccessLocal_StorageClass_already_exists_with_default_and_vritualization_annotations_ConfigMap_exist_with_virtualization_key_and_virtualization_value_updated_from_true_to_false", func() {
		testName := testNameForAnnotationTests

		request := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: testNamespaceConst,
				Name:      testName,
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

		replicatedSC := getAndValidateReconciledRSC(ctx, cl, testName)
		Expect(replicatedSC.Spec.VolumeAccess).To(Equal(controller.VolumeAccessLocal))
		Expect(replicatedSC.Status.Phase).To(Equal(controller.Created))

		storageClass := getAndValidateSC(ctx, cl, replicatedSC)
		Expect(storageClass.Annotations).NotTo(BeNil())
		Expect(len(storageClass.Annotations)).To(Equal(2))
		Expect(storageClass.Annotations[controller.DefaultStorageClassAnnotationKey]).To(Equal("true"))
		Expect(storageClass.Annotations[controller.StorageClassVirtualizationAnnotationKey]).To(Equal(controller.StorageClassVirtualizationAnnotationValue))

		virtualizationEnabled, err := controller.GetVirtualizationModuleEnabled(ctx, cl, log, types.NamespacedName{Name: controller.ControllerConfigMapName, Namespace: validCFG.ControllerNamespace})
		Expect(err).NotTo(HaveOccurred())
		Expect(virtualizationEnabled).To(BeFalse())

		scResourceAfterUpdate := controller.GetUpdatedStorageClass(&replicatedSC, storageClass, virtualizationEnabled)
		Expect(scResourceAfterUpdate).NotTo(BeNil())
		Expect(scResourceAfterUpdate.Annotations).NotTo(BeNil())
		Expect(len(scResourceAfterUpdate.Annotations)).To(Equal(1))
		Expect(scResourceAfterUpdate.Annotations[controller.DefaultStorageClassAnnotationKey]).To(Equal("true"))

		shouldRequeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		replicatedSC = getAndValidateReconciledRSC(ctx, cl, testName)
		Expect(replicatedSC.Status.Phase).To(Equal(controller.Created))

		storageClass = getAndValidateSC(ctx, cl, replicatedSC)
		Expect(storageClass.Annotations).NotTo(BeNil())
		Expect(len(storageClass.Annotations)).To(Equal(1))
		Expect(storageClass.Annotations[controller.DefaultStorageClassAnnotationKey]).To(Equal("true"))

		// Cleanup
		err = cl.Delete(ctx, &replicatedSC)
		Expect(err).NotTo(HaveOccurred())

		replicatedSC = getAndValidateReconciledRSC(ctx, cl, testName)
		Expect(replicatedSC.DeletionTimestamp).NotTo(BeNil())

		shouldRequeue, err = controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		_, err = getRSC(ctx, cl, testName)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())

		_, err = getSC(ctx, cl, testName, testNamespaceConst)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())

		err = cl.Delete(ctx, configMap)
		Expect(err).NotTo(HaveOccurred())

		_, err = getConfigMap(ctx, cl, validCFG.ControllerNamespace)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue())
	})

})
