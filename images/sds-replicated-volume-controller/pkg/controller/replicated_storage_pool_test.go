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
	"sds-replicated-volume-controller/api/v1alpha1"
	"sds-replicated-volume-controller/pkg/controller"
	"sds-replicated-volume-controller/pkg/logger"
	"strings"

	lapi "github.com/LINBIT/golinstor/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe(controller.ReplicatedStoragePoolControllerName, func() {
	const (
		testNameSpace = "test_namespace"
		testName      = "test_name"
	)

	var (
		ctx    = context.Background()
		cl     = newFakeClient()
		log, _ = logger.NewLogger("2")
		lc, _  = lapi.NewClient(lapi.Log(log))

		testReplicatedSP = &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testName,
				Namespace: testNameSpace,
			},
		}
	)

	It("GetReplicatedStoragePool", func() {
		err := cl.Create(ctx, testReplicatedSP)
		Expect(err).NotTo(HaveOccurred())

		replicatedsp, err := controller.GetReplicatedStoragePool(ctx, cl, testNameSpace, testName)
		Expect(err).NotTo(HaveOccurred())
		Expect(replicatedsp.Name).To(Equal(testName))
		Expect(replicatedsp.Namespace).To(Equal(testNameSpace))
	})

	It("UpdateReplicatedStoragePool", func() {
		const (
			testLblKey   = "test_label_key"
			testLblValue = "test_label_value"
		)

		Expect(testReplicatedSP.Labels[testLblKey]).To(Equal(""))

		replicatedspLabs := map[string]string{testLblKey: testLblValue}
		testReplicatedSP.Labels = replicatedspLabs

		err := controller.UpdateReplicatedStoragePool(ctx, cl, testReplicatedSP)
		Expect(err).NotTo(HaveOccurred())

		updatedreplicatedsp, _ := controller.GetReplicatedStoragePool(ctx, cl, testNameSpace, testName)
		Expect(updatedreplicatedsp.Labels[testLblKey]).To(Equal(testLblValue))
	})

	It("UpdateMapValue", func() {
		m := make(map[string]string)

		// Test adding a new key-value pair
		controller.UpdateMapValue(m, "key1", "value1")
		Expect(m["key1"]).To(Equal("value1"))

		// Test updating an existing key-value pair
		controller.UpdateMapValue(m, "key1", "value2")
		Expect(m["key1"]).To(Equal("value1. Also: value2"))

		// Test another updating an existing key-value pair
		controller.UpdateMapValue(m, "key1", "value3")
		Expect(m["key1"]).To(Equal("value1. Also: value2. Also: value3"))

		// Test adding another new key-value pair
		controller.UpdateMapValue(m, "key2", "value2")
		Expect(m["key2"]).To(Equal("value2"))

		// Test updating an existing key-value pair with an empty value
		controller.UpdateMapValue(m, "key2", "")
		Expect(m["key2"]).To(Equal("value2. Also: "))

		// Test adding a new key-value pair with an empty key
		controller.UpdateMapValue(m, "", "value3")
		Expect(m[""]).To(Equal("value3"))
	})

	It("GetLvmVolumeGroup", func() {
		testLvm := &v1alpha1.LvmVolumeGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testName,
				Namespace: testNameSpace,
			},
		}

		err := cl.Create(ctx, testLvm)
		Expect(err).NotTo(HaveOccurred())

		lvm, err := controller.GetLvmVolumeGroup(ctx, cl, testNameSpace, testName)
		Expect(err).NotTo(HaveOccurred())
		Expect(lvm.Name).To(Equal(testName))
		Expect(lvm.Namespace).To(Equal(testNameSpace))
	})

	It("Validations", func() {
		const (
			LvmVGOneOnFirstNodeName    = "lvmVG-1-on-FirstNode"
			ActualVGOneOnFirstNodeName = "actualVG-1-on-FirstNode"

			LvmVGTwoOnFirstNodeName    = "lvmVG-2-on-FirstNode"
			ActualVGTwoOnFirstNodeName = "actualVG-2-on-FirstNode"

			LvmVGOneOnSecondNodeName          = "lvmVG-1-on-SecondNode"
			LvmVGOneOnSecondNodeNameDublicate = "lvmVG-1-on-SecondNode"
			ActualVGOneOnSecondNodeName       = "actualVG-1-on-SecondNode"

			NotExistedlvmVGName   = "not_existed_lvmVG"
			SharedLvmVGName       = "shared_lvm_vg"
			LvmVGWithSeveralNodes = "several_nodes_lvm_vg"

			FirstNodeName  = "first_node"
			SecondNodeName = "second_node"
			ThirdNodeName  = "third_node"

			GoodReplicatedStoragePoolName = "goodreplicatedoperatorstoragepool"
			BadReplicatedStoragePoolName  = "badreplicatedoperatorstoragepool"
			TypeLVMThin                   = "LVMThin"
			TypeLVM                       = "LVM"
			LVMVGTypeLocal                = "Local"
			LVMVGTypeShared               = "Shared"
		)

		err := CreateLVMVolumeGroup(ctx, cl, LvmVGOneOnFirstNodeName, testNameSpace, LVMVGTypeLocal, ActualVGOneOnFirstNodeName, []string{FirstNodeName}, nil)
		Expect(err).NotTo(HaveOccurred())

		err = CreateLVMVolumeGroup(ctx, cl, LvmVGTwoOnFirstNodeName, testNameSpace, LVMVGTypeLocal, ActualVGTwoOnFirstNodeName, []string{FirstNodeName}, nil)
		Expect(err).NotTo(HaveOccurred())

		err = CreateLVMVolumeGroup(ctx, cl, LvmVGOneOnSecondNodeName, testNameSpace, LVMVGTypeLocal, ActualVGOneOnSecondNodeName, []string{SecondNodeName}, nil)
		Expect(err).NotTo(HaveOccurred())

		err = CreateLVMVolumeGroup(ctx, cl, SharedLvmVGName, testNameSpace, LVMVGTypeShared, ActualVGOneOnSecondNodeName, []string{FirstNodeName, SecondNodeName, ThirdNodeName}, nil)
		Expect(err).NotTo(HaveOccurred())

		err = CreateLVMVolumeGroup(ctx, cl, LvmVGWithSeveralNodes, testNameSpace, LVMVGTypeLocal, ActualVGOneOnSecondNodeName, []string{FirstNodeName, SecondNodeName, ThirdNodeName}, nil)
		Expect(err).NotTo(HaveOccurred())

		// TODO: add mock for linstor client and add positive test

		// Negative test with good LVMVolumeGroups.
		goodLVMvgs := []map[string]string{{LvmVGOneOnFirstNodeName: ""}, {LvmVGOneOnSecondNodeName: ""}}
		err = CreateReplicatedStoragePool(ctx, cl, GoodReplicatedStoragePoolName, testNameSpace, TypeLVM, goodLVMvgs)
		Expect(err).NotTo(HaveOccurred())

		goodReplicatedStoragePool, err := controller.GetReplicatedStoragePool(ctx, cl, testNameSpace, GoodReplicatedStoragePoolName)
		Expect(err).NotTo(HaveOccurred())

		goodReplicatedStoragePoolrequest := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: goodReplicatedStoragePool.ObjectMeta.Namespace, Name: goodReplicatedStoragePool.ObjectMeta.Name}}
		shouldRequeue, err := controller.ReconcileReplicatedStoragePoolEvent(ctx, cl, goodReplicatedStoragePoolrequest, *log, lc)
		Expect(err).To(HaveOccurred()) // TODO: add mock for linstor client and change to Expect(err).NotTo(HaveOccurred()) and Expect(shouldRequeue).To(BeFalse())
		Expect(shouldRequeue).To(BeTrue())

		reconciledGoodReplicatedStoragePool, err := controller.GetReplicatedStoragePool(ctx, cl, testNameSpace, GoodReplicatedStoragePoolName)
		Expect(err).NotTo(HaveOccurred())
		Expect(reconciledGoodReplicatedStoragePool.Status.Phase).To(Equal("Failed"))
		pattern := `Error getting LINSTOR Storage Pool goodreplicatedoperatorstoragepool on node first_node on vg actualVG-1-on-FirstNode: Get "http://(localhost|\[::1\]|127\.0\.0\.1):3370/v1/nodes/first_node/storage-pools/goodreplicatedoperatorstoragepool": dial tcp (\[::1\]|127\.0\.0\.1):3370: connect: connection refused`
		Expect(reconciledGoodReplicatedStoragePool.Status.Reason).To(MatchRegexp(pattern))

		// Negative test with bad LVMVolumeGroups.

		// err = CreateReplicatedStoragePool(ctx, cl, BadReplicatedStoragePoolName, testNameSpace, TypeLVM, []map[string]string{{LvmVGOneOnFirstNodeName: ""}, {NotExistedlvnVGName: ""}, {LvmVGOneOnSecondNodeName: ""}, {LvmVGTwoOnFirstNodeName: ""}, {LvmVGOneOnSecondNodeNameDublicate: ""}})

		badLVMvgs := []map[string]string{{LvmVGOneOnFirstNodeName: ""}, {NotExistedlvmVGName: ""}, {LvmVGOneOnSecondNodeName: ""}, {LvmVGTwoOnFirstNodeName: ""}, {LvmVGOneOnSecondNodeNameDublicate: ""}, {SharedLvmVGName: ""}, {LvmVGWithSeveralNodes: ""}}
		err = CreateReplicatedStoragePool(ctx, cl, BadReplicatedStoragePoolName, testNameSpace, TypeLVM, badLVMvgs)

		Expect(err).NotTo(HaveOccurred())

		badReplicatedStoragePool, err := controller.GetReplicatedStoragePool(ctx, cl, testNameSpace, BadReplicatedStoragePoolName)
		Expect(err).NotTo(HaveOccurred())

		badReplicatedStoragePoolrequest := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: badReplicatedStoragePool.ObjectMeta.Namespace, Name: badReplicatedStoragePool.ObjectMeta.Name}}
		shouldRequeue, err = controller.ReconcileReplicatedStoragePoolEvent(ctx, cl, badReplicatedStoragePoolrequest, *log, lc)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		expectedMsg := `lvmVG-1-on-SecondNode: LvmVolumeGroup name is not unique
lvmVG-2-on-FirstNode: This LvmVolumeGroup have same node first_node as LvmVolumeGroup with name: lvmVG-1-on-FirstNode. LINSTOR Storage Pool is allowed to have only one LvmVolumeGroup per node
not_existed_lvmVG: Error getting LVMVolumeGroup: lvmvolumegroups.storage.deckhouse.io "not_existed_lvmVG" not found
several_nodes_lvm_vg: LvmVolumeGroup has more than one node in status.nodes. LvmVolumeGroup for LINSTOR Storage Pool must to have only one node
shared_lvm_vg: LvmVolumeGroup type is not Local`
		reconciledBadReplicatedStoragePool, err := controller.GetReplicatedStoragePool(ctx, cl, testNameSpace, BadReplicatedStoragePoolName)
		Expect(err).NotTo(HaveOccurred())
		Expect(reconciledBadReplicatedStoragePool.Status.Phase).To(Equal("Failed"))
		Expect(strings.TrimSpace(reconciledBadReplicatedStoragePool.Status.Reason)).To(Equal(strings.TrimSpace(expectedMsg)))
		//Expect(reconciledBadReplicatedStoragePool.Status.Reason).To(Equal("s"))

	})
})

func CreateLVMVolumeGroup(ctx context.Context, cl client.WithWatch, lvmVolumeGroupName, namespace, lvmVGType, actualVGnameOnTheNode string, nodes []string, thinPools map[string]string) error {
	vgNodes := make([]v1alpha1.LvmVolumeGroupNode, len(nodes))
	for i, node := range nodes {
		vgNodes[i] = v1alpha1.LvmVolumeGroupNode{Name: node}
	}

	vgThinPools := make([]v1alpha1.SpecThinPool, 0)
	for thinPoolname, thinPoolsize := range thinPools {
		vgThinPools = append(vgThinPools, v1alpha1.SpecThinPool{Name: thinPoolname, Size: thinPoolsize})
	}

	lvmVolumeGroup := &v1alpha1.LvmVolumeGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      lvmVolumeGroupName,
			Namespace: namespace,
		},
		Spec: v1alpha1.LvmVolumeGroupSpec{
			Type:                  lvmVGType,
			ActualVGNameOnTheNode: actualVGnameOnTheNode,
			ThinPools:             vgThinPools,
		},
		Status: v1alpha1.LvmVolumeGroupStatus{
			Nodes: vgNodes,
		},
	}
	err := cl.Create(ctx, lvmVolumeGroup)
	return err
}

func CreateReplicatedStoragePool(ctx context.Context, cl client.WithWatch, replicatedStoragePoolName, namespace, lvmType string, lvmVolumeGroups []map[string]string) error {

	volumeGroups := make([]v1alpha1.ReplicatedStoragePoolLVMVolumeGroups, 0)
	for i := range lvmVolumeGroups {
		for key, value := range lvmVolumeGroups[i] {
			volumeGroups = append(volumeGroups, v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
				Name:         key,
				ThinPoolName: value,
			})
		}
	}

	replicatedsp := &v1alpha1.ReplicatedStoragePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      replicatedStoragePoolName,
			Namespace: namespace,
		},
		Spec: v1alpha1.ReplicatedStoragePoolSpec{
			Type:            "LVM",
			LvmVolumeGroups: volumeGroups,
		},
	}

	err := cl.Create(ctx, replicatedsp)
	return err
}
