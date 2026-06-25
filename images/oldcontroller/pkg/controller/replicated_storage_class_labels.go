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

package controller

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	errors2 "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/deckhouse/sds-replicated-volume/images/controller/config"
)

// labelsMatchRSC reports whether the labels of the existing StorageClass match
// the labels propagated from the ReplicatedStorageClass (CR labels + managed-by),
// taking the ignoredLabelPrefixes filter into account. Labels whose keys start
// with any of the ignoredLabelPrefixes are dropped before the comparison.
func labelsMatchRSC(scLabels, rscLabels map[string]string, ignoredLabelPrefixes []string) bool {
	filtered := filterLabelsForStorageClass(rscLabels, ignoredLabelPrefixes)
	expected := make(map[string]string, len(filtered)+1)
	for k, v := range filtered {
		expected[k] = v
	}
	expected[ManagedLabelKey] = ManagedLabelValue

	return reflect.DeepEqual(scLabels, expected)
}

// filterLabelsForStorageClass returns a copy of rscLabels with all keys whose
// prefix matches any entry in ignoredLabelPrefixes removed. Empty entries in
// ignoredLabelPrefixes are skipped to avoid silently dropping every label.
func filterLabelsForStorageClass(rscLabels map[string]string, ignoredLabelPrefixes []string) map[string]string {
	if len(rscLabels) == 0 {
		return nil
	}
	out := make(map[string]string, len(rscLabels))
	for k, v := range rscLabels {
		if isIgnoredLabelKey(k, ignoredLabelPrefixes) {
			continue
		}
		out[k] = v
	}
	return out
}

func isIgnoredLabelKey(key string, ignoredLabelPrefixes []string) bool {
	for _, prefix := range ignoredLabelPrefixes {
		if prefix == "" {
			continue
		}
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func applyPropagatedLabelsToStorageClass(sc *storagev1.StorageClass, rscLabels map[string]string, ignoredLabelPrefixes []string) {
	filteredLabels := filterLabelsForStorageClass(rscLabels, ignoredLabelPrefixes)
	if len(filteredLabels) > 0 {
		sc.Labels = filteredLabels
		sc.Labels[ManagedLabelKey] = ManagedLabelValue
	} else {
		sc.Labels = map[string]string{ManagedLabelKey: ManagedLabelValue}
	}
}

// getStorageClassLabelIgnoredPrefixes reads the controller config Secret and
// extracts the union of system and user label-key prefixes that must NOT be
// propagated from a ReplicatedStorageClass to the managed StorageClass.
//
// Returns a nil slice (no filtering) if the secret is missing or the field is
// empty. This keeps the controller forward/backward compatible with secrets
// produced by older module versions.
func getStorageClassLabelIgnoredPrefixes(ctx context.Context, cl client.Client, namespace, name string) ([]string, error) {
	if namespace == "" || name == "" {
		return nil, nil
	}
	secret := &corev1.Secret{}
	if err := cl.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, secret); err != nil {
		if errors2.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	raw, ok := secret.Data["config"]
	if !ok || len(raw) == 0 {
		return nil, nil
	}

	var parsed config.SdsReplicatedVolumeOperatorConfig
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("unable to parse config from secret %s/%s: %w", namespace, name, err)
	}

	return parsed.StorageClassLabelIgnoredPrefixes, nil
}
