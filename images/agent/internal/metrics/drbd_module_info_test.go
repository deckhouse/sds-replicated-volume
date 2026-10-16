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

package metrics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestDRBDModuleInfoCollectorExportsTheLoadedModuleVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "version")
	// The kernel terminates the value with a newline.
	if err := os.WriteFile(path, []byte("9.2.18-flant.10\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	expected := `
# HELP sds_drbd_module_info Version of the DRBD kernel module loaded on the node, including the build suffix (from /sys/module/drbd/version); the value is always 1. Absent while the module is not loaded or the version cannot be read.
# TYPE sds_drbd_module_info gauge
sds_drbd_module_info{version="9.2.18-flant.10"} 1
`
	if err := testutil.CollectAndCompare(newDRBDModuleInfoCollector(path), strings.NewReader(expected)); err != nil {
		t.Fatal(err)
	}
}

func TestDRBDModuleInfoCollectorEmitsNothingWithoutAVersion(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(empty, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for name, path := range map[string]string{
		"module not loaded": filepath.Join(t.TempDir(), "missing"),
		"empty version":     empty,
	} {
		t.Run(name, func(t *testing.T) {
			if n := testutil.CollectAndCount(newDRBDModuleInfoCollector(path)); n != 0 {
				t.Fatalf("expected no series, got %d", n)
			}
		})
	}
}
