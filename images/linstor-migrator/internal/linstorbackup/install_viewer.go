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
	"fmt"
	"os"
	"path/filepath"
)

const backupLinstorViewerFile = "linstor-viewer"

// InstallLinstorViewer writes the embedded linstor backup viewer binary into dir.
func InstallLinstorViewer(dir string) error {
	if len(embeddedLinstor) == 0 {
		return fmt.Errorf("embedded linstor viewer binary is empty (see internal/linstorbackup/embedded/README.md or build via werf)")
	}
	path := filepath.Join(dir, backupLinstorViewerFile)
	if err := os.WriteFile(path, embeddedLinstor, 0o755); err != nil {
		return fmt.Errorf("write %q: %w", path, err)
	}
	return nil
}
