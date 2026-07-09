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
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/config"
)

// envDeckhouseUID is the env var carrying the deckhouse user UID.
const envDeckhouseUID = "DECKHOUSE_UID"

// envDeckhouseGID is the env var carrying the deckhouse user GID.
const envDeckhouseGID = "DECKHOUSE_GID"

// runCreateDirectoryForYourselfMode implements --mode=create-directory-for-yourself.
// It reads the deckhouse UID/GID from the environment, creates config.MigratorHostDir
// owned by that user with 0700 permissions, logs the action, and returns. The caller
// is expected to run as root (the Job init container) so that os.Chown succeeds.
func runCreateDirectoryForYourselfMode(log *slog.Logger) error {
	uid, gid, err := deckhouseUIDGIDFromEnv()
	if err != nil {
		return err
	}
	return createMigratorHostDir(config.MigratorHostDir, uid, gid, log)
}

// deckhouseUIDGIDFromEnv parses DECKHOUSE_UID and DECKHOUSE_GID env vars.
func deckhouseUIDGIDFromEnv() (int, int, error) {
	uidStr := os.Getenv(envDeckhouseUID)
	gidStr := os.Getenv(envDeckhouseGID)
	if uidStr == "" || gidStr == "" {
		return 0, 0, fmt.Errorf("%s and %s env vars must be set", envDeckhouseUID, envDeckhouseGID)
	}
	uid, err := strconv.Atoi(uidStr)
	if err != nil || uid < 0 {
		return 0, 0, fmt.Errorf("invalid %s %q: expected a non-negative integer", envDeckhouseUID, uidStr)
	}
	gid, err := strconv.Atoi(gidStr)
	if err != nil || gid < 0 {
		return 0, 0, fmt.Errorf("invalid %s %q: expected a non-negative integer", envDeckhouseGID, gidStr)
	}
	return uid, gid, nil
}

// createMigratorHostDir creates path with 0700 permissions owned by uid:gid.
// os.Chmod is called explicitly after MkdirAll so the mode does not depend on
// the process umask. The caller must run as root for os.Chown to succeed.
func createMigratorHostDir(path string, uid, gid int, log *slog.Logger) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create migrator host directory %q: %w", path, err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("set permissions on migrator host directory %q: %w", path, err)
	}
	if err := os.Chown(path, uid, gid); err != nil {
		return fmt.Errorf("chown migrator host directory %q to %d:%d: %w", path, uid, gid, err)
	}
	log.Info("created migrator host directory", "path", path, "uid", uid, "gid", gid, "mode", "0700")
	return nil
}
