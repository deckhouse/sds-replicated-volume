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

package indexes

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/syncbudget"
)

const (
	// IndexFieldRVByReplicatedStorageClassName is used to quickly list
	// ReplicatedVolume objects referencing a specific RSC.
	IndexFieldRVByReplicatedStorageClassName = "spec.replicatedStorageClassName"

	// IndexFieldRVByStoragePoolName is used to quickly list
	// ReplicatedVolume objects using a specific RSP.
	IndexFieldRVByStoragePoolName = "status.configuration.replicatedStoragePoolName"

	// IndexFieldRVByResyncClaimCandidacy is used to quickly list the
	// ReplicatedVolume objects that could claim a resync slot.
	//
	// Unlike the indexes above, its key is not a field path: syncbudget.MayClaim
	// derives it from the volume's status.
	IndexFieldRVByResyncClaimCandidacy = "syncbudget.mayClaim"

	// IndexValueResyncClaimCandidate is the only value
	// IndexFieldRVByResyncClaimCandidacy ever takes: a volume that cannot claim
	// carries no key at all rather than a second value.
	IndexValueResyncClaimCandidate = "true"
)

// RegisterRVByReplicatedStorageClassName registers the index for listing
// ReplicatedVolume objects by spec.replicatedStorageClassName.
func RegisterRVByReplicatedStorageClassName(mgr manager.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&v1alpha1.ReplicatedVolume{},
		IndexFieldRVByReplicatedStorageClassName,
		func(obj client.Object) []string {
			rv, ok := obj.(*v1alpha1.ReplicatedVolume)
			if !ok {
				return nil
			}
			if rv.Spec.ReplicatedStorageClassName == "" {
				return nil
			}
			return []string{rv.Spec.ReplicatedStorageClassName}
		},
	); err != nil {
		return fmt.Errorf("index ReplicatedVolume by spec.replicatedStorageClassName: %w", err)
	}
	return nil
}

// RegisterRVByStoragePoolName registers the index for listing
// ReplicatedVolume objects by status.configuration.storagePoolName.
func RegisterRVByStoragePoolName(mgr manager.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&v1alpha1.ReplicatedVolume{},
		IndexFieldRVByStoragePoolName,
		func(obj client.Object) []string {
			rv, ok := obj.(*v1alpha1.ReplicatedVolume)
			if !ok {
				return nil
			}
			if rv.Status.Configuration == nil {
				return nil
			}
			return []string{rv.Status.Configuration.ReplicatedStoragePoolName}
		},
	); err != nil {
		return fmt.Errorf("index ReplicatedVolume by status.configuration.replicatedStoragePoolName: %w", err)
	}
	return nil
}

// RegisterRVByResyncClaimCandidacy registers the index for listing the
// ReplicatedVolume objects that could claim a resync slot.
func RegisterRVByResyncClaimCandidacy(mgr manager.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&v1alpha1.ReplicatedVolume{},
		IndexFieldRVByResyncClaimCandidacy,
		ExtractRVResyncClaimCandidacy,
	); err != nil {
		return fmt.Errorf("index ReplicatedVolume by syncbudget.mayClaim: %w", err)
	}
	return nil
}

// ExtractRVResyncClaimCandidacy is the extractor behind IndexFieldRVByResyncClaimCandidacy.
//
// It is exported, unlike the extractors above, so that a fake client can register the very
// function the manager registers. The predicate it wraps decides which volumes the resync queue is
// built from, and a test-only copy of it could agree with this one today and not tomorrow.
func ExtractRVResyncClaimCandidacy(obj client.Object) []string {
	rv, ok := obj.(*v1alpha1.ReplicatedVolume)
	if !ok {
		return nil
	}
	if !syncbudget.MayClaim(rv) {
		return nil
	}
	return []string{IndexValueResyncClaimCandidate}
}
