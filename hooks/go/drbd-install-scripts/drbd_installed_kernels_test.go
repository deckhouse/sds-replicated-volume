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
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const altTemplate = "nodegroupconfiguration-drbd-install-altlinux-like.yaml"

var installTemplates = []string{centosTemplate, debianTemplate, altTemplate}

const logStubs = `bb-log-info() {
  echo "info: $*"
}
bb-log-warning() {
  echo "warning: $*"
}
bb-log-error() {
  echo "error: $*"
}
`

const semverStub = `d8-semver() {
  [ "$1" = compare ] || return 2
  if [ "$2" = "$3" ]; then
    echo 0
  elif [ "$(printf '%s\n%s\n' "$2" "$3" | sort -V | head -n 1)" = "$2" ]; then
    echo -1
  else
    echo 1
  fi
}
`

const versionRegex = `^9\.[0-9]+\.[0-9]+([-+][0-9A-Za-z][0-9A-Za-z.+-]*)?$`

func lines(out string) []string {
	var got []string
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		if line != "" {
			got = append(got, line)
		}
	}
	return got
}

func modulesTree(t *testing.T, installed []string, leftovers []string) string {
	t.Helper()
	dir := t.TempDir()
	for _, kernel := range installed {
		if err := os.MkdirAll(filepath.Join(dir, kernel, "kernel"), 0o755); err != nil {
			t.Fatalf("create kernel %s: %v", kernel, err)
		}
	}
	for _, kernel := range leftovers {
		if err := os.MkdirAll(filepath.Join(dir, kernel, "updates"), 0o755); err != nil {
			t.Fatalf("create leftover %s: %v", kernel, err)
		}
	}
	return dir
}

func TestDRBDNewerKernels(t *testing.T) {
	tests := []struct {
		name      string
		running   string
		installed []string
		leftovers []string
		want      []string
	}{
		{
			name:      "only installed kernels newer than the running one are listed",
			running:   "5.15.131-35.el7.3.x86_64",
			installed: []string{"5.15.131-30.el7.3.x86_64", "5.15.131-35.el7.3.x86_64", "5.15.131-36.el7.3.x86_64", "6.1.20-1.el7.3.x86_64"},
			leftovers: []string{"5.15.131-40.el7.3.x86_64"},
			want:      []string{"5.15.131-36.el7.3.x86_64", "6.1.20-1.el7.3.x86_64"},
		},
		{
			name:      "Debian versions with an ABI number and a flavour are ordered by version",
			running:   "6.1.0-37-cloud-amd64",
			installed: []string{"6.1.0-9-cloud-amd64", "6.1.0-37-cloud-amd64", "6.1.0-40-cloud-amd64", "6.12.85+deb13-cloud-amd64"},
			want:      []string{"6.1.0-40-cloud-amd64", "6.12.85+deb13-cloud-amd64"},
		},
		{
			name:      "a node with the running kernel only has nothing to build",
			running:   "6.12.116-6.12-alt1",
			installed: []string{"6.12.116-6.12-alt1"},
		},
	}

	for _, template := range installTemplates {
		fn := scriptFunction(t, template, "drbd_newer_kernels")
		for _, tt := range tests {
			t.Run(template+"/"+tt.name, func(t *testing.T) {
				dir := modulesTree(t, tt.installed, tt.leftovers)
				out, code := runScript(t, fn+`drbd_newer_kernels "`+dir+`" "`+tt.running+`"`, nil)
				if code != 0 {
					t.Fatalf("exit code %d, output %q", code, out)
				}
				if got := lines(out); !slices.Equal(got, tt.want) {
					t.Fatalf("kernels:\n got %q\nwant %q", got, tt.want)
				}
			})
		}
	}
}

func TestDRBDKernelNeedsBuild(t *testing.T) {
	tests := []struct {
		name    string
		version string
		desired string
		want    bool
	}{
		{name: "no DRBD built for the kernel", desired: "9.2.16", want: true},
		{name: "the in-tree DRBD 8 does not count", version: "8.4.11", desired: "9.2.16", want: true},
		{name: "an older DRBD 9", version: "9.2.15", desired: "9.2.16", want: true},
		{name: "the desired DRBD", version: "9.2.16", desired: "9.2.16"},
		{name: "a newer DRBD", version: "9.2.17", desired: "9.2.16"},
		{name: "the desired Flant build", version: "9.2.19-flant.5", desired: "9.2.19-flant.5"},
	}

	for _, template := range installTemplates {
		fn := scriptFunction(t, template, "drbd_kernel_needs_build")
		for _, tt := range tests {
			t.Run(template+"/"+tt.name, func(t *testing.T) {
				stub := "modinfo() {\n  [ -n \"$VERSION\" ] || return 1\n  echo \"$VERSION\"\n}\n"
				t.Setenv("VERSION", tt.version)
				script := stub + semverStub + fn +
					`if drbd_kernel_needs_build /lib/modules 6.1.0-40-cloud-amd64 "` + tt.desired + `" '` + versionRegex + `'; then echo yes; else echo no; fi`
				out, code := runScript(t, script, nil)
				if code != 0 {
					t.Fatalf("exit code %d, output %q", code, out)
				}
				want := "no"
				if tt.want {
					want = "yes"
				}
				if got := strings.TrimSpace(out); got != want {
					t.Fatalf("needs build %q, want %q", got, want)
				}
			})
		}
	}
}

func TestDRBDTry(t *testing.T) {
	for _, template := range installTemplates {
		fn := scriptFunction(t, template, "drbd_try")
		t.Run(template, func(t *testing.T) {
			script := fn + `failing() {
  false
  echo "not stopped"
}
passing() {
  echo "passed"
}
drbd_try failing
echo "failing rc=$drbd_try_rc"
drbd_try passing
echo "passing rc=$drbd_try_rc"
false
echo "errexit is off"
`
			out, code := runScript(t, script, nil)
			if code == 0 {
				t.Fatalf("errexit is not restored after drbd_try, output %q", out)
			}
			want := []string{"failing rc=1", "passed", "passing rc=0"}
			if got := lines(out); !slices.Equal(got, want) {
				t.Fatalf("output:\n got %q\nwant %q", got, want)
			}
		})
	}
}

func TestDRBDBuildForNewerKernels(t *testing.T) {
	tests := []struct {
		name         string
		semverFails  bool
		failedKernel string
		want         []string
	}{
		{
			name: "every kernel that needs DRBD gets a build",
			want: []string{
				"info: Building DRBD 9.2.16 for kernel 6.1.0-40-cloud-amd64, which is installed and not booted yet",
				"built 6.1.0-40-cloud-amd64",
				"info: Building DRBD 9.2.16 for kernel 6.12.85+deb13-cloud-amd64, which is installed and not booted yet",
				"built 6.12.85+deb13-cloud-amd64",
			},
		},
		{
			name:         "a failed build is a warning and the next kernel is still built",
			failedKernel: "6.1.0-40-cloud-amd64",
			want: []string{
				"info: Building DRBD 9.2.16 for kernel 6.1.0-40-cloud-amd64, which is installed and not booted yet",
				"warning: DRBD 9.2.16 is not built for kernel 6.1.0-40-cloud-amd64, so the node builds it after it boots into that kernel",
				"info: Building DRBD 9.2.16 for kernel 6.12.85+deb13-cloud-amd64, which is installed and not booted yet",
				"built 6.12.85+deb13-cloud-amd64",
			},
		},
		{
			name:        "without d8-semver nothing is built and the step goes on",
			semverFails: true,
			want: []string{
				"warning: d8-semver is not installed, so DRBD is not built for the installed kernels the node has not booted yet",
			},
		},
	}

	for _, template := range installTemplates {
		fn := scriptFunction(t, template, "drbd_try") + scriptFunction(t, template, "drbd_build_for_newer_kernels")
		for _, tt := range tests {
			t.Run(template+"/"+tt.name, func(t *testing.T) {
				stubs := logStubs + `drbd_install_semver() {
  [ -z "$SEMVER_FAILS" ]
}
drbd_kernels_to_build() {
  [ "$1" = /lib/modules ] && [ "$2" = 6.1.0-37-cloud-amd64 ] && [ "$3" = 9.2.16 ] || return 1
  printf '%s\n' 6.1.0-40-cloud-amd64 6.12.85+deb13-cloud-amd64
}
drbd_build_for_kernel() {
  [ "$1" != "$FAILED_KERNEL" ]
  echo "built $1"
}
kernel_version_in_use=6.1.0-37-cloud-amd64
desired_version=9.2.16
current_version_regex='` + versionRegex + `'
`
				semverFails := ""
				if tt.semverFails {
					semverFails = "yes"
				}
				t.Setenv("SEMVER_FAILS", semverFails)
				t.Setenv("FAILED_KERNEL", tt.failedKernel)
				out, code := runScript(t, stubs+fn+"drbd_build_for_newer_kernels\necho done\n", nil)
				if code != 0 {
					t.Fatalf("exit code %d, output %q", code, out)
				}
				want := append(slices.Clone(tt.want), "done")
				if got := lines(out); !slices.Equal(got, want) {
					t.Fatalf("output:\n got %q\nwant %q", got, want)
				}
			})
		}
	}
}

func TestKernelDevelPackageName(t *testing.T) {
	fn := scriptFunction(t, centosTemplate, "kernel_devel_package_name")

	tests := []struct {
		owner string
		want  string
	}{
		{owner: "kernel", want: "kernel-devel"},
		{owner: "kernel-core", want: "kernel-devel"},
		{owner: "kernel-modules-core", want: "kernel-devel"},
		{owner: "kernel-lt", want: "kernel-lt-devel"},
		{owner: "kernel-lt-core", want: "kernel-lt-devel"},
	}

	for _, tt := range tests {
		t.Run(tt.owner, func(t *testing.T) {
			stub := "rpm() {\n  printf '%s\\t' \"$OWNER\"\n}\n"
			t.Setenv("OWNER", tt.owner)
			out, code := runScript(t, stub+fn+"kernel_devel_package_name /lib/modules/5.15.131-36.el7.3.x86_64", nil)
			if code != 0 {
				t.Fatalf("exit code %d, output %q", code, out)
			}
			if got := strings.TrimSpace(out); got != tt.want {
				t.Fatalf("package %q, want %q", got, tt.want)
			}
		})
	}
}

func TestKernelDevelPackagesWithoutKernel(t *testing.T) {
	fn := scriptFunction(t, centosTemplate, "kernel_devel_packages_without_kernel")

	tests := []struct {
		name      string
		installed []string
		leftovers []string
		packages  string
		want      []string
	}{
		{
			name:      "headers of installed kernels stay, headers of removed kernels go",
			installed: []string{"5.15.131-35.el7.3.x86_64", "5.15.131-36.el7.3.x86_64"},
			leftovers: []string{"5.15.131-30.el7.3.x86_64"},
			packages: "kernel-lt-devel-5.15.131-30.el7.3.x86_64\n" +
				"kernel-lt-devel-5.15.131-35.el7.3.x86_64\n" +
				"kernel-lt-devel-5.15.131-36.el7.3.x86_64\n" +
				"kernel-lt-devel-5.15.120-1.el7.3.x86_64\n",
			want: []string{"kernel-lt-devel-5.15.131-30.el7.3.x86_64", "kernel-lt-devel-5.15.120-1.el7.3.x86_64"},
		},
		{
			name:      "a package that is not installed gives nothing to remove",
			installed: []string{"5.15.131-35.el7.3.x86_64"},
			packages:  "package kernel-lt-devel is not installed\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := modulesTree(t, tt.installed, tt.leftovers)
			stub := "rpm() {\n  [ \"$1\" = -q ] && [ \"$2\" = kernel-lt-devel ] || return 2\n  printf '%s' \"$PACKAGES\"\n}\n"
			t.Setenv("PACKAGES", tt.packages)
			out, code := runScript(t, stub+fn+`kernel_devel_packages_without_kernel kernel-lt-devel "`+dir+`"`, nil)
			if code != 0 {
				t.Fatalf("exit code %d, output %q", code, out)
			}
			if got := lines(out); !slices.Equal(got, tt.want) {
				t.Fatalf("packages:\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestDebianHeadersPackagesWithoutKernel(t *testing.T) {
	fn := scriptFunction(t, debianTemplate, "debian_headers_packages_without_kernel")

	tests := []struct {
		name      string
		installed []string
		owners    string
		want      []string
	}{
		{
			name:      "headers of installed kernels stay, headers of removed kernels go",
			installed: []string{"6.1.0-37-cloud-amd64", "6.1.0-40-cloud-amd64"},
			owners: "linux-headers-6.1.0-37-cloud-amd64: /lib/modules/6.1.0-37-cloud-amd64/build\n" +
				"linux-headers-6.1.0-40-cloud-amd64: /lib/modules/6.1.0-40-cloud-amd64/build\n" +
				"linux-headers-6.1.0-9-cloud-amd64: /lib/modules/6.1.0-9-cloud-amd64/build\n",
			want: []string{"linux-headers-6.1.0-9-cloud-amd64"},
		},
		{
			name:      "the merged /usr layout and a path owned by two packages",
			installed: []string{"6.12.85+deb13-cloud-amd64"},
			owners: "linux-headers-6.12.85+deb13-cloud-amd64: /usr/lib/modules/6.12.85+deb13-cloud-amd64/build\n" +
				"linux-headers-6.12.41+deb13-cloud-amd64, linux-headers-6.12.41+deb13-extra: /usr/lib/modules/6.12.41+deb13-cloud-amd64/build\n",
			want: []string{"linux-headers-6.12.41+deb13-cloud-amd64", "linux-headers-6.12.41+deb13-extra"},
		},
		{
			name:      "no headers known to dpkg",
			installed: []string{"6.1.0-37-cloud-amd64"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := modulesTree(t, tt.installed, nil)
			stub := "dpkg-query() {\n  [ \"$1\" = -S ] || return 2\n  [ -n \"$OWNERS\" ] || { echo \"dpkg-query: no path found matching pattern $2\" >&2; return 1; }\n  printf '%s' \"$OWNERS\"\n}\n"
			t.Setenv("OWNERS", tt.owners)
			out, code := runScript(t, stub+fn+`debian_headers_packages_without_kernel "`+dir+`"`, nil)
			if code != 0 {
				t.Fatalf("exit code %d, output %q", code, out)
			}
			if got := lines(out); !slices.Equal(got, tt.want) {
				t.Fatalf("packages:\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestDebianKernelGcc(t *testing.T) {
	fn := scriptFunction(t, debianTemplate, "debian_kernel_gcc")

	tests := []struct {
		name   string
		config string
		want   string
	}{
		{name: "a kernel built with gcc-12", config: `CONFIG_CC_VERSION_TEXT="x86_64-linux-gnu-gcc-12 (Debian 12.2.0-14) 12.2.0"` + "\n", want: "gcc-12"},
		{name: "a kernel built with the default compiler", config: `CONFIG_CC_VERSION_TEXT="x86_64-linux-gnu-gcc-14 (Debian 14.2.0-19) 14.2.0"` + "\n", want: "gcc"},
		{name: "headers without .config", want: "gcc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.config != "" {
				if err := os.WriteFile(filepath.Join(dir, ".config"), []byte(tt.config), 0o600); err != nil {
					t.Fatalf("write .config: %v", err)
				}
			}
			out, code := runScript(t, fn+`debian_kernel_gcc "`+dir+`"`, nil)
			if code != 0 {
				t.Fatalf("exit code %d, output %q", code, out)
			}
			if got := strings.TrimSpace(out); got != tt.want {
				t.Fatalf("gcc %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAltNewerKernelOfFlavour(t *testing.T) {
	fn := scriptFunction(t, altTemplate, "drbd_newer_kernels") +
		scriptFunction(t, altTemplate, "alt_kernel_flavour") +
		scriptFunction(t, altTemplate, "alt_newer_kernel_of_flavour")

	installed := []string{"6.12.110-6.12-alt1", "6.12.116-6.12-alt1", "6.12.118-6.12-alt1", "6.1.150-un-def-alt1", "6.18.5-6.18-alt1"}

	tests := []struct {
		kernel string
		want   string
	}{
		{kernel: "6.12.116-6.12-alt1", want: "6.12.118-6.12-alt1"},
		{kernel: "6.12.118-6.12-alt1"},
		{kernel: "6.1.150-un-def-alt1"},
		{kernel: "6.18.5-6.18-alt1"},
	}

	for _, tt := range tests {
		t.Run(tt.kernel, func(t *testing.T) {
			dir := modulesTree(t, installed, nil)
			out, code := runScript(t, fn+`alt_newer_kernel_of_flavour "`+dir+`" "`+tt.kernel+`"`, nil)
			if code != 0 {
				t.Fatalf("exit code %d, output %q", code, out)
			}
			if got := strings.TrimSpace(out); got != tt.want {
				t.Fatalf("newer kernel %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSharedFunctionsAreTheSameInEveryScript(t *testing.T) {
	for _, name := range []string{
		"drbd_newer_kernels", "drbd_kernel_needs_build", "drbd_kernels_to_build", "drbd_try",
		"drbd_install_semver", "drbd_install_sources", "drbd_spaas_url", "drbd_build_and_install",
		"drbd_build_for_newer_kernels",
	} {
		want := scriptFunction(t, centosTemplate, name)
		for _, template := range []string{debianTemplate, altTemplate} {
			if got := scriptFunction(t, template, name); got != want {
				t.Errorf("%s differs between %s and %s", name, centosTemplate, template)
			}
		}
	}
}
