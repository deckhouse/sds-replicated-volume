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
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// TargetDRBDVersion is the DRBD version this agent is built against. Every managed
// module must report exactly this version, on disk and once loaded.
const TargetDRBDVersion = "9.2.19-flant.10"

// Overridden by tests.
var sysModuleDir = "/sys/module"

// upgradeNeeded compares exactly in both directions: any difference means the node
// is not running this agent's own modules.
func upgradeNeeded(log *slog.Logger) (bool, error) {
	needed := false
	for _, name := range moduleLoadOrder {
		running, err := readRunningModuleVersion(name)
		if err != nil {
			return false, fmt.Errorf("reading running version of module %q: %w", name, err)
		}

		switch {
		case running == "":
			log.Info("Kernel module not loaded, upgrade needed", "module", name)
			needed = true
		case running != TargetDRBDVersion:
			log.Info("Kernel module version differs from target, upgrade needed",
				"module", name,
				"running", running,
				"target", TargetDRBDVersion)
			needed = true
		default:
			log.Info("Kernel module version matches target",
				"module", name,
				"version", running)
		}
	}
	return needed, nil
}

// readRunningModuleVersion returns an empty string when the module is not loaded or
// declares no version — either way it is not the module this agent expects.
func readRunningModuleVersion(name string) (string, error) {
	data, err := os.ReadFile(filepath.Join(sysModuleDir, name, "version"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

type runningModule struct {
	loaded bool
	// empty when not loaded, or when the module declares no version
	version string
}

func runningModules() (map[string]runningModule, error) {
	running := make(map[string]runningModule, len(moduleLoadOrder))
	for _, name := range moduleLoadOrder {
		path := filepath.Join(sysModuleDir, name)
		switch _, err := os.Stat(path); {
		case err == nil:
		case errors.Is(err, os.ErrNotExist):
			running[name] = runningModule{}
			continue
		default:
			return nil, fmt.Errorf("stat %q: %w", path, err)
		}

		version, err := readRunningModuleVersion(name)
		if err != nil {
			return nil, fmt.Errorf("reading running version of module %q: %w", name, err)
		}
		running[name] = runningModule{loaded: true, version: version}
	}
	return running, nil
}
