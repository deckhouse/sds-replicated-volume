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

package migrator

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	srvv1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/config"
	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/linstorbackup"
	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/metadatabackupcleanup"
)

// runStage3 backs up and removes LINSTOR CRs, metadata backups, and legacy ReplicatedStoragePool objects.
func (m *Migrator) runStage3(ctx context.Context) error {
	m.log.Info("stage 3: starting LINSTOR cleanup")

	if err := m.updateMigrationStateRetrying(ctx, config.StateStage3Started); err != nil {
		return err
	}

	var linstorCRDGone bool
	if err := m.retryTransient(ctx, "check LINSTOR CRD", func() error {
		err := m.crdExists(ctx, config.LinstorCRDName)
		if err == nil {
			linstorCRDGone = false
			return nil
		}
		if apierrors.IsNotFound(err) {
			linstorCRDGone = true
			return nil
		}
		if isTransientAPIError(err) {
			return err
		}
		return fmt.Errorf("failed to check LINSTOR CRD %q: %w", config.LinstorCRDName, err)
	}); err != nil {
		return err
	}

	if linstorCRDGone {
		m.log.Info("LINSTOR CRD already gone, skipping CR backup and CRD deletion")
	} else {
		if err := m.retryTransient(ctx, "LINSTOR CR backup", func() error {
			return linstorbackup.WriteGroupBackup(ctx, m.client, m.dynamicClient, m.log, linstorbackup.BackupDir())
		}); err != nil {
			return fmt.Errorf("LINSTOR CR backup: %w", err)
		}
		if err := m.retryTransient(ctx, "delete LINSTOR CRDs", func() error {
			return linstorbackup.DeleteCRDsForGroup(ctx, m.client, m.log, linstorbackup.APIGroup)
		}); err != nil {
			return fmt.Errorf("delete LINSTOR CRDs: %w", err)
		}
	}

	if err := m.retryTransient(ctx, "delete ReplicatedStorageMetadataBackup objects", func() error {
		return metadatabackupcleanup.DeleteAll(ctx, m.dynamicClient, m.log)
	}); err != nil {
		return fmt.Errorf("delete ReplicatedStorageMetadataBackup objects: %w", err)
	}

	var toDelete []string
	if err := m.retryTransient(ctx, "backup legacy ReplicatedStoragePool objects", func() error {
		var err error
		toDelete, err = linstorbackup.WriteLegacyRSPBackupFromCluster(ctx, m.client, m.log, linstorbackup.BackupDir())
		return err
	}); err != nil {
		return fmt.Errorf("backup legacy ReplicatedStoragePool objects: %w", err)
	}
	for _, poolName := range toDelete {
		if err := m.deleteLegacyReplicatedStoragePool(ctx, poolName); err != nil {
			return err
		}
	}

	return m.updateMigrationStateAllCompleted(ctx)
}

// deleteLegacyReplicatedStoragePool issues Delete for a legacy RSP and verifies the object is gone.
// If the object is stuck in Terminating (finalizers), a warning is logged; stage 3 still continues.
func (m *Migrator) deleteLegacyReplicatedStoragePool(ctx context.Context, poolName string) error {
	var alreadyGone bool
	if err := m.retryTransient(ctx, "delete legacy ReplicatedStoragePool", func() error {
		rsp := &srvv1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: poolName},
		}
		if err := m.client.Delete(ctx, rsp); err != nil {
			if apierrors.IsNotFound(err) {
				alreadyGone = true
				return nil
			}
			return err
		}
		return nil
	}); err != nil {
		return fmt.Errorf("delete legacy ReplicatedStoragePool %q: %w", poolName, err)
	}
	if alreadyGone {
		return nil
	}

	return m.retryTransient(ctx, "verify legacy ReplicatedStoragePool deletion", func() error {
		got := &srvv1alpha1.ReplicatedStoragePool{}
		err := m.client.Get(ctx, types.NamespacedName{Name: poolName}, got)
		switch {
		case apierrors.IsNotFound(err):
			m.log.Info("deleted legacy ReplicatedStoragePool", "name", poolName)
		case err == nil && got.DeletionTimestamp != nil:
			m.log.Warn("legacy ReplicatedStoragePool stuck terminating",
				"name", poolName, "finalizers", got.Finalizers)
		case err != nil:
			return fmt.Errorf("verify deletion of legacy ReplicatedStoragePool %q: %w", poolName, err)
		}
		return nil
	})
}

func (m *Migrator) updateMigrationStateAllCompleted(ctx context.Context) error {
	if err := m.updateMigrationStateRetrying(ctx, config.StateAllCompleted); err != nil {
		return err
	}
	m.log.Info("stage 3: completed, migration finished")
	return nil
}
