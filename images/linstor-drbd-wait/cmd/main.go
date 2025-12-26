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

package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/sds-replicated-volume/images/linstor-drbd-wait/pkg/logger"
)

const (
	EnvLogLevel      = "LOG_LEVEL"
	EnvTimeout       = "TIMEOUT_SEC"
	EnvSleep         = "SLEEP_SEC"
	EnvFilePath      = "FILE_PATH"
	EnvMinVersion    = "MIN_VERSION"
	EnvWaitingMsg    = "WAITING_MSG"
	VersionKeyPrefix = "version:"
)

var (
	Loglevel logger.Verbosity
)

func getEnv(envName string, defaultValue string) string {
	envVal := os.Getenv(envName)
	if envVal == "" {
		return defaultValue
	}

	return envVal
}

// parseVersion extracts major, minor, patch from a version string like "9.2.5"
func parseVersion(versionStr string) (major, minor, patch int, err error) {
	// Remove any leading/trailing whitespace
	versionStr = strings.TrimSpace(versionStr)

	// Match version pattern like "9.2.5" or "9.2"
	re := regexp.MustCompile(`^(\d+)\.(\d+)(?:\.(\d+))?`)
	matches := re.FindStringSubmatch(versionStr)
	if matches == nil {
		return 0, 0, 0, fmt.Errorf("invalid version format: %s", versionStr)
	}

	major, err = strconv.Atoi(matches[1])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid major version: %s", matches[1])
	}

	minor, err = strconv.Atoi(matches[2])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid minor version: %s", matches[2])
	}

	if matches[3] != "" {
		patch, err = strconv.Atoi(matches[3])
		if err != nil {
			return 0, 0, 0, fmt.Errorf("invalid patch version: %s", matches[3])
		}
	}

	return major, minor, patch, nil
}

// compareVersions returns:
//
//	1 if v1 > v2
//	0 if v1 == v2
//	-1 if v1 < v2
func compareVersions(major1, minor1, patch1, major2, minor2, patch2 int) int {
	if major1 > major2 {
		return 1
	}
	if major1 < major2 {
		return -1
	}
	// major1 == major2
	if minor1 > minor2 {
		return 1
	}
	if minor1 < minor2 {
		return -1
	}
	// minor1 == minor2
	if patch1 > patch2 {
		return 1
	}
	if patch1 < patch2 {
		return -1
	}
	return 0
}

// extractVersionFromLine extracts version string from a line like "version: 9.2.5 (api:2/proto:86-121)"
func extractVersionFromLine(line string) (string, bool) {
	if !strings.Contains(line, VersionKeyPrefix) {
		return "", false
	}

	// Extract version after "version:"
	idx := strings.Index(line, VersionKeyPrefix)
	if idx == -1 {
		return "", false
	}

	remainder := strings.TrimSpace(line[idx+len(VersionKeyPrefix):])
	// Version ends at space or end of string
	fields := strings.Fields(remainder)
	if len(fields) == 0 {
		return "", false
	}

	return fields[0], true
}

func mainExecution(log *logger.Logger, filePath string, minVersion string, waitingMsg string, sleepSec int64) int {
	log.Info(fmt.Sprintf("Go Version:%s ", goruntime.Version()))
	log.Info(fmt.Sprintf("OS/Arch:Go OS/Arch:%s/%s ", goruntime.GOOS, goruntime.GOARCH))
	log.Info(fmt.Sprintf("Start to watch file '%s' for version >= '%s'", filePath, minVersion))

	// Parse the minimum required version
	minMajor, minMinor, minPatch, err := parseVersion(minVersion)
	if err != nil {
		log.Error(err, fmt.Sprintf("unable to parse minimum version '%s'", minVersion))
		return 2
	}

	for {
		// Open the file for reading
		file, err := os.Open(filePath)
		if err != nil {
			log.Warning(fmt.Sprintf("Error opening file %s: %v", filePath, err))
			log.Info(waitingMsg)
			time.Sleep(time.Duration(sleepSec) * time.Second)
			continue
		}

		// Create a scanner to read the file line by line
		scanner := bufio.NewScanner(file)
		found := false
		var currentVersion string

		// Iterate over each line
		for scanner.Scan() {
			line := scanner.Text()
			// Check if the line contains version info
			versionStr, ok := extractVersionFromLine(line)
			if !ok {
				continue
			}

			currentVersion = versionStr
			curMajor, curMinor, curPatch, err := parseVersion(versionStr)
			if err != nil {
				log.Warning(fmt.Sprintf("Unable to parse version from line '%s': %v", line, err))
				continue
			}

			// Check if current version >= minimum version
			if compareVersions(curMajor, curMinor, curPatch, minMajor, minMinor, minPatch) >= 0 {
				found = true
				break
			}

			log.Debug(fmt.Sprintf("Found version %s, but need >= %s", versionStr, minVersion))
		}

		// Check for any scanning error
		if scanErr := scanner.Err(); scanErr != nil {
			log.Error(scanErr, fmt.Sprintf("reading file %s", filePath))
			file.Close()
			return 3
		}

		file.Close()

		// If appropriate version found, exit the loop
		if found {
			log.Info(fmt.Sprintf("Found version %s >= %s in file '%s'", currentVersion, minVersion, filePath))
			break
		}

		// Print the waiting message and sleep for SLEEP seconds
		log.Info(waitingMsg)
		time.Sleep(time.Duration(sleepSec) * time.Second)
	}

	return 0
}

func main() {
	loglevel := os.Getenv(EnvLogLevel)
	if loglevel == "" {
		Loglevel = logger.DebugLevel
	} else {
		Loglevel = logger.Verbosity(loglevel)
	}

	log, err := logger.NewLogger(Loglevel)
	if err != nil {
		fmt.Printf("unable to create NewLogger, err: %s\n", err.Error())
		os.Exit(1)
	}

	sleep := os.Getenv(EnvSleep)
	sleepSec := int64(15)
	if sleep != "" {
		if sleepSec, err = strconv.ParseInt(sleep, 10, 0); err != nil {
			log.Error(err, fmt.Sprintf("unable to parse %s env variable to int", EnvSleep))
		}
	}

	filePath := getEnv(EnvFilePath, "/proc/drbd")
	minVersion := getEnv(EnvMinVersion, "9.2.0")
	waitingMsg := getEnv(EnvWaitingMsg, "Waiting for DRBD version >= 9.2.0 on host")

	returnValue := mainExecution(log, filePath, minVersion, waitingMsg, sleepSec)
	os.Exit(returnValue)
}
