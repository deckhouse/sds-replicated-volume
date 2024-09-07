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
	"testing"

	. "github.com/LINBIT/golinstor/client"
	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sds-replicated-volume-controller/pkg/controller"
)

const (
	testNamespaceConst         = "test-namespace"
	testNameForAnnotationTests = "rsc-test-annotation"
)

func TestController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controller Suite")
}

func newFakeClient() client.WithWatch {
	s := scheme.Scheme
	_ = metav1.AddMetaToScheme(s)
	_ = srv.AddToScheme(s)
	_ = snc.AddToScheme(s)

	builder := fake.NewClientBuilder().WithScheme(s)

	cl := builder.Build()
	return cl
}

func getTestAPIStorageClasses(ctx context.Context, cl client.Client) (map[string]srv.ReplicatedStorageClass, error) {
	resources := &srv.ReplicatedStorageClassList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ReplicatedStorageClass",
			APIVersion: "storage.deckhouse.io/v1alpha1",
		},
		ListMeta: metav1.ListMeta{},
		Items:    []srv.ReplicatedStorageClass{},
	}

	if err := cl.List(ctx, resources); err != nil {
		return nil, err
	}

	classes := make(map[string]srv.ReplicatedStorageClass, len(resources.Items))
	for _, res := range resources.Items {
		classes[res.Name] = res
	}

	return classes, nil
}

func generateTestName() string {
	return "test-name-" + uuid.NewString()
}

func NewLinstorClientWithMockNodes() (*Client, error) {
	lc, err := NewClient()
	lc.Nodes = MockNodes()

	return lc, err
}

func MockNodes() *NodeProviderMock {
	return &NodeProviderMock{}
}

type NodeProviderMock struct {
}

func (m *NodeProviderMock) GetAll(_ context.Context, _ ...*ListOpts) ([]Node, error) {
	return nil, nil
}

func (m *NodeProviderMock) Get(_ context.Context, _ string, _ ...*ListOpts) (Node, error) {
	return Node{}, nil
}

func (m *NodeProviderMock) Create(_ context.Context, _ Node) error {
	return nil
}

func (m *NodeProviderMock) CreateEbsNode(_ context.Context, _ string, _ string) error {
	return nil
}

func (m *NodeProviderMock) Modify(_ context.Context, _ string, _ NodeModify) error {
	return nil
}

func (m *NodeProviderMock) Delete(_ context.Context, _ string) error {
	return nil
}

func (m *NodeProviderMock) Lost(_ context.Context, _ string) error {
	return nil
}

func (m *NodeProviderMock) Reconnect(_ context.Context, _ string) error {
	return nil
}

func (m *NodeProviderMock) GetNetInterfaces(_ context.Context, _ string, _ ...*ListOpts) ([]NetInterface, error) {
	return nil, nil
}

func (m *NodeProviderMock) GetNetInterface(_ context.Context, _, _ string, _ ...*ListOpts) (NetInterface, error) {
	return NetInterface{}, nil
}

func (m *NodeProviderMock) CreateNetInterface(_ context.Context, _ string, _ NetInterface) error {
	return nil
}

func (m *NodeProviderMock) ModifyNetInterface(_ context.Context, _, _ string, _ NetInterface) error {
	return nil
}

func (m *NodeProviderMock) DeleteNetinterface(_ context.Context, _, _ string) error {
	return nil
}

func (m *NodeProviderMock) GetStoragePoolView(_ context.Context, _ ...*ListOpts) ([]StoragePool, error) {
	return nil, nil
}
func (m *NodeProviderMock) GetStoragePools(_ context.Context, _ string, _ ...*ListOpts) ([]StoragePool, error) {
	return nil, nil
}

func (m *NodeProviderMock) GetStoragePool(_ context.Context, _, _ string, _ ...*ListOpts) (StoragePool, error) {
	return StoragePool{}, nil
}
func (m *NodeProviderMock) CreateStoragePool(_ context.Context, _ string, _ StoragePool) error {
	return nil
}
func (m *NodeProviderMock) ModifyStoragePool(_ context.Context, _, _ string, _ GenericPropsModify) error {
	return nil
}
func (m *NodeProviderMock) DeleteStoragePool(_ context.Context, _, _ string) error {
	return nil
}
func (m *NodeProviderMock) CreateDevicePool(_ context.Context, _ string, _ PhysicalStorageCreate) error {
	return nil
}
func (m *NodeProviderMock) GetPhysicalStorageView(_ context.Context, _ ...*ListOpts) ([]PhysicalStorageViewItem, error) {
	return nil, nil
}
func (m *NodeProviderMock) GetPhysicalStorage(_ context.Context, _ string) ([]PhysicalStorageNode, error) {
	return nil, nil
}
func (m *NodeProviderMock) GetStoragePoolPropsInfos(_ context.Context, _ string, _ ...*ListOpts) ([]PropsInfo, error) {
	return nil, nil
}
func (m *NodeProviderMock) GetPropsInfos(_ context.Context, _ ...*ListOpts) ([]PropsInfo, error) {
	return nil, nil
}
func (m *NodeProviderMock) Evict(_ context.Context, _ string) error {
	return nil
}
func (m *NodeProviderMock) Restore(_ context.Context, _ string, _ NodeRestore) error {
	return nil
}
func (m *NodeProviderMock) Evacuate(_ context.Context, _ string) error {
	return nil
}

func getAndValidateNotReconciledRSC(ctx context.Context, cl client.Client, testName string) srv.ReplicatedStorageClass {
	replicatedSC, err := getRSC(ctx, cl, testName)
	Expect(err).NotTo(HaveOccurred())
	Expect(replicatedSC.Name).To(Equal(testName))
	Expect(replicatedSC.Finalizers).To(BeNil())
	Expect(replicatedSC.Status.Phase).To(Equal(""))
	Expect(replicatedSC.Status.Reason).To(Equal(""))

	return replicatedSC
}

func getAndValidateReconciledRSC(ctx context.Context, cl client.Client, testName string) srv.ReplicatedStorageClass {
	replicatedSC, err := getRSC(ctx, cl, testName)
	Expect(err).NotTo(HaveOccurred())
	Expect(replicatedSC.Name).To(Equal(testName))
	Expect(replicatedSC.Finalizers).To(ContainElement(controller.ReplicatedStorageClassFinalizerName))
	Expect(replicatedSC.Status).NotTo(BeNil())

	return replicatedSC
}

func getAndValidateSC(ctx context.Context, cl client.Client, replicatedSC srv.ReplicatedStorageClass) *storagev1.StorageClass {
	volumeBindingMode := getVolumeBindingMode(replicatedSC.Spec.VolumeAccess)

	storageClass, err := getSC(ctx, cl, replicatedSC.Name, replicatedSC.Namespace)
	Expect(err).NotTo(HaveOccurred())
	Expect(storageClass).NotTo(BeNil())
	Expect(storageClass.Name).To(Equal(replicatedSC.Name))
	Expect(storageClass.Namespace).To(Equal(replicatedSC.Namespace))
	Expect(storageClass.Provisioner).To(Equal(controller.StorageClassProvisioner))
	Expect(*storageClass.AllowVolumeExpansion).To(BeTrue())
	Expect(*storageClass.VolumeBindingMode).To(Equal(volumeBindingMode))
	Expect(*storageClass.ReclaimPolicy).To(Equal(corev1.PersistentVolumeReclaimPolicy(replicatedSC.Spec.ReclaimPolicy)))

	return storageClass
}

func getRSC(ctx context.Context, cl client.Client, name string) (srv.ReplicatedStorageClass, error) {
	replicatedSC := srv.ReplicatedStorageClass{}
	err := cl.Get(ctx, client.ObjectKey{
		Name:      name,
		Namespace: testNamespaceConst,
	}, &replicatedSC)

	return replicatedSC, err
}

func getSC(ctx context.Context, cl client.Client, name, namespace string) (*storagev1.StorageClass, error) {
	storageClass := &storagev1.StorageClass{}
	err := cl.Get(ctx, client.ObjectKey{
		Name:      name,
		Namespace: namespace,
	}, storageClass)

	return storageClass, err
}

func createConfigMap(ctx context.Context, cl client.Client, namespace string, data map[string]string) error {
	name := "sds-replicated-volume-controller-config"
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: data,
	}
	err := cl.Create(ctx, configMap)
	return err
}

func getConfigMap(ctx context.Context, cl client.Client, namespace string) (*corev1.ConfigMap, error) {
	name := "sds-replicated-volume-controller-config"
	configMap := &corev1.ConfigMap{}
	err := cl.Get(ctx, client.ObjectKey{
		Name:      name,
		Namespace: namespace,
	}, configMap)

	return configMap, err
}

func getVolumeBindingMode(volumeAccess string) storagev1.VolumeBindingMode {
	if volumeAccess == controller.VolumeAccessAny {
		return storagev1.VolumeBindingImmediate
	}

	return storagev1.VolumeBindingWaitForFirstConsumer
}
