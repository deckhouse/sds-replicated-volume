package controller

import (
	"context"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zapcore"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/strings/slices"
	"sds-drbd-controller/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"testing"
)

func TestDRBDStorageClassWatcher(t *testing.T) {
	var (
		cl        = newFakeClient()
		ctx       = context.Background()
		log       = zap.New(zap.Level(zapcore.Level(-1)), zap.UseDevMode(true))
		namespace = "test_namespace"
	)

	t.Run("GetAllDRBDStorageClasses_returns_DRBDStorageClasses", func(t *testing.T) {
		const (
			firstName  = "first"
			secondName = "second"
		)

		dscs := []v1alpha1.DRBDStorageClass{
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

		actual, err := GetAllDRBDStorageClasses(ctx, cl)
		if assert.NoError(t, err) {
			assert.Equal(t, 2, len(actual))
			_, exist := actual[firstName]
			assert.True(t, exist)
			_, exist = actual[secondName]
			assert.True(t, exist)
		}
	})

	t.Run("GetAllDRBDStoragePools_returns_DRBDStoragePools", func(t *testing.T) {
		const (
			firstName  = "first"
			secondName = "second"
		)

		dsps := []v1alpha1.DRBDStoragePool{
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
		for _, dsp := range dsps {
			err = cl.Create(ctx, &dsp)
			if err != nil {
				t.Error(err)
			}
		}

		if err == nil {
			defer func() {
				for _, dsc := range dsps {
					err = cl.Delete(ctx, &dsc)
					if err != nil {
						t.Error(err)
					}
				}
			}()
		}

		actual, err := GetAllDRBDStoragePools(ctx, cl)
		if assert.NoError(t, err) {
			assert.Equal(t, 2, len(actual))
			_, exist := actual[firstName]
			assert.True(t, exist)
			_, exist = actual[secondName]
			assert.True(t, exist)
		}
	})

	t.Run("GetAllLVMVolumeGroups_returns_LVMVolumeGroups", func(t *testing.T) {
		const (
			firstName  = "first"
			secondName = "second"
		)

		lvgs := []v1alpha1.LvmVolumeGroup{
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
		for _, lvg := range lvgs {
			err = cl.Create(ctx, &lvg)
			if err != nil {
				t.Error(err)
			}
		}

		if err == nil {
			defer func() {
				for _, dsc := range lvgs {
					err = cl.Delete(ctx, &dsc)
					if err != nil {
						t.Error(err)
					}
				}
			}()
		}

		actual, err := GetAllLVMVolumeGroups(ctx, cl)
		if assert.NoError(t, err) {
			assert.Equal(t, 2, len(actual.Items))
			mp := make(map[string]struct{}, len(actual.Items))
			for _, lvg := range actual.Items {
				mp[lvg.Name] = struct{}{}
			}

			_, exist := mp[firstName]
			assert.True(t, exist)
			_, exist = mp[secondName]
			assert.True(t, exist)
		}
	})

	t.Run("GetDRBDStoragePoolsZones_returns_zones", func(t *testing.T) {
		const (
			labelNode1  = "label-node1"
			labelNode2  = "label-node2"
			noLabelNode = "no-label-node"
			zone1       = "test-zone1"
			zone2       = "test-zone2"
			lvgName1    = "lvg-name1"
			lvgName2    = "lvg-name2"
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

		lvgList := v1alpha1.LvmVolumeGroupList{
			Items: []v1alpha1.LvmVolumeGroup{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: lvgName1,
					},
					Status: v1alpha1.LvmVolumeGroupStatus{
						Nodes: []v1alpha1.LvmVolumeGroupNode{
							{
								Name: labelNode1,
							},
							{
								Name: labelNode2,
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: lvgName2,
					},
					Status: v1alpha1.LvmVolumeGroupStatus{
						Nodes: []v1alpha1.LvmVolumeGroupNode{
							{
								Name: noLabelNode,
							},
						},
					},
				},
			},
		}

		dsps := map[string]v1alpha1.DRBDStoragePool{
			dspName: {
				ObjectMeta: metav1.ObjectMeta{
					Name: dspName,
				},
				Spec: v1alpha1.DRBDStoragePoolSpec{
					LvmVolumeGroups: []v1alpha1.DRBDStoragePoolLVMVolumeGroups{
						{
							Name: lvgName1,
						},
					},
				},
			},
		}

		actual := GetDRBDStoragePoolsZones(&nodeList, &lvgList, dsps)
		assert.True(t, slices.Contains(actual[dspName], zone1))
		assert.True(t, slices.Contains(actual[dspName], zone2))
	})

	t.Run("ReconcileDRBDStorageClassReplication_replication_Availability_topology_Zonal_not_enough_nodes_label_sc", func(t *testing.T) {
		const (
			labelNode1  = "label-node1"
			noLabelNode = "no-label-node"
			zone1       = "test-zone1"
			zone2       = "test-zone2"
			zone3       = "test-zone3"
			dscName     = "dsp-test"
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

		dscs := map[string]v1alpha1.DRBDStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.DRBDStorageClassSpec{
					Replication: ReplicationAvailability,
					Topology:    TopologyZonal,
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

		ReconcileDRBDStorageClassReplication(ctx, cl, log, dscs, nodeList)

		updatedSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, updatedSc)

		_, exist := updatedSc.Labels[NonOperationalByReplicasLabel]
		assert.True(t, exist)
	})

	t.Run("ReconcileDRBDStorageClassReplication_replication_Availability_topology_Zonal_enough_nodes_no_label_sc", func(t *testing.T) {
		const (
			labelNode1  = "label-node1"
			labelNode2  = "label-node2"
			noLabelNode = "no-label-node"
			zone1       = "test-zone1"
			dscName     = "dsp-test"
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

		dscs := map[string]v1alpha1.DRBDStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.DRBDStorageClassSpec{
					Replication: ReplicationAvailability,
					Topology:    TopologyZonal,
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

		ReconcileDRBDStorageClassReplication(ctx, cl, log, dscs, nodeList)

		updatedSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, updatedSc)

		_, exist := updatedSc.Labels[NonOperationalByReplicasLabel]
		assert.False(t, exist)
	})

	t.Run("ReconcileDRBDStorageClassReplication_replication_Availability_topology_TransZonal_not_enough_nodes_label_sc", func(t *testing.T) {
		const (
			labelNode1  = "label-node1"
			labelNode2  = "label-node2"
			noLabelNode = "no-label-node"
			zone1       = "test-zone1"
			zone2       = "test-zone2"
			zone3       = "test-zone3"
			dscName     = "dsp-test"
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

		dscs := map[string]v1alpha1.DRBDStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.DRBDStorageClassSpec{
					Replication: ReplicationAvailability,
					Zones:       []string{zone1, zone2, zone3},
					Topology:    TopologyTransZonal,
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

		ReconcileDRBDStorageClassReplication(ctx, cl, log, dscs, nodeList)

		updatedSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, updatedSc)

		_, exist := updatedSc.Labels[NonOperationalByReplicasLabel]
		assert.True(t, exist)
	})

	t.Run("ReconcileDRBDStorageClassReplication_replication_Availability_topology_Ignored_not_enough_nodes_label_sc", func(t *testing.T) {
		const (
			noLabelNode1 = "no-label-node1"
			noLabelNode2 = "no-label-node2"
			dscName      = "dsp-test"
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

		dscs := map[string]v1alpha1.DRBDStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.DRBDStorageClassSpec{
					Replication: ReplicationAvailability,
					Topology:    TopologyIgnored,
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

		ReconcileDRBDStorageClassReplication(ctx, cl, log, dscs, nodeList)

		updatedSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, updatedSc)

		_, exist := updatedSc.Labels[NonOperationalByReplicasLabel]
		assert.True(t, exist)
	})

	t.Run("ReconcileDRBDStorageClassReplication_replication_Availability_topology_TransZonal_enough_nodes_no_label_sc", func(t *testing.T) {
		const (
			labelNode1  = "label-node1"
			labelNode2  = "label-node2"
			noLabelNode = "no-label-node"
			zone1       = "test-zone1"
			zone2       = "test-zone2"
			zone3       = "test-zone3"
			dscName     = "dsp-test"
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

		dscs := map[string]v1alpha1.DRBDStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.DRBDStorageClassSpec{
					Replication: ReplicationAvailability,
					Zones:       []string{zone1, zone2, zone3},
					Topology:    TopologyTransZonal,
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

		ReconcileDRBDStorageClassReplication(ctx, cl, log, dscs, nodeList)

		updatedSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, updatedSc)

		_, exist := updatedSc.Labels[NonOperationalByReplicasLabel]
		assert.False(t, exist)
	})

	t.Run("ReconcileDRBDStorageClassReplication_replication_Availability_topology_Ignored_enough_nodes_no_label_sc", func(t *testing.T) {
		const (
			noLabelNode1 = "no-label-node1"
			noLabelNode2 = "no-label-node2"
			noLabelNode3 = "no-label-node3"
			dscName      = "dsp-test"
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

		dscs := map[string]v1alpha1.DRBDStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.DRBDStorageClassSpec{
					Replication: ReplicationAvailability,
					Topology:    TopologyIgnored,
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

		ReconcileDRBDStorageClassReplication(ctx, cl, log, dscs, nodeList)

		updatedSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, updatedSc)

		_, exist := updatedSc.Labels[NonOperationalByReplicasLabel]
		assert.False(t, exist)
	})

	t.Run("ReconcileDRBDStorageClassReplication_replication_Availability_topology_Zonal_removes_label_sc", func(t *testing.T) {
		const (
			noLabelNode1 = "no-label-node1"
			noLabelNode2 = "no-label-node2"
			noLabelNode  = "no-label-node3"
			zone1        = "test-zone1"
			dscName      = "dsp-test"
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

		dscs := map[string]v1alpha1.DRBDStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.DRBDStorageClassSpec{
					Replication: ReplicationAvailability,
					Topology:    TopologyZonal,
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

		ReconcileDRBDStorageClassReplication(ctx, cl, log, dscs, nodeList)

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

		ReconcileDRBDStorageClassReplication(ctx, cl, log, dscs, updatedNodeList)

		scWithNoLabel := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, scWithNoLabel)

		_, exist = scWithNoLabel.Labels[NonOperationalByReplicasLabel]
		assert.False(t, exist)
	})

	t.Run("ReconcileDRBDStorageClassReplication_replication_Availability_topology_TransZonal_removes_label_sc", func(t *testing.T) {
		const (
			labelNode1  = "label-node1"
			labelNode2  = "label-node2"
			noLabelNode = "no-label-node"
			zone1       = "test-zone1"
			zone2       = "test-zone2"
			zone3       = "test-zone3"
			dscName     = "dsp-test"
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

		dscs := map[string]v1alpha1.DRBDStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.DRBDStorageClassSpec{
					Replication: ReplicationAvailability,
					Zones:       []string{zone1, zone2, zone3},
					Topology:    TopologyTransZonal,
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

		ReconcileDRBDStorageClassReplication(ctx, cl, log, dscs, nodeList)

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

		ReconcileDRBDStorageClassReplication(ctx, cl, log, dscs, updatedNodeList)

		scWithNoLabel := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, scWithNoLabel)

		_, exist = scWithNoLabel.Labels[NonOperationalByReplicasLabel]
		assert.False(t, exist)
	})

	t.Run("ReconcileDRBDStorageClassReplication_replication_Availability_topology_Ignored_removes_label_sc", func(t *testing.T) {
		const (
			labelNode1  = "label-node1"
			labelNode2  = "label-node2"
			noLabelNode = "no-label-node"
			dscName     = "dsp-test"
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

		dscs := map[string]v1alpha1.DRBDStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.DRBDStorageClassSpec{
					Replication: ReplicationAvailability,
					Topology:    TopologyIgnored,
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

		ReconcileDRBDStorageClassReplication(ctx, cl, log, dscs, nodeList)

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

		ReconcileDRBDStorageClassReplication(ctx, cl, log, dscs, updatedNodeList)

		scWithNoLabel := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, scWithNoLabel)

		_, exist = scWithNoLabel.Labels[NonOperationalByReplicasLabel]
		assert.False(t, exist)
	})

	t.Run("ReconcileDRBDStorageClassReplication_replication_ConsistencyAndAvailability_topology_Zonal_not_enough_nodes_label_sc", func(t *testing.T) {
		const (
			labelNode1  = "label-node1"
			labelNode2  = "label-node2"
			noLabelNode = "no-label-node"
			zone1       = "test-zone1"
			zone2       = "test-zone2"
			dscName     = "dsp-test"
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

		dscs := map[string]v1alpha1.DRBDStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.DRBDStorageClassSpec{
					Replication: ReplicationConsistencyAndAvailability,
					Topology:    TopologyZonal,
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

		ReconcileDRBDStorageClassReplication(ctx, cl, log, dscs, nodeList)

		updatedSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, updatedSc)

		_, exist := updatedSc.Labels[NonOperationalByReplicasLabel]
		assert.True(t, exist)
	})

	t.Run("ReconcileDRBDStorageClassReplication_replication_ConsistencyAndAvailability_topology_Zonal_enough_nodes_no_label_sc", func(t *testing.T) {
		const (
			labelNode1 = "label-node1"
			labelNode2 = "label-node2"
			labelNode3 = "label-node3"
			zone1      = "test-zone1"
			dscName    = "dsp-test"
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

		dscs := map[string]v1alpha1.DRBDStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.DRBDStorageClassSpec{
					Replication: ReplicationConsistencyAndAvailability,
					Topology:    TopologyZonal,
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

		ReconcileDRBDStorageClassReplication(ctx, cl, log, dscs, nodeList)

		updatedSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, updatedSc)

		_, exist := updatedSc.Labels[NonOperationalByZonesLabel]
		assert.False(t, exist)
	})

	t.Run("ReconcileDRBDStorageClassReplication_replication_ConsistencyAndAvailability_topology_TransZonal_not_enough_nodes_label_sc", func(t *testing.T) {
		const (
			labelNode1 = "label-node1"
			labelNode2 = "label-node2"
			labelNode3 = "label-node3"
			zone1      = "test-zone1"
			zone2      = "test-zone2"
			zone3      = "test-zone3"
			dscName    = "dsp-test"
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

		dscs := map[string]v1alpha1.DRBDStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.DRBDStorageClassSpec{
					Replication: ReplicationConsistencyAndAvailability,
					Topology:    TopologyTransZonal,
					Zones:       []string{zone1, zone2, zone3},
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

		ReconcileDRBDStorageClassReplication(ctx, cl, log, dscs, nodeList)

		updatedSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, updatedSc)

		_, exist := updatedSc.Labels[NonOperationalByReplicasLabel]
		assert.True(t, exist)
	})

	t.Run("ReconcileDRBDStorageClassReplication_replication_ConsistencyAndAvailability_topology_TransZonal_enough_nodes_no_label_sc", func(t *testing.T) {
		const (
			labelNode1 = "label-node1"
			labelNode2 = "label-node2"
			labelNode3 = "label-node3"
			zone1      = "test-zone1"
			zone2      = "test-zone2"
			zone3      = "test-zone3"
			dscName    = "dsp-test"
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

		dscs := map[string]v1alpha1.DRBDStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.DRBDStorageClassSpec{
					Replication: ReplicationConsistencyAndAvailability,
					Topology:    TopologyTransZonal,
					Zones:       []string{zone1, zone2, zone3},
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

		ReconcileDRBDStorageClassReplication(ctx, cl, log, dscs, nodeList)

		updatedSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, updatedSc)

		_, exist := updatedSc.Labels[NonOperationalByReplicasLabel]
		assert.False(t, exist)
	})

	t.Run("ReconcileDRBDStorageClassReplication_replication_ConsistencyAndAvailability_topology_Ignored_not_enough_nodes_label_sc", func(t *testing.T) {
		const (
			noLabelNode1 = "no-label-node1"
			noLabelNode2 = "no-label-node2"
			dscName      = "dsp-test"
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

		dscs := map[string]v1alpha1.DRBDStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.DRBDStorageClassSpec{
					Replication: ReplicationConsistencyAndAvailability,
					Topology:    TopologyIgnored,
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

		ReconcileDRBDStorageClassReplication(ctx, cl, log, dscs, nodeList)

		updatedSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, updatedSc)

		_, exist := updatedSc.Labels[NonOperationalByReplicasLabel]
		assert.True(t, exist)
	})

	t.Run("ReconcileDRBDStorageClassReplication_replication_ConsistencyAndAvailability_topology_Ignored_enough_nodes_no_label_sc", func(t *testing.T) {
		const (
			noLabelNode1 = "no-label-node1"
			noLabelNode2 = "no-label-node2"
			noLabelNode3 = "no-label-node3"
			dscName      = "dsp-test"
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

		dscs := map[string]v1alpha1.DRBDStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.DRBDStorageClassSpec{
					Replication: ReplicationConsistencyAndAvailability,
					Topology:    TopologyIgnored,
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

		ReconcileDRBDStorageClassReplication(ctx, cl, log, dscs, nodeList)

		updatedSc := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, updatedSc)

		_, exist := updatedSc.Labels[NonOperationalByReplicasLabel]
		assert.False(t, exist)
	})

	t.Run("ReconcileDRBDStorageClassReplication_replication_ConsistencyAndAvailability_topology_Zonal_removes_label_sc", func(t *testing.T) {
		const (
			labelNode1  = "label-node1"
			labelNode2  = "label-node2"
			labelNode3  = "label-node3"
			noLabelNode = "no-label-node"
			zone1       = "test-zone1"
			zone2       = "test-zone2"
			dscName     = "dsp-test"
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

		dscs := map[string]v1alpha1.DRBDStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.DRBDStorageClassSpec{
					Replication: ReplicationConsistencyAndAvailability,
					Topology:    TopologyZonal,
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

		ReconcileDRBDStorageClassReplication(ctx, cl, log, dscs, nodeList)

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

		ReconcileDRBDStorageClassReplication(ctx, cl, log, dscs, updatedNodeList)

		scWithNoLabel := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, scWithNoLabel)

		_, exist = scWithNoLabel.Labels[NonOperationalByReplicasLabel]
		assert.False(t, exist)
	})

	t.Run("ReconcileDRBDStorageClassReplication_replication_ConsistencyAndAvailability_topology_TransZonal_removes_label_sc", func(t *testing.T) {
		const (
			labelNode1  = "label-node1"
			labelNode2  = "label-node2"
			labelNode3  = "label-node3"
			noLabelNode = "no-label-node"
			zone1       = "test-zone1"
			zone2       = "test-zone2"
			zone3       = "test-zone3"
			dscName     = "dsp-test"
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

		dscs := map[string]v1alpha1.DRBDStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.DRBDStorageClassSpec{
					Replication: ReplicationConsistencyAndAvailability,
					Topology:    TopologyTransZonal,
					Zones:       []string{zone1, zone2, zone3},
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

		ReconcileDRBDStorageClassReplication(ctx, cl, log, dscs, nodeList)

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

		ReconcileDRBDStorageClassReplication(ctx, cl, log, dscs, updatedNodeList)

		scWithNoLabel := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, scWithNoLabel)

		_, exist = scWithNoLabel.Labels[NonOperationalByReplicasLabel]
		assert.False(t, exist)
	})

	t.Run("ReconcileDRBDStorageClassReplication_replication_ConsistencyAndAvailability_topology_Ignored_removes_label_sc", func(t *testing.T) {
		const (
			noLabelNode1 = "no-label-node1"
			noLabelNode2 = "no-label-node2"
			noLabelNode3 = "no-label-node3"
			dscName      = "dsp-test"
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

		dscs := map[string]v1alpha1.DRBDStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.DRBDStorageClassSpec{
					Replication: ReplicationConsistencyAndAvailability,
					Topology:    TopologyIgnored,
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

		ReconcileDRBDStorageClassReplication(ctx, cl, log, dscs, nodeList)

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

		ReconcileDRBDStorageClassReplication(ctx, cl, log, dscs, updatedNodeList)

		scWithNoLabel := &storagev1.StorageClass{}
		err = cl.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      dscName,
		}, scWithNoLabel)

		_, exist = scWithNoLabel.Labels[NonOperationalByReplicasLabel]
		assert.False(t, exist)
	})

	t.Run("ReconcileDRBDStorageClassZones_correct_zones_returns_healthy_dsc_no_label_sc", func(t *testing.T) {
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

		dscs := map[string]v1alpha1.DRBDStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.DRBDStorageClassSpec{
					Replication: ReplicationConsistencyAndAvailability,
					Zones:       []string{zone1, zone2, zone3},
					StoragePool: dspName,
				},
			},
		}

		dspZones := map[string][]string{
			dspName: {zone1, zone2, zone3},
		}

		healthyDsc := ReconcileDRBDStorageClassZones(ctx, cl, log, dscs, dspZones)
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

	t.Run("ReconcileDRBDStorageClassZones_incorrect_zones_doesnt_return_unhealthy_dsc_and_label_sc", func(t *testing.T) {
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

		dscs := map[string]v1alpha1.DRBDStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.DRBDStorageClassSpec{
					Replication: ReplicationConsistencyAndAvailability,
					Zones:       []string{zone1, zone2, zone3},
					StoragePool: dspName,
				},
			},
		}

		dspZones := map[string][]string{
			dspName: {zone1, zone2},
		}

		healthyDsc := ReconcileDRBDStorageClassZones(ctx, cl, log, dscs, dspZones)
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

	t.Run("ReconcileDRBDStorageClassZones_unhealthy_dsc_fixed_removes_label_sc", func(t *testing.T) {
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

		dscs := map[string]v1alpha1.DRBDStorageClass{
			dscName: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      dscName,
					Namespace: namespace,
				},
				Spec: v1alpha1.DRBDStorageClassSpec{
					Replication: ReplicationConsistencyAndAvailability,
					Zones:       []string{zone1, zone2, zone3},
					StoragePool: dspName,
				},
			},
		}

		dspZones := map[string][]string{
			dspName: {zone1, zone2},
		}

		healthyDsc := ReconcileDRBDStorageClassZones(ctx, cl, log, dscs, dspZones)
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

		updatedHealthyDsc := ReconcileDRBDStorageClassZones(ctx, cl, log, dscs, updatedDspZones)
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
