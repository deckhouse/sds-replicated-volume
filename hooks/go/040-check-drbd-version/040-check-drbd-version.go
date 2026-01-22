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

package checkdrbdversion

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/deckhouse/module-sdk/pkg"
	"github.com/deckhouse/module-sdk/pkg/registry"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const moduleConfigName = "sds-replicated-volume"

var allowedDrbdVersions = map[string]struct{}{
	"9.2.13": {},
	"9.2.16": {},
}

var _ = registry.RegisterFunc(
	&pkg.HookConfig{
		OnBeforeHelm: &pkg.OrderedConfig{Order: 5},
	},
	onBeforeHelmChecks,
)

func onBeforeHelmChecks(ctx context.Context, input *pkg.HookInput) error {
	input.Logger.Info("Starting DRBD version validation (OnBeforeHelm)")
	cl := input.DC.MustGetK8sClient()

	modCfg := &unstructured.Unstructured{}
	modCfg.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "deckhouse.io",
		Version: "v1alpha1",
		Kind:    "ModuleConfig",
	})
	modCfg.SetName(moduleConfigName)

	if err := cl.Get(ctx, client.ObjectKey{Name: moduleConfigName}, modCfg); err != nil {
		if client.IgnoreNotFound(err) == nil {
			input.Logger.Info("ModuleConfig not found, skipping DRBD version check", "module", moduleConfigName)
			return nil
		}
		input.Logger.Error("Failed to get ModuleConfig", "module", moduleConfigName, "err", err)
		return fmt.Errorf("failed to get ModuleConfig %q: %w", moduleConfigName, err)
	}

	drbdVersion, found, _ := unstructured.NestedString(modCfg.Object, "spec", "settings", "drbdVersion")
	if !found || strings.TrimSpace(drbdVersion) == "" {
		input.Logger.Info("drbdVersion is not set, skipping DRBD version check")
		return nil
	}

	if len(allowedDrbdVersions) == 0 {
		input.Logger.Error("Allowed DRBD versions list is empty")
		return fmt.Errorf("allowed drbd versions list is empty")
	}

	if _, ok := allowedDrbdVersions[drbdVersion]; !ok {
		input.Logger.Error("drbdVersion is not in allowed list", "drbdVersion", drbdVersion)
		return fmt.Errorf("drbdVersion %q is not in allowed list", drbdVersion)
	}

	currentVersion := strings.TrimSpace(input.Values.Get("sdsReplicatedVolume.drbdVersion").String())
	if currentVersion != "" && currentVersion != drbdVersion {
		input.Logger.Info("Comparing DRBD versions", "currentVersion", currentVersion, "desiredVersion", drbdVersion)
		cmp, err := compareSemver(drbdVersion, currentVersion)
		if err != nil {
			input.Logger.Error("Failed to compare DRBD versions", "currentVersion", currentVersion, "desiredVersion", drbdVersion, "err", err)
			return err
		}
		if cmp < 0 {
			input.Logger.Error("DRBD version downgrade is not allowed", "currentVersion", currentVersion, "desiredVersion", drbdVersion)
			return fmt.Errorf("drbdVersion downgrade is not allowed (current %s, requested %s)", currentVersion, drbdVersion)
		}
		if err := verifyInOrderUpgrade(currentVersion, drbdVersion, allowedDrbdVersions); err != nil {
			input.Logger.Error("DRBD version upgrade order is invalid", "currentVersion", currentVersion, "desiredVersion", drbdVersion, "err", err)
			return err
		}
	}

	input.Logger.Info("DRBD version check passed", "drbdVersion", drbdVersion, "currentVersion", currentVersion)
	return nil
}

func verifyInOrderUpgrade(current, desired string, allowed map[string]struct{}) error {
	sortedAllowed := sortVersions(allowed)

	currentIndex := -1
	desiredIndex := -1

	for i, v := range sortedAllowed {
		if v == current {
			currentIndex = i
		}
		if v == desired {
			desiredIndex = i
		}
	}

	if currentIndex == -1 {
		return nil
	}
	if desiredIndex == -1 {
		return fmt.Errorf("desired version %s not in allowed list", desired)
	}

	if desiredIndex > currentIndex+1 {
		skipped := sortedAllowed[currentIndex+1 : desiredIndex]
		return fmt.Errorf("drbdVersion upgrade must be in order. Please upgrade to %s first. Skipped: %s",
			sortedAllowed[currentIndex+1], strings.Join(skipped, ", "))
	}

	return nil
}

func sortVersions(versions map[string]struct{}) []string {
	res := make([]string, 0, len(versions))
	for v := range versions {
		res = append(res, v)
	}

	for i := 0; i < len(res); i++ {
		for j := i + 1; j < len(res); j++ {
			cmp, _ := compareSemver(res[i], res[j])
			if cmp > 0 {
				res[i], res[j] = res[j], res[i]
			}
		}
	}
	return res
}

func compareSemver(a, b string) (int, error) {
	aParts, err := parseSemverParts(a)
	if err != nil {
		return 0, err
	}
	bParts, err := parseSemverParts(b)
	if err != nil {
		return 0, err
	}

	for i := 0; i < 3; i++ {
		if aParts[i] < bParts[i] {
			return -1, nil
		}
		if aParts[i] > bParts[i] {
			return 1, nil
		}
	}

	return 0, nil
}

func parseSemverParts(v string) ([3]int, error) {
	var result [3]int
	v = strings.TrimSpace(v)
	if v == "" {
		return result, fmt.Errorf("version is empty")
	}
	v = strings.SplitN(v, "-", 2)[0]
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return result, fmt.Errorf("version %q must be in x.y.z format", v)
	}

	for i := 0; i < 3; i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return result, fmt.Errorf("version %q has invalid numeric part", v)
		}
		result[i] = n
	}

	return result, nil
}
