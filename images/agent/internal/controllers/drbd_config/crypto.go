package drbdconfig

import (
	"bufio"
	"fmt"
	"strings"
)

var kernelHasCryptoOkCache map[string]struct{} = map[string]struct{}{}

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
