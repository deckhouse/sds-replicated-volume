package topology

import (
	"fmt"
	"slices"

	uiter "github.com/deckhouse/sds-common-lib/utils/iter"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/reconcile/rv/cluster/topology/hungarian"
)

var MaxPurposeCount = 100 // TODO adjust
var MaxSelectionCount = 8 // TODO adjust

type AssignmentMethod byte

const (
	TransZonal AssignmentMethod = iota
	Zonal
	Ignore
)

type node struct {
	nodeId string
	scores []int64
}

type zone struct {
	zoneId string

	nodes []*node

	bestNodesForPurposes  []*node // len(bestNodes) == purposeCount
	bestScoresForPurposes []int64
	// totalScores []int
}

// func (z *zone) bestScoresForPurposes() iter.Seq[Score] {
// 	return func(yield func(Score) bool) {
// 		for purposeIdx, node := range z.bestNodesForPurposes {
// 			if !yield(node.scores[purposeIdx]) {
// 				return
// 			}
// 		}
// 	}
// }

type MultiPurposeNodeSelector struct {
	purposeCount int
	method       AssignmentMethod
	zones        []*zone
	nodes        []*node
}

func NewMultiPurposeNodeSelector(purposeCount int, method AssignmentMethod) *MultiPurposeNodeSelector {
	if purposeCount <= 0 || purposeCount > MaxPurposeCount {
		panic(fmt.Sprintf("expected purposeCount to be in range [1;%d], got %d", MaxPurposeCount, purposeCount))
	}

	switch method {
	case TransZonal, Zonal:
	default:
		panic("not implemented: unknown AssignmentMethod value")
	}

	return &MultiPurposeNodeSelector{
		purposeCount: purposeCount,
		method:       method,
	}
}

func (s *MultiPurposeNodeSelector) SetNode(nodeId string, zoneId string, scores []Score) {
	if len(scores) != s.purposeCount {
		panic(fmt.Sprintf("expected len(scores) to be %d (purposeCount), got %d", s.purposeCount, len(scores)))
	}

	// TODO
	// validate no nodes with >1 AlwaysSelect
}

func (s *MultiPurposeNodeSelector) SelectNodes(counts []int) ([][]string, error) {
	if len(counts) != s.purposeCount {
		panic(fmt.Sprintf("expected len(counts) to be %d (purposeCount), got %d", s.purposeCount, len(counts)))
	}

	var totalCount int
	for i, v := range counts {
		if v < 1 || v > MaxSelectionCount {
			panic(fmt.Sprintf("expected counts[i] to be in range [1;%d], got counts[%d]=%d", MaxSelectionCount, i, v))
		}
		totalCount += v
	}

	switch s.method {
	case TransZonal:
		// TODO: validate: no zones with >1 AlwaysSelect
		// TODO: prefill: all AlwaysSelect zones
		// TODO: validate if there's a never select score

		// zone combinations

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

	case Zonal:
		var bestNodes []string
		var bestTotalScore int64

		// zones
		for _, zone := range s.zones {
			zoneNodes, totalScore := solveZone(zone.nodes, totalCount)
			if totalScore > bestTotalScore {
				bestTotalScore = totalScore
				bestNodes = zoneNodes
			}
		}

		return compact(bestNodes, counts), nil
	case Ignore:
		// the same as Zonal, but with one giant zone
		bestNodes, _ := solveZone(s.nodes, totalCount)
		return compact(bestNodes, counts), nil
	}

	return nil, nil
}

func solveZone(nodes []*node, totalCount int) ([]string, int64) {
	var bestNodes []*node
	var bestTotalScore int64

	for nodes := range elementCombinations(nodes, totalCount) {
		m := hungarian.NewScoreMatrix[*node](totalCount)

		for _, node := range nodes {
			m.AddRow(node, node.scores)
		}

		optimalNodes, totalScore := m.Solve()
		if totalScore > bestTotalScore {
			bestTotalScore = totalScore
			bestNodes = optimalNodes
		}
	}

	return slices.Collect(
			uiter.Map(
				slices.Values(bestNodes),
				func(n *node) string { return n.nodeId },
			),
		),
		bestTotalScore
}
