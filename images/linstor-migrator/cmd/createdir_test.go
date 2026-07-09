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
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// statMode returns the filesystem mode bits of path.
func statMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	return info.Mode().Perm()
}

// statOwner returns the uid and gid of path (Linux).
func statOwner(t *testing.T, path string) (uint32, uint32) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("unexpected Sys type for %q", path)
	}
	return stat.Uid, stat.Gid
}

func TestCreateMigratorHostDir_CreatesDirWithMode0700(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "linstor-migrator")

	// chown to the current user so os.Chown succeeds without root privileges.
	uid := os.Getuid()
	gid := os.Getgid()

	if err := createMigratorHostDir(target, uid, gid, slog.New(slog.NewTextHandler(os.Stderr, nil))); err != nil {
		t.Fatalf("createMigratorHostDir: %v", err)
	}

	if mode := statMode(t, target); mode != 0o700 {
		t.Fatalf("expected mode 0700, got %#o", mode)
	}
	u, g := statOwner(t, target)
	if u != uint32(uid) || g != uint32(gid) {
		t.Fatalf("expected owner %d:%d, got %d:%d", uid, gid, u, g)
	}
}

func TestCreateMigratorHostDir_Idempotent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "linstor-migrator")
	uid := os.Getuid()
	gid := os.Getgid()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	for i := 0; i < 2; i++ {
		if err := createMigratorHostDir(target, uid, gid, log); err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
	}
	if mode := statMode(t, target); mode != 0o700 {
		t.Fatalf("expected mode 0700, got %#o", mode)
	}
}

func TestDeckhouseUIDGIDFromEnv_MissingVars(t *testing.T) {
	t.Setenv(envDeckhouseUID, "")
	t.Setenv(envDeckhouseGID, "")
	if _, _, err := deckhouseUIDGIDFromEnv(); err == nil {
		t.Fatal("expected error when env vars are missing")
	}
}

func TestDeckhouseUIDGIDFromEnv_Invalid(t *testing.T) {
	cases := []struct{ uid, gid string }{
		{"abc", "64535"},
		{"64535", "abc"},
		{"-1", "64535"},
		{"64535", "-1"},
	}
	for _, c := range cases {
		t.Setenv(envDeckhouseUID, c.uid)
		t.Setenv(envDeckhouseGID, c.gid)
		if _, _, err := deckhouseUIDGIDFromEnv(); err == nil {
			t.Fatalf("expected error for uid=%q gid=%q", c.uid, c.gid)
		}
	}
}

func TestDeckhouseUIDGIDFromEnv_Valid(t *testing.T) {
	t.Setenv(envDeckhouseUID, "64535")
	t.Setenv(envDeckhouseGID, "64535")
	uid, gid, err := deckhouseUIDGIDFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uid != 64535 || gid != 64535 {
		t.Fatalf("expected 64535:64535, got %d:%d", uid, gid)
	}
}
