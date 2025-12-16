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

package drbdconfig

import (
	"path/filepath"

	"github.com/spf13/afero"
)

// FS wraps the filesystem to allow swap in tests; use FS for all file I/O.
var FS = &afero.Afero{Fs: afero.NewOsFs()}

var ResourcesDir = "/var/lib/sds-replicated-volume-agent.d/"

func FilePaths(rvName string) (regularFilePath, tempFilePath string) {
	regularFilePath = filepath.Join(ResourcesDir, rvName+".res")
	tempFilePath = regularFilePath + "_tmp"
	return
}
