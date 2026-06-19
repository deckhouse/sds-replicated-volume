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

package drbdutils

import (
	"bufio"
	"context"
	"os"
	"strings"
)

// FlantExtensionsSupported indicates whether the running DRBD kernel module
// is a Flant build that supports quorum-dynamic-voters and non-voting options.
// Detected once at startup via DetectCapabilities().
var FlantExtensionsSupported bool

// ProcDRBDPath is the path to /proc/drbd. Overridable in tests.
var ProcDRBDPath = "/proc/drbd"

// DetectCapabilities ensures the DRBD kernel module is loaded, then reads
// /proc/drbd, parses the DRBD kernel module version, and sets capability flags.
// The version line looks like:
//
//	version: 9.2.13-flant.1 (api:2/proto:118-122)
//
// If the version string contains "-flant", FlantExtensionsSupported is set to true.
//
// A "drbdsetup status" is run first purely for its side effect: when
// /sys/module/drbd does not exist, drbdsetup triggers a module load and
// initializes it, so the /proc/drbd read below (and any /sys/module/drbd access
// by the caller) observe a loaded module. The status output is irrelevant; the
// command's error is returned so the caller can report a non-zero exit.
func DetectCapabilities(ctx context.Context) error {
	_, statusErr := ExecuteStatus(ctx, "")

	version := readDRBDVersion()
	FlantExtensionsSupported = strings.Contains(version, "-flant")

	return statusErr
}

// readDRBDVersion reads the first line of /proc/drbd and extracts the version string.
func readDRBDVersion() string {
	f, err := os.Open(ProcDRBDPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return ""
	}

	// Line format: "version: 9.2.13-flant.1 (api:2/proto:118-122)"
	line := scanner.Text()
	_, after, found := strings.Cut(line, "version: ")
	if !found {
		return ""
	}

	// Take only the version part (before the first space)
	version, _, _ := strings.Cut(after, " ")
	return version
}
