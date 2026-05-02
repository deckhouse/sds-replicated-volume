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

// Package metadatabackupcleanup removes cluster-scoped ReplicatedStorageMetadataBackup objects
// (LINSTOR DB segments stored in the API, e.g. from metadata-backup CronJobs).
package metadatabackupcleanup

import (
	"context"
	"fmt"
	"log/slog"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

const (
	apiGroup    = "storage.deckhouse.io"
	apiVersion  = "v1alpha1"
	apiResource = "replicatedstoragemetadatabackups"
)

// DeleteAll removes all cluster-scoped ReplicatedStorageMetadataBackup objects in one request
// (Kubernetes deletecollection, same idea as kubectl delete <resource> --all).
func DeleteAll(ctx context.Context, dyn dynamic.Interface, log *slog.Logger) error {
	gvr := schema.GroupVersionResource{Group: apiGroup, Version: apiVersion, Resource: apiResource}
	err := dyn.Resource(gvr).DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Debug("metadata backup CRD or API not found, skipping cleanup")
			return nil
		}
		return fmt.Errorf("delete collection %s: %w", gvr.String(), err)
	}
	log.Info("deleted ReplicatedStorageMetadataBackup collection")
	return nil
}
