package drbdconfig

import "github.com/spf13/afero"

// SetFSForTests replaces filesystem for tests and returns a restore function.
// Production keeps OS-backed fs; tests swap it to memory/fs mocks.
func SetFSForTests(testFS afero.Fs) func() {
	prevAfs := afs
	afs = &afero.Afero{Fs: testFS}
	return func() {
		afs = prevAfs
	}
}
