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

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/drbdr"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/dmsetup"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdsetup"
)

// RunIfNeeded checks the DRBD kernel module version and performs an online
// upgrade if it differs from TargetDRBDVersion. This must be called before
// the controller manager starts.
func RunIfNeeded(ctx context.Context, log *slog.Logger, apiReader client.Reader, nodeName string) error {
	log = log.With("component", "drbd-upgrade")

	runningVersion, err := ReadRunningDRBDVersion()
	if err != nil {
		return fmt.Errorf("reading DRBD module version: %w", err)
	}

	if runningVersion == "" {
		log.Info("DRBD module not loaded, skipping upgrade")
		return nil
	}

	if runningVersion == TargetDRBDVersion {
		log.Info("DRBD module version matches target, no upgrade needed",
			"version", runningVersion)
		return nil
	}

	log.Info("DRBD module upgrade required",
		"running", runningVersion,
		"target", TargetDRBDVersion)

	drbdResources, err := listDRBDResourcesOnNode(ctx, apiReader, nodeName)
	if err != nil {
		return fmt.Errorf("listing DRBDResource on node: %w", err)
	}

	drbdMappers, err := listDRBDMappersOnNode(ctx, apiReader, nodeName)
	if err != nil {
		return fmt.Errorf("listing DRBDMapper on node: %w", err)
	}

	mapping, err := buildMapping(drbdResources, drbdMappers)
	if err != nil {
		return fmt.Errorf("building drbdr-to-drbdm mapping: %w", err)
	}

	for _, m := range mapping {
		log.Info("Suspending upper device", "drbdMapper", m.mapper.Name)
		if err := dmsetup.Suspend(ctx, m.mapper.Name); err != nil {
			return fmt.Errorf("suspending upper device %q: %w", m.mapper.Name, err)
		}
	}

	for i := range drbdResources {
		drbdName := drbdr.DRBDResourceNameOnTheNode(&drbdResources[i])
		log.Info("Bringing down DRBD resource", "drbdResource", drbdResources[i].Name, "drbdName", drbdName)
		if err := drbdsetup.ExecuteDown(ctx, drbdName); err != nil {
			return fmt.Errorf("bringing down DRBD resource %q: %w", drbdName, err)
		}
	}

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

	log.Info("DRBD module upgrade complete, controllers will reconfigure resources")
	return nil
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

// deleteModule unloads a kernel module using the delete_module syscall.
func deleteModule(name string) error {
	nameBytes, err := syscall.BytePtrFromString(name)
	if err != nil {
		return fmt.Errorf("preparing module name: %w", err)
	}
	_, _, errno := syscall.Syscall(syscall.SYS_DELETE_MODULE,
		uintptr(unsafe.Pointer(nameBytes)),
		0, // flags
		0)
	if errno != 0 {
		return fmt.Errorf("delete_module(%q): %w", name, errno)
	}
	return nil
}

// loadModule loads a kernel module using the finit_module syscall.
// It looks for the .ko file in /lib/modules/$(uname -r)/.
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

	koPath := fmt.Sprintf("/lib/modules/%s/updates/%s.ko", release, name)
	f, err := os.Open(koPath)
	if err != nil {
		return fmt.Errorf("opening module file %q: %w", koPath, err)
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
		0) // flags
	if errno != 0 {
		return fmt.Errorf("finit_module(%q): %w", koPath, errno)
	}
	return nil
}

func buildMapping(resources []v1alpha1.DRBDResource, mappers []v1alpha1.DRBDMapper) ([]drbdrMapperPair, error) {
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
			// No DRBDMapper for this resource -- OK, not all resources need a mapper
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
