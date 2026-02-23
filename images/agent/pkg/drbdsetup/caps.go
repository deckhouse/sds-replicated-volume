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

package drbdsetup

import (
	"bufio"
	"os"
	"strings"
)

// FlantExtensionsSupported indicates whether the running DRBD kernel module
// is a Flant build that supports quorum-dynamic-voters and non-voting options.
// Detected once at startup via DetectCapabilities().
var FlantExtensionsSupported bool

// ProcDRBDPath is the path to /proc/drbd. Overridable in tests.
var ProcDRBDPath = "/proc/drbd"

// DetectCapabilities reads /proc/drbd, parses the DRBD kernel module version,
// and sets capability flags. The version line looks like:
//
//	version: 9.2.13-flant.1 (api:2/proto:118-122)
//
// If the version string contains "-flant", FlantExtensionsSupported is set to true.
func DetectCapabilities() {
	version := readDRBDVersion()
	FlantExtensionsSupported = strings.Contains(version, "-flant")
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
