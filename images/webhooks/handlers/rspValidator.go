/*
Copyright 2025 Flant JSC

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

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/slok/kubewebhook/v2/pkg/model"
	kwhvalidating "github.com/slok/kubewebhook/v2/pkg/webhook/validating"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	mc "github.com/deckhouse/sds-replicated-volume/images/webhooks/api"
)

const (
	LVMThinType                   = "LVMThin"
	sdsReplicatedVolumeModuleName = "sds-replicated-volume"
)

func RSPValidate(ctx context.Context, _ *model.AdmissionReview, obj metav1.Object) (*kwhvalidating.ValidatorResult, error) {
	rsp, ok := obj.(*srv.ReplicatedStoragePool)
	if !ok {
		// If not a storage class just continue the validation chain(if there is one) and do nothing.
		return &kwhvalidating.ValidatorResult{}, nil
	}

	config, err := rest.InClusterConfig()
	if err != nil {
		klog.Fatal(err.Error())
	}

	staticClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatal(err)
	}

	cl, err := NewKubeClient("")
	if err != nil {
		klog.Fatal(err)
	}

	var ephemeralNodesList []string

	nodes, _ := staticClient.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{LabelSelector: "node.deckhouse.io/type=CloudEphemeral"})
	for _, node := range nodes.Items {
		ephemeralNodesList = append(ephemeralNodesList, node.Name)
	}

	listDevice := &snc.LVMVolumeGroupList{}

	err = cl.List(ctx, listDevice)
	if err != nil {
		klog.Fatal(err)
	}

	errMsg := ""
	var lvmVolumeGroupUnique []string

	for _, rspLVMvg := range rsp.Spec.LVMVolumeGroups {
		lvgExists := false
		thinPoolExists := false

		if slices.Contains(lvmVolumeGroupUnique, rspLVMvg.Name) {
			errMsg = fmt.Sprintf("There must be unique LVMVolumeGroup names (%s duplicates)", rspLVMvg.Name)
			klog.Info(errMsg)
			return &kwhvalidating.ValidatorResult{Valid: false, Message: errMsg},
				nil
		}

		lvmVolumeGroupUnique = append(lvmVolumeGroupUnique, rspLVMvg.Name)

		if rsp.Spec.Type == LVMThinType && rspLVMvg.ThinPoolName == "" {
			errMsg = fmt.Sprintf("ThinPoolName must be set for LVMThin type in LVMVolumeGroup %s; ", rspLVMvg.Name)
			klog.Info(errMsg)
			return &kwhvalidating.ValidatorResult{Valid: false, Message: errMsg},
				nil
		}

		for _, lvmVG := range listDevice.Items {
			if lvmVG.Name == rspLVMvg.Name {
				lvgExists = true
				if rsp.Spec.Type == LVMThinType {
					for _, thinPool := range lvmVG.Spec.ThinPools {
						if thinPool.Name == rspLVMvg.ThinPoolName {
							thinPoolExists = true
							break
						}
					}
				}

				for _, lvgNode := range lvmVG.Status.Nodes {
					if slices.Contains(ephemeralNodesList, lvgNode.Name) {
						klog.Infof("Cannot create storage pool on ephemeral node (%s)", lvgNode.Name)
						return &kwhvalidating.ValidatorResult{Valid: false, Message: fmt.Sprintf("Cannot create storage pool on ephemeral node (%s)", lvgNode.Name)},
							nil
					}
				}
				break
			}
		}

		if thinPoolExists {
			ctx := context.Background()
			cl, err := NewKubeClient("")
			if err != nil {
				klog.Fatal(err.Error())
			}

			srvModuleConfig := &mc.ModuleConfig{}

			err = cl.Get(ctx, types.NamespacedName{Name: sdsReplicatedVolumeModuleName, Namespace: ""}, srvModuleConfig)
			if err != nil {
				klog.Fatal(err)
			}

			if value, exists := srvModuleConfig.Spec.Settings["enableThinProvisioning"]; exists && value == true {
				klog.Info("Thin pools support is enabled")
			} else {
				klog.Info("Enabling thin pools support")
				patchBytes, err := json.Marshal(map[string]interface{}{
					"spec": map[string]interface{}{
						"version": 1,
						"settings": map[string]interface{}{
							"enableThinProvisioning": true,
						},
					},
				})

				if err != nil {
					klog.Fatalf("Error marshalling patch: %s", err.Error())
				}

				err = cl.Patch(context.TODO(), srvModuleConfig, client.RawPatch(types.MergePatchType, patchBytes))
				if err != nil {
					klog.Fatalf("Error patching object: %s", err.Error())
				}
			}
		}

		if !lvgExists {
			errMsg = fmt.Sprintf("LVMVolumeGroup %s not found; ", rspLVMvg.Name)
			klog.Info(errMsg)
			return &kwhvalidating.ValidatorResult{Valid: false, Message: errMsg},
				nil
		}

		if rsp.Spec.Type == LVMThinType && !thinPoolExists {
			errMsg = fmt.Sprintf("ThinPool %s not found in LVMVolumeGroup %s; ", rspLVMvg.ThinPoolName, rspLVMvg.Name)
			klog.Info(errMsg)
			return &kwhvalidating.ValidatorResult{Valid: false, Message: errMsg},
				nil
		}
	}

	return &kwhvalidating.ValidatorResult{Valid: true},
		nil
}
