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

	linstor "github.com/LINBIT/golinstor/client"
	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sds-replicated-volume-controller/pkg/controller"
	"sds-replicated-volume-controller/pkg/logger"
)

var _ = Describe(controller.LinstorNodeControllerName, func() {
	const (
		secretName = "test_name"
		secretNS   = "test_NS"
	)

	var (
		ctx       = context.Background()
		cl        = newFakeClient()
		cfgSecret *v1.Secret

		testSecret = &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: secretNS,
			},
		}
	)

	It("GetKubernetesSecretByName", func() {
		err := cl.Create(ctx, testSecret)
		Expect(err).NotTo(HaveOccurred())

		cfgSecret, err = controller.GetKubernetesSecretByName(ctx, cl, secretName, secretNS)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfgSecret.Name).To(Equal(secretName))
		Expect(cfgSecret.Namespace).To(Equal(secretNS))
	})

	const (
		testLblKey = "test_label_key"
		testLblVal = "test_label_value"
	)

	It("GetNodeSelectorFromConfig", func() {
		cfgSecret.Data = make(map[string][]byte)
		cfgSecret.Data["config"] = []byte(fmt.Sprintf("{\"nodeSelector\":{\"%s\":\"%s\"}}", testLblKey, testLblVal))

		cfgNodeSelector, err := controller.GetNodeSelectorFromConfig(*cfgSecret)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfgNodeSelector[testLblKey]).To(Equal(testLblVal))
	})

	const (
		testNodeName    = "test_node_name"
		testNodeAddress = "test_address"
	)
	var (
		selectedKubeNodes *v1.NodeList
	)

	It("GetKubernetesNodesBySelector", func() {
		cfgNodeSelector := map[string]string{}
		testLabels := map[string]string{testLblKey: testLblVal}
		testNode := v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:   testNodeName,
				Labels: testLabels,
			},
			Status: v1.NodeStatus{
				Addresses: []v1.NodeAddress{
					{
						Address: testNodeAddress,
					},
				},
			},
		}

		err := cl.Create(ctx, &testNode)
		Expect(err).NotTo(HaveOccurred())

		selectedKubeNodes, err = controller.GetKubernetesNodesBySelector(ctx, cl, cfgNodeSelector)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(selectedKubeNodes.Items)).To(Equal(1))

		actualNode := selectedKubeNodes.Items[0]
		Expect(actualNode.ObjectMeta.Name).To(Equal(testNodeName))
		Expect(actualNode.ObjectMeta.Labels).To(Equal(testLabels))
		Expect(actualNode.Status.Addresses[0].Address).To(Equal(testNodeAddress))
	})

	It("GetAllKubernetesNodes", func() {
		allKubsNodes, err := controller.GetAllKubernetesNodes(ctx, cl)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(allKubsNodes.Items)).To(Equal(1))

		kubNode := allKubsNodes.Items[0]
		Expect(kubNode.Name).To(Equal(testNodeName))
	})

	It("ContainsNode", func() {
		const (
			existName = "exist"
		)
		nodes := &v1.NodeList{Items: []v1.Node{
			{ObjectMeta: metav1.ObjectMeta{
				Name: existName,
			}},
		}}
		existingNode := v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: existName,
			},
		}
		absentNode := v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "absentName",
			},
		}

		exists := controller.ContainsNode(nodes, existingNode)
		Expect(exists).To(BeTrue())

		absent := controller.ContainsNode(nodes, absentNode)
		Expect(absent).To(BeFalse())
	})

	It("DiffNodeLists", func() {
		nodeList1 := &v1.NodeList{}
		nodeList1.Items = []v1.Node{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node1",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node2",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node3",
				},
			},
		}

		nodeList2 := &v1.NodeList{}
		nodeList2.Items = []v1.Node{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node1",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node3",
				},
			},
		}
		expectedNodesToRemove := []v1.Node{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node2",
				},
			},
		}

		actualNodesToRemove := controller.DiffNodeLists(nodeList1, nodeList2)
		Expect(actualNodesToRemove.Items).To(Equal(expectedNodesToRemove))
	})

	var (
		mockLc *linstor.Client
	)

	It("AddOrConfigureDRBDNodes", func() {
		mockLc, err := NewLinstorClientWithMockNodes()
		Expect(err).NotTo(HaveOccurred())

		log := logger.Logger{}
		drbdNodeSelector := map[string]string{controller.SdsReplicatedVolumeNodeSelectorKey: ""}
		replicatedStorageClasses := srv.ReplicatedStorageClassList{}
		Expect(err).NotTo(HaveOccurred())

		err = controller.AddOrConfigureDRBDNodes(ctx, cl, mockLc, log, selectedKubeNodes, []linstor.Node{}, replicatedStorageClasses, drbdNodeSelector)
		Expect(err).NotTo(HaveOccurred())
	})

	var (
		drbdNodeProps map[string]string
	)

	It("KubernetesNodeLabelsToProperties", func() {
		const (
			testValue1 = "test_value1"
			testValue2 = "test_value2"
		)

		var (
			testKey1 = controller.AllowedLabels[0]
			testKey2 = controller.AllowedLabels[1]
		)

		kubeNodeLabels := map[string]string{
			testKey1: testValue1,
			testKey2: testValue2,
		}

		drbdNodeProps := controller.KubernetesNodeLabelsToProperties(kubeNodeLabels)
		Expect(drbdNodeProps["Aux/registered-by"]).To(Equal(controller.LinstorNodeControllerName))
		Expect(drbdNodeProps["Aux/"+testKey1]).To(Equal(testValue1))
		Expect(drbdNodeProps["Aux/"+testKey2]).To(Equal(testValue2))
	})

	It("ConfigureDRBDNode", func() {
		err := controller.ConfigureDRBDNode(ctx, mockLc, linstor.Node{}, drbdNodeProps)
		Expect(err).NotTo(HaveOccurred())
	})
})
