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

package utils //nolint:revive // pre-existing package name; renaming is a separate refactor

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/deckhouse/sds-common-lib/logger"
)

func isExtFS(fsType string) bool {
	switch fsType {
	case "ext3", "ext4":
		return true
	}
	return false
}

func extHasErrors(dumpe2fsOutput string) bool {
	for _, line := range strings.Split(dumpe2fsOutput, "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "Filesystem state":
			if strings.Contains(value, "with errors") {
				return true
			}
		case "FS Error count":
			if count, err := strconv.ParseUint(value, 10, 64); err == nil && count > 0 {
				return true
			}
		}
	}
	return false
}

func (s *Store) explainResizeError(devicePath string, resizeErr error) error {
	fsType, err := s.NodeStorage.GetDiskFormat(devicePath)
	if err != nil || !isExtFS(fsType) {
		return resizeErr
	}

	out, err := s.NodeStorage.Exec.Command("dumpe2fs", "-h", devicePath).CombinedOutput()
	if err != nil {
		s.Log.Warn("Failed to read the ext superblock after a resize failure", logger.Err(err), slog.Any("devicePath", devicePath))
		return resizeErr
	}
	if !extHasErrors(string(out)) {
		return resizeErr
	}

	return fmt.Errorf("the %s filesystem on %s is marked as having errors, and the kernel refuses to resize such a filesystem while it is mounted; "+
		"stop the pods that use the volume and check the filesystem with `e2fsck -f` while it is not mounted (see the sds-replicated-volume FAQ), "+
		"then the resize is retried when the volume is mounted again; original error: %w",
		fsType, devicePath, resizeErr)
}
