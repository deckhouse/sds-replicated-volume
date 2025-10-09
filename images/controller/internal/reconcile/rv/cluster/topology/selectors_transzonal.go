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

func (s *TransZonalMultiPurposeNodeSelector) SetNode(nodeId string, zoneId string, scores []Score) {
	if len(scores) != s.purposeCount {
		panic(fmt.Sprintf("expected len(scores) to be %d (purposeCount), got %d", s.purposeCount, len(scores)))
	}

	idx, found := slices.BinarySearchFunc(
		s.zones,
		zoneId,
		func(z *zone, id string) int { return cmp.Compare(z.zoneId, id) },
	)

	var z *zone
	if found {
		z = s.zones[idx]
	} else {
		z = &zone{
			zoneId:                zoneId,
			bestNodesForPurposes:  make([]*node, s.purposeCount),
			bestScoresForPurposes: make([]int64, s.purposeCount),
		}
		s.zones = slices.Insert(s.zones, idx, z)
	}

	idx, found = slices.BinarySearchFunc(z.nodes, nodeId, func(n *node, id string) int { return cmp.Compare(n.nodeId, id) })
	var n *node
	if found {
		n = z.nodes[idx]
	} else {
		n = &node{
			nodeId: nodeId,
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

	// TODO: validate: no zones with >1 AlwaysSelect
	// TODO: prefill: all AlwaysSelect zones
	// TODO: validate if there's a never select score

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

	// TODO: check if there are results at all and return error if none

	// convert bestZones to bestNodes by taking the best node for purpose
	compactedBestZones := compact(bestZones, counts)
	result := make([][]string, 0, len(counts))
	for purposeIdx, bestZones := range compactedBestZones {
		bestNodes := slices.Collect(
			uiter.Map(
				slices.Values(bestZones),
				func(z *zone) string {
					return z.bestNodesForPurposes[purposeIdx].nodeId
				},
			),
		)
		result = append(result, bestNodes)
	}

	return result, nil
}
