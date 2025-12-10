package drbdconfig

import "github.com/spf13/afero"

// FS wraps the filesystem to allow swap in tests; use FS for all file I/O.
var FS = &afero.Afero{Fs: afero.NewOsFs()}
