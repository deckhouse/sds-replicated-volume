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

package metrics

import (
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func TestCollectNodeNamesIncludesUnknownForUnscheduledObjectsOnce(t *testing.T) {
	nodes := collectNodeNames(
		[]corev1.Node{
			{ObjectMeta: metav1.ObjectMeta{Name: currentMetricsNodeUnknown}},
			{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}},
		},
		[]v1alpha1.ReplicatedVolumeReplica{
			{Spec: v1alpha1.ReplicatedVolumeReplicaSpec{}},
		},
		[]v1alpha1.ReplicatedVolumeAttachment{
			{Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{}},
		},
	)

	if !slices.Contains(nodes, currentMetricsNodeUnknown) || countString(nodes, currentMetricsNodeUnknown) != 1 {
		t.Fatalf("unexpected nodes: %v", nodes)
	}
}

func TestCollectStorageClassNamesUsesUnknownForMissingLabels(t *testing.T) {
	storageClasses := collectStorageClassNames(
		nil,
		nil,
		[]v1alpha1.ReplicatedVolumeReplica{
			{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{}}},
		},
		[]v1alpha1.ReplicatedVolumeAttachment{
			{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{}}},
		},
	)

	if !slices.Equal(storageClasses, []string{currentMetricsSCUnknown}) {
		t.Fatalf("unexpected storage classes: %v", storageClasses)
	}
}

func TestPrependMissingCurrentMetricsNodesDoesNotDuplicateSyntheticNodes(t *testing.T) {
	nodes := prependMissingCurrentMetricsNodes(
		[]string{currentMetricsNodeGlobal, currentMetricsNodeUnknown, "node-a"},
		currentMetricsNodeGlobal,
		currentMetricsNodeUnknown,
	)

	if !slices.Equal(nodes, []string{currentMetricsNodeGlobal, currentMetricsNodeUnknown, "node-a"}) {
		t.Fatalf("unexpected nodes: %v", nodes)
	}
}

func countString(values []string, target string) int {
	var count int
	for _, value := range values {
		if value == target {
			count++
		}
	}
	return count
}
