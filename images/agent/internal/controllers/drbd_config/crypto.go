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
	"bufio"
	"fmt"
	"strings"
)

var kernelHasCryptoOkCache = map[string]struct{}{}

func kernelHasCrypto(name string) (bool, error) {
	if _, ok := kernelHasCryptoOkCache[name]; ok {
		return true, nil
	}

	f, err := FS.Open("/proc/crypto")
	if err != nil {
		return false, fmt.Errorf("opening /proc/crypto: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	found := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "name") {
			// line is like: "name         : aes"
			fields := strings.SplitN(line, ":", 2)
			if len(fields) == 2 && strings.TrimSpace(fields[1]) == name {
				found = true
			}
		}
		// each algorithm entry is separated by a blank line
		if line == "" && found {
			kernelHasCryptoOkCache[name] = struct{}{}
			return true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("reading /proc/crypto: %w", err)
	}
	return false, nil
}
