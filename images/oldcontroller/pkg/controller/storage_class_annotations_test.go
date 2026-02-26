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

package controller_test

import (
	"context"
	"fmt"
	"maps"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/oldcontroller/config"
	"github.com/deckhouse/sds-replicated-volume/images/oldcontroller/pkg/controller"
	"github.com/deckhouse/sds-replicated-volume/images/oldcontroller/pkg/logger"
)

var _ = Describe(controller.StorageClassAnnotationsCtrlName, func() {

	const (
		testNameSpace = "test-namespace"
		testName      = "test-name"
	)

	var (
		ctx context.Context
		cl  client.WithWatch
		log logger.Logger

		validCFG, _ = config.NewConfig()

		allowVolumeExpansion   = true
		volumeBindingMode      = storagev1.VolumeBindingWaitForFirstConsumer
		reclaimPolicy          = corev1.PersistentVolumeReclaimPolicy(controller.ReclaimPolicyRetain)
		storageClassParameters = map[string]string{
			srv.ReplicatedStorageClassParamNameKey: testName,
		}

		validStorageClassResource = &storagev1.StorageClass{
			TypeMeta: metav1.TypeMeta{
				Kind:       srv.StorageClassKind,
				APIVersion: srv.StorageClassAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:            testName,
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
			Provisioner:          srv.StorageClassProvisioner,
		}

		storageClassResource           *storagev1.StorageClass
		configMap                      *corev1.ConfigMap
		replicatedStorageClassResource *srv.ReplicatedStorageClass
	)

	BeforeEach(func() {
		ctx = context.Background()
		cl = newFakeClient()
		log = logger.Logger{}
		storageClassResource = nil
		configMap = nil
		replicatedStorageClassResource = nil
	})

	whenStorageClassExists := func(foo func()) {
		When("StorageClass exists", func() {
			BeforeEach(func() {
				storageClassResource = validStorageClassResource.DeepCopy()
				replicatedStorageClassResource = &srv.ReplicatedStorageClass{
					ObjectMeta: metav1.ObjectMeta{
						Name:            testName,
						OwnerReferences: nil,
						Finalizers:      nil,
						ManagedFields:   nil,
						Labels: map[string]string{
							"storage.deckhouse.io/managed-by": "sds-replicated-volume",
						},
					},
				}
			})
			JustBeforeEach(func() {
				err := cl.Create(ctx, storageClassResource)
				Expect(err).NotTo(HaveOccurred())
				if storageClassResource.Annotations != nil {
					replicatedStorageClassResource.Annotations = make(map[string]string, len(storageClassResource.Annotations))
					maps.Copy(replicatedStorageClassResource.Annotations, storageClassResource.Annotations)
				}
				err = cl.Create(ctx, replicatedStorageClassResource)
				Expect(err).NotTo(HaveOccurred())
			})
			JustAfterEach(func() {
				storageClass, err := getSC(ctx, cl, storageClassResource.Name, storageClassResource.Namespace)
				Expect(err).NotTo(HaveOccurred())
				Expect(storageClass).NotTo(BeNil())
				Expect(storageClass.Name).To(Equal(storageClassResource.Name))
				Expect(storageClass.Namespace).To(Equal(storageClassResource.Namespace))

				// Cleanup
				err = cl.Delete(ctx, storageClassResource)
				Expect(err).NotTo(HaveOccurred())

				err = cl.Delete(ctx, replicatedStorageClassResource)
				Expect(err).ToNot(HaveOccurred())

				_, err = getSC(ctx, cl, storageClassResource.Name, storageClassResource.Namespace)
				Expect(err).To(HaveOccurred())
				Expect(errors.IsNotFound(err)).To(BeTrue())
			})

			foo()
		})
	}

	When("ReconcileControllerConfigMapEvent", func() {
		var request reconcile.Request
		BeforeEach(func() {
			request = reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: validCFG.ControllerNamespace,
					Name:      controller.ControllerConfigMapName,
				},
			}
		})

		whenConfigMapExistsIs := func(value bool, foo func()) {
			if value {
				When("ConfigMap exists", func() {
					BeforeEach(func() {
						configMap = &corev1.ConfigMap{
							ObjectMeta: metav1.ObjectMeta{
								Namespace: request.Namespace,
								Name:      request.Name,
							},
						}
					})
					JustBeforeEach(func() {
						err := cl.Create(ctx, configMap)
						Expect(err).NotTo(HaveOccurred())
					})
					JustAfterEach(func() {
						err := cl.Delete(ctx, configMap)
						Expect(err).NotTo(HaveOccurred())

						_, err = getConfigMap(ctx, cl, validCFG.ControllerNamespace)
						Expect(err).To(HaveOccurred())
						Expect(errors.IsNotFound(err)).To(BeTrue())
					})

					foo()
				})
			} else {
				When("ConfigMap does not exist", func() {
					JustBeforeEach(func() {
						var err error
						configMap, err := getConfigMap(ctx, cl, validCFG.ControllerNamespace)

						Expect(err).To(HaveOccurred())
						Expect(errors.IsNotFound(err)).To(BeTrue())
						Expect(configMap).NotTo(BeNil())
						Expect(configMap.Name).To(Equal(""))

						virtualizationEnabled, err := controller.GetVirtualizationModuleEnabled(ctx, cl, log, request.NamespacedName)
						Expect(err).NotTo(HaveOccurred())
						Expect(virtualizationEnabled).To(BeFalse())
					})

					foo()
				})
			}
		}

		whenVolumeAccessIsLocal := func(local bool, foo func()) {
			if local {
				When("local", func() {
					BeforeEach(func() {
						if replicatedStorageClassResource == nil {
							return
						}
						replicatedStorageClassResource.Spec.VolumeAccess = srv.VolumeAccessLocal
					})
					foo()
				})
			} else {
				When("non local", func() {
					BeforeEach(func() {
						if replicatedStorageClassResource == nil {
							return
						}
						replicatedStorageClassResource.Spec.VolumeAccess = srv.VolumeAccessAny
					})
					foo()
				})
			}
		}

		whenDefaultAnnotationExistsIs := func(value bool, foo func()) {
			if value {
				When("with default annotation", func() {
					BeforeEach(func() {
						Expect(storageClassResource).ToNot(BeNil())
						if storageClassResource.Annotations == nil {
							storageClassResource.Annotations = make(map[string]string)
						}
						storageClassResource.Annotations[srv.DefaultStorageClassAnnotationKey] = "true"
					})
					JustBeforeEach(func() {
						Expect(storageClassResource).ToNot(BeNil())
						Expect(storageClassResource.Annotations).To(HaveKeyWithValue(srv.DefaultStorageClassAnnotationKey, "true"))
					})
					foo()
				})
			} else {
				When("without default annotation", func() {
					BeforeEach(func() {
						if storageClassResource != nil {
							storageClassResource.Annotations = nil
						}
					})
					JustBeforeEach(func() {
						if storageClassResource != nil {
							Expect(storageClassResource.Annotations).To(BeNil())
						}
					})
					foo()
				})
			}
		}

		whenVirtualizationIs := func(value bool, foo func()) {
			When(fmt.Sprintf("with virtualization value is %v", value), func() {
				BeforeEach(func() {
					strValue := "false"
					if value {
						strValue = "true"
					}
					if configMap.Data == nil {
						configMap.Data = make(map[string]string)
					}
					configMap.Data[controller.VirtualizationModuleEnabledKey] = strValue
				})
				JustBeforeEach(func() {
					virtualizationEnabled, err := controller.GetVirtualizationModuleEnabled(ctx, cl, log, request.NamespacedName)
					Expect(err).NotTo(HaveOccurred())
					Expect(virtualizationEnabled).To(BeEquivalentTo(value))
				})
				foo()
			})
		}

		itHasNoAnnotations := func() {
			It("has no annotations", func() {
				shouldRequeue, err := controller.ReconcileControllerConfigMapEvent(ctx, cl, log, request)
				Expect(err).NotTo(HaveOccurred())
				Expect(shouldRequeue).To(BeFalse())

				storageClass, err := getSC(ctx, cl, storageClassResource.Name, storageClassResource.Namespace)
				Expect(err).NotTo(HaveOccurred())
				Expect(storageClass).NotTo(BeNil())
				Expect(storageClass.Annotations).To(BeNil())
			})
		}

		itHasOnlyDefaultStorageClassAnnotationKey := func() {
			It("has only default storage class annotation", func() {
				shouldRequeue, err := controller.ReconcileControllerConfigMapEvent(ctx, cl, log, request)
				Expect(err).NotTo(HaveOccurred())
				Expect(shouldRequeue).To(BeFalse())

				storageClass, err := getSC(ctx, cl, storageClassResource.Name, storageClassResource.Namespace)
				Expect(err).NotTo(HaveOccurred())
				Expect(storageClass).NotTo(BeNil())
				Expect(storageClass.Annotations).NotTo(BeNil())
				Expect(storageClass.Annotations).To(HaveLen(1))
				Expect(storageClass.Annotations).To(HaveKeyWithValue(srv.DefaultStorageClassAnnotationKey, "true"))
			})
		}

		whenStorageClassExists(func() {
			whenConfigMapExistsIs(false, func() {
				whenVolumeAccessIsLocal(true, func() {
					whenDefaultAnnotationExistsIs(false, func() {
						itHasNoAnnotations()
					})
					whenDefaultAnnotationExistsIs(true, func() {
						itHasOnlyDefaultStorageClassAnnotationKey()
					})
				})
			})
			whenConfigMapExistsIs(true, func() {
				whenVirtualizationIs(false, func() {
					whenDefaultAnnotationExistsIs(false, func() {
						whenVolumeAccessIsLocal(true, func() {
							itHasNoAnnotations()
						})
						whenVolumeAccessIsLocal(false, func() {
							itHasNoAnnotations()
						})
					})
					whenDefaultAnnotationExistsIs(true, func() {
						whenVolumeAccessIsLocal(true, func() {
							itHasOnlyDefaultStorageClassAnnotationKey()
						})
						whenVolumeAccessIsLocal(false, func() {
							itHasOnlyDefaultStorageClassAnnotationKey()
						})
					})
				})
				whenVirtualizationIs(true, func() {
					whenDefaultAnnotationExistsIs(false, func() {
						whenVolumeAccessIsLocal(true, func() {
							It("has only access mode annotation", func() {
								shouldRequeue, err := controller.ReconcileControllerConfigMapEvent(ctx, cl, log, request)
								Expect(err).NotTo(HaveOccurred())
								Expect(shouldRequeue).To(BeFalse())

								storageClass, err := getSC(ctx, cl, storageClassResource.Name, storageClassResource.Namespace)
								Expect(err).NotTo(HaveOccurred())
								Expect(storageClass).NotTo(BeNil())
								Expect(storageClass.Annotations).NotTo(BeNil())
								Expect(storageClass.Annotations).To(HaveLen(1))
								Expect(storageClass.Annotations).To(HaveKeyWithValue(controller.StorageClassVirtualizationAnnotationKey, controller.StorageClassVirtualizationAnnotationValue))
							})
						})
						whenVolumeAccessIsLocal(false, func() {
							itHasNoAnnotations()
						})
					})
					whenDefaultAnnotationExistsIs(true, func() {
						whenVolumeAccessIsLocal(true, func() {
							It("has default storage class and access mode annotations", func() {
								shouldRequeue, err := controller.ReconcileControllerConfigMapEvent(ctx, cl, log, request)
								Expect(err).NotTo(HaveOccurred())
								Expect(shouldRequeue).To(BeFalse())

								storageClass, err := getSC(ctx, cl, storageClassResource.Name, storageClassResource.Namespace)
								Expect(err).NotTo(HaveOccurred())
								Expect(storageClass).NotTo(BeNil())
								Expect(storageClass.Annotations).NotTo(BeNil())
								Expect(storageClass.Annotations).To(HaveLen(2))
								Expect(storageClass.Annotations).To(HaveKeyWithValue(srv.DefaultStorageClassAnnotationKey, "true"))
								Expect(storageClass.Annotations).To(HaveKeyWithValue(controller.StorageClassVirtualizationAnnotationKey, controller.StorageClassVirtualizationAnnotationValue))
							})
						})
						whenVolumeAccessIsLocal(false, func() {
							itHasOnlyDefaultStorageClassAnnotationKey()
						})
					})

					When("local VolumeAccess with another provisioner", func() {
						var anotherProvisioner string
						BeforeEach(func() {
							anotherProvisioner = "another.provisioner"
							storageClassResource.Annotations = map[string]string{srv.DefaultStorageClassAnnotationKey: "true"}
							replicatedStorageClassResource.Spec.VolumeAccess = srv.VolumeAccessLocal
							storageClassResource.Provisioner = anotherProvisioner
						})

						itHasOnlyDefaultStorageClassAnnotationKey()

						It("local VolumeAccess is ignored for non-matching provisioner", func() {
							shouldRequeue, err := controller.ReconcileControllerConfigMapEvent(ctx, cl, log, request)
							Expect(err).NotTo(HaveOccurred())
							Expect(shouldRequeue).To(BeFalse())

							storageClass, err := getSC(ctx, cl, storageClassResource.Name, storageClassResource.Namespace)
							Expect(err).NotTo(HaveOccurred())
							Expect(storageClass).NotTo(BeNil())
							Expect(storageClass.Provisioner).To(Equal(anotherProvisioner))
						})
					})
				})
			})
		})
	})
})
