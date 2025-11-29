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
