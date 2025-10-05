package topology

import (
	"fmt"
	"iter"

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
	scores []Score
}

type zone struct {
	zoneId                string
	bestNodesForPurposes  []*node // len(bestNodes) == purposeCount
	bestScoresForPurposes []Score

	// totalScores []int
}

func (z *zone) bestScoresForPurposes() iter.Seq[Score] {
	return func(yield func(Score) bool) {
		for purposeIdx, node := range z.bestNodesForPurposes {
			if !yield(node.scores[purposeIdx]) {
				return
			}
		}
	}
}

type MultiPurposeNodeSelector struct {
	purposeCount int
	method       AssignmentMethod
	zones        []*zone
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
	}
}

func (s *MultiPurposeNodeSelector) SetNode(nodeId string, zoneId string, scores []Score) {
	if len(scores) != s.purposeCount {
		panic(fmt.Sprintf("expected len(scores) to be %d (purposeCount), got %d", s.purposeCount, len(scores)))
	}

	// TODO
}

func (s *MultiPurposeNodeSelector) SelectNodes(counts []int) ([][]string, error) {
	if len(counts) != s.purposeCount {
		panic(fmt.Sprintf("expected len(counts) to be %d (purposeCount), got %d", s.purposeCount, len(counts)))
	}

	var totalCount int
	for i, v := range counts {
		if v < 0 || v > MaxSelectionCount {
			panic(fmt.Sprintf("expected counts[i] to be in range [0;%d], got counts[%d]=%d", MaxSelectionCount, i, v))
		}
		totalCount += v
	}

	switch s.method {
	case TransZonal:
		// zone combinations

		for zones := range elementCombinations(s.zones, totalCount) {

			m := hungarian.NewMatrix(totalCount)

			for _, zone := range zones {

				var purposeIdx int // TODO: deriver from i & counts

				_ = zone

				zone.bestScoresForPurposes()

				// m.AddRow(zone.zoneId,
			}

			// note: there are no elements for counts[i]==0
			m.Solve()

		}

		// score - slot with the best score for each zone
		// hungarian algorithm
	case Zonal:
		// groups
		// slot combinations
		// hungarian algorithm
	}

	return nil, nil
}

//
// resultCandidate
//

type resultCandidate struct {
	groups []string
	slots  []string
	score  int
}

func (c *resultCandidate) addSlot(slotId string, groupId string, score int) {
	// TODO check uniqueness
	c.score += score
	c.slots = append(c.slots, slotId)
	c.groups = append(c.groups, groupId)
}

func (c *resultCandidate) toResult(counts []int) [][]string {
	result := make([][]string, 0, len(counts))

	var nextSlotIdx int
	for _, count := range counts {
		slots := make([]string, 0, count)
		result = append(result, slots)
		for range count {
			slots = append(slots, c.slots[nextSlotIdx])
			nextSlotIdx++
		}
	}

	// just to be sure
	if nextSlotIdx != len(c.slots) {
		panic("not all resultCandidate slots were consumed")
	}

	return result
}
