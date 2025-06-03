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
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/sds-replicated-volume/images/linstor-drbd-wait/pkg/logger"
)

const (
	EnvLogLevel    = "LOG_LEVEL"
	EnvTimeout     = "TIMEOUT_SEC"
	EnvSleep       = "SLEEP_SEC"
	EnvFilePath    = "FILE_PATH"
	EnvFileContent = "FILE_CONTENT"
	EnvWaitingMsg  = "WAITING_MSG"
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

func mainExecution(log *logger.Logger, filePath string, fileContent string, waitingMsg string, sleepSec int64) int {
	log.Info(fmt.Sprintf("Go Version:%s ", goruntime.Version()))
	log.Info(fmt.Sprintf("OS/Arch:Go OS/Arch:%s/%s ", goruntime.GOOS, goruntime.GOARCH))
	log.Info(fmt.Sprintf("Start to watch file '%s' for string '%s'", filePath, fileContent))

	for {
		// Open the file for reading
		file, err := os.Open(filePath)
		if err != nil {
			log.Warning(fmt.Sprintf("Error opening file %s: %v", filePath, err))
			log.Info(waitingMsg)
			time.Sleep(time.Duration(sleepSec) * time.Second)
			continue
		}
		defer file.Close()

		// Create a scanner to read the file line by line
		scanner := bufio.NewScanner(file)
		found := false

		// Iterate over each line
		for scanner.Scan() {
			line := scanner.Text()
			// Check if the line contains the desired string
			if strings.Contains(line, fileContent) {
				found = true
				break
			}
		}

		// Check for any scanning error
		if err := scanner.Err(); err != nil {
			log.Error(err, fmt.Sprintf("reading file %s", filePath))
			return 3
		}

		// If string found, exit the loop
		if found {
			log.Debug(fmt.Sprintf("Found string '%s' in file '%s'", fileContent, filePath))
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
	fileContent := getEnv(EnvFileContent, "version: 9.2")
	waitingMsg := getEnv(EnvWaitingMsg, "Waiting for DRBD version 9.2.x on host")

	returnValue := mainExecution(log, filePath, fileContent, waitingMsg, sleepSec)
	os.Exit(returnValue)
}
