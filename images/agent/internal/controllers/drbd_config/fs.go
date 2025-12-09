package drbdconfig

import "github.com/spf13/afero"

// afs wraps the filesystem to allow swap in tests; use afs for all file I/O.
var afs = &afero.Afero{Fs: afero.NewOsFs()}
