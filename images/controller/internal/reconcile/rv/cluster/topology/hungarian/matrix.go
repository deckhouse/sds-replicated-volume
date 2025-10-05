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

type Matrix struct {
	n      int
	rowIds []string
	scores [][]int
}

func NewMatrix(n int) *Matrix {
	if n <= 0 {
		panic("expected n to be positive")
	}
	return &Matrix{
		n:      n,
		rowIds: make([]string, 0, n),
		scores: make([][]int, 0, n),
	}
}

func (m *Matrix) AddRow(id string, scores []int) {
	m.rowIds = append(m.rowIds, id)
	m.scores = append(m.scores, scores)
}

func (m *Matrix) Solve() []string {
	if len(m.rowIds) != m.n {
		panic(fmt.Sprintf("expected %d rows, got %d", m.n, len(m.rowIds)))
	}

	mx := munkres.NewMatrix(m.n)
	var aIdx int
	for _, row := range m.scores {
		for _, score := range row {
			mx.A[aIdx] = int64(score)
			aIdx++
		}
	}

	rowCols := munkres.ComputeMunkresMax(mx)

	result := make([]string, m.n)
	for _, rowCol := range rowCols {
		result[rowCol.Col] = m.rowIds[rowCol.Row]
	}

	return result
}
