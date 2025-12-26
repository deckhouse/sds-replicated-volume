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

package rvstatusreplicas

import (
	"context"
	"fmt"
	"log/slog"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	u "github.com/deckhouse/sds-common-lib/utils"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

const (
	IndexRVRByReplicatedVolumeName = "spec.replicatedVolumeName"
)

func BuildController(mgr manager.Manager) error {
	log := slog.Default().With("name", ControllerName)

	// Setup field indexer for RVR by replicatedVolumeName
	if err := setupIndexers(mgr); err != nil {
		return u.LogError(log, fmt.Errorf("setting up indexers: %w", err))
	}

	rec := NewReconciler(
		mgr.GetClient(),
		log,
	)

	return u.LogError(
		log,
		builder.ControllerManagedBy(mgr).
			Named(ControllerName).
			Watches(
				&v1alpha1.ReplicatedVolumeReplica{},
				handler.EnqueueRequestsFromMapFunc(mapRVRToOwnerRV),
				builder.WithPredicates(predicate.Funcs{
					UpdateFunc: func(e event.UpdateEvent) bool {
						oldRVR := e.ObjectOld.(*v1alpha1.ReplicatedVolumeReplica)
						newRVR := e.ObjectNew.(*v1alpha1.ReplicatedVolumeReplica)
						switch {
						case oldRVR.Spec.NodeName != newRVR.Spec.NodeName:
							return true
						case oldRVR.Spec.Type != newRVR.Spec.Type:
							return true
						case oldRVR.DeletionTimestamp != newRVR.DeletionTimestamp:
							return true
						default:
							return false
						}
					},
				}),
			).
			Complete(rec))
}

// mapRVRToOwnerRV maps a ReplicatedVolumeReplica to its owner ReplicatedVolume
func mapRVRToOwnerRV(ctx context.Context, obj client.Object) []reconcile.Request {
	rvr, ok := obj.(*v1alpha1.ReplicatedVolumeReplica)
	if !ok {
		return nil
	}

	if rvr.Spec.ReplicatedVolumeName == "" {
		return nil
	}

	return []reconcile.Request{
		{
			NamespacedName: types.NamespacedName{
				Name: rvr.Spec.ReplicatedVolumeName,
			},
		},
	}
}

// setupIndexers sets up field indexers for efficient filtering
func setupIndexers(mgr manager.Manager) error {
	return mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&v1alpha1.ReplicatedVolumeReplica{},
		IndexRVRByReplicatedVolumeName,
		IndexRVRByReplicatedVolumeNameFunc,
	)
}

// IndexRVRByReplicatedVolumeNameFunc is the indexer function for RVR by replicatedVolumeName
func IndexRVRByReplicatedVolumeNameFunc(obj client.Object) []string {
	rvr := obj.(*v1alpha1.ReplicatedVolumeReplica)
	if rvr.Spec.ReplicatedVolumeName == "" {
		return nil
	}
	return []string{rvr.Spec.ReplicatedVolumeName}
}
