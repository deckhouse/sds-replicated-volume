package topology

import (
	"fmt"
)

// MultiPurposeNodeSelector: topology is ignored, nodes are selected cluster-wide
type MultiPurposeNodeSelector struct {
	purposeCount int
	nodes        []*node
}

func NewMultiPurposeNodeSelector(purposeCount int) *MultiPurposeNodeSelector {
	validatePurposeCount(purposeCount)
	return &MultiPurposeNodeSelector{purposeCount: purposeCount}
}

func (s *MultiPurposeNodeSelector) SetNode(nodeId string, scores []Score) {
	if len(scores) != s.purposeCount {
		panic(fmt.Sprintf("expected len(scores) to be %d (purposeCount), got %d", s.purposeCount, len(scores)))
	}

	node := &node{
		nodeId: nodeId,
	}
	node.scores = scores

	s.nodes = append(s.nodes, node)

	// validate no nodes with >1 AlwaysSelect
}

func (s *MultiPurposeNodeSelector) SelectNodes(counts []int) ([][]string, error) {
	totalCount, err := validateAndSumCounts(s.purposeCount, counts)
	if err != nil {
		return nil, err
	}

	// the same as Zonal, but with one giant zone
	bestNodes, _ := solveZone(s.nodes, totalCount, counts)
	return sortEachElement(compact(bestNodes, counts)), nil
}
