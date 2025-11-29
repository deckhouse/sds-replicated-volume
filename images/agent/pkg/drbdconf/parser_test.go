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

package drbdconf

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestConf(t *testing.T) {
	root, err := os.OpenRoot("./testdata/")
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := Parse(root.FS(), "root.conf")
	if err != nil {
		t.Fatal(err)
	}

	err = cfg.WalkConfigs(func(conf *Root) error {
		filename := "./testdata/out/" + conf.Filename
		dir := filepath.Dir(filename)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
		file, err := os.OpenFile(filename, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
		if err != nil {
			return fmt.Errorf("open file %s: %w", filename, err)
		}
		if n, err := conf.WriteTo(file); err != nil {
			return fmt.Errorf("writing to file %s: %w", filename, err)
		} else {
			t.Logf("wrote %d bytes to %s", n, filename)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	_ = cfg
}
