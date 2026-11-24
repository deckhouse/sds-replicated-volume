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

const initramfsTemplate = "nodegroupconfiguration-drbd-initramfs.yaml"

const initramfsStubs = `lsinitrd() {
  cat "$1"
}
dracut() {
  echo "dracut $*"
  [ "$3" != "$FAILING_KERNEL" ]
}
bb-log-info() {
  echo "info: $*"
}
bb-log-warning() {
  echo "warning: $*"
}
`

const (
	dracutListing        = "-rw-r--r-- 1 root root 1024 Nov  7 10:00 usr/lib/dracut/build-parameter.txt\n"
	initramfsToolsMarker = "-rw-r--r-- 1 root root 1024 Nov  7 10:00 conf/initramfs.conf\n"
	inTreeDRBD           = "-rw-r--r-- 1 root root 7680 Nov  7 10:00 usr/lib/modules/%s/kernel/drivers/block/drbd/drbd.ko.xz\n"
	updatesDRBD          = "-rw-r--r-- 1 root root 13604 Nov  9 10:00 usr/lib/modules/%s/updates/drbd.ko\n"
	modulesLoadConf      = "-rw-r--r-- 1 root root 5 Nov  7 10:00 etc/modules-load.d/d8_drbd.conf\n"
)

func TestRebuildInitramfsWithDRBD(t *testing.T) {
	fn := scriptFunction(t, initramfsTemplate, "rebuild_initramfs_with_drbd")

	type image struct {
		name    string
		listing string
	}

	tests := []struct {
		name    string
		kernels []string
		images  []image
		failing string
		want    []string
	}{
		{
			name:    "a dracut image with the in-tree DRBD of a kernel DRBD 9 was not built for yet is rebuilt",
			kernels: []string{"5.15.131-35.el7.3.x86_64"},
			images: []image{{
				name:    "initramfs-5.15.131-35.el7.3.x86_64.img",
				listing: dracutListing + modulesLoadConf + strings.ReplaceAll(inTreeDRBD, "%s", "5.15.131-35.el7.3.x86_64"),
			}},
			want: []string{
				"info: BOOT/initramfs-5.15.131-35.el7.3.x86_64.img carries a DRBD kernel module, rebuilding it without one",
				"dracut -f BOOT/initramfs-5.15.131-35.el7.3.x86_64.img 5.15.131-35.el7.3.x86_64",
			},
		},
		{
			name:    "a dracut image named the Debian way with a DRBD 9 from updates is rebuilt",
			kernels: []string{"5.15.0-75-generic"},
			images: []image{{
				name:    "initrd.img-5.15.0-75-generic",
				listing: dracutListing + modulesLoadConf + strings.ReplaceAll(updatesDRBD, "%s", "5.15.0-75-generic"),
			}},
			want: []string{
				"info: BOOT/initrd.img-5.15.0-75-generic carries a DRBD kernel module, rebuilding it without one",
				"dracut -f BOOT/initrd.img-5.15.0-75-generic 5.15.0-75-generic",
			},
		},
		{
			name:    "a dracut image without DRBD is left as it is",
			kernels: []string{"6.12.87-1.red80.x86_64"},
			images: []image{{
				name:    "initramfs-6.12.87-1.red80.x86_64.img",
				listing: dracutListing + modulesLoadConf,
			}},
		},
		{
			name:    "an image dracut did not build is left as it is even with DRBD in it",
			kernels: []string{"6.8.0-90-generic"},
			images: []image{{
				name:    "initrd.img-6.8.0-90-generic",
				listing: initramfsToolsMarker + strings.ReplaceAll(inTreeDRBD, "%s", "6.8.0-90-generic"),
			}},
		},
		{
			name:    "a kernel without an image and a rescue image of no installed kernel are skipped",
			kernels: []string{"5.15.131-36.el7.3.x86_64"},
			images: []image{{
				name:    "initramfs-0-rescue-0123456789abcdef.img",
				listing: dracutListing + strings.ReplaceAll(inTreeDRBD, "%s", "5.15.131-35.el7.3.x86_64"),
			}},
		},
		{
			name:    "a failed rebuild is reported and the other kernels are still handled",
			kernels: []string{"5.15.131-35.el7.3.x86_64", "5.15.131-36.el7.3.x86_64"},
			images: []image{
				{
					name:    "initramfs-5.15.131-35.el7.3.x86_64.img",
					listing: dracutListing + strings.ReplaceAll(updatesDRBD, "%s", "5.15.131-35.el7.3.x86_64"),
				},
				{
					name:    "initramfs-5.15.131-36.el7.3.x86_64.img",
					listing: dracutListing + strings.ReplaceAll(inTreeDRBD, "%s", "5.15.131-36.el7.3.x86_64"),
				},
			},
			failing: "5.15.131-35.el7.3.x86_64",
			want: []string{
				"info: BOOT/initramfs-5.15.131-35.el7.3.x86_64.img carries a DRBD kernel module, rebuilding it without one",
				"dracut -f BOOT/initramfs-5.15.131-35.el7.3.x86_64.img 5.15.131-35.el7.3.x86_64",
				"warning: dracut failed to rebuild BOOT/initramfs-5.15.131-35.el7.3.x86_64.img, so the node may still load DRBD from it at boot",
				"info: BOOT/initramfs-5.15.131-36.el7.3.x86_64.img carries a DRBD kernel module, rebuilding it without one",
				"dracut -f BOOT/initramfs-5.15.131-36.el7.3.x86_64.img 5.15.131-36.el7.3.x86_64",
			},
		},
		{
			name: "a node without kernel module directories is left as it is",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			boot := filepath.Join(root, "boot")
			modules := filepath.Join(root, "modules")
			for _, dir := range []string{boot, modules} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatalf("create %s: %v", dir, err)
				}
			}
			for _, kernel := range tt.kernels {
				if err := os.MkdirAll(filepath.Join(modules, kernel), 0o755); err != nil {
					t.Fatalf("create modules of %s: %v", kernel, err)
				}
			}
			for _, img := range tt.images {
				if err := os.WriteFile(filepath.Join(boot, img.name), []byte(img.listing), 0o600); err != nil {
					t.Fatalf("write %s: %v", img.name, err)
				}
			}

			t.Setenv("FAILING_KERNEL", tt.failing)
			script := initramfsStubs + fn + `rebuild_initramfs_with_drbd "` + boot + `" "` + modules + `"`
			out, code := runScript(t, script, nil)
			if code != 0 {
				t.Fatalf("exit code %d, output %q", code, out)
			}

			var got []string
			for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
				if line != "" {
					got = append(got, strings.ReplaceAll(line, boot, "BOOT"))
				}
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("output:\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}
