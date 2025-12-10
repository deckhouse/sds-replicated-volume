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
