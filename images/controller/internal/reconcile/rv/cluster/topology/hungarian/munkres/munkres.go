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

package munkres

import (
	"bytes"
	"fmt"
	"math"
)

type Matrix struct {
	n int
	A []int64
}

func NewMatrix(n int) *Matrix {
	m := new(Matrix)
	m.n = n
	m.A = make([]int64, n*n)
	return m
}

func (m *Matrix) Print() {
	for i := 0; i < m.n; i++ {
		rowStart := i * m.n
		for j := 0; j < m.n; j++ {
			fmt.Print(m.A[rowStart+j], " ")
		}
		fmt.Println()
	}
}

type Mark int

const (
	Unset Mark = iota
	Starred
	Primed
)

type Context struct {
	m          *Matrix
	rowCovered []bool
	colCovered []bool
	marked     []Mark
	z0row      int
	z0column   int
	rowPath    []int
	colPath    []int
}

func newContext(m *Matrix) *Context {
	n := m.n
	ctx := Context{
		m: &Matrix{
			A: make([]int64, n*n),
			n: n,
		},
		rowPath: make([]int, 2*n),
		colPath: make([]int, 2*n),
		marked:  make([]Mark, n*n),
	}
	copy(ctx.m.A, m.A)
	clearCovers(&ctx)
	return &ctx
}

type Step interface {
	Compute(*Context) (Step, bool)
}

type Step1 struct{}
type Step2 struct{}
type Step3 struct{}
type Step4 struct{}
type Step5 struct{}
type Step6 struct{}

func minInt64(a ...int64) int64 {
	result := int64(math.MaxInt64)
	for _, i := range a {
		if i < result {
			result = i
		}
	}
	return result
}

func (Step1) Compute(ctx *Context) (Step, bool) {
	n := ctx.m.n
	for i := 0; i < n; i++ {
		row := ctx.m.A[i*n : (i+1)*n]
		minval := minInt64(row...)
		for idx := range row {
			row[idx] -= minval
		}
	}
	return Step2{}, false
}

func clearCovers(ctx *Context) {
	n := ctx.m.n
	ctx.rowCovered = make([]bool, n)
	ctx.colCovered = make([]bool, n)
}

func (Step2) Compute(ctx *Context) (Step, bool) {
	n := ctx.m.n
	for i := 0; i < n; i++ {
		rowStart := i * n
		for j := 0; j < n; j++ {
			pos := rowStart + j
			if (ctx.m.A[pos] == 0) &&
				!ctx.colCovered[j] && !ctx.rowCovered[i] {
				ctx.marked[pos] = Starred
				ctx.colCovered[j] = true
				ctx.rowCovered[i] = true
			}
		}
	}
	clearCovers(ctx)
	return Step3{}, false
}

func (Step3) Compute(ctx *Context) (Step, bool) {
	n := ctx.m.n
	count := 0
	for i := 0; i < n; i++ {
		rowStart := i * n
		for j := 0; j < n; j++ {
			pos := rowStart + j
			if ctx.marked[pos] == Starred {
				ctx.colCovered[j] = true
				count++
			}
		}
	}
	if count >= n {
		return nil, true
	}

	return Step4{}, false
}

func findAZero(ctx *Context) (int, int) {
	row := -1
	col := -1
	n := ctx.m.n
Loop:
	for i := 0; i < n; i++ {
		rowStart := i * n
		for j := 0; j < n; j++ {
			if (ctx.m.A[rowStart+j] == 0) &&
				!ctx.rowCovered[i] && !ctx.colCovered[j] {
				row = i
				col = j
				break Loop
			}
		}
	}
	return row, col
}

func findStarInRow(ctx *Context, row int) int {
	n := ctx.m.n
	for j := 0; j < n; j++ {
		if ctx.marked[row*n+j] == Starred {
			return j
		}
	}
	return -1
}

func (Step4) Compute(ctx *Context) (Step, bool) {
	for {
		row, col := findAZero(ctx)
		if row < 0 {
			return Step6{}, false
		}
		n := ctx.m.n
		pos := row*n + col
		ctx.marked[pos] = Primed
		starCol := findStarInRow(ctx, row)
		if starCol >= 0 {
			col = starCol
			ctx.rowCovered[row] = true
			ctx.colCovered[col] = false
		} else {
			ctx.z0row = row
			ctx.z0column = col
			break
		}
	}
	return Step5{}, false
}

func findStarInCol(ctx *Context, col int) int {
	n := ctx.m.n
	for i := 0; i < n; i++ {
		if ctx.marked[i*n+col] == Starred {
			return i
		}
	}
	return -1
}

func findPrimeInRow(ctx *Context, row int) int {
	n := ctx.m.n
	for j := 0; j < n; j++ {
		if ctx.marked[row*n+j] == Primed {
			return j
		}
	}
	return -1
}

func convertPath(ctx *Context, count int) {
	n := ctx.m.n
	for i := 0; i < count+1; i++ {
		r, c := ctx.rowPath[i], ctx.colPath[i]
		offset := r*n + c
		if ctx.marked[offset] == Starred {
			ctx.marked[offset] = Unset
		} else {
			ctx.marked[offset] = Starred
		}
	}
}

func erasePrimes(ctx *Context) {
	n := ctx.m.n
	for i := 0; i < n; i++ {
		rowStart := i * n
		for j := 0; j < n; j++ {
			if ctx.marked[rowStart+j] == Primed {
				ctx.marked[rowStart+j] = Unset
			}
		}
	}
}

func (Step5) Compute(ctx *Context) (Step, bool) {
	count := 0
	ctx.rowPath[count] = ctx.z0row
	ctx.colPath[count] = ctx.z0column
	var done bool
	for !done {
		row := findStarInCol(ctx, ctx.colPath[count])
		if row >= 0 {
			count++
			ctx.rowPath[count] = row
			ctx.colPath[count] = ctx.colPath[count-1]
		} else {
			done = true
		}

		if !done {
			col := findPrimeInRow(ctx, ctx.rowPath[count])
			count++
			ctx.rowPath[count] = ctx.rowPath[count-1]
			ctx.colPath[count] = col
		}
	}
	convertPath(ctx, count)
	clearCovers(ctx)
	erasePrimes(ctx)
	return Step3{}, false
}

func findSmallest(ctx *Context) int64 {
	n := ctx.m.n
	minval := int64(math.MaxInt64)
	for i := 0; i < n; i++ {
		rowStart := i * n
		for j := 0; j < n; j++ {
			if (!ctx.rowCovered[i]) && (!ctx.colCovered[j]) {
				a := ctx.m.A[rowStart+j]
				if minval > a {
					minval = a
				}
			}
		}
	}
	return minval
}

func (Step6) Compute(ctx *Context) (Step, bool) {
	n := ctx.m.n
	minval := findSmallest(ctx)
	for i := 0; i < n; i++ {
		rowStart := i * n
		for j := 0; j < n; j++ {
			if ctx.rowCovered[i] {
				ctx.m.A[rowStart+j] += minval
			}
			if !ctx.colCovered[j] {
				ctx.m.A[rowStart+j] -= minval
			}
		}
	}
	return Step4{}, false
}

type RowCol struct {
	Row, Col int
}

func (ctx *Context) String() string {
	var buf bytes.Buffer
	n := ctx.m.n
	for i := 0; i < n; i++ {
		rowStart := i * n
		for j := 0; j < n; j++ {
			fmt.Fprint(&buf, ctx.m.A[i*n+j])
			if ctx.marked[rowStart+j] == Starred {
				fmt.Fprint(&buf, "*")
			}
			if ctx.marked[rowStart+j] == Primed {
				fmt.Fprint(&buf, "'")
			}
			fmt.Fprint(&buf, " ")
		}
	}
	fmt.Fprint(&buf, "; cover row/col: ")
	printCover := func(c []bool) {
		for _, r := range c {
			if r {
				fmt.Fprint(&buf, "T")
			} else {
				fmt.Fprint(&buf, "F")
			}
		}
	}
	printCover(ctx.rowCovered)
	fmt.Fprint(&buf, "/")
	printCover(ctx.colCovered)
	return buf.String()
}

var (
	Debugger = func(Step, *Context) {}
)

func computeMunkres(m *Matrix, minimize bool) []RowCol {
	ctx := newContext(m)
	if !minimize {
		for idx := range ctx.m.A {
			ctx.m.A[idx] = math.MaxInt64 - ctx.m.A[idx]
		}
	}
	var step Step
	step = Step1{}
	for {
		nextStep, done := step.Compute(ctx)
		Debugger(step, ctx)
		if done {
			break
		}
		step = nextStep
	}
	results := []RowCol{}
	n := m.n
	for i := 0; i < n; i++ {
		rowStart := i * n
		for j := 0; j < n; j++ {
			if ctx.marked[rowStart+j] == Starred {
				results = append(results, RowCol{i, j})
			}
		}
	}
	return results
}

func ComputeMunkresMax(m *Matrix) []RowCol {
	return computeMunkres(m, false)
}

func ComputeMunkresMin(m *Matrix) []RowCol {
	return computeMunkres(m, true)
}
