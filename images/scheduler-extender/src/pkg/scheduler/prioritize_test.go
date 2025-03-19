package scheduler

import (
	c "scheduler-extender/pkg/cache"
	"scheduler-extender/pkg/consts"
	"scheduler-extender/pkg/logger"
	"testing"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	storageClassNameOne string = "storage-class-1"
	storageClassNameTwo string = "storage-class-2"
)

func TestScoreNodes(t *testing.T) {
	log := logger.Logger{}
	nodeNames := []string{"node1", "node2", "node3"}

	cache := c.Cache{}
	lvgCache := []*c.LvgCache{
		{
			Lvg: &snc.LVMVolumeGroup{
				ObjectMeta: metav1.ObjectMeta{Name: "lvg-1"},
			},
		},
		{
			Lvg: &snc.LVMVolumeGroup{
				ObjectMeta: metav1.ObjectMeta{Name: "lvg-2"},
			},
		},
	}

	for _, lvgC := range lvgCache {
		cache.Lvgs.Store(lvgC.Lvg.Name, lvgC)
	}

	pvcRequests := map[string]PVCRequest{
		"pvc-1": {
			DeviceType:    consts.Thick,
			RequestedSize: 1073741824, // 1Gb
		},
		"pvc-2": {
			DeviceType:    consts.Thin,
			RequestedSize: 524288000, // 500mb
		},
	}

	scs := map[string]*storagev1.StorageClass{
		storageClassNameOne: {
			ObjectMeta: metav1.ObjectMeta{
				Name: storageClassNameOne,
			},
			Provisioner: "replicated.csi.storage.deckhouse.io",
		},
		storageClassNameTwo: {
			ObjectMeta: metav1.ObjectMeta{
				Name: storageClassNameTwo,
			},
			Provisioner: "replicated.csi.storage.deckhouse.io",
		},
	}

	pvcs := map[string]*v1.PersistentVolumeClaim{
		"pvc-1": {
			ObjectMeta: metav1.ObjectMeta{
				Name: "pvc-1",
			},
			Spec: v1.PersistentVolumeClaimSpec{
				StorageClassName: &storageClassNameOne,
			},
		},
		"pvc-2": {
			ObjectMeta: metav1.ObjectMeta{
				Name: "pvc-2",
			},
			Spec: v1.PersistentVolumeClaimSpec{
				StorageClassName: &storageClassNameTwo,
			},
		},
	}

	tests := []struct {
		testName  string
		nodeNames []string
		pvcs      map[string]*v1.PersistentVolumeClaim
	}{
		{
			testName:  "Test Case #1",
			nodeNames: nodeNames,
			pvcs:      pvcs,
		},
	}

	for _, tt := range tests {
		t.Run(tt.testName, func(t *testing.T) {
			score, err := scoreNodes(log, &cache, &nodeNames, pvcs, scs, pvcRequests, 1)
			if err != nil {
				t.Error(err)
			}
			t.Logf("Node score: %v", score)
		})
	}
}
