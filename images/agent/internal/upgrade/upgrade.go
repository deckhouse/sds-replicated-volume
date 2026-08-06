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

	"golang.org/x/sync/errgroup"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/drbdm"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/dmsetup"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdutils"
	commonsync "github.com/deckhouse/sds-replicated-volume/lib/go/common/sync"
)

// Upgrader gates reconciliation on the node running the DRBD kernel modules this
// agent was built against. Every node-level reconciler must call EnsureUpgraded
// before touching DRBD or device-mapper state, and must requeue rather than
// proceed on error.
//
// Valid only after InitializeUpgrader.
var Upgrader *commonsync.OnceUpgrader

// InitializeUpgrader prepares Upgrader and must run before any reconcile.
//
// It fails when an upgrade is needed but its module files are not usable, because
// an agent that can never complete its upgrade would block reconciliation
// silently.
func InitializeUpgrader(log *slog.Logger, cl client.Reader, nodeName string) error {
	log = log.With("component", "drbd-upgrade")

	needed, err := upgradeNeeded(log)
	if err != nil {
		return err
	}

	if needed {
		if _, err := preflight(log); err != nil {
			return err
		}
	}

	Upgrader = commonsync.NewOnceUpgrader(needed, func(ctx context.Context) error {
		return execute(ctx, log, cl, nodeName)
	})
	return nil
}

// A failure we could have detected up front must never cost a suspend.
func execute(ctx context.Context, log *slog.Logger, cl client.Reader, nodeName string) error {
	log = log.With("component", "drbd-upgrade")
	log.Info("DRBD module upgrade executing", "target", TargetDRBDVersion)

	plan, err := prepare(ctx, log, cl, nodeName)
	if err != nil {
		log.Error("DRBD module upgrade failed before suspending anything; "+
			"resources on this node keep running, cluster changes are not applied until an attempt succeeds",
			"target", TargetDRBDVersion,
			"err", err)
		return err
	}

	if err := apply(ctx, log, plan); err != nil {
		// No rollback and no bring-up against the old modules: running on
		// potentially incompatible modules is worse than the downtime, and
		// rollback belongs to a larger orchestration layer.
		log.Error("DRBD module upgrade FAILED in its destructive phase; "+
			"node stays in the upgrade retry loop, resources are NOT brought up against the old modules and the modules are NOT rolled back",
			"target", TargetDRBDVersion,
			"suspendedDevices", len(plan.paired),
			"err", err)
		return err
	}

	log.Info("DRBD module upgrade complete, controllers will reconfigure resources and resume devices",
		"target", TargetDRBDVersion)
	return nil
}

type upgradePlan struct {
	unload []string
	load   []plannedModule
	// resources whose device is held open by a DRBDMapper
	paired   []drbdrMapperPair
	unpaired []v1alpha1.DRBDResource
}

// Must stay read-only: that is what makes a failed attempt free of consequences.
func prepare(ctx context.Context, log *slog.Logger, cl client.Reader, nodeName string) (*upgradePlan, error) {
	verified, err := preflight(log)
	if err != nil {
		return nil, err
	}

	running, err := runningModules()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPreflight, err)
	}

	plan := planModules(log, verified, running)

	// Nothing loses its device if no module is taken away.
	if len(plan.unload) == 0 {
		return plan, nil
	}

	drbdResources, err := listDRBDResourcesOnNode(ctx, cl, nodeName)
	if err != nil {
		return nil, fmt.Errorf("listing DRBDResource on node: %w", err)
	}

	drbdMappers, err := listDRBDMappersOnNode(ctx, cl, nodeName)
	if err != nil {
		return nil, fmt.Errorf("listing DRBDMapper on node: %w", err)
	}

	paired, err := buildMapping(drbdResources, drbdMappers)
	if err != nil {
		return nil, fmt.Errorf("building drbdr-to-drbdm mapping: %w", err)
	}

	pairedResources := make(map[string]struct{}, len(paired))
	for _, m := range paired {
		pairedResources[m.resource.Name] = struct{}{}
	}
	var unpaired []v1alpha1.DRBDResource
	for i := range drbdResources {
		if _, ok := pairedResources[drbdResources[i].Name]; !ok {
			unpaired = append(unpaired, drbdResources[i])
		}
	}

	plan.paired = paired
	plan.unpaired = unpaired
	return plan, nil
}

// Only taking a wrongly-versioned module out of the kernel justifies freezing I/O.
// An absent one is inserted alongside what runs — as the kernel does itself when it
// demand-loads a transport — so a current core is never disturbed for it.
func planModules(log *slog.Logger, verified []plannedModule, running map[string]runningModule) *upgradePlan {
	stale := false
	for _, name := range moduleLoadOrder {
		if r := running[name]; r.loaded && r.version != TargetDRBDVersion {
			log.Info("Loaded kernel module has to be replaced",
				"module", name,
				"running", r.version,
				"target", TargetDRBDVersion)
			stale = true
		}
	}

	plan := &upgradePlan{}

	if !stale {
		for _, m := range verified {
			if !running[m.name].loaded {
				log.Info("Kernel module is absent and will be loaded", "module", m.name)
				plan.load = append(plan.load, m)
			}
		}
		return plan
	}

	// The core cannot be removed while its transport references it. Current
	// modules go too, because the core underneath them is being replaced.
	for i := len(moduleLoadOrder) - 1; i >= 0; i-- {
		if running[moduleLoadOrder[i]].loaded {
			plan.unload = append(plan.unload, moduleLoadOrder[i])
		}
	}
	plan.load = verified
	return plan
}

// Everything here is destructive: consumer I/O stays frozen until it returns.
func apply(ctx context.Context, log *slog.Logger, plan *upgradePlan) error {
	eg, egCtx := errgroup.WithContext(ctx)

	for _, m := range plan.paired {
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

	for i := range plan.unpaired {
		res := plan.unpaired[i]
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

	for _, name := range plan.unload {
		log.Info("Unloading kernel module", "module", name)
		if err := deleteModule(name); err != nil {
			return fmt.Errorf("unloading module %q: %w", name, err)
		}
	}

	reloadedCore := false
	for _, mod := range plan.load {
		log.Info("Loading kernel module", "module", mod.name, "path", mod.path, "version", TargetDRBDVersion)
		if err := loadModule(mod); err != nil {
			return fmt.Errorf("loading module %q from %q: %w", mod.name, mod.path, err)
		}
		reloadedCore = reloadedCore || mod.name == drbdModuleName
	}

	if reloadedCore {
		disableDRBDUsermodeHelper(log)

		// Capabilities are process-global and were sampled from the module this
		// one just replaced.
		if err := drbdutils.DetectCapabilities(ctx); err != nil {
			log.Warn("DRBD capability detection after module reload failed", "err", err)
		}
		log.Info("DRBD capabilities re-detected after module reload",
			"flantExtensions", drbdutils.FlantExtensionsSupported)
	}

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

// Copied because drbdr imports this package. Keep in sync with
// drbdr.DRBDResourceNameOnTheNode.
const drbdNamePrefix = "sdsrv-"

func drbdResourceNameOnTheNode(drbdr *v1alpha1.DRBDResource) string {
	if drbdr.Spec.ActualNameOnTheNode != "" {
		return drbdr.Spec.ActualNameOnTheNode
	}
	return drbdNamePrefix + drbdr.Name
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
