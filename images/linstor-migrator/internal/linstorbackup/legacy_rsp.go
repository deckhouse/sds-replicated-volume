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

package linstorbackup

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	srvv1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/config"
)

const backupLegacyRSPGzFile = "legacy-rsp.gz"

// IsLegacyReplicatedStoragePoolCandidate reports whether poolName may refer to a legacy
// ReplicatedStoragePool removed after migration (excludes migrator/RSC auto-created names).
func IsLegacyReplicatedStoragePoolCandidate(poolName string) bool {
	if poolName == "" {
		return false
	}
	if strings.HasPrefix(poolName, config.AutoReplicatedStoragePoolNamePrefix) {
		return false
	}
	if strings.HasPrefix(poolName, config.AutoReplicatedStoragePoolRSCNamePrefix) {
		return false
	}
	return true
}

// WriteLegacyRSPBackupFromCluster lists all ReplicatedStoragePool objects, backs up those that pass
// IsLegacyReplicatedStoragePoolCandidate to legacy-rsp.gz, and returns names to delete.
func WriteLegacyRSPBackupFromCluster(
	ctx context.Context,
	kClient client.Client,
	log *slog.Logger,
	dir string,
) ([]string, error) {
	var list srvv1alpha1.ReplicatedStoragePoolList
	if err := kClient.List(ctx, &list); err != nil {
		return nil, fmt.Errorf("list ReplicatedStoragePool: %w", err)
	}

	names := make([]string, 0, len(list.Items))
	for i := range list.Items {
		name := list.Items[i].Name
		if IsLegacyReplicatedStoragePoolCandidate(name) {
			names = append(names, name)
		}
	}
	return WriteLegacyRSPBackup(ctx, kClient, log, dir, names)
}

// WriteLegacyRSPBackup loads legacy ReplicatedStoragePool objects by name, writes legacy-rsp.gz
// when at least one exists, and returns the names that were backed up (for a subsequent delete pass).
// No file is created when nothing is found.
func WriteLegacyRSPBackup(
	ctx context.Context,
	kClient client.Client,
	log *slog.Logger,
	dir string,
	linstorPoolNames []string,
) ([]string, error) {
	var backedUp []srvv1alpha1.ReplicatedStoragePool

	seen := make(map[string]struct{})
	for _, poolName := range linstorPoolNames {
		if !IsLegacyReplicatedStoragePoolCandidate(poolName) {
			continue
		}
		if _, ok := seen[poolName]; ok {
			continue
		}
		seen[poolName] = struct{}{}

		rsp := &srvv1alpha1.ReplicatedStoragePool{}
		if err := kClient.Get(ctx, client.ObjectKey{Name: poolName}, rsp); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("get legacy ReplicatedStoragePool %q: %w", poolName, err)
		}
		backedUp = append(backedUp, *rsp.DeepCopy())
	}

	if len(backedUp) == 0 {
		log.Debug("no legacy ReplicatedStoragePool objects to back up")
		return nil, nil
	}

	sort.Slice(backedUp, func(i, j int) bool {
		return backedUp[i].Name < backedUp[j].Name
	})

	var yamlBuf bytes.Buffer
	for i := range backedUp {
		b, err := rspToApplyFriendlyYAML(&backedUp[i])
		if err != nil {
			return nil, fmt.Errorf("marshal legacy ReplicatedStoragePool %q: %w", backedUp[i].Name, err)
		}
		if i > 0 {
			yamlBuf.WriteString("---\n")
		}
		yamlBuf.Write(b)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create backup directory %q: %w", dir, err)
	}

	path := getNextFilePath(filepath.Join(dir, backupLegacyRSPGzFile))
	if err := writeGzipFile(path, yamlBuf.Bytes()); err != nil {
		return nil, err
	}
	if err := deduplicateBackups(log, dir, backupLegacyRSPGzFile); err != nil {
		return nil, fmt.Errorf("deduplicate legacy RSP backups: %w", err)
	}

	names := make([]string, len(backedUp))
	for i := range backedUp {
		names[i] = backedUp[i].Name
	}
	log.Info("wrote legacy ReplicatedStoragePool backup", "file", backupLegacyRSPGzFile, "count", len(names))
	return names, nil
}

func rspToApplyFriendlyYAML(rsp *srvv1alpha1.ReplicatedStoragePool) ([]byte, error) {
	u, err := runtime.DefaultUnstructuredConverter.ToUnstructured(rsp.DeepCopy())
	if err != nil {
		return nil, err
	}
	stripApplyUnfriendlyFields(u)
	u["apiVersion"] = srvv1alpha1.SchemeGroupVersion.String()
	u["kind"] = "ReplicatedStoragePool"
	return yaml.Marshal(u)
}
