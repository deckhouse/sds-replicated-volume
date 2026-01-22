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

package handlers

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/slok/kubewebhook/v2/pkg/model"
	kwhvalidating "github.com/slok/kubewebhook/v2/pkg/webhook/validating"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	d8commonapi "github.com/deckhouse/sds-common-lib/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	drbdVersionsConfigMapName = "d8-sds-replicated-volume-drbd-versions"
	drbdVersionsConfigMapKey  = "allowedDrbdVersions"
)

func ModuleConfigValidate(ctx context.Context, _ *model.AdmissionReview, obj metav1.Object) (*kwhvalidating.ValidatorResult, error) {
	mc, ok := obj.(*d8commonapi.ModuleConfig)
	if !ok {
		return &kwhvalidating.ValidatorResult{Valid: true}, nil
	}

	if mc.Name != sdsReplicatedVolumeModuleName {
		return &kwhvalidating.ValidatorResult{Valid: true}, nil
	}

	desiredVersion, exists, err := getDrbdVersionFromSettings(mc.Spec.Settings)
	if err != nil {
		return &kwhvalidating.ValidatorResult{Valid: false, Message: err.Error()}, nil
	}
	if !exists {
		return &kwhvalidating.ValidatorResult{Valid: true}, nil
	}

	cl, err := kubeClientFactory("")
	if err != nil {
		return &kwhvalidating.ValidatorResult{Valid: false, Message: err.Error()}, nil
	}

	allowedVersions, err := fetchAllowedDrbdVersions(ctx, cl)
	if err != nil {
		return &kwhvalidating.ValidatorResult{Valid: false, Message: err.Error()}, nil
	}

	if _, ok := allowedVersions[desiredVersion]; !ok {
		return &kwhvalidating.ValidatorResult{
			Valid:   false,
			Message: fmt.Sprintf("drbdVersion %q is not allowed", desiredVersion),
		}, nil
	}

	currentVersion, err := getCurrentModuleConfigVersion(ctx, cl)
	if err != nil {
		return &kwhvalidating.ValidatorResult{Valid: false, Message: err.Error()}, nil
	}

	if currentVersion != "" {
		if desiredVersion == currentVersion {
			return &kwhvalidating.ValidatorResult{Valid: true}, nil
		}

		cmp, err := compareSemver(desiredVersion, currentVersion)
		if err != nil {
			return &kwhvalidating.ValidatorResult{Valid: false, Message: err.Error()}, nil
		}
		if cmp < 0 {
			return &kwhvalidating.ValidatorResult{
				Valid:   false,
				Message: fmt.Sprintf("drbdVersion downgrade is not allowed (current %s, requested %s)", currentVersion, desiredVersion),
			}, nil
		}

		// Enforce "one step at a time" upgrade based on allowed versions list
		if err := verifyInOrderUpgrade(currentVersion, desiredVersion, allowedVersions); err != nil {
			return &kwhvalidating.ValidatorResult{Valid: false, Message: err.Error()}, nil
		}
	}

	return &kwhvalidating.ValidatorResult{Valid: true}, nil
}

func verifyInOrderUpgrade(current, desired string, allowed map[string]struct{}) error {
	// We need to find if there are any allowed versions between current and desired
	// This requires sorting allowed versions
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
		// Current version is not in the allowed list, we can't strictly enforce "in order"
		// but we still allow it as long as it's not a downgrade (checked earlier)
		return nil
	}

	if desiredIndex == -1 {
		// This should not happen as desiredVersion was already checked against allowed list
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

	// Simple semver sort
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

func getDrbdVersionFromSettings(settings map[string]any) (string, bool, error) {
	raw, ok := settings["drbdVersion"]
	if !ok {
		return "", false, nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", false, fmt.Errorf("drbdVersion must be a string")
	}
	if strings.TrimSpace(value) == "" {
		return "", false, fmt.Errorf("drbdVersion must be non-empty")
	}
	return value, true, nil
}

func fetchAllowedDrbdVersions(ctx context.Context, cl client.Client) (map[string]struct{}, error) {
	namespace := getNamespace()
	configMap := &corev1.ConfigMap{}
	err := cl.Get(ctx, types.NamespacedName{Name: drbdVersionsConfigMapName, Namespace: namespace}, configMap)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s/%s configmap: %w", namespace, drbdVersionsConfigMapName, err)
	}

	raw, ok := configMap.Data[drbdVersionsConfigMapKey]
	if !ok {
		return nil, fmt.Errorf("configmap %s/%s does not have a key %s", namespace, drbdVersionsConfigMapName, drbdVersionsConfigMapKey)
	}
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("configmap %s/%s has empty %s", namespace, drbdVersionsConfigMapName, drbdVersionsConfigMapKey)
	}

	versions := parseAllowedVersions(raw)
	if len(versions) == 0 {
		return nil, fmt.Errorf("configmap %s/%s has no allowed versions", namespace, drbdVersionsConfigMapName)
	}

	return versions, nil
}

func parseAllowedVersions(raw string) map[string]struct{} {
	versions := map[string]struct{}{}
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == '\r' || r == '\t' || r == ' ' || r == ','
	}) {
		v := strings.TrimSpace(part)
		if v == "" {
			continue
		}
		versions[v] = struct{}{}
	}
	return versions
}

func getCurrentModuleConfigVersion(ctx context.Context, cl client.Client) (string, error) {
	current := &d8commonapi.ModuleConfig{}
	err := cl.Get(ctx, types.NamespacedName{Name: sdsReplicatedVolumeModuleName}, current)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return "", nil
		}
		return "", fmt.Errorf("failed to read current ModuleConfig: %w", err)
	}

	version, exists, err := getDrbdVersionFromSettings(current.Spec.Settings)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", nil
	}
	return version, nil
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

func getNamespace() string {
	if namespace := strings.TrimSpace(os.Getenv("POD_NAMESPACE")); namespace != "" {
		return namespace
	}

	data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err == nil {
		namespace := strings.TrimSpace(string(data))
		if namespace != "" {
			return namespace
		}
	}

	return "default"
}
