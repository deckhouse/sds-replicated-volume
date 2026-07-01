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
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	srvv1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/config"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := srvv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	return s
}

func TestIsLegacyReplicatedStoragePoolCandidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		label    string
		poolName string
		want     bool
	}{
		{"empty", "", false},
		{"legacy", "thick-data", true},
		{"linstor-auto", config.AutoReplicatedStoragePoolNamePrefix + "foo", false},
		{"auto-rsp", config.AutoReplicatedStoragePoolRSCNamePrefix + "foo", false},
	}
	for _, tc := range tests {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			if got := IsLegacyReplicatedStoragePoolCandidate(tc.poolName); got != tc.want {
				t.Fatalf("IsLegacyReplicatedStoragePoolCandidate(%q) = %v, want %v", tc.poolName, got, tc.want)
			}
		})
	}
}

func TestWriteLegacyRSPBackup_skipsWhenNoneExist(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cl := fake.NewClientBuilder().WithScheme(testScheme(t)).Build()

	names, err := WriteLegacyRSPBackup(context.Background(), cl, slog.Default(), dir, []string{"thick-data", "missing-pool"})
	if err != nil {
		t.Fatalf("WriteLegacyRSPBackup: %v", err)
	}
	if names != nil {
		t.Fatalf("expected nil names, got %v", names)
	}
	if _, err := os.Stat(filepath.Join(dir, backupLegacyRSPGzFile)); !os.IsNotExist(err) {
		t.Fatalf("expected no %s, stat err=%v", backupLegacyRSPGzFile, err)
	}
}

func TestWriteLegacyRSPBackup_writesGzipMultiDoc(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	legacy := &srvv1alpha1.ReplicatedStoragePool{
		ObjectMeta: metav1.ObjectMeta{Name: "thick-data"},
		Spec: srvv1alpha1.ReplicatedStoragePoolSpec{
			Type: srvv1alpha1.ReplicatedStoragePoolTypeLVM,
			LVMVolumeGroups: []srvv1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
				{Name: "lvg-1"},
			},
		},
		Status: srvv1alpha1.ReplicatedStoragePoolStatus{
			Phase: srvv1alpha1.ReplicatedStoragePoolPhaseReady,
		},
	}
	cl := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(legacy).Build()

	names, err := WriteLegacyRSPBackup(context.Background(), cl, slog.Default(), dir, []string{
		"thick-data",
		config.AutoReplicatedStoragePoolNamePrefix + "skip",
		"thick-data",
	})
	if err != nil {
		t.Fatalf("WriteLegacyRSPBackup: %v", err)
	}
	if len(names) != 1 || names[0] != "thick-data" {
		t.Fatalf("names: %v", names)
	}

	f, err := os.Open(filepath.Join(dir, backupLegacyRSPGzFile))
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer f.Close()
	gzr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer gzr.Close()
	body, err := io.ReadAll(gzr)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, "kind: ReplicatedStoragePool") {
		t.Fatalf("missing kind in backup:\n%s", text)
	}
	if !strings.Contains(text, "name: thick-data") {
		t.Fatalf("missing name in backup:\n%s", text)
	}
	if strings.Contains(text, "status:") {
		t.Fatalf("status should be stripped:\n%s", text)
	}
}
