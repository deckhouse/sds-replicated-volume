package scheduler

import (
	c "scheduler-extender/pkg/cache"
	"scheduler-extender/pkg/consts"
	"scheduler-extender/pkg/logger"
	"testing"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	storageClassNameOne string = "storage-class-1"
	storageClassNameTwo string = "storage-class-2"
)

const (
	node1 string = "node-1"
	node2 string = "node-2"
	node3 string = "node-3"
)

func TestScoreNodes(t *testing.T) {
	log := logger.Logger{}
	nodeNames := []string{node1, node2, node3}

	cache := c.Cache{}
	lvgCache := []*c.LvgCache{
		{
			Lvg: &snc.LVMVolumeGroup{
				ObjectMeta: metav1.ObjectMeta{Name: "lvg-1"},
				Spec: snc.LVMVolumeGroupSpec{
					Local: snc.LVMVolumeGroupLocalSpec{
						NodeName: node1,
					},
				},
				Status: snc.LVMVolumeGroupStatus{
					Nodes: []snc.LVMVolumeGroupNode{
						{
							Name: node1,
						},
					},
					VGFree: resource.MustParse("30Gi"),
				},
			},
		},
		{
			Lvg: &snc.LVMVolumeGroup{
				ObjectMeta: metav1.ObjectMeta{Name: "lvg-2"},
				Spec: snc.LVMVolumeGroupSpec{
					Local: snc.LVMVolumeGroupLocalSpec{
						NodeName: node2,
					},
				},
				Status: snc.LVMVolumeGroupStatus{
					Nodes: []snc.LVMVolumeGroupNode{
						{
							Name: node2,
						},
					},
					VGFree: resource.MustParse("1Gi"),
				},
			},
		},
	}

	for _, lvgC := range lvgCache {
		cache.AddLVG(lvgC.Lvg)
	}
	cache.AddLVGToPVC("lvg-1", "pvc-1")
	cache.AddLVGToPVC("lvg-2", "pvc-2")

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

	// Do not change intendation here or else these LVGs will not be parsed
	mockLVGYamlOne := `- name: lvg-1
  Thin:
    poolName: pool1
- name: lvg-2
  Thin:
    poolName: pool2`

	mockLVGYamlTwo := `- name: lvg-3
  Thin:
    poolName: pool3`

	scs := map[string]*storagev1.StorageClass{
		storageClassNameOne: {
			ObjectMeta: metav1.ObjectMeta{
				Name: storageClassNameOne,
			},
			Provisioner: "replicated.csi.storage.deckhouse.io",
			Parameters: map[string]string{
				consts.LVMVolumeGroupsParamKey: mockLVGYamlOne,
			},
		},
		storageClassNameTwo: {
			ObjectMeta: metav1.ObjectMeta{
				Name: storageClassNameTwo,
			},
			Provisioner: "replicated.csi.storage.deckhouse.io",
			Parameters: map[string]string{
				consts.LVMVolumeGroupsParamKey: mockLVGYamlTwo,
			},
		},
	}

	pvcs := map[string]*v1.PersistentVolumeClaim{
		"pvc-1": {
			ObjectMeta: metav1.ObjectMeta{
				Name: "pvc-1",
			},
			Spec: v1.PersistentVolumeClaimSpec{
				StorageClassName: &storageClassNameOne,
				Resources: v1.VolumeResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceStorage: resource.MustParse("1Gi"),
					},
				},
			},
		},
		"pvc-2": {
			ObjectMeta: metav1.ObjectMeta{
				Name: "pvc-2",
			},
			Spec: v1.PersistentVolumeClaimSpec{
				StorageClassName: &storageClassNameTwo,
				Resources: v1.VolumeResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceStorage: resource.MustParse("500Mi"),
					},
				},
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
