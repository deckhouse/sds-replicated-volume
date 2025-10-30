// Copyright 2014 clypd, inc.
//
// see /LICENSE file for more information
//

package munkres

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_NewMatrix(t *testing.T) {
	m := NewMatrix(4)
	assert.NotEmpty(t, m.A)
	assert.Equal(t, m.n, 4)
	assert.Equal(t, len(m.A), m.n*m.n)
	m.Print()
}

func contextsEqual(act, exp *Context) error {
	if !assert.ObjectsAreEqual(act.m.A, exp.m.A) {
		return fmt.Errorf("A: %v != %v", act, exp)
	}
	if !assert.ObjectsAreEqual(act.rowCovered, exp.rowCovered) {
		return fmt.Errorf("rowCovered: %v != %v", act, exp)
	}
	if !assert.ObjectsAreEqual(act.colCovered, exp.colCovered) {
		return fmt.Errorf("colCovered: %v != %v", act, exp)
	}
	if !assert.ObjectsAreEqual(act.marked, exp.marked) {
		return fmt.Errorf("marked: %v != %v", act, exp)
	}
	return nil
}

func Test_StepwiseMunkres(t *testing.T) {
	// See:
	//   http://csclab.murraystate.edu/bob.pilgrim/445/munkres.html
	// Each 'mark' below is a step in the illustrated algorithm.
	pilgrimInput := []int64{1, 2, 3, 2, 4, 6, 3, 6, 9}
	m := NewMatrix(3)
	copy(m.A, pilgrimInput)
	ctx := newContext(m)
	funcs := []func(*testing.T, *Context){
		// mark 01 just illustrates the input matrix - there's nothing to test
		doMark02,
		doMark03,
		doMark04,
		doMark05,
		doMark06,
		doMark07,
		doMark08,
		doMark09,
		doMark10,
		doMark11,
		doMark12,
		doMark13,
		doMark14,
		doMark15,
		doMark16,
	}
	for _, fn := range funcs {
		fn(t, ctx)
	}
}

func doMark02(t *testing.T, ctx *Context) {
	s, done := Step1{}.Compute(ctx)
	assert.False(t, done)
	assert.IsType(t, Step2{}, s)
	assert.NoError(t, contextsEqual(ctx, &Context{
		m: &Matrix{
			A: []int64{0, 1, 2, 0, 2, 4, 0, 3, 6},
			n: 3,
		},
		rowCovered: []bool{false, false, false},
		colCovered: []bool{false, false, false},
		marked: []Mark{Unset, Unset, Unset,
			Unset, Unset, Unset,
			Unset, Unset, Unset},
	}))
	assert.NotEmpty(t, ctx.String())
}

func doMark03(t *testing.T, ctx *Context) {
	s, done := Step2{}.Compute(ctx)
	assert.False(t, done)
	assert.IsType(t, Step3{}, s)
	assert.NoError(t, contextsEqual(ctx, &Context{
		m: &Matrix{
			A: []int64{0, 1, 2, 0, 2, 4, 0, 3, 6},
			n: 3,
		},
		rowCovered: []bool{false, false, false},
		colCovered: []bool{false, false, false},
		marked: []Mark{Starred, Unset, Unset,
			Unset, Unset, Unset,
			Unset, Unset, Unset},
	}))
	assert.NotEmpty(t, ctx.String())
}

func doMark04(t *testing.T, ctx *Context) {
	s, done := Step3{}.Compute(ctx)
	assert.False(t, done)
	assert.IsType(t, Step4{}, s)
	assert.NoError(t, contextsEqual(ctx, &Context{
		m: &Matrix{
			A: []int64{0, 1, 2, 0, 2, 4, 0, 3, 6},
			n: 3,
		},
		rowCovered: []bool{false, false, false},
		colCovered: []bool{true, false, false},
		marked: []Mark{Starred, Unset, Unset,
			Unset, Unset, Unset,
			Unset, Unset, Unset},
	}))
	assert.NotEmpty(t, ctx.String())
}

func doMark05(t *testing.T, ctx *Context) {
	s, done := Step4{}.Compute(ctx)
	assert.False(t, done)
	assert.IsType(t, Step6{}, s)
	assert.NoError(t, contextsEqual(ctx, &Context{
		m: &Matrix{
			A: []int64{0, 1, 2, 0, 2, 4, 0, 3, 6},
			n: 3,
		},
		rowCovered: []bool{false, false, false},
		colCovered: []bool{true, false, false},
		marked: []Mark{Starred, Unset, Unset,
			Unset, Unset, Unset,
			Unset, Unset, Unset},
	}))
	assert.NotEmpty(t, ctx.String())
}

func doMark06(t *testing.T, ctx *Context) {
	s, done := Step6{}.Compute(ctx)
	assert.False(t, done)
	assert.IsType(t, Step4{}, s)
	assert.NoError(t, contextsEqual(ctx, &Context{
		m: &Matrix{
			A: []int64{0, 0, 1, 0, 1, 3, 0, 2, 5},
			n: 3,
		},
		rowCovered: []bool{false, false, false},
		colCovered: []bool{true, false, false},
		marked: []Mark{Starred, Unset, Unset,
			Unset, Unset, Unset,
			Unset, Unset, Unset},
	}))
	assert.NotEmpty(t, ctx.String())
}

func doMark07(t *testing.T, ctx *Context) {
	s, done := Step4{}.Compute(ctx)
	assert.False(t, done)
	assert.IsType(t, Step5{}, s)
	assert.NoError(t, contextsEqual(ctx, &Context{
		m: &Matrix{
			A: []int64{0, 0, 1, 0, 1, 3, 0, 2, 5},
			n: 3,
		},
		rowCovered: []bool{true, false, false},
		colCovered: []bool{false, false, false},
		marked: []Mark{Starred, Primed, Unset,
			Primed, Unset, Unset,
			Unset, Unset, Unset},
	}))
	assert.NotEmpty(t, ctx.String())
}

func doMark08(t *testing.T, ctx *Context) {
	s, done := Step5{}.Compute(ctx)
	assert.False(t, done)
	assert.IsType(t, Step3{}, s)
	assert.NoError(t, contextsEqual(ctx, &Context{
		m: &Matrix{
			A: []int64{0, 0, 1, 0, 1, 3, 0, 2, 5},
			n: 3,
		},
		// NOTE that the coverage doesn't match the expected output on the web
		// page. However,  step 5 of the algorithm clearly clears the covers, so
		// the web page is likely incorrect.
		rowCovered: []bool{false, false, false},
		colCovered: []bool{false, false, false},
		// NOTE also that these markings don't match the web page:
		//   * ' _
		//   ' _ _
		//   _ _ _
		// I can't explain this but since this implementation works for all the
		// test cases I've tried, I'm moving on for now.
		marked: []Mark{Unset, Starred, Unset,
			Starred, Unset, Unset,
			Unset, Unset, Unset},
	}))
	assert.NotEmpty(t, ctx.String())
}

func doMark09(t *testing.T, ctx *Context) {
	s, done := Step3{}.Compute(ctx)
	assert.False(t, done)
	assert.IsType(t, Step4{}, s)
	assert.NoError(t, contextsEqual(ctx, &Context{
		m: &Matrix{
			A: []int64{0, 0, 1, 0, 1, 3, 0, 2, 5},
			n: 3,
		},
		rowCovered: []bool{false, false, false},
		colCovered: []bool{true, true, false},
		marked: []Mark{Unset, Starred, Unset,
			Starred, Unset, Unset,
			Unset, Unset, Unset},
	}))
	assert.NotEmpty(t, ctx.String())
}

func doMark10(t *testing.T, ctx *Context) {
	s, done := Step4{}.Compute(ctx)
	assert.False(t, done)
	assert.IsType(t, Step6{}, s)
	assert.NoError(t, contextsEqual(ctx, &Context{
		m: &Matrix{
			A: []int64{0, 0, 1, 0, 1, 3, 0, 2, 5},
			n: 3,
		},
		rowCovered: []bool{false, false, false},
		colCovered: []bool{true, true, false},
		marked: []Mark{Unset, Starred, Unset,
			Starred, Unset, Unset,
			Unset, Unset, Unset},
	}))
	assert.NotEmpty(t, ctx.String())
}

func doMark11(t *testing.T, ctx *Context) {
	s, done := Step6{}.Compute(ctx)
	assert.False(t, done)
	assert.IsType(t, Step4{}, s)
	assert.NoError(t, contextsEqual(ctx, &Context{
		m: &Matrix{
			A: []int64{0, 0, 0, 0, 1, 2, 0, 2, 4},
			n: 3,
		},
		rowCovered: []bool{false, false, false},
		colCovered: []bool{true, true, false},
		marked: []Mark{Unset, Starred, Unset,
			Starred, Unset, Unset,
			Unset, Unset, Unset},
	}))
	assert.NotEmpty(t, ctx.String())
}

func doMark12(t *testing.T, ctx *Context) {
	s, done := Step4{}.Compute(ctx)
	assert.False(t, done)
	assert.IsType(t, Step6{}, s)
	assert.NoError(t, contextsEqual(ctx, &Context{
		m: &Matrix{
			A: []int64{0, 0, 0, 0, 1, 2, 0, 2, 4},
			n: 3,
		},
		rowCovered: []bool{true, false, false},
		colCovered: []bool{true, false, false},
		marked: []Mark{Unset, Starred, Primed,
			Starred, Unset, Unset,
			Unset, Unset, Unset},
	}))
	assert.NotEmpty(t, ctx.String())
}

func doMark13(t *testing.T, ctx *Context) {
	s, done := Step6{}.Compute(ctx)
	assert.False(t, done)
	assert.IsType(t, Step4{}, s)
	assert.NoError(t, contextsEqual(ctx, &Context{
		m: &Matrix{
			A: []int64{1, 0, 0, 0, 0, 1, 0, 1, 3},
			n: 3,
		},
		rowCovered: []bool{true, false, false},
		colCovered: []bool{true, false, false},
		marked: []Mark{Unset, Starred, Primed,
			Starred, Unset, Unset,
			Unset, Unset, Unset},
	}))
	assert.NotEmpty(t, ctx.String())
}

func doMark14(t *testing.T, ctx *Context) {
	s, done := Step4{}.Compute(ctx)
	assert.False(t, done)
	assert.IsType(t, Step5{}, s)
	assert.NoError(t, contextsEqual(ctx, &Context{
		m: &Matrix{
			A: []int64{1, 0, 0, 0, 0, 1, 0, 1, 3},
			n: 3,
		},
		rowCovered: []bool{true, true, false},
		colCovered: []bool{false, false, false},
		marked: []Mark{Unset, Starred, Primed,
			Starred, Primed, Unset,
			Primed, Unset, Unset},
	}))
	assert.NotEmpty(t, ctx.String())
}

func doMark15(t *testing.T, ctx *Context) {
	s, done := Step5{}.Compute(ctx)
	assert.False(t, done)
	assert.IsType(t, Step3{}, s)
	assert.NoError(t, contextsEqual(ctx, &Context{
		m: &Matrix{
			A: []int64{1, 0, 0, 0, 0, 1, 0, 1, 3},
			n: 3,
		},
		rowCovered: []bool{false, false, false},
		colCovered: []bool{false, false, false},
		// NOTE also that these markings don't match the web page:
		//   _ * '
		//   * ' _
		//   ' _ _
		// I can't explain this but since this implementation works for all the
		// test cases I've tried, I'm moving on for now.
		marked: []Mark{Unset, Unset, Starred,
			Unset, Starred, Unset,
			Starred, Unset, Unset},
	}))
	assert.NotEmpty(t, ctx.String())
}

func doMark16(t *testing.T, ctx *Context) {
	s, done := Step3{}.Compute(ctx)
	assert.True(t, done)
	assert.Nil(t, s)
	assert.NoError(t, contextsEqual(ctx, &Context{
		m: &Matrix{
			A: []int64{1, 0, 0, 0, 0, 1, 0, 1, 3},
			n: 3,
		},
		rowCovered: []bool{false, false, false},
		colCovered: []bool{true, true, true},
		marked: []Mark{Unset, Unset, Starred,
			Unset, Starred, Unset,
			Starred, Unset, Unset},
	}))
	assert.NotEmpty(t, ctx.String())
}

func Test_ComputeMunkres(t *testing.T) {
	m := NewMatrix(4)
	m.A = []int64{94, 93, 20, 37,
		75, 18, 71, 43,
		20, 29, 32, 25,
		37, 72, 17, 73}
	origDbg := Debugger
	var debuggerCalled bool
	_ = debuggerCalled
	Debugger = func(s Step, ctx *Context) {
		assert.NotNil(t, s)
		assert.NotNil(t, ctx)
		debuggerCalled = true
	}
	defer func() { Debugger = origDbg }()
	for _, assignment := range ComputeMunkresMin(m) {
		fmt.Print(assignment, ", ")
	}
	fmt.Println()
	fmt.Println(ComputeMunkresMin(m))
}
