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

package debug

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SaveLogBody writes data to a timestamped file in the given snapshots
// directory. Returns the absolute path, or "" if snapshotsDir is empty or
// the write fails.
func SaveLogBody(snapshotsDir, key string, data []byte) string {
	if snapshotsDir == "" {
		return ""
	}
	stamp := time.Now().Format("15-04-05.000")
	filename := fmt.Sprintf("log-%s-%s.json", stamp, key)
	p := filepath.Join(snapshotsDir, filename)

	if err := os.WriteFile(p, data, 0o644); err != nil {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// SaveSnapshot writes the full object as pretty-printed YAML to
// <snapshotsDir>/<kind>-<name>-<HH-MM-SS.mmm>.yaml and returns the absolute
// path. Returns "" if snapshotsDir is empty or the write fails.
func SaveSnapshot(snapshotsDir, kind, name string, content string) string {
	if snapshotsDir == "" {
		return ""
	}
	stamp := time.Now().Format("15-04-05.000")
	filename := fmt.Sprintf("%s-%s-%s.yaml", kind, name, stamp)
	p := filepath.Join(snapshotsDir, filename)

	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		return ""
	}

	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// OSC8Link wraps text in an OSC 8 hyperlink escape sequence pointing to a
// local file. If path is empty, text is returned unchanged.
func OSC8Link(text, path string) string {
	if path == "" {
		return text
	}
	return fmt.Sprintf("\033]8;;file://%s\033\\%s\033]8;;\033\\", path, text)
}
