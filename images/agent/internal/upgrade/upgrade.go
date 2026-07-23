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
	"context"
	"fmt"
	"log/slog"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sync/errgroup"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/drbdm"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/dmsetup"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdutils"
)

// CheckAndArm reads the running DRBD module version and arms the trigger
// if an upgrade is needed. This is lightweight (no K8s API, no dmsetup)
// and should be called at agent startup before mgr.Start().
func CheckAndArm(log *slog.Logger) {
	log = log.With("component", "drbd-upgrade")

	runningVersion, err := ReadRunningDRBDVersion()
	if err != nil {
		log.Warn("Failed to read DRBD module version, skipping upgrade check", "err", err)
		return
	}

	if runningVersion == "" {
		log.Info("DRBD module not loaded, skipping upgrade")
		return
	}

	if !FakeUpgrade && runningVersion == TargetDRBDVersion {
		log.Info("DRBD module version matches target, no upgrade needed",
			"version", runningVersion)
		return
	}

	log.Info("DRBD module upgrade needed, arming trigger",
		"running", runningVersion,
		"target", TargetDRBDVersion,
		"fakeUpgrade", FakeUpgrade)
	Trigger.EnableOnce()
}

// Execute performs the actual DRBD module upgrade sequence:
// suspend DRBDMapper upper devices, wipe internal tables, down all DRBD
// resources, and reload the kernel module. Called from inside the DRBDR
// reconciler via the trigger.
func Execute(ctx context.Context, log *slog.Logger, cl client.Reader, nodeName string) error {
	log = log.With("component", "drbd-upgrade")
	log.Info("DRBD module upgrade executing")

	drbdResources, err := listDRBDResourcesOnNode(ctx, cl, nodeName)
	if err != nil {
		return fmt.Errorf("listing DRBDResource on node: %w", err)
	}

	drbdMappers, err := listDRBDMappersOnNode(ctx, cl, nodeName)
	if err != nil {
		return fmt.Errorf("listing DRBDMapper on node: %w", err)
	}

	mapping, err := buildMapping(log, drbdResources, drbdMappers)
	if err != nil {
		return fmt.Errorf("building drbdr-to-drbdm mapping: %w", err)
	}

	mappedResources := make(map[string]struct{})
	for _, m := range mapping {
		mappedResources[m.resource.Name] = struct{}{}
	}

	eg, egCtx := errgroup.WithContext(ctx)

	for _, m := range mapping {
		m := m
		eg.Go(func() error {
			internalName := drbdm.InternalDeviceName(m.mapper.Name)

			log.Info("Suspending upper device", "drbdMapper", m.mapper.Name)
			if err := dmsetup.Suspend(egCtx, m.mapper.Name); err != nil {
				return fmt.Errorf("suspending upper device %q: %w", m.mapper.Name, err)
			}

			log.Info("Suspending internal device", "internalDevice", internalName)
			if err := dmsetup.Suspend(egCtx, internalName); err != nil {
				return fmt.Errorf("suspending internal device %q: %w", internalName, err)
			}

			log.Info("Wiping internal device table to release DRBD", "internalDevice", internalName)
			if err := dmsetup.WipeTable(egCtx, internalName); err != nil {
				return fmt.Errorf("wiping table on %q: %w", internalName, err)
			}

			drbdName := drbdResourceNameOnTheNode(&m.resource)
			log.Info("Bringing down DRBD resource", "drbdResource", m.resource.Name, "drbdName", drbdName)
			if err := drbdutils.ExecuteDown(egCtx, drbdName); err != nil {
				return fmt.Errorf("bringing down DRBD resource %q: %w", drbdName, err)
			}
			return nil
		})
	}

	for i := range drbdResources {
		if _, mapped := mappedResources[drbdResources[i].Name]; mapped {
			continue
		}
		res := drbdResources[i]
		eg.Go(func() error {
			drbdName := drbdResourceNameOnTheNode(&res)
			log.Info("Bringing down DRBD resource", "drbdResource", res.Name, "drbdName", drbdName)
			if err := drbdutils.ExecuteDown(egCtx, drbdName); err != nil {
				return fmt.Errorf("bringing down DRBD resource %q: %w", drbdName, err)
			}
			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return err
	}

	if FakeUpgrade {
		log.Info("FakeUpgrade: skipping kernel module unload/load")
	} else {
		modulesToUnload := []string{"drbd_transport_tcp", "drbd"}
		for _, mod := range modulesToUnload {
			log.Info("Unloading kernel module", "module", mod)
			if err := deleteModule(mod); err != nil {
				return fmt.Errorf("unloading module %q: %w", mod, err)
			}
		}

		modulesToLoad := []string{"drbd", "drbd_transport_tcp"}
		for _, mod := range modulesToLoad {
			log.Info("Loading kernel module", "module", mod)
			if err := loadModule(mod); err != nil {
				return fmt.Errorf("loading module %q: %w", mod, err)
			}
		}

		disableDRBDUsermodeHelper(log)
	}

	log.Info("DRBD module upgrade complete, controllers will reconfigure resources and resume devices")
	return nil
}

const drbdUsermodeHelperPath = "/sys/module/drbd/parameters/usermode_helper"

func disableDRBDUsermodeHelper(log *slog.Logger) {
	// MUST be exactly "disabled" without a trailing newline: DRBD's
	// drbd_maybe_khelper short-circuits on strcmp(drbd_usermode_helper,
	// "disabled") == 0. A reloaded module resets this parameter, so it must be
	// re-disabled after the module reload above.
	if err := os.WriteFile(drbdUsermodeHelperPath, []byte("disabled"), 0o644); err != nil {
		log.Warn("failed to disable DRBD usermode helper after module reload", "path", drbdUsermodeHelperPath, "err", err)
	} else {
		log.Info("disabled DRBD usermode helper after module reload", "path", drbdUsermodeHelperPath)
	}
}

type drbdrMapperPair struct {
	resource v1alpha1.DRBDResource
	mapper   v1alpha1.DRBDMapper
}

func listDRBDResourcesOnNode(ctx context.Context, r client.Reader, nodeName string) ([]v1alpha1.DRBDResource, error) {
	var list v1alpha1.DRBDResourceList
	if err := r.List(ctx, &list); err != nil {
		return nil, err
	}
	var result []v1alpha1.DRBDResource
	for i := range list.Items {
		if list.Items[i].Spec.NodeName == nodeName {
			result = append(result, list.Items[i])
		}
	}
	return result, nil
}

func listDRBDMappersOnNode(ctx context.Context, r client.Reader, nodeName string) ([]v1alpha1.DRBDMapper, error) {
	var list v1alpha1.DRBDMapperList
	if err := r.List(ctx, &list); err != nil {
		return nil, err
	}
	var result []v1alpha1.DRBDMapper
	for i := range list.Items {
		if list.Items[i].Spec.NodeName == nodeName {
			result = append(result, list.Items[i])
		}
	}
	return result, nil
}

func deleteModule(name string) error {
	nameBytes, err := syscall.BytePtrFromString(name)
	if err != nil {
		return fmt.Errorf("preparing module name: %w", err)
	}
	_, _, errno := syscall.Syscall(syscall.SYS_DELETE_MODULE,
		uintptr(unsafe.Pointer(nameBytes)),
		0, 0)
	if errno != 0 {
		return fmt.Errorf("delete_module(%q): %w", name, errno)
	}
	return nil
}

func loadModule(name string) error {
	var utsname syscall.Utsname
	if err := syscall.Uname(&utsname); err != nil {
		return fmt.Errorf("uname: %w", err)
	}
	var releaseBuf []byte
	for _, b := range utsname.Release {
		if b == 0 {
			break
		}
		releaseBuf = append(releaseBuf, byte(b))
	}
	release := string(releaseBuf)

	// Try container path first, then host filesystem via /proc/1/root
	// (agent runs as a privileged container with host PID access).
	paths := []string{
		fmt.Sprintf("/lib/modules/%s/updates/%s.ko", release, name),
		fmt.Sprintf("/proc/1/root/lib/modules/%s/updates/%s.ko", release, name),
	}

	var f *os.File
	var koPath string
	var err error
	for _, p := range paths {
		f, err = os.Open(p)
		if err == nil {
			koPath = p
			break
		}
	}
	if err != nil {
		return fmt.Errorf("opening module file (tried %v): %w", paths, err)
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
		return fmt.Errorf("finit_module(%q): %w", koPath, errno)
	}
	return nil
}

const drbdNamePrefix = "sdsrv-"

func drbdResourceNameOnTheNode(drbdr *v1alpha1.DRBDResource) string {
	if drbdr.Spec.ActualNameOnTheNode != "" {
		return drbdr.Spec.ActualNameOnTheNode
	}
	return drbdNamePrefix + drbdr.Name
}

func buildMapping(log *slog.Logger, resources []v1alpha1.DRBDResource, mappers []v1alpha1.DRBDMapper) ([]drbdrMapperPair, error) {
	mapperByLower := make(map[string][]v1alpha1.DRBDMapper)
	for i := range mappers {
		lower := mappers[i].Spec.LowerDevicePath
		mapperByLower[lower] = append(mapperByLower[lower], mappers[i])
	}

	var result []drbdrMapperPair
	for i := range resources {
		device := resources[i].Status.Device
		if device == "" {
			continue
		}
		matches := mapperByLower[device]
		switch len(matches) {
		case 0:
		case 1:
			result = append(result, drbdrMapperPair{
				resource: resources[i],
				mapper:   matches[0],
			})
		default:
			return nil, fmt.Errorf("DRBDResource %q (device=%q) has %d matching DRBDMappers (expected at most 1)",
				resources[i].Name, device, len(matches))
		}
	}
	return result, nil
}
