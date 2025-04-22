package scheduler

// import (
// 	"fmt"
// 	c "scheduler-extender/pkg/cache"
// 	"scheduler-extender/pkg/consts"
// 	"scheduler-extender/pkg/logger"
// 	"testing"

// 	apiv1 "k8s.io/api/core/v1"
// 	v1 "k8s.io/api/core/v1"
// 	storagev1 "k8s.io/api/storage/v1"
// 	"k8s.io/apimachinery/pkg/api/resource"
// 	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
// )

// const (
// 	node_1 string = "node-1"
// 	node_2 string = "node-2"
// 	node_3 string = "node-3"
// )

// func mockPod(podName, pvcName string) *apiv1.Pod {
// 	return &apiv1.Pod{
// 		ObjectMeta: metav1.ObjectMeta{
// 			Name: podName,
// 		},
// 		Spec: apiv1.PodSpec{
// 			Volumes: []apiv1.Volume{
// 				{
// 					Name: fmt.Sprintf("volume/%s", pvcName),
// 					VolumeSource: apiv1.VolumeSource{
// 						PersistentVolumeClaim: &apiv1.PersistentVolumeClaimVolumeSource{
// 							ClaimName: pvcName,
// 						},
// 					},
// 				},
// 			},
// 		},
// 	}
// }

// func TestFilterNodes(t *testing.T) {
// 	log := logger.Logger{}

// 	cache := c.Cache{}
// 	lvgCache := []*c.LvgCache{
// 		{
// 			Lvg: mockLVG("lvg-1", node_1, "2Gi"),
// 		},
// 		{
// 			Lvg: mockLVG("lvg-2", node_2, "1Gi"),
// 		},
// 		{
// 			Lvg: mockLVG("lvg-3", node_2, "1Gi"),
// 		},
// 	}

// 	for _, lvgC := range lvgCache {
// 		cache.AddLVG(lvgC.Lvg)
// 	}
// 	cache.AddLVGToPVC("lvg-1", "pvc-1")
// 	cache.AddLVGToPVC("lvg-2", "pvc-2")

// 	nodeNames := []string{node_1, node_2, node_3}

// 	inputData := ExtenderArgs{
// 		NodeNames: &nodeNames,
// 		Pod:       mockPod("pod-1", "pvc-1"),
// 	}

// 	namagedPvcs := map[string]*v1.PersistentVolumeClaim{
// 		"pvc-1": {
// 			ObjectMeta: metav1.ObjectMeta{
// 				Name: "pvc-1",
// 			},
// 			Spec: v1.PersistentVolumeClaimSpec{
// 				StorageClassName: &storageClassNameOne,
// 				Resources: v1.VolumeResourceRequirements{
// 					Requests: v1.ResourceList{
// 						v1.ResourceStorage: resource.MustParse("1Gi"),
// 					},
// 				},
// 			},
// 		},
// 		"pvc-2": {
// 			ObjectMeta: metav1.ObjectMeta{
// 				Name: "pvc-2",
// 			},
// 			Spec: v1.PersistentVolumeClaimSpec{
// 				StorageClassName: &storageClassNameTwo,
// 				Resources: v1.VolumeResourceRequirements{
// 					Requests: v1.ResourceList{
// 						v1.ResourceStorage: resource.MustParse("500Mi"),
// 					},
// 				},
// 			},
// 		},
// 	}

// 	// Do not change intendation here or else these LVGs will not be parsed
// 	mockLVGYamlOne := `- name: lvg-1
//   Thin:
//     poolName: pool1
// - name: lvg-2
//   Thin:
//     poolName: pool2`

// 	mockLVGYamlTwo := `- name: lvg-3
//   Thin:
//     poolName: pool3`

// 	storageClasses := map[string]*storagev1.StorageClass{
// 		storageClassNameOne: {
// 			ObjectMeta: metav1.ObjectMeta{
// 				Name: storageClassNameOne,
// 			},
// 			Provisioner: "replicated.csi.storage.deckhouse.io",
// 			Parameters: map[string]string{
// 				consts.LVMVolumeGroupsParamKey: mockLVGYamlOne,
// 			},
// 		},
// 		storageClassNameTwo: {
// 			ObjectMeta: metav1.ObjectMeta{
// 				Name: storageClassNameTwo,
// 			},
// 			Provisioner: "replicated.csi.storage.deckhouse.io",
// 			Parameters: map[string]string{
// 				consts.LVMVolumeGroupsParamKey: mockLVGYamlTwo,
// 			},
// 		},
// 	}

// 	pvcRequests := map[string]PVCRequest{
// 		"pvc-1": {
// 			DeviceType:    consts.Thick,
// 			RequestedSize: 1073741824, // 1Gb
// 		},
// 		"pvc-2": {
// 			DeviceType:    consts.Thin,
// 			RequestedSize: 524288000, // 500mb
// 		},
// 	}

// 	tests := []struct {
// 		testName    string
// 		nodeNames   []string
// 		pvcRequests map[string]PVCRequest
// 		storageClasses map[string]*storagev1.StorageClass
// 		pvcs        map[string]*v1.PersistentVolumeClaim
// 		expect      map[string]int
// 	}{
// 		{
// 			testName:  "Test Case #1",
// 			nodeNames: []string{node1},
// 			pvcs:      pvcs,
// 			expect:    map[string]int{node1: 11},
// 		},
// 	}
// }
