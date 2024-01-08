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
	"sds-drbd-controller/api/v1alpha1"
	"sds-drbd-controller/pkg/controller"
	"strings"

	lapi "github.com/LINBIT/golinstor/client"
	llog "github.com/sirupsen/logrus"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/zap/zapcore"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe(controller.DRBDStoragePoolControllerName, func() {
	const (
		testNameSpace = "test_namespace"
		testName      = "test_name"
	)

	var (
		ctx   = context.Background()
		cl    = newFakeClient()
		log   = zap.New(zap.Level(zapcore.Level(-1)), zap.UseDevMode(true))
		lc, _ = lapi.NewClient(lapi.Log(llog.StandardLogger()))

		testDRBDSP = &v1alpha1.DRBDStoragePool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testName,
				Namespace: testNameSpace,
			},
		}
	)

	It("GetDRBDStoragePool", func() {
		err := cl.Create(ctx, testDRBDSP)
		Expect(err).NotTo(HaveOccurred())

		drbdsp, err := controller.GetDRBDStoragePool(ctx, cl, testNameSpace, testName)
		Expect(err).NotTo(HaveOccurred())
		Expect(drbdsp.Name).To(Equal(testName))
		Expect(drbdsp.Namespace).To(Equal(testNameSpace))
	})

	It("UpdateDRBDStoragePool", func() {
		const (
			testLblKey   = "test_label_key"
			testLblValue = "test_label_value"
		)

		Expect(testDRBDSP.Labels[testLblKey]).To(Equal(""))

		drbdspLabs := map[string]string{testLblKey: testLblValue}
		testDRBDSP.Labels = drbdspLabs

		err := controller.UpdateDRBDStoragePool(ctx, cl, testDRBDSP)
		Expect(err).NotTo(HaveOccurred())

		updateddrbdsp, _ := controller.GetDRBDStoragePool(ctx, cl, testNameSpace, testName)
		Expect(updateddrbdsp.Labels[testLblKey]).To(Equal(testLblValue))
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

			GoodDRBDStoragePoolName = "gooddrbdoperatorstoragepool"
			BadDRBDStoragePoolName  = "baddrbdoperatorstoragepool"
			TypeLVMThin             = "LVMThin"
			TypeLVM                 = "LVM"
			LVMVGTypeLocal          = "Local"
			LVMVGTypeShared         = "Shared"
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
		err = CreateDRBDStoragePool(ctx, cl, GoodDRBDStoragePoolName, testNameSpace, TypeLVM, goodLVMvgs)
		Expect(err).NotTo(HaveOccurred())

		goodDRBDStoragePool, err := controller.GetDRBDStoragePool(ctx, cl, testNameSpace, GoodDRBDStoragePoolName)
		Expect(err).NotTo(HaveOccurred())

		goodDRBDStoragePoolrequest := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: goodDRBDStoragePool.ObjectMeta.Namespace, Name: goodDRBDStoragePool.ObjectMeta.Name}}
		shouldRequeue, err := controller.ReconcileDRBDStoragePoolEvent(ctx, cl, goodDRBDStoragePoolrequest, log, lc)
		Expect(err).To(HaveOccurred()) // TODO: add mock for linstor client and change to Expect(err).NotTo(HaveOccurred()) and Expect(shouldRequeue).To(BeFalse())
		Expect(shouldRequeue).To(BeTrue())

		reconciledGoodDRBDStoragePool, err := controller.GetDRBDStoragePool(ctx, cl, testNameSpace, GoodDRBDStoragePoolName)
		Expect(err).NotTo(HaveOccurred())
		Expect(reconciledGoodDRBDStoragePool.Status.Phase).To(Equal("Failed"))
		pattern := `Error getting LINSTOR Storage Pool gooddrbdoperatorstoragepool on node first_node on vg actualVG-1-on-FirstNode: Get "http://(localhost|\[::1\]|127\.0\.0\.1):3370/v1/nodes/first_node/storage-pools/gooddrbdoperatorstoragepool": dial tcp (\[::1\]|127\.0\.0\.1):3370: connect: connection refused`
		Expect(reconciledGoodDRBDStoragePool.Status.Reason).To(MatchRegexp(pattern))

		// Negative test with bad LVMVolumeGroups.

		// err = CreateDRBDStoragePool(ctx, cl, BadDRBDStoragePoolName, testNameSpace, TypeLVM, []map[string]string{{LvmVGOneOnFirstNodeName: ""}, {NotExistedlvnVGName: ""}, {LvmVGOneOnSecondNodeName: ""}, {LvmVGTwoOnFirstNodeName: ""}, {LvmVGOneOnSecondNodeNameDublicate: ""}})

		badLVMvgs := []map[string]string{{LvmVGOneOnFirstNodeName: ""}, {NotExistedlvmVGName: ""}, {LvmVGOneOnSecondNodeName: ""}, {LvmVGTwoOnFirstNodeName: ""}, {LvmVGOneOnSecondNodeNameDublicate: ""}, {SharedLvmVGName: ""}, {LvmVGWithSeveralNodes: ""}}
		err = CreateDRBDStoragePool(ctx, cl, BadDRBDStoragePoolName, testNameSpace, TypeLVM, badLVMvgs)

		Expect(err).NotTo(HaveOccurred())

		badDRBDStoragePool, err := controller.GetDRBDStoragePool(ctx, cl, testNameSpace, BadDRBDStoragePoolName)
		Expect(err).NotTo(HaveOccurred())

		badDRBDStoragePoolrequest := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: badDRBDStoragePool.ObjectMeta.Namespace, Name: badDRBDStoragePool.ObjectMeta.Name}}
		shouldRequeue, err = controller.ReconcileDRBDStoragePoolEvent(ctx, cl, badDRBDStoragePoolrequest, log, lc)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		expectedMsg := `lvmVG-1-on-SecondNode: LvmVolumeGroup name is not unique
lvmVG-2-on-FirstNode: This LvmVolumeGroup have same node first_node as LvmVolumeGroup with name: lvmVG-1-on-FirstNode. LINSTOR Storage Pool is allowed to have only one LvmVolumeGroup per node
not_existed_lvmVG: Error getting LVMVolumeGroup: lvmvolumegroups.storage.deckhouse.io "not_existed_lvmVG" not found
several_nodes_lvm_vg: LvmVolumeGroup has more than one node in status.nodes. LvmVolumeGroup for LINSTOR Storage Pool must to have only one node
shared_lvm_vg: LvmVolumeGroup type is not Local`
		reconciledBadDRBDStoragePool, err := controller.GetDRBDStoragePool(ctx, cl, testNameSpace, BadDRBDStoragePoolName)
		Expect(err).NotTo(HaveOccurred())
		Expect(reconciledBadDRBDStoragePool.Status.Phase).To(Equal("Failed"))
		Expect(strings.TrimSpace(reconciledBadDRBDStoragePool.Status.Reason)).To(Equal(strings.TrimSpace(expectedMsg)))
		//Expect(reconciledBadDRBDStoragePool.Status.Reason).To(Equal("s"))

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

func CreateDRBDStoragePool(ctx context.Context, cl client.WithWatch, drbdStoragePoolName, namespace, lvmType string, lvmVolumeGroups []map[string]string) error {

	volumeGroups := make([]v1alpha1.DRBDStoragePoolLVMVolumeGroups, 0)
	for i := range lvmVolumeGroups {
		for key, value := range lvmVolumeGroups[i] {
			volumeGroups = append(volumeGroups, v1alpha1.DRBDStoragePoolLVMVolumeGroups{
				Name:         key,
				ThinPoolName: value,
			})
		}
	}

	drbdsp := &v1alpha1.DRBDStoragePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      drbdStoragePoolName,
			Namespace: namespace,
		},
		Spec: v1alpha1.DRBDStoragePoolSpec{
			Type:            "LVM",
			LvmVolumeGroups: volumeGroups,
		},
	}

	err := cl.Create(ctx, drbdsp)
	return err
}
