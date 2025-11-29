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

package topology

import (
	"cmp"
	"fmt"
	"slices"

	uiter "github.com/deckhouse/sds-common-lib/utils/iter"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/reconcile/rv/cluster/topology/hungarian"
)

type TransZonalMultiPurposeNodeSelector struct {
	purposeCount int
	zones        []*zone
}

func NewTransZonalMultiPurposeNodeSelector(purposeCount int) *TransZonalMultiPurposeNodeSelector {
	validatePurposeCount(purposeCount)
	return &TransZonalMultiPurposeNodeSelector{purposeCount: purposeCount}
}

func (s *TransZonalMultiPurposeNodeSelector) SetNode(nodeID string, zoneID string, scores []Score) {
	if len(scores) != s.purposeCount {
		panic(fmt.Sprintf("expected len(scores) to be %d (purposeCount), got %d", s.purposeCount, len(scores)))
	}

	idx, found := slices.BinarySearchFunc(
		s.zones,
		zoneID,
		func(z *zone, id string) int { return cmp.Compare(z.zoneID, id) },
	)

	var z *zone
	if found {
		z = s.zones[idx]
	} else {
		z = &zone{
			zoneID:                zoneID,
			bestNodesForPurposes:  make([]*node, s.purposeCount),
			bestScoresForPurposes: make([]int64, s.purposeCount),
		}
		s.zones = slices.Insert(s.zones, idx, z)
	}

	idx, found = slices.BinarySearchFunc(z.nodes, nodeID, func(n *node, id string) int { return cmp.Compare(n.nodeID, id) })
	var n *node
	if found {
		n = z.nodes[idx]
	} else {
		n = &node{
			nodeID: nodeID,
		}
		z.nodes = slices.Insert(z.nodes, idx, n)
	}
	n.scores = scores

	for i, bestScore := range z.bestScoresForPurposes {
		nodeScore := int64(scores[i])
		if z.bestNodesForPurposes[i] == nil || nodeScore > bestScore {
			z.bestScoresForPurposes[i] = nodeScore
			z.bestNodesForPurposes[i] = n
		}
	}

	// TODO
	// validate no nodes with >1 AlwaysSelect
}

func (s *TransZonalMultiPurposeNodeSelector) SelectNodes(counts []int) ([][]string, error) {
	totalCount, err := validateAndSumCounts(s.purposeCount, counts)
	if err != nil {
		return nil, err
	}

	// Validate AlwaysSelect first: check if AlwaysSelect nodes can be selected
	// This must be checked before the general "not enough slots" check
	alwaysSelectZonesByPurpose := make([][]*zone, s.purposeCount)
	for purposeIdx := range counts {
		for _, z := range s.zones {
			if z.bestNodesForPurposes[purposeIdx] != nil {
				score := z.bestScoresForPurposes[purposeIdx]
				if score == int64(AlwaysSelect) {
					alwaysSelectZonesByPurpose[purposeIdx] = append(alwaysSelectZonesByPurpose[purposeIdx], z)
				}
			}
		}
	}

	for purposeIdx, count := range counts {
		alwaysSelectZones := alwaysSelectZonesByPurpose[purposeIdx]
		if len(alwaysSelectZones) > 0 {
			// Check if AlwaysSelect zones are in the same zone (same zoneId)
			zoneIDs := make(map[string]int)
			for _, z := range alwaysSelectZones {
				zoneIDs[z.zoneID]++
			}

			// In transzonal mode, each zone can only be selected once per purpose
			// If we have multiple AlwaysSelect nodes in the same zone, we can only get 1 node from that zone
			// So if count > number of distinct zones with AlwaysSelect, it's impossible
			distinctAlwaysSelectZones := len(zoneIDs)
			if count > distinctAlwaysSelectZones {
				return nil, fmt.Errorf("can not select slot, which is required for selection")
			}

			// If AlwaysSelect nodes are in different zones, we need at least that many zones
			// But in transzonal mode, we can only select each zone once, so if count < len(alwaysSelectZones), it's impossible
			if len(zoneIDs) > 1 && count < len(alwaysSelectZones) {
				return nil, fmt.Errorf("can not select slot, which is required for selection")
			}
		}
	}

	// Validate NeverSelect: check if there are enough valid zones total
	// In transzonal mode, we need totalCount zones, and each zone must be valid for at least one purpose
	validZones := make(map[string]bool)
	for _, z := range s.zones {
		hasValidScore := false
		for purposeIdx := range counts {
			if z.bestNodesForPurposes[purposeIdx] != nil {
				score := z.bestScoresForPurposes[purposeIdx]
				if score != int64(NeverSelect) {
					hasValidScore = true
					break
				}
			}
		}
		if hasValidScore {
			validZones[z.zoneID] = true
		}
	}
	if len(validZones) < totalCount {
		return nil, fmt.Errorf("not enough slots for selection")
	}

	// Validate NeverSelect per purpose: check if there are enough valid zones for each purpose
	for purposeIdx, count := range counts {
		validZonesCount := 0
		for _, z := range s.zones {
			if z.bestNodesForPurposes[purposeIdx] != nil {
				score := z.bestScoresForPurposes[purposeIdx]
				if score != int64(NeverSelect) {
					validZonesCount++
				}
			}
		}
		if validZonesCount < count {
			return nil, fmt.Errorf("not enough slots for selection")
		}
	}

	var bestZones []*zone
	var bestTotalScore int64
	for zones := range elementCombinations(s.zones, totalCount) {
		m := hungarian.NewScoreMatrix[*zone](totalCount)

		for _, zone := range zones {
			m.AddRow(
				zone,
				slices.Collect(repeat(zone.bestScoresForPurposes, counts)),
			)
		}

		optimalZones, totalScore := m.Solve()
		if totalScore > bestTotalScore {
			bestTotalScore = totalScore
			bestZones = optimalZones
		}
	}

	if len(bestZones) == 0 {
		return nil, fmt.Errorf("not enough slots for selection")
	}

	// convert bestZones to bestNodes by taking the best node for purpose
	compactedBestZones := compact(bestZones, counts)
	result := make([][]string, 0, len(counts))
	for purposeIdx, bestZones := range compactedBestZones {
		bestNodes := slices.Collect(
			uiter.Map(
				slices.Values(bestZones),
				func(z *zone) string {
					return z.bestNodesForPurposes[purposeIdx].nodeID
				},
			),
		)
		result = append(result, bestNodes)
	}

	return result, nil
}
