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

package rvrtiebreakercount_test

import (
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	corev1 "k8s.io/api/core/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func TestRvrTieBreakerCount(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "RvrTieBreakerCount Suite")
}

func HaveTieBreakerCount(matcher types.GomegaMatcher) types.GomegaMatcher {
	return WithTransform(func(list []v1alpha1.ReplicatedVolumeReplica) int {
		tbCount := 0
		for _, rvr := range list {
			if rvr.Spec.Type == v1alpha1.ReplicaTypeTieBreaker {
				tbCount++
			}
		}
		return tbCount
	}, matcher)
}

// HaveBalancedFDDistribution checks that scheduled TBs are not in zones with max base replica count
// (if there are zones with fewer base replicas).
// Note: This matcher only checks TB placement, not the total count.
// Total TB count is verified by HaveTieBreakerCount separately.
// zones - list of allowed zones from RSC
// nodeList - list of nodes with zone labels
func HaveBalancedFDDistribution(zones []string, nodeList []corev1.Node) types.GomegaMatcher {
	return WithTransform(func(list []v1alpha1.ReplicatedVolumeReplica) error {
		// Build node -> zone map
		nodeToZone := make(map[string]string)
		for _, node := range nodeList {
			nodeToZone[node.Name] = node.Labels[corev1.LabelTopologyZone]
		}

		// Count base replicas (Diskful + Access) per zone
		zoneBaseCounts := make(map[string]int)
		for _, zone := range zones {
			zoneBaseCounts[zone] = 0
		}

		// Count scheduled TBs per zone
		zoneTBCounts := make(map[string]int)

		for _, rvr := range list {
			if rvr.Spec.NodeName == "" {
				continue // skip unscheduled
			}

			zone := nodeToZone[rvr.Spec.NodeName]
			if _, ok := zoneBaseCounts[zone]; !ok {
				continue // zone not in allowed zones
			}

			if rvr.Spec.Type == v1alpha1.ReplicaTypeTieBreaker {
				zoneTBCounts[zone]++
			} else {
				zoneBaseCounts[zone]++
			}
		}

		// Find max base replica count
		maxBaseCount := 0
		for _, count := range zoneBaseCounts {
			maxBaseCount = max(maxBaseCount, count)
		}

		// Check: scheduled TBs should not be in zones with max base count
		// (if there are zones with fewer base replicas)
		hasZonesWithFewerBase := false
		for _, count := range zoneBaseCounts {
			if count < maxBaseCount {
				hasZonesWithFewerBase = true
				break
			}
		}

		if hasZonesWithFewerBase {
			for zone, tbCount := range zoneTBCounts {
				if tbCount > 0 && zoneBaseCounts[zone] == maxBaseCount {
					return fmt.Errorf(
						"scheduled TB in zone %q with max base count (%d), but there are zones with fewer base replicas; "+
							"zoneBaseCounts=%v, zoneTBCounts=%v",
						zone, maxBaseCount, zoneBaseCounts, zoneTBCounts,
					)
				}
			}
		}

		return nil
	}, Succeed())
}
