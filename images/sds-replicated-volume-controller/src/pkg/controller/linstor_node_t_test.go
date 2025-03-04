package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	v12 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sds-replicated-volume-controller/pkg/logger"
)

func TestReconcileCSINodeLabelsIfDiffExists(t *testing.T) {
	ctx := context.Background()
	cl := newFakeClient()
	log := logger.Logger{}

	const (
		testNode1 = "test-node1"
		testNode2 = "test-node2"
		testNode3 = "test-node3"

		postfix = "test-sp"
	)

	labels := make(map[string]string, len(AllowedLabels)+len(AllowedPrefixes))
	for _, l := range AllowedLabels {
		labels[l] = ""
	}
	for _, p := range AllowedPrefixes {
		labels[p+postfix] = ""
	}
	labels["not-syncable-label"] = ""

	nodes := []v1.Node{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:   testNode1,
				Labels: labels,
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:   testNode2,
				Labels: labels,
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:   testNode3,
				Labels: labels,
			},
		},
	}

	topologyKeys := make([]string, 0, len(AllowedLabels)+len(AllowedPrefixes))
	topologyKeys = append(topologyKeys, AllowedLabels...)
	for _, lbl := range AllowedPrefixes {
		topologyKeys = append(topologyKeys, lbl+postfix)
	}

	randomKeys := []string{
		"random1",
		"random2",
		"random3",
	}
	topologyKeys = append(topologyKeys, randomKeys...)

	csiNodes := []v12.CSINode{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: testNode1,
			},
			Spec: v12.CSINodeSpec{
				Drivers: []v12.CSINodeDriver{
					{
						Name:         LinstorDriverName,
						TopologyKeys: topologyKeys,
					},
				},
			},
		}, {
			ObjectMeta: metav1.ObjectMeta{
				Name: testNode2,
			},
			Spec: v12.CSINodeSpec{
				Drivers: []v12.CSINodeDriver{
					{
						Name:         LinstorDriverName,
						TopologyKeys: topologyKeys,
					},
				},
			},
		}, {
			ObjectMeta: metav1.ObjectMeta{
				Name: testNode3,
			},
			Spec: v12.CSINodeSpec{
				Drivers: []v12.CSINodeDriver{
					{
						Name:         LinstorDriverName,
						TopologyKeys: topologyKeys,
					},
				},
			},
		},
	}

	var err error
	for _, n := range csiNodes {
		err = cl.Create(ctx, &n)
		if err != nil {
			t.Error(err)
		}
	}

	err = ReconcileCSINodeLabels(ctx, cl, log, nodes)
	if err != nil {
		t.Error(err)
	}

	expectedTopologyKeys := make([]string, 0, len(AllowedLabels)+len(AllowedPrefixes))
	expectedTopologyKeys = append(expectedTopologyKeys, AllowedLabels...)
	for _, lbl := range AllowedPrefixes {
		expectedTopologyKeys = append(expectedTopologyKeys, lbl+postfix)
	}

	syncedCSINodes := &v12.CSINodeList{}
	err = cl.List(ctx, syncedCSINodes)
	if err != nil {
		t.Error(err)
	}

	for _, n := range syncedCSINodes.Items {
		for _, d := range n.Spec.Drivers {
			if d.Name == LinstorDriverName {
				assert.ElementsMatch(t, d.TopologyKeys, expectedTopologyKeys)
				break
			}
		}
	}
}

func TestReconcileCSINodeLabelsIfDiffDoesNotExists(t *testing.T) {
	ctx := context.Background()
	cl := newFakeClient()
	log := logger.Logger{}

	const (
		testNode1 = "test-node1"
		testNode2 = "test-node2"
		testNode3 = "test-node3"

		postfix = "test-sp"
	)

	labels := make(map[string]string, len(AllowedLabels)+len(AllowedPrefixes))
	for _, l := range AllowedLabels {
		labels[l] = ""
	}
	for _, p := range AllowedPrefixes {
		labels[p+postfix] = ""
	}
	labels["not-syncable-label"] = ""

	nodes := []v1.Node{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:   testNode1,
				Labels: labels,
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:   testNode2,
				Labels: labels,
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:   testNode3,
				Labels: labels,
			},
		},
	}

	topologyKeys := make([]string, 0, len(AllowedLabels)+len(AllowedPrefixes))
	topologyKeys = append(topologyKeys, AllowedLabels...)
	for _, lbl := range AllowedPrefixes {
		topologyKeys = append(topologyKeys, lbl+postfix)
	}

	csiNodes := []v12.CSINode{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: testNode1,
			},
			Spec: v12.CSINodeSpec{
				Drivers: []v12.CSINodeDriver{
					{
						Name:         LinstorDriverName,
						TopologyKeys: topologyKeys,
					},
				},
			},
		}, {
			ObjectMeta: metav1.ObjectMeta{
				Name: testNode2,
			},
			Spec: v12.CSINodeSpec{
				Drivers: []v12.CSINodeDriver{
					{
						Name:         LinstorDriverName,
						TopologyKeys: topologyKeys,
					},
				},
			},
		}, {
			ObjectMeta: metav1.ObjectMeta{
				Name: testNode3,
			},
			Spec: v12.CSINodeSpec{
				Drivers: []v12.CSINodeDriver{
					{
						Name:         LinstorDriverName,
						TopologyKeys: topologyKeys,
					},
				},
			},
		},
	}

	var err error
	for _, n := range csiNodes {
		err = cl.Create(ctx, &n)
		if err != nil {
			t.Error(err)
		}
	}

	err = ReconcileCSINodeLabels(ctx, cl, log, nodes)
	if err != nil {
		t.Error(err)
	}

	expectedTopologyKeys := make([]string, 0, len(AllowedLabels)+len(AllowedPrefixes))
	expectedTopologyKeys = append(expectedTopologyKeys, AllowedLabels...)
	for _, lbl := range AllowedPrefixes {
		expectedTopologyKeys = append(expectedTopologyKeys, lbl+postfix)
	}

	syncedCSINodes := &v12.CSINodeList{}
	err = cl.List(ctx, syncedCSINodes)
	if err != nil {
		t.Error(err)
	}

	for _, n := range syncedCSINodes.Items {
		for _, d := range n.Spec.Drivers {
			if d.Name == LinstorDriverName {
				assert.ElementsMatch(t, d.TopologyKeys, expectedTopologyKeys)
				break
			}
		}
	}
}

func TestRenameLinbitLabels(t *testing.T) {
	const (
		linbitHostnameLabelValue             = "test-host"
		linbitDfltDisklessStorPoolLabelValue = "test-dflt"
		linbitStoragePoolPrefixLabelValue    = "test-sp"
		postfix                              = "postfix"

		SdsDfltDisklessStorPoolLabelKey    = "storage.deckhouse.io/sds-replicated-volume-sp-DfltDisklessStorPool"
		LinbitDfltDisklessStorPoolLabelKey = "linbit.com/sp-DfltDisklessStorPool"
	)
	ctx := context.Background()
	cl := newFakeClient()
	nodes := []v1.Node{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-node1",
				Labels: map[string]string{
					LinbitHostnameLabelKey:                    linbitHostnameLabelValue,
					LinbitDfltDisklessStorPoolLabelKey:        linbitDfltDisklessStorPoolLabelValue,
					LinbitStoragePoolPrefixLabelKey + postfix: linbitStoragePoolPrefixLabelValue,
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-node2",
				Labels: map[string]string{
					LinbitHostnameLabelKey:                    linbitHostnameLabelValue,
					LinbitDfltDisklessStorPoolLabelKey:        linbitDfltDisklessStorPoolLabelValue,
					LinbitStoragePoolPrefixLabelKey + postfix: linbitStoragePoolPrefixLabelValue,
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-node3",
				Labels: map[string]string{
					LinbitHostnameLabelKey:                    linbitHostnameLabelValue,
					LinbitDfltDisklessStorPoolLabelKey:        linbitDfltDisklessStorPoolLabelValue,
					LinbitStoragePoolPrefixLabelKey + postfix: linbitStoragePoolPrefixLabelValue,
				},
			},
		},
	}

	for _, n := range nodes {
		err := cl.Create(ctx, &n)
		if err != nil {
			t.Error(err)
		}
	}

	expected := map[string]v1.Node{
		"test-node1": {
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-node1",
				Labels: map[string]string{
					SdsHostnameLabelKey:                    linbitHostnameLabelValue,
					SdsDfltDisklessStorPoolLabelKey:        linbitDfltDisklessStorPoolLabelValue,
					SdsStoragePoolPrefixLabelKey + postfix: linbitStoragePoolPrefixLabelValue,
				},
			},
		},
		"test-node2": {
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-node2",
				Labels: map[string]string{
					SdsHostnameLabelKey:                    linbitHostnameLabelValue,
					SdsDfltDisklessStorPoolLabelKey:        linbitDfltDisklessStorPoolLabelValue,
					SdsStoragePoolPrefixLabelKey + postfix: linbitStoragePoolPrefixLabelValue,
				},
			},
		},
		"test-node3": {
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-node3",
				Labels: map[string]string{
					SdsHostnameLabelKey:                    linbitHostnameLabelValue,
					SdsDfltDisklessStorPoolLabelKey:        linbitDfltDisklessStorPoolLabelValue,
					SdsStoragePoolPrefixLabelKey + postfix: linbitStoragePoolPrefixLabelValue,
				},
			},
		},
	}

	err := renameLinbitLabels(ctx, cl, nodes)
	if err != nil {
		t.Error(err)
	}

	renamedNodes := &v1.NodeList{}
	err = cl.List(ctx, renamedNodes)
	if err != nil {
		t.Error(err)
	}

	for _, n := range renamedNodes.Items {
		exp := expected[n.Name]
		assert.Equal(t, n.Labels, exp.Labels)
	}
}
