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
)

type ZonalMultiPurposeNodeSelector struct {
	purposeCount int
	zones        []*zone
}

func NewZonalMultiPurposeNodeSelector(purposeCount int) *ZonalMultiPurposeNodeSelector {
	validatePurposeCount(purposeCount)
	return &ZonalMultiPurposeNodeSelector{purposeCount: purposeCount}
}

func (s *ZonalMultiPurposeNodeSelector) SetNode(nodeID string, zoneID string, scores []Score) {
	if len(scores) != s.purposeCount {
		panic(fmt.Sprintf("expected len(scores) to be %d (purposeCount), got %d", s.purposeCount, len(scores)))
	}

	// find or create zone (keep zones sorted by zoneID for determinism)
	zoneIdx, found := slices.BinarySearchFunc(
		s.zones,
		zoneID,
		func(z *zone, id string) int { return cmp.Compare(z.zoneID, id) },
	)
	var z *zone
	if found {
		z = s.zones[zoneIdx]
	} else {
		z = &zone{
			zoneID: zoneID,
		}
		// insert new zone in order
		s.zones = slices.Insert(s.zones, zoneIdx, z)
		// backfill this new zone with already-known "filler" nodes (nodes with all scores == -1)
		for _, other := range s.zones {
			if other == z {
				continue
			}
			for _, n := range other.nodes {
				if isAllMinusOne(n.scores) {
					// insert if absent
					nIdx, nFound := slices.BinarySearchFunc(z.nodes, n.nodeID, func(x *node, id string) int { return cmp.Compare(x.nodeID, id) })
					if !nFound {
						// use biased scores to prefer assigning fillers to the last purpose group
						biased := make([]Score, len(n.scores))
						copy(biased, n.scores)
						for i := 0; i < len(biased)-1; i++ {
							biased[i] = Score(-1 << 60)
						}
						z.nodes = slices.Insert(z.nodes, nIdx, &node{
							nodeID: n.nodeID,
							scores: biased,
						})
					}
				}
			}
		}
	}

	// insert the node into its own zone (keep nodes sorted by nodeID)
	nIdx, nFound := slices.BinarySearchFunc(z.nodes, nodeID, func(n *node, id string) int { return cmp.Compare(n.nodeID, id) })
	if !nFound {
		n := &node{nodeID: nodeID}
		n.scores = scores
		z.nodes = slices.Insert(z.nodes, nIdx, n)
	} else {
		// update scores if node already present
		z.nodes[nIdx].scores = scores
	}

	// If this node is a "filler" (all scores == -1), make it available in all zones as a low-priority fallback.
	// This ensures SelectNodes has enough candidates without preferring cross-zone high scores.
	if isAllMinusOne(scores) {
		for _, other := range s.zones {
			if other == z {
				continue
			}
			idx, exists := slices.BinarySearchFunc(other.nodes, nodeID, func(n *node, id string) int { return cmp.Compare(n.nodeID, id) })
			if !exists {
				// reuse the same node reference; scores are already -1 for all purposes
				// but use biased scores to steer assignment to the last purpose group
				biased := make([]Score, len(scores))
				copy(biased, scores)
				for i := 0; i < len(biased)-1; i++ {
					biased[i] = Score(-1 << 60)
				}
				other.nodes = slices.Insert(other.nodes, idx, &node{
					nodeID: nodeID,
					scores: biased,
				})
			}
		}
	}

	// TODO: validate no nodes with >1 AlwaysSelect
}

func (s *ZonalMultiPurposeNodeSelector) SelectNodes(counts []int) ([][]string, error) {
	totalCount, err := validateAndSumCounts(s.purposeCount, counts)
	if err != nil {
		return nil, err
	}

	var bestNodes []string
	var bestTotalScore int64

	// zones
	for _, zone := range s.zones {
		if len(zone.nodes) < totalCount {
			// not enough nodes in this zone to satisfy selection
			continue
		}
		zoneNodes, totalScore := solveZone(zone.nodes, totalCount, counts)
		if totalScore > bestTotalScore {
			bestTotalScore = totalScore
			bestNodes = zoneNodes
		} else if totalScore == bestTotalScore && len(zoneNodes) > 0 {
			// tie-breaker: prefer lexicographically greater node sequence
			if lexGreater(zoneNodes, bestNodes) {
				bestNodes = zoneNodes
			}
		}
	}

	if len(bestNodes) == 0 {
		return nil, ErrSelectionImpossibleError
	}

	return sortEachElementNatural(compact(bestNodes, counts)), nil
}

func isAllMinusOne(scores []Score) bool {
	for _, s := range scores {
		if s != -1 {
			return false
		}
	}
	return true
}

// lexGreater compares two equal-length slices of strings lexicographically and
// returns true if a > b. If lengths differ, longer slice is considered greater.
func lexGreater(a, b []string) bool {
	if len(a) != len(b) {
		return len(a) > len(b)
	}
	for i := range a {
		if a[i] == b[i] {
			continue
		}
		if a[i] > b[i] {
			return true
		}
		return false
	}
	return false
}

// sortEachElementNatural sorts each inner slice by numeric suffix if present, otherwise lexicographically.
func sortEachElementNatural(s [][]string) [][]string {
	for _, el := range s {
		slices.SortFunc(el, func(a, b string) int {
			an, aok := parseTrailingInt(a)
			bn, bok := parseTrailingInt(b)
			if aok && bok {
				if an < bn {
					return -1
				}
				if an > bn {
					return 1
				}
				return 0
			}
			if a < b {
				return -1
			}
			if a > b {
				return 1
			}
			return 0
		})
	}
	return s
}

func parseTrailingInt(s string) (int, bool) {
	// find last '-' and parse the rest as int
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '-' {
			num := s[i+1:]
			if num == "" {
				return 0, false
			}
			// simple base-10 parse; ignore errors
			var n int
			sign := 1
			j := 0
			if num[0] == '-' {
				sign = -1
				j = 1
			}
			for ; j < len(num); j++ {
				c := num[j]
				if c < '0' || c > '9' {
					return 0, false
				}
				n = n*10 + int(c-'0')
			}
			return sign * n, true
		}
	}
	return 0, false
}
