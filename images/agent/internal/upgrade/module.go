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
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

// The core must come first: nothing may hold a reference to it when it is removed.
var moduleLoadOrder = []string{drbdModuleName, "drbd_transport_tcp"}

const drbdModuleName = "drbd"

// ErrPreflight marks a failure that cost no I/O, so the node is still serving its
// resources.
var ErrPreflight = errors.New("drbd module upgrade preflight")

type plannedModule struct {
	name string
	path string
}

// preflight changes no state, so it must run to completion before anything is
// suspended. Its errors wrap ErrPreflight.
func preflight(log *slog.Logger) ([]plannedModule, error) {
	release, err := kernelRelease()
	if err != nil {
		return nil, fmt.Errorf("%w: reading kernel release: %w", ErrPreflight, err)
	}

	verified := make([]plannedModule, 0, len(moduleLoadOrder))
	for _, name := range moduleLoadOrder {
		m, err := preflightModule(release, name)
		if err != nil {
			return nil, fmt.Errorf("%w: module %q: %w", ErrPreflight, name, err)
		}
		log.Info("Module file verified",
			"module", m.name,
			"path", m.path,
			"version", TargetDRBDVersion)
		verified = append(verified, m)
	}

	return verified, nil
}

// preflightModule compares versions exactly in both directions, matching the
// running version check.
func preflightModule(release, name string) (plannedModule, error) {
	path, err := resolveModulePath(release, name)
	if err != nil {
		return plannedModule{}, err
	}

	version, err := readModuleFileVersion(path)
	if err != nil {
		return plannedModule{}, fmt.Errorf("inspecting %q: %w", path, err)
	}
	if version == "" {
		return plannedModule{}, fmt.Errorf("%q declares no version, expected %q", path, TargetDRBDVersion)
	}
	if version != TargetDRBDVersion {
		return plannedModule{}, fmt.Errorf("%q has version %q, expected %q", path, version, TargetDRBDVersion)
	}

	return plannedModule{name: name, path: path}, nil
}

// /lib/modules is the host's, bind-mounted read-only into the container by
// templates/agent/daemonset.yaml. The /proc/1/root form is a fallback for a pod
// that has host PID access but not that mount. Overridden by tests.
var moduleDirs = []string{"/lib/modules", "/proc/1/root/lib/modules"}

func modulePathCandidates(release, name string) []string {
	candidates := make([]string, 0, len(moduleDirs))
	for _, dir := range moduleDirs {
		candidates = append(candidates, filepath.Join(dir, release, "updates", name+".ko"))
	}
	return candidates
}

// resolveModulePath keeps "no such file" and "cannot be read" apart: a missing file
// means the delivery has not landed, an unreadable one means the mount or
// permissions are wrong.
func resolveModulePath(release, name string) (string, error) {
	candidates := modulePathCandidates(release, name)

	var readErrs []error
	for _, path := range candidates {
		f, err := os.Open(path)
		if err == nil {
			f.Close()
			return path, nil
		}
		if !os.IsNotExist(err) {
			readErrs = append(readErrs, err)
		}
	}

	if len(readErrs) > 0 {
		return "", fmt.Errorf("module file is not readable (tried %v): %w", candidates, errors.Join(readErrs...))
	}
	return "", fmt.Errorf("module file not found (tried %v)", candidates)
}

// readModuleFileVersion returns an empty string when the module declares no
// MODULE_VERSION. Reading the section also proves the file is a readable,
// uncompressed module.
func readModuleFileVersion(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	ef, err := elf.NewFile(f)
	if err != nil {
		return "", fmt.Errorf("not a readable ELF kernel module: %w", err)
	}
	defer ef.Close()

	section := ef.Section(".modinfo")
	if section == nil {
		return "", errors.New("no .modinfo section")
	}
	data, err := section.Data()
	if err != nil {
		return "", fmt.Errorf("reading .modinfo: %w", err)
	}

	// .modinfo is a sequence of NUL-terminated "key=value" entries.
	for _, entry := range bytes.Split(data, []byte{0}) {
		if value, ok := bytes.CutPrefix(entry, []byte("version=")); ok {
			return string(value), nil
		}
	}
	return "", nil
}

func kernelRelease() (string, error) {
	var utsname syscall.Utsname
	if err := syscall.Uname(&utsname); err != nil {
		return "", fmt.Errorf("uname: %w", err)
	}
	var release []byte
	for _, b := range utsname.Release {
		if b == 0 {
			break
		}
		release = append(release, byte(b))
	}
	return string(release), nil
}

// Overridden by tests.
var (
	deleteModule = removeKernelModule
	loadModule   = insertKernelModule
)

func removeKernelModule(name string) error {
	nameBytes, err := syscall.BytePtrFromString(name)
	if err != nil {
		return fmt.Errorf("preparing module name: %w", err)
	}
	_, _, errno := syscall.Syscall(syscall.SYS_DELETE_MODULE,
		uintptr(unsafe.Pointer(nameBytes)),
		0, 0)
	// ENOENT means the module is already gone, which is the state being asked for.
	if errno != 0 && errno != syscall.ENOENT {
		return fmt.Errorf("delete_module(%q): %w", name, errno)
	}
	return nil
}

func insertKernelModule(m plannedModule) error {
	f, err := os.Open(m.path)
	if err != nil {
		return fmt.Errorf("opening %q: %w", m.path, err)
	}
	defer f.Close()

	params, err := syscall.BytePtrFromString("")
	if err != nil {
		return fmt.Errorf("preparing params: %w", err)
	}
	const sysFinitModule = 313
	_, _, errno := syscall.Syscall(sysFinitModule,
		f.Fd(),
		uintptr(unsafe.Pointer(params)),
		0)
	if errno != 0 {
		return fmt.Errorf("finit_module(%q): %w", m.path, errno)
	}
	return nil
}
