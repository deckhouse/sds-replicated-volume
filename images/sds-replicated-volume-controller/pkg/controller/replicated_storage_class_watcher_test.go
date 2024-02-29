package controller

import (
	"context"
	client2 "github.com/LINBIT/golinstor/client"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/strings/slices"
	"sds-replicated-volume-controller/api/v1alpha1"
	"sds-replicated-volume-controller/pkg/logger"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
)

func TestReplicatedStorageClassWatcher(t *testing.T) {
	var (
		cl        = newFakeClient()
		ctx       = context.Background()
		log       = logger.Logger{}
		namespace = "test_namespace"
	)

	t.Run("ReconcileReplicatedStorageClassPools_returns_correctly_and_sets_label", func(t *testing.T) {
		const (
			firstName  = "first"
			secondName = "second"
			badName    = "bad"
			firstSp    = "sp1"
			secondSp   = "sp2"
			thirdSp    = "sp3"
		)

		dscs := map[string]v1alpha1.ReplicatedStorageClass{
			firstName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      firstName,
					Namespace: namespace,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: firstSp,
				},
			},
			secondName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      secondName,
					Namespace: namespace,
				},

				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: secondSp,
				},
			},
			badName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      badName,
					Namespace: namespace,
				},

				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: "unknown",
				},
			},
		}

		sc := &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      badName,
				Namespace: namespace,
			},
		}

		err := cl.Create(ctx, sc)
		if err != nil {
			t.Error(err)
		} else {
			defer func() {
				err = cl.Delete(ctx, sc)
				if err != nil {
					t.Error(err)
				}
			}()
		}

		sps := map[string][]client2.StoragePool{
			firstSp:  {},
			secondSp: {},
			thirdSp:  {},
		}

		expected := map[string]v1alpha1.ReplicatedStorageClass{
			firstName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      firstName,
					Namespace: namespace,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: firstSp,
				},
			},
			secondName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      secondName,
					Namespace: namespace,
				},

				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: secondSp,
				},
			},
		}

		actual := ReconcileReplicatedStorageClassPools(ctx, cl, log, dscs, sps)
		assert.Equal(t, expected, actual)

		badSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      badName,
		}, badSc)
		if assert.NoError(t, err) {
			_, exist := badSc.Labels[NonOperationalByStoragePool]
			assert.True(t, exist)
		}
	})

	t.Run("ReconcileReplicatedStorageClassPools_returns_correctly_and_removes_label", func(t *testing.T) {
		const (
			firstName  = "first"
			secondName = "second"
			badName    = "bad"
			firstSp    = "sp1"
			secondSp   = "sp2"
			thirdSp    = "sp3"
		)

		dscs := map[string]v1alpha1.ReplicatedStorageClass{
			firstName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      firstName,
					Namespace: namespace,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: firstSp,
				},
			},
			secondName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      secondName,
					Namespace: namespace,
				},

				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: secondSp,
				},
			},
			badName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      badName,
					Namespace: namespace,
				},

				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: thirdSp,
				},
			},
		}

		sc := &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      badName,
				Namespace: namespace,
			},
		}

		err := cl.Create(ctx, sc)
		if err != nil {
			t.Error(err)
		} else {
			defer func() {
				err = cl.Delete(ctx, sc)
				if err != nil {
					t.Error(err)
				}
			}()
		}

		sps := map[string][]client2.StoragePool{
			firstSp:  {},
			secondSp: {},
		}

		expected := map[string]v1alpha1.ReplicatedStorageClass{
			firstName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      firstName,
					Namespace: namespace,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: firstSp,
				},
			},
			secondName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      secondName,
					Namespace: namespace,
				},

				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: secondSp,
				},
			},
		}

		actual := ReconcileReplicatedStorageClassPools(ctx, cl, log, dscs, sps)
		assert.Equal(t, expected, actual)

		badSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      badName,
		}, badSc)
		if assert.NoError(t, err) {
			_, exist := badSc.Labels[NonOperationalByStoragePool]
			assert.True(t, exist)
		}

		newSps := map[string][]client2.StoragePool{
			firstSp:  {},
			secondSp: {},
			thirdSp:  {},
		}

		newExpected := map[string]v1alpha1.ReplicatedStorageClass{
			firstName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      firstName,
					Namespace: namespace,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: firstSp,
				},
			},
			secondName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      secondName,
					Namespace: namespace,
				},

				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: secondSp,
				},
			},
			badName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      badName,
					Namespace: namespace,
				},

				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: thirdSp,
				},
			},
		}

		newActual := ReconcileReplicatedStorageClassPools(ctx, cl, log, dscs, newSps)
		assert.Equal(t, newExpected, newActual)

		updatedBadSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      badName,
		}, updatedBadSc)
		if assert.NoError(t, err) {
			_, exist := updatedBadSc.Labels[NonOperationalByStoragePool]
			assert.False(t, exist)
		}

	})

	t.Run("SortNodesByStoragePool_returns_correctly", func(t *testing.T) {
		const (
			node1  = "node1"
			node2  = "node2"
			node3  = "node3"
			spName = "test-sp"
		)

		sps := map[string][]client2.StoragePool{
			spName: {
				{
					NodeName:        node1,
					StoragePoolName: spName,
				},
				{
					NodeName:        node2,
					StoragePoolName: spName,
				},
			},
		}

		nodeList := &v1.NodeList{
			Items: []v1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: node1,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: node2,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: node3,
					},
				},
			},
		}
		expected := map[string][]v1.Node{
			spName: {nodeList.Items[0], nodeList.Items[1]},
		}

		actual := SortNodesByStoragePool(nodeList, sps)
		assert.Equal(t, expected, actual)
	})

	t.Run("GetAllReplicatedStorageClasses_returns_ReplicatedStorageClasses", func(t *testing.T) {
		const (
			firstName  = "first"
			secondName = "second"
		)

		dscs := []v1alpha1.ReplicatedStorageClass{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      firstName,
					Namespace: namespace,
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secondName,
					Namespace: namespace,
				},
			},
		}

		var err error
		for _, dsc := range dscs {
			err = cl.Create(ctx, &dsc)
			if err != nil {
				t.Error(err)
			}
		}

		if err == nil {
			defer func() {
				for _, dsc := range dscs {
					err = cl.Delete(ctx, &dsc)
					if err != nil {
						t.Error(err)
					}
				}
			}()
		}

		actual, err := GetAllReplicatedStorageClasses(ctx, cl)
		if assert.NoError(t, err) {
			assert.Equal(t, 2, len(actual))
			_, exist := actual[firstName]
			assert.True(t, exist)
			_, exist = actual[secondName]
			assert.True(t, exist)
		}
	})

	t.Run("GetReplicatedStoragePoolsZones_returns_zones", func(t *testing.T) {
		const (
			labelNode1  = "label-node1"
			labelNode2  = "label-node2"
			noLabelNode = "no-label-node"
			zone1       = "test-zone1"
			zone2       = "test-zone2"
			dspName     = "dsp-test"
		)
		nodeList := v1.NodeList{
			Items: []v1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode1,
						Labels: map[string]string{
							ZoneLabel: zone1,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode2,
						Labels: map[string]string{
							ZoneLabel: zone2,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode,
					},
				},
			},
		}

		spNodes := map[string][]v1.Node{
			dspName: nodeList.Items,
		}

		actual := GetReplicatedStoragePoolsZones(spNodes)
		assert.True(t, slices.Contains(actual[dspName], zone1))
		assert.True(t, slices.Contains(actual[dspName], zone2))
	})

	t.Run("ReconcileReplicatedStorageClassReplication_replication_Availability_topology_Zonal_not_enough_nodes_label_sc", func(t *testing.T) {
		const (
			labelNode1  = "label-node1"
			noLabelNode = "no-label-node"
			zone1       = "test-zone1"
			dscName     = "dsc-test"
			dspName     = "dsp-test"
		)

		nodeList := &v1.NodeList{
			Items: []v1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode1,
						Labels: map[string]string{
							ZoneLabel: zone1,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode,
					},
				},
			},
		}

		spNodes := map[string][]v1.Node{
			dspName: nodeList.Items,
		}

		dscs := map[string]v1alpha1.ReplicatedStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: ReplicationAvailability,
					Topology:    TopologyZonal,
					StoragePool: dspName,
				},
			},
		}

		sc := &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dscName,
				Namespace: namespace,
			},
		}

		err := cl.Create(ctx, sc)
		if err != nil {
			t.Error(err)
		} else {
			defer func() {
				err = cl.Delete(ctx, sc)
				if err != nil {
					t.Error(err)
				}
			}()
		}

		ReconcileReplicatedStorageClassReplication(ctx, cl, log, dscs, spNodes)

		updatedSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, updatedSc)

		_, exist := updatedSc.Labels[NonOperationalByReplicasLabel]
		assert.True(t, exist)
	})

	t.Run("ReconcileReplicatedStorageClassReplication_replication_Availability_topology_Zonal_enough_nodes_no_label_sc", func(t *testing.T) {
		const (
			labelNode1  = "label-node1"
			labelNode2  = "label-node2"
			noLabelNode = "no-label-node"
			zone1       = "test-zone1"
			dscName     = "dsc-test"
			dspName     = "dsp-test"
		)

		nodeList := &v1.NodeList{
			Items: []v1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode1,
						Labels: map[string]string{
							ZoneLabel: zone1,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode2,
						Labels: map[string]string{
							ZoneLabel: zone1,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode,
						Labels: map[string]string{
							ZoneLabel: zone1,
						},
					},
				},
			},
		}

		spNodes := map[string][]v1.Node{
			dspName: nodeList.Items,
		}

		dscs := map[string]v1alpha1.ReplicatedStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: ReplicationAvailability,
					Topology:    TopologyZonal,
					StoragePool: dspName,
				},
			},
		}

		sc := &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dscName,
				Namespace: namespace,
			},
		}

		err := cl.Create(ctx, sc)
		if err != nil {
			t.Error(err)
		} else {
			defer func() {
				err = cl.Delete(ctx, sc)
				if err != nil {
					t.Error(err)
				}
			}()
		}

		ReconcileReplicatedStorageClassReplication(ctx, cl, log, dscs, spNodes)

		updatedSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, updatedSc)

		_, exist := updatedSc.Labels[NonOperationalByReplicasLabel]
		assert.False(t, exist)
	})

	t.Run("ReconcileReplicatedStorageClassReplication_replication_Availability_topology_TransZonal_not_enough_nodes_label_sc", func(t *testing.T) {
		const (
			labelNode1  = "label-node1"
			labelNode2  = "label-node2"
			noLabelNode = "no-label-node"
			zone1       = "test-zone1"
			zone2       = "test-zone2"
			zone3       = "test-zone3"
			dscName     = "dsc-test"
			dspName     = "dsp-test"
		)

		nodeList := &v1.NodeList{
			Items: []v1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode1,
						Labels: map[string]string{
							ZoneLabel: zone1,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode2,
						Labels: map[string]string{
							ZoneLabel: zone1,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode,
						Labels: map[string]string{
							ZoneLabel: zone1,
						},
					},
				},
			},
		}

		spNodes := map[string][]v1.Node{
			dspName: nodeList.Items,
		}

		dscs := map[string]v1alpha1.ReplicatedStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: ReplicationAvailability,
					Zones:       []string{zone1, zone2, zone3},
					Topology:    TopologyTransZonal,
					StoragePool: dspName,
				},
			},
		}

		sc := &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dscName,
				Namespace: namespace,
			},
		}

		err := cl.Create(ctx, sc)
		if err != nil {
			t.Error(err)
		} else {
			defer func() {
				err = cl.Delete(ctx, sc)
				if err != nil {
					t.Error(err)
				}
			}()
		}

		ReconcileReplicatedStorageClassReplication(ctx, cl, log, dscs, spNodes)

		updatedSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, updatedSc)

		_, exist := updatedSc.Labels[NonOperationalByReplicasLabel]
		assert.True(t, exist)
	})

	t.Run("ReconcileReplicatedStorageClassReplication_replication_Availability_topology_Ignored_not_enough_nodes_label_sc", func(t *testing.T) {
		const (
			noLabelNode1 = "no-label-node1"
			noLabelNode2 = "no-label-node2"
			dscName      = "dsc-test"
			dspName      = "dsp-test"
		)

		nodeList := &v1.NodeList{
			Items: []v1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode1,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode2,
					},
				},
			},
		}

		spNodes := map[string][]v1.Node{
			dspName: nodeList.Items,
		}

		dscs := map[string]v1alpha1.ReplicatedStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: ReplicationAvailability,
					Topology:    TopologyIgnored,
					StoragePool: dspName,
				},
			},
		}

		sc := &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dscName,
				Namespace: namespace,
			},
		}

		err := cl.Create(ctx, sc)
		if err != nil {
			t.Error(err)
		} else {
			defer func() {
				err = cl.Delete(ctx, sc)
				if err != nil {
					t.Error(err)
				}
			}()
		}

		ReconcileReplicatedStorageClassReplication(ctx, cl, log, dscs, spNodes)

		updatedSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, updatedSc)

		_, exist := updatedSc.Labels[NonOperationalByReplicasLabel]
		assert.True(t, exist)
	})

	t.Run("ReconcileReplicatedStorageClassReplication_replication_Availability_topology_TransZonal_enough_nodes_no_label_sc", func(t *testing.T) {
		const (
			labelNode1  = "label-node1"
			labelNode2  = "label-node2"
			noLabelNode = "no-label-node"
			zone1       = "test-zone1"
			zone2       = "test-zone2"
			zone3       = "test-zone3"
			dscName     = "dsc-test"
			dspName     = "dsp-test"
		)

		nodeList := &v1.NodeList{
			Items: []v1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode1,
						Labels: map[string]string{
							ZoneLabel: zone1,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode2,
						Labels: map[string]string{
							ZoneLabel: zone2,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode,
						Labels: map[string]string{
							ZoneLabel: zone3,
						},
					},
				},
			},
		}

		spNodes := map[string][]v1.Node{
			dspName: nodeList.Items,
		}

		dscs := map[string]v1alpha1.ReplicatedStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: ReplicationAvailability,
					Zones:       []string{zone1, zone2, zone3},
					Topology:    TopologyTransZonal,
					StoragePool: dspName,
				},
			},
		}

		sc := &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dscName,
				Namespace: namespace,
			},
		}

		err := cl.Create(ctx, sc)
		if err != nil {
			t.Error(err)
		} else {
			defer func() {
				err = cl.Delete(ctx, sc)
				if err != nil {
					t.Error(err)
				}
			}()
		}

		ReconcileReplicatedStorageClassReplication(ctx, cl, log, dscs, spNodes)

		updatedSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, updatedSc)

		_, exist := updatedSc.Labels[NonOperationalByReplicasLabel]
		assert.False(t, exist)
	})

	t.Run("ReconcileReplicatedStorageClassReplication_replication_Availability_topology_Ignored_enough_nodes_no_label_sc", func(t *testing.T) {
		const (
			noLabelNode1 = "no-label-node1"
			noLabelNode2 = "no-label-node2"
			noLabelNode3 = "no-label-node3"
			dscName      = "dsc-test"
			dspName      = "dsp-test"
		)

		nodeList := &v1.NodeList{
			Items: []v1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode1,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode2,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode3,
					},
				},
			},
		}

		spNodes := map[string][]v1.Node{
			dspName: nodeList.Items,
		}

		dscs := map[string]v1alpha1.ReplicatedStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: ReplicationAvailability,
					Topology:    TopologyIgnored,
					StoragePool: dspName,
				},
			},
		}

		sc := &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dscName,
				Namespace: namespace,
			},
		}

		err := cl.Create(ctx, sc)
		if err != nil {
			t.Error(err)
		} else {
			defer func() {
				err = cl.Delete(ctx, sc)
				if err != nil {
					t.Error(err)
				}
			}()
		}

		ReconcileReplicatedStorageClassReplication(ctx, cl, log, dscs, spNodes)

		updatedSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, updatedSc)

		_, exist := updatedSc.Labels[NonOperationalByReplicasLabel]
		assert.False(t, exist)
	})

	t.Run("ReconcileReplicatedStorageClassReplication_replication_Availability_topology_Zonal_removes_label_sc", func(t *testing.T) {
		const (
			noLabelNode1 = "no-label-node1"
			noLabelNode2 = "no-label-node2"
			noLabelNode  = "no-label-node3"
			zone1        = "test-zone1"
			dscName      = "dsc-test"
			dspName      = "dsp-test"
		)

		nodeList := &v1.NodeList{
			Items: []v1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode1,
						Labels: map[string]string{
							ZoneLabel: zone1,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode,
					},
				},
			},
		}

		spNodes := map[string][]v1.Node{
			dspName: nodeList.Items,
		}

		dscs := map[string]v1alpha1.ReplicatedStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: ReplicationAvailability,
					Topology:    TopologyZonal,
					StoragePool: dspName,
				},
			},
		}

		sc := &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dscName,
				Namespace: namespace,
			},
		}

		err := cl.Create(ctx, sc)
		if err != nil {
			t.Error(err)
		} else {
			defer func() {
				err = cl.Delete(ctx, sc)
				if err != nil {
					t.Error(err)
				}
			}()
		}

		ReconcileReplicatedStorageClassReplication(ctx, cl, log, dscs, spNodes)

		updatedSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, updatedSc)

		_, exist := updatedSc.Labels[NonOperationalByReplicasLabel]
		assert.True(t, exist)

		updatedNodeList := &v1.NodeList{
			Items: []v1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode1,
						Labels: map[string]string{
							ZoneLabel: zone1,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode2,
						Labels: map[string]string{
							ZoneLabel: zone1,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode,
						Labels: map[string]string{
							ZoneLabel: zone1,
						},
					},
				},
			},
		}

		spNodes = map[string][]v1.Node{
			dspName: updatedNodeList.Items,
		}

		ReconcileReplicatedStorageClassReplication(ctx, cl, log, dscs, spNodes)

		scWithNoLabel := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, scWithNoLabel)

		_, exist = scWithNoLabel.Labels[NonOperationalByReplicasLabel]
		assert.False(t, exist)
	})

	t.Run("ReconcileReplicatedStorageClassReplication_replication_Availability_topology_TransZonal_removes_label_sc", func(t *testing.T) {
		const (
			labelNode1  = "label-node1"
			labelNode2  = "label-node2"
			noLabelNode = "no-label-node"
			zone1       = "test-zone1"
			zone2       = "test-zone2"
			zone3       = "test-zone3"
			dscName     = "dsc-test"
			dspName     = "dsp-test"
		)

		nodeList := &v1.NodeList{
			Items: []v1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode1,
						Labels: map[string]string{
							ZoneLabel: zone1,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode,
					},
				},
			},
		}

		spNodes := map[string][]v1.Node{
			dspName: nodeList.Items,
		}

		dscs := map[string]v1alpha1.ReplicatedStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: ReplicationAvailability,
					Zones:       []string{zone1, zone2, zone3},
					Topology:    TopologyTransZonal,
					StoragePool: dspName,
				},
			},
		}

		sc := &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dscName,
				Namespace: namespace,
			},
		}

		err := cl.Create(ctx, sc)
		if err != nil {
			t.Error(err)
		} else {
			defer func() {
				err = cl.Delete(ctx, sc)
				if err != nil {
					t.Error(err)
				}
			}()
		}

		ReconcileReplicatedStorageClassReplication(ctx, cl, log, dscs, spNodes)

		updatedSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, updatedSc)

		_, exist := updatedSc.Labels[NonOperationalByReplicasLabel]
		assert.True(t, exist)

		updatedNodeList := &v1.NodeList{
			Items: []v1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode1,
						Labels: map[string]string{
							ZoneLabel: zone1,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode2,
						Labels: map[string]string{
							ZoneLabel: zone2,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode,
						Labels: map[string]string{
							ZoneLabel: zone3,
						},
					},
				},
			},
		}

		spNodes = map[string][]v1.Node{
			dspName: updatedNodeList.Items,
		}

		ReconcileReplicatedStorageClassReplication(ctx, cl, log, dscs, spNodes)

		scWithNoLabel := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, scWithNoLabel)

		_, exist = scWithNoLabel.Labels[NonOperationalByReplicasLabel]
		assert.False(t, exist)
	})

	t.Run("ReconcileReplicatedStorageClassReplication_replication_Availability_topology_Ignored_removes_label_sc", func(t *testing.T) {
		const (
			labelNode1  = "label-node1"
			labelNode2  = "label-node2"
			noLabelNode = "no-label-node"
			dscName     = "dsc-test"
			dspName     = "dsp-test"
		)

		nodeList := &v1.NodeList{
			Items: []v1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode1,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode,
					},
				},
			},
		}

		spNodes := map[string][]v1.Node{
			dspName: nodeList.Items,
		}

		dscs := map[string]v1alpha1.ReplicatedStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: ReplicationAvailability,
					Topology:    TopologyIgnored,
					StoragePool: dspName,
				},
			},
		}

		sc := &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dscName,
				Namespace: namespace,
			},
		}

		err := cl.Create(ctx, sc)
		if err != nil {
			t.Error(err)
		} else {
			defer func() {
				err = cl.Delete(ctx, sc)
				if err != nil {
					t.Error(err)
				}
			}()
		}

		ReconcileReplicatedStorageClassReplication(ctx, cl, log, dscs, spNodes)

		updatedSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, updatedSc)

		_, exist := updatedSc.Labels[NonOperationalByReplicasLabel]
		assert.True(t, exist)

		updatedNodeList := &v1.NodeList{
			Items: []v1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode1,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode2,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode,
					},
				},
			},
		}

		spNodes = map[string][]v1.Node{
			dspName: updatedNodeList.Items,
		}

		ReconcileReplicatedStorageClassReplication(ctx, cl, log, dscs, spNodes)

		scWithNoLabel := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, scWithNoLabel)

		_, exist = scWithNoLabel.Labels[NonOperationalByReplicasLabel]
		assert.False(t, exist)
	})

	t.Run("ReconcileReplicatedStorageClassReplication_replication_ConsistencyAndAvailability_topology_Zonal_not_enough_nodes_label_sc", func(t *testing.T) {
		const (
			labelNode1  = "label-node1"
			labelNode2  = "label-node2"
			noLabelNode = "no-label-node"
			zone1       = "test-zone1"
			zone2       = "test-zone2"
			dscName     = "dsc-test"
			dspName     = "dsp-test"
		)

		nodeList := &v1.NodeList{
			Items: []v1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode1,
						Labels: map[string]string{
							ZoneLabel: zone1,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode2,
						Labels: map[string]string{
							ZoneLabel: zone2,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode,
					},
				},
			},
		}

		spNodes := map[string][]v1.Node{
			dspName: nodeList.Items,
		}

		dscs := map[string]v1alpha1.ReplicatedStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: ReplicationConsistencyAndAvailability,
					Topology:    TopologyZonal,
					StoragePool: dspName,
				},
			},
		}

		sc := &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dscName,
				Namespace: namespace,
			},
		}

		err := cl.Create(ctx, sc)
		if err != nil {
			t.Error(err)
		} else {
			defer func() {
				err = cl.Delete(ctx, sc)
				if err != nil {
					t.Error(err)
				}
			}()
		}

		ReconcileReplicatedStorageClassReplication(ctx, cl, log, dscs, spNodes)

		updatedSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, updatedSc)

		_, exist := updatedSc.Labels[NonOperationalByReplicasLabel]
		assert.True(t, exist)
	})

	t.Run("ReconcileReplicatedStorageClassReplication_replication_ConsistencyAndAvailability_topology_Zonal_enough_nodes_no_label_sc", func(t *testing.T) {
		const (
			labelNode1 = "label-node1"
			labelNode2 = "label-node2"
			labelNode3 = "label-node3"
			zone1      = "test-zone1"
			dscName    = "dsc-test"
			dspName    = "dsp-test"
		)

		nodeList := &v1.NodeList{
			Items: []v1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode1,
						Labels: map[string]string{
							ZoneLabel: zone1,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode2,
						Labels: map[string]string{
							ZoneLabel: zone1,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode3,
						Labels: map[string]string{
							ZoneLabel: zone1,
						},
					},
				},
			},
		}

		spNodes := map[string][]v1.Node{
			dspName: nodeList.Items,
		}

		dscs := map[string]v1alpha1.ReplicatedStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: ReplicationConsistencyAndAvailability,
					Topology:    TopologyZonal,
					StoragePool: dspName,
				},
			},
		}

		sc := &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dscName,
				Namespace: namespace,
			},
		}

		err := cl.Create(ctx, sc)
		if err != nil {
			t.Error(err)
		} else {
			defer func() {
				err = cl.Delete(ctx, sc)
				if err != nil {
					t.Error(err)
				}
			}()
		}

		ReconcileReplicatedStorageClassReplication(ctx, cl, log, dscs, spNodes)

		updatedSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, updatedSc)

		_, exist := updatedSc.Labels[NonOperationalByZonesLabel]
		assert.False(t, exist)
	})

	t.Run("ReconcileReplicatedStorageClassReplication_replication_ConsistencyAndAvailability_topology_TransZonal_not_enough_nodes_label_sc", func(t *testing.T) {
		const (
			labelNode1 = "label-node1"
			labelNode2 = "label-node2"
			labelNode3 = "label-node3"
			zone1      = "test-zone1"
			zone2      = "test-zone2"
			zone3      = "test-zone3"
			dscName    = "dsc-test"
			dspName    = "dsp-test"
		)

		nodeList := &v1.NodeList{
			Items: []v1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode1,
						Labels: map[string]string{
							ZoneLabel: zone1,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode2,
						Labels: map[string]string{
							ZoneLabel: zone1,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode3,
						Labels: map[string]string{
							ZoneLabel: zone1,
						},
					},
				},
			},
		}

		spNodes := map[string][]v1.Node{
			dspName: nodeList.Items,
		}

		dscs := map[string]v1alpha1.ReplicatedStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: ReplicationConsistencyAndAvailability,
					Topology:    TopologyTransZonal,
					Zones:       []string{zone1, zone2, zone3},
					StoragePool: dspName,
				},
			},
		}

		sc := &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dscName,
				Namespace: namespace,
			},
		}

		err := cl.Create(ctx, sc)
		if err != nil {
			t.Error(err)
		} else {
			defer func() {
				err = cl.Delete(ctx, sc)
				if err != nil {
					t.Error(err)
				}
			}()
		}

		ReconcileReplicatedStorageClassReplication(ctx, cl, log, dscs, spNodes)

		updatedSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, updatedSc)

		_, exist := updatedSc.Labels[NonOperationalByReplicasLabel]
		assert.True(t, exist)
	})

	t.Run("ReconcileReplicatedStorageClassReplication_replication_ConsistencyAndAvailability_topology_TransZonal_enough_nodes_no_label_sc", func(t *testing.T) {
		const (
			labelNode1 = "label-node1"
			labelNode2 = "label-node2"
			labelNode3 = "label-node3"
			zone1      = "test-zone1"
			zone2      = "test-zone2"
			zone3      = "test-zone3"
			dscName    = "dsc-test"
			dspName    = "dsp-test"
		)

		nodeList := &v1.NodeList{
			Items: []v1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode1,
						Labels: map[string]string{
							ZoneLabel: zone1,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode2,
						Labels: map[string]string{
							ZoneLabel: zone2,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode3,
						Labels: map[string]string{
							ZoneLabel: zone3,
						},
					},
				},
			},
		}

		spNodes := map[string][]v1.Node{
			dspName: nodeList.Items,
		}

		dscs := map[string]v1alpha1.ReplicatedStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: ReplicationConsistencyAndAvailability,
					Topology:    TopologyTransZonal,
					Zones:       []string{zone1, zone2, zone3},
					StoragePool: dspName,
				},
			},
		}

		sc := &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dscName,
				Namespace: namespace,
			},
		}

		err := cl.Create(ctx, sc)
		if err != nil {
			t.Error(err)
		} else {
			defer func() {
				err = cl.Delete(ctx, sc)
				if err != nil {
					t.Error(err)
				}
			}()
		}

		ReconcileReplicatedStorageClassReplication(ctx, cl, log, dscs, spNodes)

		updatedSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, updatedSc)

		_, exist := updatedSc.Labels[NonOperationalByReplicasLabel]
		assert.False(t, exist)
	})

	t.Run("ReconcileReplicatedStorageClassReplication_replication_ConsistencyAndAvailability_topology_Ignored_not_enough_nodes_label_sc", func(t *testing.T) {
		const (
			noLabelNode1 = "no-label-node1"
			noLabelNode2 = "no-label-node2"
			dscName      = "dsc-test"
			dspName      = "dsp-test"
		)

		nodeList := &v1.NodeList{
			Items: []v1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode1,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode2,
					},
				},
			},
		}

		spNodes := map[string][]v1.Node{
			dspName: nodeList.Items,
		}

		dscs := map[string]v1alpha1.ReplicatedStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: ReplicationConsistencyAndAvailability,
					Topology:    TopologyIgnored,
					StoragePool: dspName,
				},
			},
		}

		sc := &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dscName,
				Namespace: namespace,
			},
		}

		err := cl.Create(ctx, sc)
		if err != nil {
			t.Error(err)
		} else {
			defer func() {
				err = cl.Delete(ctx, sc)
				if err != nil {
					t.Error(err)
				}
			}()
		}

		ReconcileReplicatedStorageClassReplication(ctx, cl, log, dscs, spNodes)

		updatedSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, updatedSc)

		_, exist := updatedSc.Labels[NonOperationalByReplicasLabel]
		assert.True(t, exist)
	})

	t.Run("ReconcileReplicatedStorageClassReplication_replication_ConsistencyAndAvailability_topology_Ignored_enough_nodes_no_label_sc", func(t *testing.T) {
		const (
			noLabelNode1 = "no-label-node1"
			noLabelNode2 = "no-label-node2"
			noLabelNode3 = "no-label-node3"
			dscName      = "dsc-test"
			dspName      = "dsp-test"
		)

		nodeList := &v1.NodeList{
			Items: []v1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode1,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode2,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode3,
					},
				},
			},
		}

		spNodes := map[string][]v1.Node{
			dspName: nodeList.Items,
		}

		dscs := map[string]v1alpha1.ReplicatedStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: ReplicationConsistencyAndAvailability,
					Topology:    TopologyIgnored,
					StoragePool: dspName,
				},
			},
		}

		sc := &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dscName,
				Namespace: namespace,
			},
		}

		err := cl.Create(ctx, sc)
		if err != nil {
			t.Error(err)
		} else {
			defer func() {
				err = cl.Delete(ctx, sc)
				if err != nil {
					t.Error(err)
				}
			}()
		}

		ReconcileReplicatedStorageClassReplication(ctx, cl, log, dscs, spNodes)

		updatedSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, updatedSc)

		_, exist := updatedSc.Labels[NonOperationalByReplicasLabel]
		assert.False(t, exist)
	})

	t.Run("ReconcileReplicatedStorageClassReplication_replication_ConsistencyAndAvailability_topology_Zonal_removes_label_sc", func(t *testing.T) {
		const (
			labelNode1  = "label-node1"
			labelNode2  = "label-node2"
			labelNode3  = "label-node3"
			noLabelNode = "no-label-node"
			zone1       = "test-zone1"
			zone2       = "test-zone2"
			dscName     = "dsc-test"
			dspName     = "dsp-test"
		)

		nodeList := &v1.NodeList{
			Items: []v1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode1,
						Labels: map[string]string{
							ZoneLabel: zone1,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode2,
						Labels: map[string]string{
							ZoneLabel: zone2,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode,
					},
				},
			},
		}

		spNodes := map[string][]v1.Node{
			dspName: nodeList.Items,
		}

		dscs := map[string]v1alpha1.ReplicatedStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: ReplicationConsistencyAndAvailability,
					Topology:    TopologyZonal,
					StoragePool: dspName,
				},
			},
		}

		sc := &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dscName,
				Namespace: namespace,
			},
		}

		err := cl.Create(ctx, sc)
		if err != nil {
			t.Error(err)
		} else {
			defer func() {
				err = cl.Delete(ctx, sc)
				if err != nil {
					t.Error(err)
				}
			}()
		}

		ReconcileReplicatedStorageClassReplication(ctx, cl, log, dscs, spNodes)

		updatedSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, updatedSc)

		_, exist := updatedSc.Labels[NonOperationalByReplicasLabel]
		assert.True(t, exist)

		updatedNodeList := &v1.NodeList{
			Items: []v1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode1,
						Labels: map[string]string{
							ZoneLabel: zone1,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode2,
						Labels: map[string]string{
							ZoneLabel: zone1,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode3,
						Labels: map[string]string{
							ZoneLabel: zone1,
						},
					},
				},
			},
		}

		spNodes = map[string][]v1.Node{
			dspName: updatedNodeList.Items,
		}

		ReconcileReplicatedStorageClassReplication(ctx, cl, log, dscs, spNodes)

		scWithNoLabel := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, scWithNoLabel)

		_, exist = scWithNoLabel.Labels[NonOperationalByReplicasLabel]
		assert.False(t, exist)
	})

	t.Run("ReconcileReplicatedStorageClassReplication_replication_ConsistencyAndAvailability_topology_TransZonal_removes_label_sc", func(t *testing.T) {
		const (
			labelNode1  = "label-node1"
			labelNode2  = "label-node2"
			labelNode3  = "label-node3"
			noLabelNode = "no-label-node"
			zone1       = "test-zone1"
			zone2       = "test-zone2"
			zone3       = "test-zone3"
			dscName     = "dsc-test"
			dspName     = "dsp-test"
		)

		nodeList := &v1.NodeList{
			Items: []v1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode1,
						Labels: map[string]string{
							ZoneLabel: zone1,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode2,
						Labels: map[string]string{
							ZoneLabel: zone2,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode,
					},
				},
			},
		}

		spNodes := map[string][]v1.Node{
			dspName: nodeList.Items,
		}

		dscs := map[string]v1alpha1.ReplicatedStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: ReplicationConsistencyAndAvailability,
					Topology:    TopologyTransZonal,
					Zones:       []string{zone1, zone2, zone3},
					StoragePool: dspName,
				},
			},
		}

		sc := &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dscName,
				Namespace: namespace,
			},
		}

		err := cl.Create(ctx, sc)
		if err != nil {
			t.Error(err)
		} else {
			defer func() {
				err = cl.Delete(ctx, sc)
				if err != nil {
					t.Error(err)
				}
			}()
		}

		ReconcileReplicatedStorageClassReplication(ctx, cl, log, dscs, spNodes)

		updatedSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, updatedSc)

		_, exist := updatedSc.Labels[NonOperationalByReplicasLabel]
		assert.True(t, exist)

		updatedNodeList := &v1.NodeList{
			Items: []v1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode1,
						Labels: map[string]string{
							ZoneLabel: zone1,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode2,
						Labels: map[string]string{
							ZoneLabel: zone2,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: labelNode3,
						Labels: map[string]string{
							ZoneLabel: zone3,
						},
					},
				},
			},
		}

		spNodes = map[string][]v1.Node{
			dspName: updatedNodeList.Items,
		}

		ReconcileReplicatedStorageClassReplication(ctx, cl, log, dscs, spNodes)

		scWithNoLabel := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, scWithNoLabel)

		_, exist = scWithNoLabel.Labels[NonOperationalByReplicasLabel]
		assert.False(t, exist)
	})

	t.Run("ReconcileReplicatedStorageClassReplication_replication_ConsistencyAndAvailability_topology_Ignored_removes_label_sc", func(t *testing.T) {
		const (
			noLabelNode1 = "no-label-node1"
			noLabelNode2 = "no-label-node2"
			noLabelNode3 = "no-label-node3"
			dscName      = "dsc-test"
			dspName      = "dsp-test"
		)

		nodeList := &v1.NodeList{
			Items: []v1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode1,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode2,
					},
				},
			},
		}

		spNodes := map[string][]v1.Node{
			dspName: nodeList.Items,
		}

		dscs := map[string]v1alpha1.ReplicatedStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: ReplicationConsistencyAndAvailability,
					Topology:    TopologyIgnored,
					StoragePool: dspName,
				},
			},
		}

		sc := &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dscName,
				Namespace: namespace,
			},
		}

		err := cl.Create(ctx, sc)
		if err != nil {
			t.Error(err)
		} else {
			defer func() {
				err = cl.Delete(ctx, sc)
				if err != nil {
					t.Error(err)
				}
			}()
		}

		ReconcileReplicatedStorageClassReplication(ctx, cl, log, dscs, spNodes)

		updatedSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, updatedSc)

		_, exist := updatedSc.Labels[NonOperationalByReplicasLabel]
		assert.True(t, exist)

		updatedNodeList := &v1.NodeList{
			Items: []v1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode1,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode2,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: noLabelNode3,
					},
				},
			},
		}

		spNodes = map[string][]v1.Node{
			dspName: updatedNodeList.Items,
		}

		ReconcileReplicatedStorageClassReplication(ctx, cl, log, dscs, spNodes)

		scWithNoLabel := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, scWithNoLabel)

		_, exist = scWithNoLabel.Labels[NonOperationalByReplicasLabel]
		assert.False(t, exist)
	})

	t.Run("ReconcileReplicatedStorageClassZones_correct_zones_returns_healthy_dsc_no_label_sc", func(t *testing.T) {
		const (
			zone1   = "test-zone1"
			zone2   = "test-zone2"
			zone3   = "test-zone3"
			dscName = "dsp-test"
			dspName = "dsp-test"
		)

		sc := &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dscName,
				Namespace: namespace,
			},
		}

		err := cl.Create(ctx, sc)
		if err != nil {
			t.Error(err)
		} else {
			defer func() {
				err = cl.Delete(ctx, sc)
				if err != nil {
					t.Error(err)
				}
			}()
		}

		dscs := map[string]v1alpha1.ReplicatedStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: ReplicationConsistencyAndAvailability,
					Zones:       []string{zone1, zone2, zone3},
					StoragePool: dspName,
				},
			},
		}

		dspZones := map[string][]string{
			dspName: {zone1, zone2, zone3},
		}

		healthyDsc := ReconcileReplicatedStorageClassZones(ctx, cl, log, dscs, dspZones)
		_, healthy := healthyDsc[dscName]
		assert.True(t, healthy)

		scWithNoLabel := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, scWithNoLabel)

		_, exist := scWithNoLabel.Labels[NonOperationalByZonesLabel]
		assert.False(t, exist)
	})

	t.Run("ReconcileReplicatedStorageClassZones_incorrect_zones_doesnt_return_unhealthy_dsc_and_label_sc", func(t *testing.T) {
		const (
			zone1   = "test-zone1"
			zone2   = "test-zone2"
			zone3   = "test-zone3"
			dscName = "dsp-test"
			dspName = "dsp-test"
		)

		sc := &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dscName,
				Namespace: namespace,
			},
		}

		err := cl.Create(ctx, sc)
		if err != nil {
			t.Error(err)
		} else {
			defer func() {
				err = cl.Delete(ctx, sc)
				if err != nil {
					t.Error(err)
				}
			}()
		}

		dscs := map[string]v1alpha1.ReplicatedStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: ReplicationConsistencyAndAvailability,
					Zones:       []string{zone1, zone2, zone3},
					StoragePool: dspName,
				},
			},
		}

		dspZones := map[string][]string{
			dspName: {zone1, zone2},
		}

		healthyDsc := ReconcileReplicatedStorageClassZones(ctx, cl, log, dscs, dspZones)
		_, healthy := healthyDsc[dscName]
		assert.False(t, healthy)

		scWithNoLabel := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, scWithNoLabel)

		_, exist := scWithNoLabel.Labels[NonOperationalByZonesLabel]
		assert.True(t, exist)
	})

	t.Run("ReconcileReplicatedStorageClassZones_unhealthy_dsc_fixed_removes_label_sc", func(t *testing.T) {
		const (
			zone1   = "test-zone1"
			zone2   = "test-zone2"
			zone3   = "test-zone3"
			dscName = "dsp-test"
			dspName = "dsp-test"
		)

		sc := &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dscName,
				Namespace: namespace,
			},
		}

		err := cl.Create(ctx, sc)
		if err != nil {
			t.Error(err)
		} else {
			defer func() {
				err = cl.Delete(ctx, sc)
				if err != nil {
					t.Error(err)
				}
			}()
		}

		dscs := map[string]v1alpha1.ReplicatedStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: ReplicationConsistencyAndAvailability,
					Zones:       []string{zone1, zone2, zone3},
					StoragePool: dspName,
				},
			},
		}

		dspZones := map[string][]string{
			dspName: {zone1, zone2},
		}

		healthyDsc := ReconcileReplicatedStorageClassZones(ctx, cl, log, dscs, dspZones)
		_, healthy := healthyDsc[dscName]
		assert.False(t, healthy)

		scWithLbl := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, scWithLbl)

		_, exist := scWithLbl.Labels[NonOperationalByZonesLabel]
		assert.True(t, exist)

		updatedDspZones := map[string][]string{
			dspName: {zone1, zone2, zone3},
		}

		updatedHealthyDsc := ReconcileReplicatedStorageClassZones(ctx, cl, log, dscs, updatedDspZones)
		_, healthy = updatedHealthyDsc[dscName]
		assert.True(t, healthy)

		scWithNoLabel := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, scWithNoLabel)

		_, exist = scWithNoLabel.Labels[NonOperationalByZonesLabel]
		assert.False(t, exist)
	})
}

func newFakeClient() client.WithWatch {
	s := scheme.Scheme
	_ = metav1.AddMetaToScheme(s)
	_ = v1alpha1.AddToScheme(s)

	builder := fake.NewClientBuilder().WithScheme(s)

	cl := builder.Build()
	return cl
}
