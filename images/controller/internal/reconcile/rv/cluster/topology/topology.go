package topology

import (
	"errors"
	"slices"
	"strings"
)

var ErrNotEnoughSlots = errors.New("not enough slots for selection")

var ErrCannotSelectRequiredSlot = errors.New("can not select slot, which is required for selection")

// This function is applied to each slot id before comparing to others.
//
// It may be useful to override it if you want to interfere the default slot id
// ordering, which is lexicographical. Function is called frequently, so
// consider caching.
var HashSlotId = func(id string) string { return id }

type PackMethod byte

const (
	OnePerGroup PackMethod = iota
	SingleGroup
	// FEAT: Evenly - start like in OnePerGroup, and then allow putting more per group
)

type Score int64

const (
	NeverSelect  Score = 0
	AlwaysSelect Score = 1<<63 - 1 // MaxInt64
)

type slotData struct {
	id     string
	group  string
	scores []Score
}

type compareByScore func(*slotData, *slotData) int

type Packer struct {
	byId []*slotData

	byScores [][]*slotData

	// to optimize closure allocation
	compareByScoreCache []compareByScore
}

func (p *Packer) SetSlot(id string, group string, scores []Score) {
	p.initByScores(len(scores))

	idx, exists := slices.BinarySearchFunc(p.byId, id, compareBySlotId)

	if !exists {
		// append
		slot := &slotData{
			id: id,
		}

		p.byId = slices.Insert(p.byId, idx, slot)

	}

	// update
	slot := p.byId[idx]
	slot.group = group
	slot.scores = scores

	// index
	for i := range p.byScores {
		p.byScores[i] = append(p.byScores[i], slot)

		slices.SortStableFunc(p.byScores[i], p.getCompareByScoreDesc(i))
	}
}

func (p *Packer) Select(counts []int, method PackMethod) ([][]string, error) {
	selectedGroups := map[string]struct{}{}

	res := make([][]string, 0, len(counts))
OUTER:
	for i, count := range counts {
		// if scores are not initialized, it means they all zeroes
		byScore := sliceGetOrDefault(p.byScores, i, p.byId)

		if count == 0 {
			if len(byScore) > 0 && sliceGetOrDefault(byScore[0].scores, i, 0) == AlwaysSelect {
				return nil, ErrCannotSelectRequiredSlot
			}
			res = append(res, nil)
			continue
		}

		ids := make([]string, 0, count)
		selectSlot := func(s *slotData) (done bool) {
			selectedGroups[s.group] = struct{}{}

			ids = append(ids, s.id)
			if len(ids) == count {
				res = append(res, ids)
				done = true
			}
			return
		}

		for j, slot := range byScore {
			if sliceGetOrDefault(slot.scores, i, 0) == NeverSelect {
				continue
			}
			if _, ok := selectedGroups[slot.group]; ok == methodToBool(method) {
				if sliceGetOrDefault(slot.scores, i, 0) == AlwaysSelect {
					return nil, ErrCannotSelectRequiredSlot
				}
				continue
			}
			if selectSlot(slot) {
				nextSlot := sliceGetOrDefault(byScore, j+1, nil)
				if nextSlot != nil && sliceGetOrDefault(nextSlot.scores, i, 0) == AlwaysSelect {
					return nil, ErrCannotSelectRequiredSlot
				}
				continue OUTER
			}
		}

		return nil, ErrNotEnoughSlots
	}

	return res, nil
}

func (p *Packer) initByScores(scoresLen int) {
	for len(p.byScores) < scoresLen {
		p.byScores = append(p.byScores, slices.Clone(p.byId))
	}
}

func (p *Packer) getCompareByScoreDesc(idx int) compareByScore {
	for i := len(p.compareByScoreCache); i <= idx; i++ {
		p.compareByScoreCache = append(
			p.compareByScoreCache,
			func(a, b *slotData) int {
				as := sliceGetOrDefault(a.scores, i, 0)
				bs := sliceGetOrDefault(b.scores, i, 0)
				// using arithmetics is dangerous here,
				// because of special values of [Score]
				if as < bs {
					// in descending order
					return 1
				} else if as > bs {
					return -1
				}
				return 0
			},
		)
	}
	return p.compareByScoreCache[idx]
}

func sliceGetOrDefault[T any](s []T, index int, v T) T {
	if len(s) > index {
		v = s[index]
	}
	return v
}

func compareBySlotId(s *slotData, id string) int {
	return strings.Compare(HashSlotId(s.id), HashSlotId(id))
}

func methodToBool(method PackMethod) (onePerGroup bool) {
	switch method {
	case OnePerGroup:
		onePerGroup = true
	case SingleGroup:
	default:
		panic("not implemented - unknown method")
	}
	return
}
