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

// TODO: https://github.com/clyphub/munkres
//
// TODO: github.com/oddg/hungarian-algorithm
//
// TODO: github.com/arthurkushman/go-hungarian
//
// TODO: more?
package hungarian

import (
	"fmt"

	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/reconcile/rv/cluster/topology/hungarian/munkres"
)

type ScoreMatrix[T any] struct {
	n      int
	rows   []T
	scores [][]int64
}

func NewScoreMatrix[T any](n int) *ScoreMatrix[T] {
	if n <= 0 {
		panic("expected n to be positive")
	}
	return &ScoreMatrix[T]{
		n:      n,
		rows:   make([]T, 0, n),
		scores: make([][]int64, 0, n),
	}
}

func (m *ScoreMatrix[T]) AddRow(row T, scores []int64) {
	m.rows = append(m.rows, row)
	m.scores = append(m.scores, scores)
}

func (m *ScoreMatrix[T]) Solve() ([]T, int64) {
	if len(m.rows) != m.n {
		panic(fmt.Sprintf("expected %d rows, got %d", m.n, len(m.rows)))
	}

	mx := munkres.NewMatrix(m.n)
	var aIdx int
	for _, row := range m.scores {
		for _, score := range row {
			mx.A[aIdx] = score
			aIdx++
		}
	}

	rowCols := munkres.ComputeMunkresMax(mx)

	resultRowIds := make([]T, m.n)
	var totalScore int64
	for _, rowCol := range rowCols {
		resultRowIds[rowCol.Col] = m.rows[rowCol.Row]
		totalScore += m.scores[rowCol.Row][rowCol.Col]
	}

	return resultRowIds, totalScore
}
