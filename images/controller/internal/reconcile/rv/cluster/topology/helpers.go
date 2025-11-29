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
	"errors"
	"fmt"
	"iter"
	"slices"

	uiter "github.com/deckhouse/sds-common-lib/utils/iter"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/reconcile/rv/cluster/topology/hungarian"
)

var MaxPurposeCount = 100 // TODO adjust
var MaxSelectionCount = 8 // TODO adjust

var ErrInputError = errors.New("invalid input to SelectNodes")
var ErrSelectionImpossibleError = errors.New("node selection problem is not solvable")

type Score int64

const (
	NeverSelect  Score = 0
	AlwaysSelect Score = 1<<63 - 1 // MaxInt64
)

type NodeSelector interface {
	SelectNodes(counts []int) ([][]string, error)
}

type node struct {
	nodeID string
	scores []Score
}

type zone struct {
	zoneID string

	nodes []*node

	bestNodesForPurposes  []*node // len(bestNodes) == purposeCount
	bestScoresForPurposes []int64
}

// helpers shared across selectors
func validatePurposeCount(purposeCount int) {
	if purposeCount <= 0 || purposeCount > MaxPurposeCount {
		panic(fmt.Sprintf("expected purposeCount to be in range [1;%d], got %d", MaxPurposeCount, purposeCount))
	}
}

func validateAndSumCounts(purposeCount int, counts []int) (int, error) {
	if len(counts) != purposeCount {
		return 0, fmt.Errorf("%w: expected len(counts) to be %d (purposeCount), got %d", ErrInputError, purposeCount, len(counts))
	}
	var totalCount int
	for i, v := range counts {
		if v < 1 || v > MaxSelectionCount {
			return 0, fmt.Errorf("%w: expected counts[i] to be in range [1;%d], got counts[%d]=%d", ErrInputError, MaxSelectionCount, i, v)
		}
		totalCount += v
	}
	return totalCount, nil
}

func solveZone(nodes []*node, totalCount int, counts []int) ([]string, int64) {
	var bestNodes []*node
	var bestTotalScore int64

	for nodes := range elementCombinations(nodes, totalCount) {
		m := hungarian.NewScoreMatrix[*node](totalCount)

		for _, node := range nodes {
			m.AddRow(
				node,
				slices.Collect(
					uiter.Map(
						repeat(node.scores, counts),
						func(s Score) int64 { return int64(s) },
					),
				),
			)
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
				func(n *node) string { return n.nodeID },
			),
		),
		bestTotalScore
}

//
// iter
//

func repeat[T any](src []T, counts []int) iter.Seq[T] {
	if len(src) != len(counts) {
		panic("expected len(src) == len(counts)")
	}

	return func(yield func(T) bool) {
		for i := 0; i < len(src); i++ {
			for range counts[i] {
				if !yield(src[i]) {
					return
				}
			}
		}
	}
}

func sortEachElement[T cmp.Ordered](s [][]T) [][]T {
	for _, el := range s {
		slices.Sort(el)
	}
	return s
}

// opposite of [repeat]
func compact[T any](src []T, counts []int) [][]T {
	res := make([][]T, len(counts))

	var srcIndex int
	for i, count := range counts {
		for range count {
			if srcIndex == len(src) {
				panic("expected len(src) to be sum of all counts, got smaller")
			}
			res[i] = append(res[i], src[srcIndex])
			srcIndex++
		}
	}
	if srcIndex != len(src) {
		panic("expected len(src) to be sum of all counts, got bigger")
	}
	return res
}

//
// combinations
//

func elementCombinations[T any](s []T, k int) iter.Seq[[]T] {
	result := make([]T, k)

	return func(yield func([]T) bool) {
		for sIndexes := range indexCombinations(len(s), k) {
			for i, sIndex := range sIndexes {
				result[i] = s[sIndex]
			}

			if !yield(result) {
				return
			}
		}
	}
}

// indexCombinations yields all k-combinations of indices [0..n).
// The same backing slice is reused for every yield.
// If you need to retain a combination, copy it in the caller.
func indexCombinations(n int, k int) iter.Seq[[]int] {
	if k > n {
		panic(fmt.Sprintf("expected k<=n, got k=%d, n=%d", k, n))
	}

	result := make([]int, k)

	return func(yield func([]int) bool) {
		if k == 0 {
			return
		}

		// Initialize to the first combination: [0,1,2,...,k-1]
		for i := range k {
			result[i] = i
		}
		if !yield(result) {
			return
		}

		resultTail := k - 1
		nk := n - k

		for {
			// find rightmost index that can be incremented
			i := resultTail

			for {
				if result[i] == nk+i {
					// already maximum
					i--
				} else {
					// found
					break
				}

				if i < 0 {
					// all combinations generated
					return
				}
			}

			// increment and reset the tail to the minimal increasing sequence.
			result[i]++
			next := result[i]
			for j := i + 1; j < k; j++ {
				next++
				result[j] = next
			}

			if !yield(result) {
				return
			}
		}
	}
}
