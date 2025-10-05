package topology_test

import (
	"testing"

	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/reconcile/rv/cluster/topology"
	"github.com/google/go-cmp/cmp"
)

type setSlotArgs struct {
	id     string
	group  string
	scores []topology.Score
}

type selectArgs struct {
	counts []int
	method topology.PackMethod
}

type selectResult struct {
	expectedResult [][]string
	expectedErr    error
}

type testCase struct {
	name    string
	arrange []setSlotArgs
	act     selectArgs
	assert  selectResult
}

var testCases []testCase = []testCase{
	{
		name: "OnePerGroup_positive",
		arrange: []setSlotArgs{
			{"node-0", "zone-a", []topology.Score{1, 0, 0}},
			{"node-1", "zone-a", []topology.Score{0, 1, 0}},
			{"node-2", "zone-a", []topology.Score{0, 0, 1}},
			{"node-3", "zone-b", []topology.Score{2, 0, 0}},
			{"node-4", "zone-b", []topology.Score{0, 2, 0}},
			{"node-5", "zone-b", []topology.Score{0, 0, 2}},
			{"node-6", "zone-c", []topology.Score{3, 0, 0}},
			{"node-7", "zone-c", []topology.Score{0, 3, 0}},
			{"node-8", "zone-c", []topology.Score{0, 0, 3}},
			{"node-9", "zone-d0", []topology.Score{-1, -1, -1}},
			{"node-10", "zone-d1", []topology.Score{-1, -1, -1}},
			{"node-11", "zone-d2", []topology.Score{-1, -1, -1}},
			{"node-12", "zone-e", []topology.Score{topology.NeverSelect, topology.NeverSelect, topology.NeverSelect}},
		},
		act: selectArgs{
			counts: []int{1, 2, 3},
			method: topology.OnePerGroup,
		},
		assert: selectResult{
			expectedResult: [][]string{
				{"node-6"},
				{"node-4", "node-1"},
				{"node-9", "node-10", "node-11"},
			},
		},
	},
	{
		name: "OnePerGroup_positive_blank_scores_and_zero_counts",
		arrange: []setSlotArgs{
			{"node-0", "zone-a", []topology.Score{1}},
			{"node-1", "zone-a", []topology.Score{0, 1, 0}},
			{"node-2", "zone-a", []topology.Score{0, 0, 1}},
			{"node-3", "zone-b", []topology.Score{2}},
			{"node-4", "zone-b", []topology.Score{0, 2}},
			{"node-5", "zone-b", []topology.Score{0, 0, 2}},
			{"node-6", "zone-c", []topology.Score{3}},
			{"node-7", "zone-c", []topology.Score{0, 3}},
			{"node-8", "zone-c", []topology.Score{0, 0, 3}},
			{"node-9", "zone-d0", []topology.Score{-1, -1, -1}},
			{"node-10", "zone-d1", []topology.Score{-1, -1, -1}},
			{"node-11", "zone-d2", []topology.Score{-1, -1, -1}},
			{"node-12", "zone-e", []topology.Score{topology.NeverSelect, topology.NeverSelect, topology.NeverSelect}},
		},
		act: selectArgs{
			counts: []int{1, 2, 3, 0, 0},
			method: topology.OnePerGroup,
		},
		assert: selectResult{
			expectedResult: [][]string{
				{"node-6"},
				{"node-4", "node-1"},
				{"node-9", "node-10", "node-11"},
				nil,
				nil,
			},
		},
	},
	{
		name: "OnePerGroup_negative_because_NeverSelect",
		arrange: []setSlotArgs{
			{"node-0", "zone-a", []topology.Score{1, 0, 0}},
			{"node-1", "zone-a", []topology.Score{0, 1, 0}},
			{"node-2", "zone-a", []topology.Score{0, 0, 1}},
			{"node-3", "zone-b", []topology.Score{2, 0, 0}},
			{"node-4", "zone-b", []topology.Score{0, 2, 0}},
			{"node-5", "zone-b", []topology.Score{0, 0, 2}},
			{"node-6", "zone-c", []topology.Score{3, 0, 0}},
			{"node-7", "zone-c", []topology.Score{0, 3, 0}},
			{"node-8", "zone-c", []topology.Score{0, 0, 3}},
			{"node-9", "zone-d0", []topology.Score{-1, -1, -1}},
			{"node-10", "zone-d1", []topology.Score{-1, -1, -1}},
			{"node-11", "zone-d2", []topology.Score{-1, -1, -1}},
			{"node-12", "zone-e", []topology.Score{topology.NeverSelect, topology.NeverSelect, topology.NeverSelect}},
		},
		act: selectArgs{
			counts: []int{1, 2, 4},
			method: topology.OnePerGroup,
		},
		assert: selectResult{
			expectedErr: topology.ErrNotEnoughSlots,
		},
	},
	{
		name: "OnePerGroup_negative_because_AlwaysSelect_same_group",
		arrange: []setSlotArgs{
			{"node-0", "zone-a", []topology.Score{0}},
			{"node-1", "zone-a", []topology.Score{0}},
			{"node-2", "zone-a", []topology.Score{0}},
			{"node-3", "zone-b", []topology.Score{topology.AlwaysSelect}},
			{"node-4", "zone-b", []topology.Score{topology.AlwaysSelect}},
			{"node-5", "zone-b", []topology.Score{0}},
		},
		act: selectArgs{
			counts: []int{2},
			method: topology.OnePerGroup,
		},
		assert: selectResult{
			expectedErr: topology.ErrCannotSelectRequiredSlot,
		},
	},
	{
		name: "OnePerGroup_negative_because_AlwaysSelect_different_group",
		arrange: []setSlotArgs{
			{"node-0", "zone-a", []topology.Score{topology.AlwaysSelect}},
			{"node-1", "zone-a", []topology.Score{0}},
			{"node-2", "zone-a", []topology.Score{0}},
			{"node-3", "zone-b", []topology.Score{0}},
			{"node-4", "zone-b", []topology.Score{0}},
			{"node-5", "zone-b", []topology.Score{topology.AlwaysSelect}},
		},
		act: selectArgs{
			counts: []int{1},
			method: topology.OnePerGroup,
		},
		assert: selectResult{
			expectedErr: topology.ErrCannotSelectRequiredSlot,
		},
	},
	{
		name: "OnePerGroup_negative_because_AlwaysSelect_count_zero",
		arrange: []setSlotArgs{
			{"node-0", "zone-a", []topology.Score{topology.AlwaysSelect}},
			{"node-1", "zone-a", []topology.Score{0}},
			{"node-2", "zone-a", []topology.Score{0}},
			{"node-3", "zone-b", []topology.Score{0}},
			{"node-4", "zone-b", []topology.Score{0}},
			{"node-5", "zone-b", []topology.Score{0}},
		},
		act: selectArgs{
			counts: []int{0},
			method: topology.OnePerGroup,
		},
		assert: selectResult{
			expectedErr: topology.ErrCannotSelectRequiredSlot,
		},
	},
	{
		name: "SingleGroup_positive",
		arrange: []setSlotArgs{
			{"node-0", "zone-a", []topology.Score{1, 0, 0}},
			{"node-1", "zone-a", []topology.Score{0, 3, 0}},
			{"node-2", "zone-a", []topology.Score{0, 0, 1}},
			{"node-3", "zone-b", []topology.Score{2, 0, 0}},
			{"node-4", "zone-b", []topology.Score{0, 2, 0}},
			{"node-5", "zone-b", []topology.Score{0, 0, 2}},
			{"node-6", "zone-c", []topology.Score{3, 0, 0}},
			{"node-7", "zone-c", []topology.Score{0, 1, 0}},
			{"node-8", "zone-c", []topology.Score{0, 0, 3}},
			{"node-9", "zone-c", []topology.Score{0, topology.NeverSelect, 0}},
			{"node-10", "zone-c", []topology.Score{0, topology.NeverSelect, 0}},
			{"node-11", "zone-c", []topology.Score{0, topology.NeverSelect, 0}},
			{"node-9", "zone-d0", []topology.Score{-1, -1, -1}},
			{"node-10", "zone-d1", []topology.Score{-1, -1, -1}},
			{"node-11", "zone-d2", []topology.Score{-1, -1, -1}},
			{"node-12", "zone-e", []topology.Score{topology.NeverSelect, topology.NeverSelect, topology.NeverSelect}},
		},
		act: selectArgs{
			counts: []int{1, 2, 3},
			method: topology.SingleGroup,
		},
		assert: selectResult{
			expectedResult: [][]string{
				{"node-6"},
				{"node-7", "node-8"},
				{"node-9", "node-10", "node-11"},
			},
		},
	},
}

func TestPacker(t *testing.T) {
	for _, tc := range testCases {
		t.Run(
			tc.name,
			func(t *testing.T) {
				p := &topology.Packer{}

				for _, a := range tc.arrange {
					p.SetSlot(a.id, a.group, a.scores)
				}

				res, err := p.Select(tc.act.counts, tc.act.method)

				if err != tc.assert.expectedErr {
					t.Errorf("expected error '%v', got '%v'", tc.assert.expectedErr, err)
				}

				if diff := cmp.Diff(tc.assert.expectedResult, res); diff != "" {
					t.Errorf("mismatch (-want +got):\n%s", diff)
				}
			},
		)
	}
}
