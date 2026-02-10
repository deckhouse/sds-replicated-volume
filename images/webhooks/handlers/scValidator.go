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
	"reflect"

	"github.com/slok/kubewebhook/v2/pkg/model"
	kwhvalidating "github.com/slok/kubewebhook/v2/pkg/webhook/validating"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

const (
	replicatedCSIProvisioner = "replicated.csi.storage.deckhouse.io"
)

var allowedUserNames = map[string]struct{}{
	"system:serviceaccount:d8-sds-replicated-volume:sds-replicated-volume-controller": {},
	"system:serviceaccount:d8-sds-replicated-volume:controller":                       {},
}

func SCValidate(_ context.Context, arReview *model.AdmissionReview, obj metav1.Object) (*kwhvalidating.ValidatorResult, error) {
	sc, ok := obj.(*storagev1.StorageClass)
	if !ok {
		// If not a storage class just continue the validation chain(if there is one) and do nothing.
		return &kwhvalidating.ValidatorResult{}, nil
	}

	if sc.Provisioner == replicatedCSIProvisioner {
		if _, ok := allowedUserNames[arReview.UserInfo.Username]; ok {
			klog.Infof("User %s is allowed to manage storage classes with provisioner %s", arReview.UserInfo.Username, replicatedCSIProvisioner)
			return &kwhvalidating.ValidatorResult{Valid: true}, nil
		}
		if arReview.Operation == model.OperationUpdate {
			changed, err := isStorageClassChangedExceptAnnotations(arReview.OldObjectRaw, arReview.NewObjectRaw)
			if err != nil {
				return nil, err
			}

			if !changed {
				klog.Infof("User %s is allowed to change annotations for storage classes with provisioner %s", arReview.UserInfo.Username, replicatedCSIProvisioner)
				return &kwhvalidating.ValidatorResult{Valid: true}, nil
			}
		}

		klog.Infof("User %s is not allowed to manage storage classes with provisioner %s", arReview.UserInfo.Username, replicatedCSIProvisioner)
		return &kwhvalidating.ValidatorResult{
				Valid:   false,
				Message: fmt.Sprintf("Direct modifications to the StorageClass (other than annotations) with the provisioner %s are not allowed. Please use ReplicatedStorageClass for such operations.", replicatedCSIProvisioner),
			},
			nil
	}

	return &kwhvalidating.ValidatorResult{Valid: true}, nil
}

func isStorageClassChangedExceptAnnotations(oldObjectRaw, newObjectRaw []byte) (bool, error) {
	var oldSC, newSC storagev1.StorageClass

	if err := json.Unmarshal(oldObjectRaw, &oldSC); err != nil {
		err := fmt.Errorf("failed to unmarshal old object: %v", err)
		return false, err
	}

	if err := json.Unmarshal(newObjectRaw, &newSC); err != nil {
		err := fmt.Errorf("failed to unmarshal new object: %v", err)
		return false, err
	}

	klog.Info("=====================================")
	klog.Infof("Comparing old object: %+v", oldSC)
	klog.Info("=====================================")
	klog.Infof("Comparing new object: %+v", newSC)
	klog.Info("=====================================")

	if oldSC.Provisioner != newSC.Provisioner {
		klog.Infof("Provisioner changed from %s to %s", oldSC.Provisioner, newSC.Provisioner)
		return true, nil
	}

	if *oldSC.VolumeBindingMode != *newSC.VolumeBindingMode {
		klog.Infof("VolumeBindingMode changed from %s to %s", *oldSC.VolumeBindingMode, *newSC.VolumeBindingMode)
		return true, nil
	}

	if *oldSC.ReclaimPolicy != *newSC.ReclaimPolicy {
		klog.Infof("ReclaimPolicy changed from %s to %s", *oldSC.ReclaimPolicy, *newSC.ReclaimPolicy)
		return true, nil
	}

	if !reflect.DeepEqual(oldSC.Parameters, newSC.Parameters) {
		klog.Infof("Parameters changed from %v to %v", oldSC.Parameters, newSC.Parameters)
		return true, nil
	}

	if *oldSC.AllowVolumeExpansion != *newSC.AllowVolumeExpansion {
		klog.Infof("AllowVolumeExpansion changed from %v to %v", *oldSC.AllowVolumeExpansion, *newSC.AllowVolumeExpansion)
		return true, nil
	}

	if !reflect.DeepEqual(oldSC.MountOptions, newSC.MountOptions) {
		klog.Infof("MountOptions changed from %v to %v", oldSC.MountOptions, newSC.MountOptions)
		return true, nil
	}

	if !reflect.DeepEqual(oldSC.AllowedTopologies, newSC.AllowedTopologies) {
		klog.Infof("AllowedTopologies changed from %v to %v", oldSC.AllowedTopologies, newSC.AllowedTopologies)
		return true, nil
	}

	newSC.ObjectMeta.Annotations = nil
	oldSC.ObjectMeta.Annotations = nil

	newSC.ObjectMeta.ManagedFields = nil
	oldSC.ObjectMeta.ManagedFields = nil

	if !reflect.DeepEqual(oldSC.ObjectMeta, newSC.ObjectMeta) {
		klog.Infof("ObjectMeta changed from %+v to %+v", oldSC.ObjectMeta, newSC.ObjectMeta)
		return true, nil
	}

	return false, nil
}
