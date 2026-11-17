/*
Copyright 2026 Flant JSC

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
	"net/http"

	"github.com/slok/kubewebhook/v2/pkg/model"
	kwhvalidating "github.com/slok/kubewebhook/v2/pkg/webhook/validating"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/deckhouse/sds-common-lib/logger"
	"github.com/deckhouse/sds-common-lib/logger/kwhlogger"
	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

const (
	RSCValidatorID = "RSCValidator"
	RSPValidatorID = "RSPValidator"
	SCValidatorID  = "SCValidator"
	PVCValidatorID = "PVCValidator"
	LVGValidatorID = "LVGValidator"

	PVCDataSourceValidatorID = "PVCDataSourceValidator"
	VSValidatorID            = "VolumeSnapshotValidator"
)

func Handlers(l logger.Logger) (map[string]http.Handler, error) {
	SetLogger(l)
	kwhLog := kwhlogger.New(l)

	routes := []struct {
		path     string
		id       string
		validate func(context.Context, *model.AdmissionReview, metav1.Object) (*kwhvalidating.ValidatorResult, error)
		obj      metav1.Object
	}{
		{"/rsp-validate", RSPValidatorID, RSPValidate, &srv.ReplicatedStoragePool{}},
		{"/rsc-validate", RSCValidatorID, RSCValidate, &srv.ReplicatedStorageClass{}},
		{"/sc-validate", SCValidatorID, SCValidate, &storagev1.StorageClass{}},
		{"/pvc-validate", PVCValidatorID, PVCValidate, &corev1.PersistentVolumeClaim{}},
		{"/lvg-validate", LVGValidatorID, LVGValidate, &snc.LVMVolumeGroup{}},
		{"/pvc-data-source-validate", PVCDataSourceValidatorID, PVCDataSourceValidate, &corev1.PersistentVolumeClaim{}},
		{"/volumesnapshot-validate", VSValidatorID, VolumeSnapshotValidate, &unstructured.Unstructured{}},
	}

	hs := make(map[string]http.Handler, len(routes))
	for _, r := range routes {
		h, err := GetValidatingWebhookHandler(r.validate, r.id, r.obj, kwhLog)
		if err != nil {
			return nil, fmt.Errorf("creating the %s handler: %w", r.id, err)
		}
		hs[r.path] = h
	}
	return hs, nil
}
