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

package upgrade

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUpgradeNeeded(t *testing.T) {
	const otherVersion = "9.2.18-flant.9"

	tests := []struct {
		name string
		// module name -> version reported in sysfs; absent means not loaded,
		// noVersion means loaded without a version file
		running map[string]string
		want    bool
	}{
		{
			name: "both modules at the target version",
			running: map[string]string{
				"drbd":               TargetDRBDVersion,
				"drbd_transport_tcp": TargetDRBDVersion,
			},
			want: false,
		},
		{
			name:    "nothing loaded",
			running: map[string]string{},
			want:    true,
		},
		{
			name: "drbd loaded at the target version, transport not loaded",
			running: map[string]string{
				"drbd": TargetDRBDVersion,
			},
			want: true,
		},
		{
			name: "transport loaded at the target version, drbd not loaded",
			running: map[string]string{
				"drbd_transport_tcp": TargetDRBDVersion,
			},
			want: true,
		},
		{
			name: "drbd at an older version",
			running: map[string]string{
				"drbd":               otherVersion,
				"drbd_transport_tcp": TargetDRBDVersion,
			},
			want: true,
		},
		{
			name: "transport at an older version",
			running: map[string]string{
				"drbd":               TargetDRBDVersion,
				"drbd_transport_tcp": otherVersion,
			},
			want: true,
		},
		{
			name: "drbd at a newer version",
			running: map[string]string{
				"drbd":               "9.2.20-flant.1",
				"drbd_transport_tcp": TargetDRBDVersion,
			},
			want: true,
		},
		{
			name: "loaded but declaring no version",
			running: map[string]string{
				"drbd":               noVersion,
				"drbd_transport_tcp": noVersion,
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withFakeSysModuleDir(t, tt.running)

			got, err := upgradeNeeded(discardLogger())
			if err != nil {
				t.Fatalf("upgradeNeeded() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("upgradeNeeded() = %v; want %v", got, tt.want)
			}
		})
	}
}

// Loaded-but-unidentifiable must not read as "not loaded": the difference decides
// whether the upgrade unloads the module or just loads it.
func TestRunningModules(t *testing.T) {
	withFakeSysModuleDir(t, map[string]string{
		"drbd":               TargetDRBDVersion,
		"drbd_transport_tcp": noVersion,
	})

	running, err := runningModules()
	if err != nil {
		t.Fatalf("runningModules() error = %v", err)
	}

	want := map[string]runningModule{
		"drbd":               {loaded: true, version: TargetDRBDVersion},
		"drbd_transport_tcp": {loaded: true, version: ""},
	}
	if len(running) != len(want) {
		t.Fatalf("runningModules() = %+v; want %+v", running, want)
	}
	for name, wantModule := range want {
		if running[name] != wantModule {
			t.Errorf("runningModules()[%q] = %+v; want %+v", name, running[name], wantModule)
		}
	}
}

func TestRunningModulesWhenAbsent(t *testing.T) {
	withFakeSysModuleDir(t, nil)

	running, err := runningModules()
	if err != nil {
		t.Fatalf("runningModules() error = %v", err)
	}
	for _, name := range moduleLoadOrder {
		if running[name].loaded {
			t.Errorf("runningModules()[%q].loaded = true; want false", name)
		}
	}
}

func TestReadRunningModuleVersion(t *testing.T) {
	// The kernel writes a trailing newline into these files.
	withFakeSysModuleDir(t, map[string]string{"drbd": TargetDRBDVersion + "\n"})

	got, err := readRunningModuleVersion("drbd")
	if err != nil {
		t.Fatalf("readRunningModuleVersion() error = %v", err)
	}
	if got != TargetDRBDVersion {
		t.Errorf("readRunningModuleVersion() = %q; want %q", got, TargetDRBDVersion)
	}

	got, err = readRunningModuleVersion("drbd_transport_tcp")
	if err != nil {
		t.Fatalf("readRunningModuleVersion() on an absent module: error = %v; want nil", err)
	}
	if got != "" {
		t.Errorf("readRunningModuleVersion() on an absent module = %q; want empty", got)
	}
}

// withFakeSysModuleDir points sysfs lookups at a temporary tree.
func withFakeSysModuleDir(t *testing.T, running map[string]string) {
	t.Helper()

	original := sysModuleDir
	t.Cleanup(func() { sysModuleDir = original })
	sysModuleDir = t.TempDir()

	for name, version := range running {
		dir := filepath.Join(sysModuleDir, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", dir, err)
		}
		if version == noVersion {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, "version"), []byte(version), 0o644); err != nil {
			t.Fatalf("writing version for %q: %v", name, err)
		}
	}
}
