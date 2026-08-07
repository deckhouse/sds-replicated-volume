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

package upgrade

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// noVersion asks the fixtures for a module with no MODULE_VERSION at all.
const noVersion = "\x00none"

func TestPreflight(t *testing.T) {
	const otherVersion = "9.2.18-flant.9"

	tests := []struct {
		name string
		// module name -> version its .ko declares; absent means no file at all
		versions map[string]string
		// modules whose file is present but is not an ELF module
		garbage []string
		// modules placed in the fallback directory instead of the first one
		secondDir []string
		// substrings the error must contain, all of them
		wantErr []string
	}{
		{
			name: "all modules present with the target version",
			versions: map[string]string{
				"drbd":               TargetDRBDVersion,
				"drbd_transport_tcp": TargetDRBDVersion,
			},
		},
		{
			name: "module found in the fallback directory",
			versions: map[string]string{
				"drbd":               TargetDRBDVersion,
				"drbd_transport_tcp": TargetDRBDVersion,
			},
			secondDir: []string{"drbd"},
		},
		{
			name: "drbd module file missing",
			versions: map[string]string{
				"drbd_transport_tcp": TargetDRBDVersion,
			},
			wantErr: []string{`module "drbd": module file not found`},
		},
		{
			name: "transport module file missing",
			versions: map[string]string{
				"drbd": TargetDRBDVersion,
			},
			wantErr: []string{`module "drbd_transport_tcp": module file not found`},
		},
		{
			name: "drbd module has an older version",
			versions: map[string]string{
				"drbd":               otherVersion,
				"drbd_transport_tcp": TargetDRBDVersion,
			},
			wantErr: []string{`module "drbd":`, `drbd.ko" has version "` + otherVersion + `", expected "` + TargetDRBDVersion + `"`},
		},
		{
			name: "transport module has an older version",
			versions: map[string]string{
				"drbd":               TargetDRBDVersion,
				"drbd_transport_tcp": otherVersion,
			},
			wantErr: []string{`module "drbd_transport_tcp":`, `drbd_transport_tcp.ko" has version "` + otherVersion + `", expected "` + TargetDRBDVersion + `"`},
		},
		{
			name: "drbd module declares no version",
			versions: map[string]string{
				"drbd":               noVersion,
				"drbd_transport_tcp": TargetDRBDVersion,
			},
			wantErr: []string{`module "drbd":`, `drbd.ko" declares no version, expected "` + TargetDRBDVersion + `"`},
		},
		{
			name: "transport module declares no version",
			versions: map[string]string{
				"drbd":               TargetDRBDVersion,
				"drbd_transport_tcp": noVersion,
			},
			wantErr: []string{`module "drbd_transport_tcp":`, `drbd_transport_tcp.ko" declares no version, expected "` + TargetDRBDVersion + `"`},
		},
		{
			name: "drbd module file is not an ELF module",
			versions: map[string]string{
				"drbd_transport_tcp": TargetDRBDVersion,
			},
			garbage: []string{"drbd"},
			wantErr: []string{`module "drbd":`, "not a readable ELF kernel module"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			release := withFakeModuleDirs(t)
			second := map[string]bool{}
			for _, name := range tt.secondDir {
				second[name] = true
			}
			for name, version := range tt.versions {
				writeFakeModule(t, moduleDirs[boolToIndex(second[name])], release, name, version)
			}
			for _, name := range tt.garbage {
				writeRawModule(t, moduleDirs[0], release, name, []byte("not an elf file"))
			}

			verified, err := preflight(discardLogger())

			if len(tt.wantErr) > 0 {
				if err == nil {
					t.Fatalf("preflight() = %+v, nil; want error containing %q", verified, tt.wantErr)
				}
				if !errors.Is(err, ErrPreflight) {
					t.Errorf("preflight() error %v does not wrap ErrPreflight", err)
				}
				for _, want := range tt.wantErr {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("preflight() error = %q; want it to contain %q", err, want)
					}
				}
				if verified != nil {
					t.Errorf("preflight() returned modules alongside an error: %+v", verified)
				}
				return
			}

			if err != nil {
				t.Fatalf("preflight() error = %v; want nil", err)
			}
			want := []string{"drbd", "drbd_transport_tcp"}
			if len(verified) != len(want) {
				t.Fatalf("preflight() verified %d modules; want %d", len(verified), len(want))
			}
			for i, m := range verified {
				if m.name != want[i] {
					t.Errorf("verified[%d].name = %q; want %q", i, m.name, want[i])
				}
				if _, err := os.Stat(m.path); err != nil {
					t.Errorf("verified[%d].path = %q is not usable: %v", i, m.path, err)
				}
			}
		})
	}
}

func TestPreflightUnreadableModuleFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: file permissions do not deny reads")
	}

	release := withFakeModuleDirs(t)
	for _, name := range moduleLoadOrder {
		writeFakeModule(t, moduleDirs[0], release, name, TargetDRBDVersion)
	}
	unreadable := filepath.Join(moduleDirs[0], release, "updates", "drbd.ko")
	if err := os.Chmod(unreadable, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	_, err := preflight(discardLogger())
	if err == nil {
		t.Fatal("preflight() error = nil; want an unreadable-file error")
	}
	if !errors.Is(err, ErrPreflight) {
		t.Errorf("preflight() error %v does not wrap ErrPreflight", err)
	}
	if !strings.Contains(err.Error(), "not readable") {
		t.Errorf("preflight() error = %q; want it to mention that the file is not readable", err)
	}
}

// Asserted against literals: comparing against moduleLoadOrder would prove nothing.
func TestModuleLoadOrder(t *testing.T) {
	want := []string{"drbd", "drbd_transport_tcp"}
	if !slices.Equal(moduleLoadOrder, want) {
		t.Errorf("moduleLoadOrder = %v; want %v", moduleLoadOrder, want)
	}
}

func TestReadModuleFileVersion(t *testing.T) {
	tests := []struct {
		name    string
		modinfo []string
		want    string
	}{
		{
			name:    "version among other entries",
			modinfo: []string{"license=GPL", "srcversion=DEADBEEF", "version=" + TargetDRBDVersion, "depends="},
			want:    TargetDRBDVersion,
		},
		{
			name:    "no version entry",
			modinfo: []string{"license=GPL", "srcversion=DEADBEEF"},
			want:    "",
		},
		{
			name:    "srcversion must not be mistaken for version",
			modinfo: []string{"srcversion=DEADBEEF"},
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mod.ko")
			writeRawModule(t, filepath.Dir(path), "", "mod", fakeModuleELF(t, tt.modinfo))

			got, err := readModuleFileVersion(filepath.Join(filepath.Dir(path), "mod.ko"))
			if err != nil {
				t.Fatalf("readModuleFileVersion() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("readModuleFileVersion() = %q; want %q", got, tt.want)
			}
		})
	}
}

// --- fixtures ---

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func boolToIndex(b bool) int {
	if b {
		return 1
	}
	return 0
}

// withFakeModuleDirs returns the real kernel release, because module lookup builds
// its paths from its own uname call.
func withFakeModuleDirs(t *testing.T) string {
	t.Helper()

	release, err := kernelRelease()
	if err != nil {
		t.Fatalf("kernelRelease() error = %v", err)
	}

	original := moduleDirs
	t.Cleanup(func() { moduleDirs = original })
	moduleDirs = []string{t.TempDir(), t.TempDir()}

	return release
}

func writeFakeModule(t *testing.T, dir, release, name, version string) {
	t.Helper()

	modinfo := []string{"license=GPL", "srcversion=0BADC0DE"}
	if version != noVersion {
		modinfo = append(modinfo, "version="+version)
	}
	writeRawModule(t, dir, release, name, fakeModuleELF(t, modinfo))
}

func writeRawModule(t *testing.T, dir, release, name string, content []byte) {
	t.Helper()

	path := filepath.Join(dir, release, "updates", name+".ko")
	if release == "" {
		path = filepath.Join(dir, name+".ko")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("writing %q: %v", path, err)
	}
}

// fakeModuleELF is the smallest ELF64 object debug/elf accepts, so the tests
// exercise the real parser rather than a stub.
func fakeModuleELF(t *testing.T, modinfo []string) []byte {
	t.Helper()

	var modinfoData bytes.Buffer
	for _, entry := range modinfo {
		modinfoData.WriteString(entry)
		modinfoData.WriteByte(0)
	}

	const (
		modinfoNameOff  = 1
		shstrtabNameOff = modinfoNameOff + len(".modinfo") + 1
	)
	shstrtab := append([]byte{0}, ".modinfo\x00.shstrtab\x00"...)

	const shentsize = 64
	modinfoOff := uint64(binary.Size(elf.Header64{}))
	shstrtabOff := modinfoOff + uint64(modinfoData.Len())
	shoff := shstrtabOff + uint64(len(shstrtab))

	header := elf.Header64{
		Ident: [16]byte{
			0x7f, 'E', 'L', 'F',
			byte(elf.ELFCLASS64), byte(elf.ELFDATA2LSB), byte(elf.EV_CURRENT),
		},
		Type:      uint16(elf.ET_REL),
		Machine:   uint16(elf.EM_X86_64),
		Version:   uint32(elf.EV_CURRENT),
		Shoff:     shoff,
		Ehsize:    uint16(binary.Size(elf.Header64{})),
		Shentsize: shentsize,
		Shnum:     3,
		Shstrndx:  2,
	}

	sections := []elf.Section64{
		{}, // SHT_NULL
		{
			Name:      modinfoNameOff,
			Type:      uint32(elf.SHT_PROGBITS),
			Flags:     uint64(elf.SHF_ALLOC),
			Off:       modinfoOff,
			Size:      uint64(modinfoData.Len()),
			Addralign: 1,
		},
		{
			Name:      uint32(shstrtabNameOff),
			Type:      uint32(elf.SHT_STRTAB),
			Off:       shstrtabOff,
			Size:      uint64(len(shstrtab)),
			Addralign: 1,
		},
	}

	var out bytes.Buffer
	if err := binary.Write(&out, binary.LittleEndian, header); err != nil {
		t.Fatalf("writing ELF header: %v", err)
	}
	out.Write(modinfoData.Bytes())
	out.Write(shstrtab)
	for _, section := range sections {
		if err := binary.Write(&out, binary.LittleEndian, section); err != nil {
			t.Fatalf("writing section header: %v", err)
		}
	}
	return out.Bytes()
}
