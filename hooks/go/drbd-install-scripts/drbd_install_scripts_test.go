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

package drbdinstallscripts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	debianTemplate = "nodegroupconfiguration-drbd-install-debian-like.yaml"
	centosTemplate = "nodegroupconfiguration-drbd-install-centos-like.yaml"
	snapshotAPI    = "http://snapshot.debian.org/mr"
	headers85      = "linux-headers-6.12.85+deb13-cloud-amd64"
	headers85URI   = "linux-headers-6.12.85%2Bdeb13-cloud-amd64"
)

const snapshotStub = `debian_snapshot_get() {
  local url="$1"
  local fixture
  fixture="$FIXTURES/$(printf '%s' "$url" | sha256sum | cut -c1-64)"
  if [ ! -f "$fixture" ]; then
    return 22
  fi
  cat "$fixture"
}
`

func scriptFunction(t *testing.T, template, name string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("..", "..", "..", "templates", template))
	if err != nil {
		t.Fatalf("read %s: %v", template, err)
	}

	_, content, found := strings.Cut(string(data), "\n  content: |\n")
	if !found {
		t.Fatalf("%s has no content block", template)
	}

	var body []string
	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimPrefix(line, "    ")
		if len(body) == 0 && trimmed != name+"() {" {
			continue
		}
		body = append(body, trimmed)
		if trimmed == "}" {
			return strings.Join(body, "\n") + "\n"
		}
	}

	t.Fatalf("%s has no function %s", template, name)
	return ""
}

func runScript(t *testing.T, script string, fixtures map[string]string) (string, int) {
	t.Helper()

	for _, tool := range []string{"bash", "jq", "sha256sum"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("%s is required to run the install script functions: %v", tool, err)
		}
	}

	dir := t.TempDir()
	for url, body := range fixtures {
		sum := sha256.Sum256([]byte(url))
		if err := os.WriteFile(filepath.Join(dir, hex.EncodeToString(sum[:])), []byte(body), 0o600); err != nil {
			t.Fatalf("write fixture for %s: %v", url, err)
		}
	}

	cmd := exec.Command("bash", "--noprofile", "--norc", "-eEo", "pipefail", "-c", script)
	cmd.Env = append(os.Environ(), "FIXTURES="+dir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		return stdout.String(), 0
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("run script: %v (stderr: %s)", err, stderr.String())
	}
	return stdout.String(), exitErr.ExitCode()
}

func TestDebianSnapshotHeadersSources(t *testing.T) {
	fn := scriptFunction(t, debianTemplate, "debian_snapshot_headers_sources")

	versions := `{"result":[{"binary_version":"6.12.85-1"}]}`
	binfiles := `{"result":[{"architecture":"arm64","hash":"aaaa"},{"architecture":"amd64","hash":"8319"}]}`
	versionsURL := snapshotAPI + "/binary/" + headers85URI + "/"
	binfilesURL := snapshotAPI + "/binary/" + headers85URI + "/6.12.85-1/binfiles"
	infoURL := snapshotAPI + "/file/8319/info"

	tests := []struct {
		name     string
		arch     string
		fixtures map[string]string
		want     []string
	}{
		{
			name: "a package published through the security archive is taken from there",
			arch: "amd64",
			fixtures: map[string]string{
				versionsURL: versions,
				binfilesURL: binfiles,
				infoURL: `{"result":[` +
					`{"archive_name":"debian-security","first_seen":"20260430T195431Z"},` +
					`{"archive_name":"debian","first_seen":"20260502T211508Z"}]}`,
			},
			want: []string{
				"deb [check-valid-until=no] http://snapshot.debian.org/archive/debian-security/20260430T195431Z/ trixie-security main",
			},
		},
		{
			name: "a package published only through the main archive comes from its update suites",
			arch: "amd64",
			fixtures: map[string]string{
				versionsURL: versions,
				binfilesURL: binfiles,
				infoURL:     `{"result":[{"archive_name":"debian","first_seen":"20260502T211508Z"}]}`,
			},
			want: []string{
				"deb [check-valid-until=no] http://snapshot.debian.org/archive/debian/20260502T211508Z/ trixie-proposed-updates main",
				"deb [check-valid-until=no] http://snapshot.debian.org/archive/debian/20260502T211508Z/ trixie-backports main",
			},
		},
		{
			name: "a package without a build for the node architecture gives no sources",
			arch: "ppc64el",
			fixtures: map[string]string{
				versionsURL: versions,
				binfilesURL: binfiles,
			},
		},
		{
			name: "a package unknown to the archive gives no sources",
			arch: "amd64",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script := snapshotStub + fn + `debian_snapshot_headers_sources "` + headers85 + `" "` + tt.arch + `" trixie || true`
			out, code := runScript(t, script, tt.fixtures)
			if code != 0 {
				t.Fatalf("exit code %d, output %q", code, out)
			}
			got := strings.Split(strings.TrimSpace(out), "\n")
			if strings.TrimSpace(out) == "" {
				got = nil
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("sources:\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestDebianCandidateVersion(t *testing.T) {
	fn := scriptFunction(t, debianTemplate, "debian_candidate_version")

	tests := []struct {
		name   string
		policy string
		want   string
	}{
		{
			name: "a package in the configured repositories",
			policy: headers85 + ":\n" +
				"  Installed: (none)\n" +
				"  Candidate: 6.12.85-1\n" +
				"  Version table:\n" +
				"     6.12.85-1 500\n",
			want: "6.12.85-1",
		},
		{
			name: "a package known only from a dependency has no candidate",
			policy: headers85 + ":\n" +
				"  Installed: (none)\n" +
				"  Candidate: (none)\n" +
				"  Version table:\n",
		},
		{
			name: "a binary package that only the Sources index of its source package names",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := "apt-cache() {\n  [ \"$1\" = policy ] || return 100\n  printf '%s' \"$POLICY\"\n}\n"
			script := stub + fn + `debian_candidate_version "` + headers85 + `"`
			t.Setenv("POLICY", tt.policy)
			out, code := runScript(t, script, nil)
			if code != 0 {
				t.Fatalf("exit code %d, output %q", code, out)
			}
			if got := strings.TrimSpace(out); got != tt.want {
				t.Fatalf("candidate %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRockyVaultPackageURLs(t *testing.T) {
	fn := scriptFunction(t, centosTemplate, "rocky_vault_package_urls")

	tests := []struct {
		name     string
		kernel   string
		wantCode int
		want     []string
	}{
		{
			name:   "a release kernel of an archived minor release",
			kernel: "5.14.0-570.17.1.el9_6.x86_64",
			want: []string{
				"https://dl.rockylinux.org/vault/rocky/9.6/AppStream/x86_64/os/Packages/k/kernel-devel-5.14.0-570.17.1.el9_6.x86_64.rpm",
				"https://dl.rockylinux.org/vault/rocky/9.6/AppStream/x86_64/kickstart/Packages/k/kernel-devel-5.14.0-570.17.1.el9_6.x86_64.rpm",
			},
		},
		{
			name:   "a respin kernel on another architecture",
			kernel: "5.14.0-687.10.1.el9_8.0.1.aarch64",
			want: []string{
				"https://dl.rockylinux.org/vault/rocky/9.8/AppStream/aarch64/os/Packages/k/kernel-devel-5.14.0-687.10.1.el9_8.0.1.aarch64.rpm",
				"https://dl.rockylinux.org/vault/rocky/9.8/AppStream/aarch64/kickstart/Packages/k/kernel-devel-5.14.0-687.10.1.el9_8.0.1.aarch64.rpm",
			},
		},
		{
			name:     "a kernel without an Enterprise Linux release tag",
			kernel:   "6.1.0-37-cloud-amd64",
			wantCode: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script := fn + `rocky_vault_package_urls "kernel-devel-` + tt.kernel + `" "` + tt.kernel + `"`
			out, code := runScript(t, script, nil)
			if code != tt.wantCode {
				t.Fatalf("exit code %d, want %d, output %q", code, tt.wantCode, out)
			}
			got := strings.Fields(out)
			if len(got) == 0 {
				got = nil
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("urls:\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}
