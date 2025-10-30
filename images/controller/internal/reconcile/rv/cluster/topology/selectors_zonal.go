package topology

import (
	"fmt"
)

type ZonalMultiPurposeNodeSelector struct {
	purposeCount int
	zones        []*zone
}

func NewZonalMultiPurposeNodeSelector(purposeCount int) *ZonalMultiPurposeNodeSelector {
	validatePurposeCount(purposeCount)
	return &ZonalMultiPurposeNodeSelector{purposeCount: purposeCount}
}

func (s *ZonalMultiPurposeNodeSelector) SetNode(nodeId string, zoneId string, scores []Score) {
	if len(scores) != s.purposeCount {
		panic(fmt.Sprintf("expected len(scores) to be %d (purposeCount), got %d", s.purposeCount, len(scores)))
	}

	// TODO
	// validate no nodes with >1 AlwaysSelect
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
		zoneNodes, totalScore := solveZone(zone.nodes, totalCount, counts)
		if totalScore > bestTotalScore {
			bestTotalScore = totalScore
			bestNodes = zoneNodes
		}
	}

	if len(bestNodes) == 0 {
		return nil, ErrSelectionImpossibleError
	}

	return compact(bestNodes, counts), nil
}
