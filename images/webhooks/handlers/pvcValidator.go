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
	"fmt"
	"os"
	"strings"

	"github.com/slok/kubewebhook/v2/pkg/model"
	kwhvalidating "github.com/slok/kubewebhook/v2/pkg/webhook/validating"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	pvcRwxAllowedUsernamesEnv = "PVC_RWX_ALLOWED_USERNAMES" // comma-separated
	pvcRwxAllowedGroupsEnv    = "PVC_RWX_ALLOWED_GROUPS"    // comma-separated

	storageClassIsDefaultAnnotation     = "storageclass.kubernetes.io/is-default-class"
	storageClassIsDefaultBetaAnnotation = "storageclass.beta.kubernetes.io/is-default-class"
)

var pvcRwxDefaultAllowedUsernames = []string{
	"system:serviceaccount:d8-virtualization:cdi-sa",
	"system:serviceaccount:d8-virtualization:virtualization-controller",
}

// kubeClientFactory allows injecting a custom client factory for testing
var kubeClientFactory = NewKubeClient

func PVCValidate(ctx context.Context, arReview *model.AdmissionReview, obj metav1.Object) (*kwhvalidating.ValidatorResult, error) {
	pvc, ok := obj.(*v1.PersistentVolumeClaim)
	if !ok {
		// If not a PVC, just continue the validation chain(if there is one) and do nothing.
		return &kwhvalidating.ValidatorResult{Valid: true}, nil
	}

	// We only care about RWX PVCs.
	if !pvcRequestsRWX(pvc) {
		return &kwhvalidating.ValidatorResult{Valid: true}, nil
	}

	cl, err := kubeClientFactory("")
	if err != nil {
		return nil, err
	}

	sc, err := resolvePVCStorageClass(ctx, cl, pvc)
	if err != nil {
		return nil, err
	}
	if sc == nil {
		// If we can't determine the StorageClass, don't block.
		return &kwhvalidating.ValidatorResult{Valid: true}, nil
	}

	if sc.Provisioner != replicatedCSIProvisioner {
		return &kwhvalidating.ValidatorResult{Valid: true}, nil
	}

	allowedUsernames := splitCommaEnv(pvcRwxAllowedUsernamesEnv)
	allowedGroups := splitCommaEnv(pvcRwxAllowedGroupsEnv)
	if len(allowedUsernames) == 0 {
		allowedUsernames = pvcRwxDefaultAllowedUsernames
	}

	if userAllowed(arReview.UserInfo.Username, arReview.UserInfo.Groups, allowedUsernames, allowedGroups) {
		klog.Infof("User %s is allowed to create RWX PVC with provisioner %s", arReview.UserInfo.Username, replicatedCSIProvisioner)
		return &kwhvalidating.ValidatorResult{Valid: true}, nil
	}

	klog.Infof("User %s is not allowed to create RWX PVC with provisioner %s (storageClass=%s)", arReview.UserInfo.Username, replicatedCSIProvisioner, sc.Name)
	return &kwhvalidating.ValidatorResult{
		Valid: false,
		Message: fmt.Sprintf(
			"Creating PVC with accessMode ReadWriteMany using StorageClass %q (provisioner %q) is not allowed for user %q.",
			sc.Name, replicatedCSIProvisioner, arReview.UserInfo.Username,
		),
	}, nil
}

func pvcRequestsRWX(pvc *v1.PersistentVolumeClaim) bool {
	for _, m := range pvc.Spec.AccessModes {
		if m == v1.ReadWriteMany {
			return true
		}
	}
	return false
}

func resolvePVCStorageClass(ctx context.Context, cl client.Client, pvc *v1.PersistentVolumeClaim) (*storagev1.StorageClass, error) {
	// Explicit StorageClassName.
	if pvc.Spec.StorageClassName != nil && *pvc.Spec.StorageClassName != "" {
		scName := *pvc.Spec.StorageClassName
		return getStorageClass(ctx, cl, scName)
	}

	// Try default StorageClass to avoid bypass via omitting storageClassName.
	var scList storagev1.StorageClassList
	if err := cl.List(ctx, &scList); err != nil {
		return nil, err
	}
	for i := range scList.Items {
		sc := &scList.Items[i]
		if isDefaultStorageClass(sc) {
			return sc, nil
		}
	}

	return nil, nil
}

func isDefaultStorageClass(sc *storagev1.StorageClass) bool {
	if sc.Annotations == nil {
		return false
	}
	if sc.Annotations[storageClassIsDefaultAnnotation] == "true" {
		return true
	}
	return sc.Annotations[storageClassIsDefaultBetaAnnotation] == "true"
}

func splitCommaEnv(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func userAllowed(username string, groups []string, allowedUsernames []string, allowedGroups []string) bool {
	for _, u := range allowedUsernames {
		if username == u {
			return true
		}
	}
	for _, g := range groups {
		for _, ag := range allowedGroups {
			if g == ag {
				return true
			}
		}
	}
	return false
}

func getStorageClass(ctx context.Context, cl client.Client, name string) (*storagev1.StorageClass, error) {
	nn := types.NamespacedName{Name: name}
	var sc storagev1.StorageClass
	if err := cl.Get(ctx, nn, &sc); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &sc, nil
}
