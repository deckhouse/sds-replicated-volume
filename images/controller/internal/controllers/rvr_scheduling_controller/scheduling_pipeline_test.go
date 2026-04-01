/*
Copyright 2026 Flant JSC

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

package rvrschedulingcontroller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	schext "github.com/deckhouse/sds-replicated-volume/images/controller/internal/scheduler_extender"
)

func TestSchedulingPipeline(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "SchedulingPipeline Suite")
}

// ──────────────────────────────────────────────────────────────────────────────
// Test helpers
//

func makeNode(name, zone string, lvgs ...v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup) v1alpha1.ReplicatedStoragePoolEligibleNode {
	return v1alpha1.ReplicatedStoragePoolEligibleNode{
		NodeName:        name,
		ZoneName:        zone,
		LVMVolumeGroups: lvgs,
		NodeReady:       true,
		AgentReady:      true,
	}
}

func makeLVG(name string, ready bool) v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup {
	return v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
		Name:  name,
		Ready: ready,
	}
}

func makeThinLVG(name, thinPool string, ready bool) v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup {
	return v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
		Name:         name,
		ThinPoolName: thinPool,
		Ready:        ready,
	}
}

func collect(p SchedulingPipeline) []*CandidateEntry {
	var result []*CandidateEntry
	for _, e := range p.Seq() {
		result = append(result, e)
	}
	return result
}

type mockExtenderClient struct {
	scores []schext.ScoredLVMVolumeGroup
	err    error
	called bool
}

func (m *mockExtenderClient) FilterAndScore(_ context.Context, _ string, _ time.Duration, _ int64, _ []schext.LVMVolumeGroup) ([]schext.ScoredLVMVolumeGroup, error) {
	m.called = true
	return m.scores, m.err
}

func (m *mockExtenderClient) NarrowReservation(_ context.Context, _ string, _ time.Duration, _ schext.LVMVolumeGroup) error {
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// CandidateSource
//

var _ = Describe("CandidateSource", func() {
	logger := logr.Discard()

	Describe("TakeLVGsFromEligibleNodes", func() {
		It("yields one entry per LVG", func() {
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-a", "zone-a", makeLVG("vg-a1", true), makeLVG("vg-a2", true)),
				makeNode("node-b", "zone-b", makeLVG("vg-b1", true)),
			}
			p := TakeLVGsFromEligibleNodes(nodes, logger)
			entries := collect(p)

			Expect(entries).To(HaveLen(3))
			Expect(entries[0].Node.NodeName).To(Equal("node-a"))
			Expect(entries[0].LVG.Name).To(Equal("vg-a1"))
			Expect(entries[1].LVG.Name).To(Equal("vg-a2"))
			Expect(entries[2].Node.NodeName).To(Equal("node-b"))
			Expect(entries[2].LVG.Name).To(Equal("vg-b1"))
		})

		It("sets ThinPool when ThinPoolName is non-empty", func() {
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-a", "zone-a", makeThinLVG("vg-a", "thin-a", true)),
			}
			entries := collect(TakeLVGsFromEligibleNodes(nodes, logger))

			Expect(entries).To(HaveLen(1))
			Expect(entries[0].ThinPool).NotTo(BeNil())
			Expect(*entries[0].ThinPool).To(Equal("thin-a"))
		})

		It("leaves ThinPool nil for thick LVG", func() {
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
			}
			entries := collect(TakeLVGsFromEligibleNodes(nodes, logger))

			Expect(entries).To(HaveLen(1))
			Expect(entries[0].ThinPool).To(BeNil())
		})

		It("skips nodes with 0 LVGs", func() {
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-a", "zone-a"),
				makeNode("node-b", "zone-b", makeLVG("vg-b", true)),
			}
			entries := collect(TakeLVGsFromEligibleNodes(nodes, logger))

			Expect(entries).To(HaveLen(1))
			Expect(entries[0].Node.NodeName).To(Equal("node-b"))
		})

		It("returns 0 entries for empty nodes slice", func() {
			entries := collect(TakeLVGsFromEligibleNodes(nil, logger))
			Expect(entries).To(BeEmpty())
		})

		It("produces correct Summary", func() {
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-a", "zone-a", makeLVG("vg-a1", true), makeLVG("vg-a2", true)),
			}
			p := TakeLVGsFromEligibleNodes(nodes, logger)
			_ = collect(p) // must consume Seq before Summary
			Expect(p.Summary()).To(Equal("2 candidates (node×LVG) from 1 eligible nodes"))
		})
	})

	Describe("TakeOnlyNodesFromEligibleNodes", func() {
		It("yields one entry per node with LVG=nil", func() {
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-a", "zone-a", makeLVG("vg-a1", true)),
				makeNode("node-b", "zone-b"),
				makeNode("node-c", "zone-c"),
			}
			p := TakeOnlyNodesFromEligibleNodes(nodes, logger)
			entries := collect(p)

			Expect(entries).To(HaveLen(3))
			for _, e := range entries {
				Expect(e.LVG).To(BeNil())
				Expect(e.ThinPool).To(BeNil())
			}
			Expect(entries[0].Node.NodeName).To(Equal("node-a"))
			Expect(entries[1].Node.NodeName).To(Equal("node-b"))
			Expect(entries[2].Node.NodeName).To(Equal("node-c"))
		})

		It("returns 0 entries for empty nodes slice", func() {
			entries := collect(TakeOnlyNodesFromEligibleNodes(nil, logger))
			Expect(entries).To(BeEmpty())
		})

		It("produces correct Summary", func() {
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-a", "zone-a"),
				makeNode("node-b", "zone-b"),
				makeNode("node-c", "zone-c"),
			}
			p := TakeOnlyNodesFromEligibleNodes(nodes, logger)
			_ = collect(p)
			Expect(p.Summary()).To(Equal("3 candidates from 3 eligible nodes"))
		})
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// WithPredicate
//

var _ = Describe("WithPredicate", func() {
	logger := logr.Discard()

	nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
		makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
		makeNode("node-b", "zone-b", makeLVG("vg-b", true)),
		makeNode("node-c", "zone-c", makeLVG("vg-c", true)),
	}

	It("passes all entries when predicate always returns entry", func() {
		source := TakeLVGsFromEligibleNodes(nodes, logger)
		p := WithPredicate(source, "pass-all", logger, func(e *CandidateEntry) *CandidateEntry {
			return e
		})
		entries := collect(p)
		Expect(entries).To(HaveLen(3))
		Expect(p.Summary()).NotTo(ContainSubstring("excluded"))
	})

	It("excludes all entries when predicate always returns nil", func() {
		source := TakeLVGsFromEligibleNodes(nodes, logger)
		p := WithPredicate(source, "exclude-all", logger, func(_ *CandidateEntry) *CandidateEntry {
			return nil
		})
		entries := collect(p)
		Expect(entries).To(BeEmpty())
		Expect(p.Summary()).To(ContainSubstring("3 excluded: exclude-all"))
	})

	It("excludes some entries", func() {
		source := TakeLVGsFromEligibleNodes(nodes, logger)
		p := WithPredicate(source, "skip-node-b", logger, func(e *CandidateEntry) *CandidateEntry {
			if e.Node.NodeName == "node-b" {
				return nil
			}
			return e
		})
		entries := collect(p)
		Expect(entries).To(HaveLen(2))
		Expect(entries[0].Node.NodeName).To(Equal("node-a"))
		Expect(entries[1].Node.NodeName).To(Equal("node-c"))
		Expect(p.Summary()).To(ContainSubstring("1 excluded: skip-node-b"))
	})

	It("chains summary from two predicates", func() {
		source := TakeLVGsFromEligibleNodes(nodes, logger)
		p1 := WithPredicate(source, "filter-A", logger, func(e *CandidateEntry) *CandidateEntry {
			if e.Node.NodeName == "node-a" {
				return nil
			}
			return e
		})
		p2 := WithPredicate(p1, "filter-B", logger, func(e *CandidateEntry) *CandidateEntry {
			if e.Node.NodeName == "node-b" {
				return nil
			}
			return e
		})
		entries := collect(p2)
		Expect(entries).To(HaveLen(1))
		Expect(entries[0].Node.NodeName).To(Equal("node-c"))

		summary := p2.Summary()
		Expect(summary).To(ContainSubstring("1 excluded: filter-A"))
		Expect(summary).To(ContainSubstring("1 excluded: filter-B"))
	})

	It("allows predicate to adjust Score", func() {
		source := TakeLVGsFromEligibleNodes(nodes, logger)
		p := WithPredicate(source, "add-score", logger, func(e *CandidateEntry) *CandidateEntry {
			if e.Node.NodeName == "node-a" {
				e.Score += 100
			}
			return e
		})
		entries := collect(p)
		Expect(entries).To(HaveLen(3))
		Expect(entries[0].Score).To(Equal(100))
		Expect(entries[1].Score).To(Equal(0))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// FilteredAndScoredBySchedulerExtender
//

var _ = Describe("FilteredAndScoredBySchedulerExtender", func() {
	logger := logr.Discard()
	ctx := context.Background()

	nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
		makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
		makeNode("node-b", "zone-b", makeLVG("vg-b", true)),
		makeNode("node-c", "zone-c", makeLVG("vg-c", true)),
	}

	It("assigns scores from extender", func() {
		mock := &mockExtenderClient{
			scores: []schext.ScoredLVMVolumeGroup{
				{LVMVolumeGroup: schext.LVMVolumeGroup{LVGName: "vg-a"}, Score: 10},
				{LVMVolumeGroup: schext.LVMVolumeGroup{LVGName: "vg-b"}, Score: 5},
				{LVMVolumeGroup: schext.LVMVolumeGroup{LVGName: "vg-c"}, Score: 8},
			},
		}
		source := TakeLVGsFromEligibleNodes(nodes, logger)
		p, err := FilteredAndScoredBySchedulerExtender(ctx, source, mock, "res-1", time.Second, 1024, logger)

		Expect(err).NotTo(HaveOccurred())
		entries := collect(p)
		Expect(entries).To(HaveLen(3))

		scores := map[string]int{}
		for _, e := range entries {
			scores[e.LVGName()] = e.Score
		}
		Expect(scores["vg-a"]).To(Equal(10))
		Expect(scores["vg-b"]).To(Equal(5))
		Expect(scores["vg-c"]).To(Equal(8))
	})

	It("excludes entries with score=0", func() {
		mock := &mockExtenderClient{
			scores: []schext.ScoredLVMVolumeGroup{
				{LVMVolumeGroup: schext.LVMVolumeGroup{LVGName: "vg-a"}, Score: 10},
				{LVMVolumeGroup: schext.LVMVolumeGroup{LVGName: "vg-b"}, Score: 0},
				{LVMVolumeGroup: schext.LVMVolumeGroup{LVGName: "vg-c"}, Score: 8},
			},
		}
		source := TakeLVGsFromEligibleNodes(nodes, logger)
		p, err := FilteredAndScoredBySchedulerExtender(ctx, source, mock, "res-1", time.Second, 1024, logger)

		Expect(err).NotTo(HaveOccurred())
		entries := collect(p)
		Expect(entries).To(HaveLen(2))
		Expect(p.Summary()).To(ContainSubstring("1 excluded: scoring (no capacity)"))
	})

	It("excludes entries with no matching score from extender", func() {
		mock := &mockExtenderClient{
			scores: []schext.ScoredLVMVolumeGroup{
				{LVMVolumeGroup: schext.LVMVolumeGroup{LVGName: "vg-a"}, Score: 10},
				// vg-b and vg-c not returned by extender
			},
		}
		source := TakeLVGsFromEligibleNodes(nodes, logger)
		p, err := FilteredAndScoredBySchedulerExtender(ctx, source, mock, "res-1", time.Second, 1024, logger)

		Expect(err).NotTo(HaveOccurred())
		entries := collect(p)
		Expect(entries).To(HaveLen(1))
		Expect(entries[0].LVGName()).To(Equal("vg-a"))
		Expect(p.Summary()).To(ContainSubstring("2 excluded: scoring (no capacity)"))
	})

	It("silently ignores extra scores not in pipeline", func() {
		mock := &mockExtenderClient{
			scores: []schext.ScoredLVMVolumeGroup{
				{LVMVolumeGroup: schext.LVMVolumeGroup{LVGName: "vg-a"}, Score: 10},
				{LVMVolumeGroup: schext.LVMVolumeGroup{LVGName: "vg-b"}, Score: 5},
				{LVMVolumeGroup: schext.LVMVolumeGroup{LVGName: "vg-c"}, Score: 8},
				{LVMVolumeGroup: schext.LVMVolumeGroup{LVGName: "vg-unknown"}, Score: 99},
			},
		}
		source := TakeLVGsFromEligibleNodes(nodes, logger)
		p, err := FilteredAndScoredBySchedulerExtender(ctx, source, mock, "res-1", time.Second, 1024, logger)

		Expect(err).NotTo(HaveOccurred())
		entries := collect(p)
		Expect(entries).To(HaveLen(3))
	})

	It("returns error when extender fails", func() {
		mock := &mockExtenderClient{err: errors.New("connection refused")}
		source := TakeLVGsFromEligibleNodes(nodes, logger)
		p, err := FilteredAndScoredBySchedulerExtender(ctx, source, mock, "res-1", time.Second, 1024, logger)

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("connection refused"))
		Expect(p).To(BeNil())
	})

	It("excludes all when upstream has only node-only entries (no LVGs)", func() {
		nodeOnlyNodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			makeNode("node-a", "zone-a"),
			makeNode("node-b", "zone-b"),
		}
		mock := &mockExtenderClient{}
		source := TakeOnlyNodesFromEligibleNodes(nodeOnlyNodes, logger)
		p, err := FilteredAndScoredBySchedulerExtender(ctx, source, mock, "res-1", time.Second, 1024, logger)

		Expect(err).NotTo(HaveOccurred())
		Expect(mock.called).To(BeFalse())
		entries := collect(p)
		Expect(entries).To(BeEmpty())
		Expect(p.Summary()).To(ContainSubstring("2 excluded: scoring (no capacity)"))
	})

	It("handles empty upstream", func() {
		mock := &mockExtenderClient{}
		source := TakeLVGsFromEligibleNodes(nil, logger)
		p, err := FilteredAndScoredBySchedulerExtender(ctx, source, mock, "res-1", time.Second, 1024, logger)

		Expect(err).NotTo(HaveOccurred())
		Expect(mock.called).To(BeFalse())
		entries := collect(p)
		Expect(entries).To(BeEmpty())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// WithGroupScore / WithZoneScore / WithNodeScore
//

var _ = Describe("GroupScoring", func() {
	logger := logr.Discard()

	nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
		makeNode("node-a1", "zone-a", makeLVG("vg-a1", true)),
		makeNode("node-a2", "zone-a", makeLVG("vg-a2", true)),
		makeNode("node-b1", "zone-b", makeLVG("vg-b1", true)),
	}

	Describe("WithGroupScore", func() {
		It("adds score to all entries in a group", func() {
			source := TakeLVGsFromEligibleNodes(nodes, logger)
			p := WithGroupScore(source, "test", logger,
				func(e *CandidateEntry) string { return e.Node.ZoneName },
				func(key string, _ []*CandidateEntry) int {
					if key == "zone-a" {
						return 10
					}
					return 0
				},
			)
			entries := collect(p)

			Expect(entries).To(HaveLen(3))
			Expect(entries[0].Score).To(Equal(10)) // zone-a
			Expect(entries[1].Score).To(Equal(10)) // zone-a
			Expect(entries[2].Score).To(Equal(0))  // zone-b
		})

		It("does not change entries when score is 0", func() {
			source := TakeLVGsFromEligibleNodes(nodes, logger)
			p := WithGroupScore(source, "noop", logger,
				func(e *CandidateEntry) string { return e.Node.ZoneName },
				func(_ string, _ []*CandidateEntry) int { return 0 },
			)
			entries := collect(p)
			for _, e := range entries {
				Expect(e.Score).To(Equal(0))
			}
		})

		It("handles multiple groups with different scores", func() {
			source := TakeLVGsFromEligibleNodes(nodes, logger)
			p := WithGroupScore(source, "multi", logger,
				func(e *CandidateEntry) string { return e.Node.ZoneName },
				func(key string, _ []*CandidateEntry) int {
					switch key {
					case "zone-a":
						return 5
					case "zone-b":
						return 10
					}
					return 0
				},
			)
			entries := collect(p)
			Expect(entries[0].Score).To(Equal(5))  // zone-a
			Expect(entries[1].Score).To(Equal(5))  // zone-a
			Expect(entries[2].Score).To(Equal(10)) // zone-b
		})

		It("handles empty upstream", func() {
			source := TakeLVGsFromEligibleNodes(nil, logger)
			p := WithGroupScore(source, "empty", logger,
				func(_ *CandidateEntry) string { return "x" },
				func(_ string, _ []*CandidateEntry) int { return 99 },
			)
			entries := collect(p)
			Expect(entries).To(BeEmpty())
		})

		It("preserves entry order", func() {
			source := TakeLVGsFromEligibleNodes(nodes, logger)
			p := WithGroupScore(source, "order", logger,
				func(e *CandidateEntry) string { return e.Node.ZoneName },
				func(_ string, _ []*CandidateEntry) int { return 1 },
			)
			entries := collect(p)
			Expect(entries[0].Node.NodeName).To(Equal("node-a1"))
			Expect(entries[1].Node.NodeName).To(Equal("node-a2"))
			Expect(entries[2].Node.NodeName).To(Equal("node-b1"))
		})

		It("passes through upstream Summary", func() {
			source := TakeLVGsFromEligibleNodes(nodes, logger)
			p := WithGroupScore(source, "passthrough", logger,
				func(_ *CandidateEntry) string { return "x" },
				func(_ string, _ []*CandidateEntry) int { return 0 },
			)
			_ = collect(p) // consume
			Expect(p.Summary()).To(ContainSubstring("candidates (node×LVG)"))
		})
	})

	Describe("WithZoneScore", func() {
		It("groups by ZoneName", func() {
			source := TakeLVGsFromEligibleNodes(nodes, logger)
			called := map[string]int{}
			p := WithZoneScore(source, "zone-test", logger,
				func(key string, group []*CandidateEntry) int {
					called[key] = len(group)
					return 0
				},
			)
			_ = collect(p)
			Expect(called).To(HaveKeyWithValue("zone-a", 2))
			Expect(called).To(HaveKeyWithValue("zone-b", 1))
		})
	})

	Describe("WithNodeScore", func() {
		It("groups by NodeName", func() {
			twoLVGNodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-a", "zone-a", makeLVG("vg-a1", true), makeLVG("vg-a2", true)),
				makeNode("node-b", "zone-b", makeLVG("vg-b1", true)),
			}
			source := TakeLVGsFromEligibleNodes(twoLVGNodes, logger)
			called := map[string]int{}
			p := WithNodeScore(source, "node-test", logger,
				func(key string, group []*CandidateEntry) int {
					called[key] = len(group)
					return 0
				},
			)
			_ = collect(p)
			Expect(called).To(HaveKeyWithValue("node-a", 2))
			Expect(called).To(HaveKeyWithValue("node-b", 1))
		})
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// SelectBest
//

var _ = Describe("SelectBest", func() {
	logger := logr.Discard()

	isBetter := func(a, b *CandidateEntry) bool {
		return a.Score > b.Score
	}

	It("returns nil for empty pipeline", func() {
		source := TakeLVGsFromEligibleNodes(nil, logger)
		best, _ := SelectBest(source, isBetter)
		Expect(best).To(BeNil())
	})

	It("returns the single entry", func() {
		nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
		}
		source := TakeLVGsFromEligibleNodes(nodes, logger)
		best, _ := SelectBest(source, isBetter)
		Expect(best).NotTo(BeNil())
		Expect(best.Node.NodeName).To(Equal("node-a"))
	})

	It("selects the highest score", func() {
		nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
			makeNode("node-b", "zone-b", makeLVG("vg-b", true)),
			makeNode("node-c", "zone-c", makeLVG("vg-c", true)),
		}
		source := TakeLVGsFromEligibleNodes(nodes, logger)
		// Assign different scores via predicate
		p := WithPredicate(source, "score", logger, func(e *CandidateEntry) *CandidateEntry {
			switch e.Node.NodeName {
			case "node-a":
				e.Score = 5
			case "node-b":
				e.Score = 10
			case "node-c":
				e.Score = 3
			}
			return e
		})
		best, _ := SelectBest(p, isBetter)
		Expect(best).NotTo(BeNil())
		Expect(best.Node.NodeName).To(Equal("node-b"))
		Expect(best.Score).To(Equal(10))
	})

	It("uses comparator for tiebreak", func() {
		nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			makeNode("node-b", "zone-a", makeLVG("vg-b", true)),
			makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
		}
		source := TakeLVGsFromEligibleNodes(nodes, logger)
		// Tiebreak: prefer alphabetically smaller NodeName
		best, _ := SelectBest(source, func(a, b *CandidateEntry) bool {
			if a.Score != b.Score {
				return a.Score > b.Score
			}
			return a.Node.NodeName < b.Node.NodeName
		})
		Expect(best).NotTo(BeNil())
		Expect(best.Node.NodeName).To(Equal("node-a"))
	})

	It("returns summary from pipeline", func() {
		nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
		}
		source := TakeLVGsFromEligibleNodes(nodes, logger)
		_, summary := SelectBest(source, isBetter)
		Expect(summary).To(ContainSubstring("1 candidates (node×LVG) from 1 eligible nodes"))
	})
})
