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

	"github.com/slok/kubewebhook/v2/pkg/model"
	kwhvalidating "github.com/slok/kubewebhook/v2/pkg/webhook/validating"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

const (
	volumeSnapshotAPIGroup    = "snapshot.storage.k8s.io"
	volumeSnapshotKind        = "VolumeSnapshot"
	persistentVolumeClaimKind = "PersistentVolumeClaim"
)

type pvcDataSource struct {
	kind      string
	name      string
	namespace string
}

func PVCDataSourceValidate(ctx context.Context, arReview *model.AdmissionReview, obj metav1.Object) (*kwhvalidating.ValidatorResult, error) {
	pvc, ok := obj.(*v1.PersistentVolumeClaim)
	if !ok {
		return &kwhvalidating.ValidatorResult{Valid: true}, nil
	}

	source := clonedOrRestoredFrom(pvc, objectNamespace(pvc, arReview))
	if source == nil {
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
	thick, err := thickStorage(ctx, cl, sc)
	if err != nil {
		return nil, err
	}
	if thick != "" {
		return &kwhvalidating.ValidatorResult{
			Valid: false,
			Message: fmt.Sprintf(
				"StorageClass %q keeps its volumes in %s; cloning a PersistentVolumeClaim and restoring one from a VolumeSnapshot need a storage pool of type LVMThin.",
				sc.Name, thick,
			),
		}, nil
	}

	if source.kind != persistentVolumeClaimKind {
		return &kwhvalidating.ValidatorResult{Valid: true}, nil
	}

	sourceSC, err := claimStorageClass(ctx, cl, source.namespace, source.name)
	if err != nil {
		return nil, err
	}
	thick, err = thickStorage(ctx, cl, sourceSC)
	if err != nil {
		return nil, err
	}
	if thick != "" {
		return &kwhvalidating.ValidatorResult{
			Valid: false,
			Message: fmt.Sprintf(
				"PersistentVolumeClaim %q uses StorageClass %q, which keeps its volumes in %s; cloning a PersistentVolumeClaim needs a storage pool of type LVMThin.",
				source.name, sourceSC.Name, thick,
			),
		}, nil
	}

	return &kwhvalidating.ValidatorResult{Valid: true}, nil
}

func VolumeSnapshotValidate(ctx context.Context, arReview *model.AdmissionReview, obj metav1.Object) (*kwhvalidating.ValidatorResult, error) {
	vs, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return &kwhvalidating.ValidatorResult{Valid: true}, nil
	}

	claimName, _, err := unstructured.NestedString(vs.Object, "spec", "source", "persistentVolumeClaimName")
	if err != nil || claimName == "" {
		return &kwhvalidating.ValidatorResult{Valid: true}, nil
	}

	cl, err := kubeClientFactory("")
	if err != nil {
		return nil, err
	}

	sc, err := claimStorageClass(ctx, cl, objectNamespace(vs, arReview), claimName)
	if err != nil {
		return nil, err
	}
	thick, err := thickStorage(ctx, cl, sc)
	if err != nil {
		return nil, err
	}
	if thick != "" {
		return &kwhvalidating.ValidatorResult{
			Valid: false,
			Message: fmt.Sprintf(
				"PersistentVolumeClaim %q uses StorageClass %q, which keeps its volumes in %s; snapshots need a storage pool of type LVMThin.",
				claimName, sc.Name, thick,
			),
		}, nil
	}

	return &kwhvalidating.ValidatorResult{Valid: true}, nil
}

func objectNamespace(obj metav1.Object, arReview *model.AdmissionReview) string {
	if ns := obj.GetNamespace(); ns != "" {
		return ns
	}
	if arReview == nil {
		return ""
	}
	return arReview.Namespace
}

func clonedOrRestoredFrom(pvc *v1.PersistentVolumeClaim, namespace string) *pvcDataSource {
	if ref := pvc.Spec.DataSourceRef; ref != nil {
		ns := namespace
		if ref.Namespace != nil && *ref.Namespace != "" {
			ns = *ref.Namespace
		}
		return newPVCDataSource(ref.APIGroup, ref.Kind, ref.Name, ns)
	}
	if ref := pvc.Spec.DataSource; ref != nil {
		return newPVCDataSource(ref.APIGroup, ref.Kind, ref.Name, namespace)
	}
	return nil
}

func newPVCDataSource(apiGroup *string, kind, name, namespace string) *pvcDataSource {
	group := ""
	if apiGroup != nil {
		group = *apiGroup
	}
	switch {
	case group == "" && kind == persistentVolumeClaimKind,
		group == volumeSnapshotAPIGroup && kind == volumeSnapshotKind:
		return &pvcDataSource{kind: kind, name: name, namespace: namespace}
	}
	return nil
}

func claimStorageClass(ctx context.Context, cl client.Client, namespace, name string) (*storagev1.StorageClass, error) {
	var pvc v1.PersistentVolumeClaim
	if err := cl.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &pvc); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	className := pvc.Annotations[v1.BetaStorageClassAnnotation]
	if className == "" && pvc.Spec.StorageClassName != nil {
		className = *pvc.Spec.StorageClassName
	}
	if className == "" {
		return nil, nil
	}
	return getStorageClass(ctx, cl, className)
}

func thickStorage(ctx context.Context, cl client.Client, sc *storagev1.StorageClass) (string, error) {
	if sc == nil || sc.Provisioner != replicatedCSIProvisioner {
		return "", nil
	}

	rscName := sc.Parameters[srv.CSIParamRSCNameKey]
	if rscName == "" {
		rscName = sc.Name
	}

	var rsc srv.ReplicatedStorageClass
	if err := cl.Get(ctx, client.ObjectKey{Name: rscName}, &rsc); err != nil {
		if apierrors.IsNotFound(err) {
			return "", nil
		}
		return "", err
	}

	if rsc.Spec.Storage != nil {
		if rsc.Spec.Storage.Type == srv.ReplicatedStoragePoolTypeLVM {
			return fmt.Sprintf("thick LVM volumes (spec.storage.type of ReplicatedStorageClass %q is LVM)", rsc.Name), nil
		}
		return "", nil
	}

	poolName := rsc.Spec.StoragePool //nolint:staticcheck // SA1019: the LINSTOR control plane names its pool in the deprecated field
	if poolName == "" {
		return "", nil
	}

	var rsp srv.ReplicatedStoragePool
	if err := cl.Get(ctx, client.ObjectKey{Name: poolName}, &rsp); err != nil {
		if apierrors.IsNotFound(err) {
			return "", nil
		}
		return "", err
	}
	if rsp.Spec.Type == srv.ReplicatedStoragePoolTypeLVM {
		return fmt.Sprintf("the thick (LVM) ReplicatedStoragePool %q", rsp.Name), nil
	}
	return "", nil
}
