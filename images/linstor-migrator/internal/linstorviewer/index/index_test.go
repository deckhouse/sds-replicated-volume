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

package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/linstorviewer/load"
)

func TestBuildFromMinimalFixture(t *testing.T) {
	path := filepath.Join("..", "testdata", "minimal-crs.yaml")
	docs, err := load.FromFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	store, err := Build(docs)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	nodes := store.Nodes()
	if len(nodes) != 1 {
		t.Fatalf("nodes: got %d, want 1", len(nodes))
	}
	if nodes[0].Node != "node-a" {
		t.Errorf("node display: got %q, want node-a", nodes[0].Node)
	}
	if !strings.Contains(nodes[0].Addresses, "10.0.0.1:3367") {
		t.Errorf("addresses: got %q", nodes[0].Addresses)
	}

	pools := store.StoragePools()
	if len(pools) != 1 {
		t.Fatalf("pools: got %d, want 1", len(pools))
	}
	if pools[0].StoragePool != "pool-a" || pools[0].PoolName != "vg0" {
		t.Errorf("pool row: %+v", pools[0])
	}

	vols := store.Volumes()
	if len(vols) != 1 {
		t.Fatalf("volumes: got %d, want 1", len(vols))
	}
	if vols[0].Resource != "pvc-test" {
		t.Errorf("resource: got %q", vols[0].Resource)
	}
	if vols[0].MinorNr != "1000" || vols[0].DeviceName != "/dev/drbd1000" {
		t.Errorf("drbd: minor=%q device=%q", vols[0].MinorNr, vols[0].DeviceName)
	}
}

func TestBuildFromGzipFixtureOptional(t *testing.T) {
	path := os.Getenv("LINSTOR_CRS_BACKUP")
	if path == "" {
		path = "/tmp/crs.gz"
	}
	if _, err := os.Stat(path); err != nil {
		t.Skip("no crs.gz at", path)
	}
	docs, err := load.FromFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	store, err := Build(docs)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(store.Nodes()) == 0 {
		t.Error("expected nodes from real backup")
	}
}
