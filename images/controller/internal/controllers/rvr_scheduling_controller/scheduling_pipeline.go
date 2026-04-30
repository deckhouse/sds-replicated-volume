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

package rvrschedulingcontroller

import (
	"cmp"
	"context"
	"fmt"
	"iter"
	"slices"
	"time"

	"github.com/go-logr/logr"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	schext "github.com/deckhouse/sds-replicated-volume/images/controller/internal/scheduler_extender"
)

// ──────────────────────────────────────────────────────────────────────────────
// Core types
//

// CandidateEntry is the fundamental scheduling tuple: (node, LVG, thinpool, score).
// Every pipeline stage operates on these entries.
type CandidateEntry struct {
	// Node is always non-nil.
	Node *v1alpha1.ReplicatedStoragePoolEligibleNode
	// LVG is nil for TieBreaker (node-only) sources.
	LVG *v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup
	// ThinPool is nil for thick LVM or when LVG is nil.
	ThinPool *string
	// Score is the cumulative scheduling score; predicates may adjust it.
	Score int
}

// LVGName returns the LVG name or "" when LVG is nil.
func (e *CandidateEntry) LVGName() string {
	if e.LVG == nil {
		return ""
	}
	return e.LVG.Name
}

// ThinPoolName returns the thin-pool name or "" when ThinPool is nil.
func (e *CandidateEntry) ThinPoolName() string {
	if e.ThinPool == nil {
		return ""
	}
	return *e.ThinPool
}

// CandidateSeq is an iterator over scheduling candidates.
type CandidateSeq = iter.Seq2[int, *CandidateEntry]

// CandidateFilterFn inspects/mutates a candidate and returns it (possibly with
// adjusted Score) or nil to exclude it from the pipeline.
type CandidateFilterFn func(entry *CandidateEntry) *CandidateEntry

// CandidateCompareFn returns true if a is a better candidate than b.
type CandidateCompareFn func(a, b *CandidateEntry) bool

// SchedulingPipeline is a pipeline component that yields candidates and can report
// a diagnostic summary of what it filtered. All pipeline components implement
// this interface so that Summary() can chain recursively through the pipeline.
type SchedulingPipeline interface {
	Seq() CandidateSeq
	// Summary returns a human-readable chain of exclusion messages from this
	// step and all upstream steps.
	// Example: "12 candidates from 4 nodes; 3 excluded: node not ready; 2 excluded: node occupied"
	Summary() string
}

// ──────────────────────────────────────────────────────────────────────────────
// candidateSource
//

// TakeLVGsFromEligibleNodes yields one CandidateEntry per LVG on each eligible node
// (for Diskful scheduling). ThinPool is set when LVG.ThinPoolName is non-empty.
func TakeLVGsFromEligibleNodes(nodes []v1alpha1.ReplicatedStoragePoolEligibleNode, logger logr.Logger) SchedulingPipeline {
	return &candidateSource{nodes: nodes, nodeOnly: false, logger: logger}
}

// TakeOnlyNodesFromEligibleNodes yields one CandidateEntry per node with LVG=nil
// and ThinPool=nil (for TieBreaker scheduling — no LVG needed).
func TakeOnlyNodesFromEligibleNodes(nodes []v1alpha1.ReplicatedStoragePoolEligibleNode, logger logr.Logger) SchedulingPipeline {
	return &candidateSource{nodes: nodes, nodeOnly: true, logger: logger}
}

// candidateSource produces the initial candidate sequence from eligible nodes.
type candidateSource struct {
	nodes    []v1alpha1.ReplicatedStoragePoolEligibleNode
	nodeOnly bool // true = one entry per node (TieBreaker), false = one entry per LVG (Diskful)
	total    int  // populated after Seq() iteration
	logger   logr.Logger
}

// Seq returns an iterator over candidate entries.
func (s *candidateSource) Seq() CandidateSeq {
	return func(yield func(int, *CandidateEntry) bool) {
		idx := 0
		for i := range s.nodes {
			node := &s.nodes[i]
			if s.nodeOnly {
				if !yield(idx, &CandidateEntry{Node: node}) {
					s.total = idx + 1
					return
				}
				idx++
			} else {
				for j := range node.LVMVolumeGroups {
					lvg := &node.LVMVolumeGroups[j]
					entry := &CandidateEntry{
						Node: node,
						LVG:  lvg,
					}
					if lvg.ThinPoolName != "" {
						entry.ThinPool = &lvg.ThinPoolName
					}
					if !yield(idx, entry) {
						s.total = idx + 1
						return
					}
					idx++
				}
			}
		}
		s.total = idx
	}
}

// Summary returns a description of how many candidates the source produced.
func (s *candidateSource) Summary() string {
	if s.nodeOnly {
		return fmt.Sprintf("%d candidates from %d eligible nodes", s.total, len(s.nodes))
	}
	return fmt.Sprintf("%d candidates (node×LVG) from %d eligible nodes", s.total, len(s.nodes))
}

// ──────────────────────────────────────────────────────────────────────────────
// schedulingPredicate
//

// schedulingPredicate wraps an upstream SchedulingPipeline and applies a
// CandidateFilterFn to each element. It counts exclusions, logs each one at
// V(3), and builds a diagnostic summary message that chains with upstream.
//
// Use this type for both stateless filters (fn is a pure function) and stateful
// filters (fn closes over mutable state).
type schedulingPredicate struct {
	upstream SchedulingPipeline
	name     string // human-readable predicate name, e.g. "node not ready"
	fn       CandidateFilterFn
	excluded int
	logger   logr.Logger
}

// WithPredicate creates a new predicate step in the pipeline.
// name is a short human-readable description used in diagnostic messages and
// V(3) exclusion logs (e.g. "node not ready", "node occupied").
func WithPredicate(upstream SchedulingPipeline, name string, logger logr.Logger, fn CandidateFilterFn) SchedulingPipeline {
	return &schedulingPredicate{
		upstream: upstream,
		fn:       fn,
		name:     name,
		logger:   logger,
	}
}

// Seq returns an iterator that applies the filter function to each upstream
// entry, counting and logging exclusions.
func (p *schedulingPredicate) Seq() CandidateSeq {
	return func(yield func(int, *CandidateEntry) bool) {
		idx := 0
		for _, entry := range p.upstream.Seq() {
			result := p.fn(entry)
			if result == nil {
				p.excluded++
				p.logger.V(3).Info("candidate excluded",
					"predicate", p.name,
					"node", entry.Node.NodeName,
					"lvg", entry.LVGName(),
					"thinPool", entry.ThinPoolName(),
				)
				continue
			}
			if !yield(idx, result) {
				return
			}
			idx++
		}
	}
}

// Summary returns a chain of exclusion messages from upstream and this step.
func (p *schedulingPredicate) Summary() string {
	upstream := p.upstream.Summary()
	if p.excluded == 0 {
		return upstream
	}
	own := fmt.Sprintf("%d excluded: %s", p.excluded, p.name)
	if upstream == "" {
		return own
	}
	return upstream + "; " + own
}

// ──────────────────────────────────────────────────────────────────────────────
// ScoringStep
//

// ScoringStep is a SchedulingPipeline that eagerly collects upstream entries,
// queries the scheduler-extender for LVG capacity scores, and merge-joins
// the results. Entries without a matching score (or score <= 0) are excluded.
//
// All heavy work (upstream collection, extender call, sort, merge) happens
// in the constructor (FilteredAndScoredBySchedulerExtender). Seq() simply iterates over the
// pre-computed result.
type ScoringStep struct {
	upstream SchedulingPipeline
	entries  []*CandidateEntry
	excluded int
	logger   logr.Logger
}

// FilteredAndScoredBySchedulerExtender creates a ScoringStep by eagerly:
//  1. Collecting all upstream entries.
//  2. Deduplicating LVGs and building a query.
//  3. Calling the extender (FilterAndScore).
//  4. Sorting both slices by LVG name and two-pointer merge-joining to assign
//     scores.
//
// Entries with no matching score or score <= 0 are excluded and logged at V(3).
// Returns an error if the extender call fails.
func FilteredAndScoredBySchedulerExtender(
	ctx context.Context,
	upstream SchedulingPipeline,
	client schext.Client,
	reservationID string,
	reservationTTL time.Duration,
	size int64,
	logger logr.Logger,
) (SchedulingPipeline, error) {
	// TODO Прочитать, что тут написано!!!!!

	// 1. Collect all upstream entries and build the LVG query list.
	// Candidates are already unique (LVG, thinpool) tuples, so no dedup needed.
	var entries []*CandidateEntry
	var queries []schext.LVMVolumeGroup
	for _, entry := range upstream.Seq() {
		entries = append(entries, entry)
		if entry.LVG == nil {
			continue
		}
		queries = append(queries, schext.LVMVolumeGroup{
			LVGName:      entry.LVGName(),
			ThinPoolName: entry.ThinPoolName(),
		})
	}

	// 2. If no LVG queries were collected (e.g. all entries are node-only),
	// return an empty step that reports all entries as excluded — there is
	// nothing to score.
	if len(queries) == 0 {
		return &ScoringStep{
			upstream: upstream,
			excluded: len(entries),
			logger:   logger,
		}, nil
	}

	// 3. Call the extender.
	scored, err := client.FilterAndScore(ctx, reservationID, reservationTTL, size, queries)
	if err != nil {
		return nil, err
	}

	// 4. Sort scored LVGs by (Name, ThinPoolName).
	slices.SortFunc(scored, func(a, b schext.ScoredLVMVolumeGroup) int {
		if c := cmp.Compare(a.LVGName, b.LVGName); c != 0 {
			return c
		}
		return cmp.Compare(a.ThinPoolName, b.ThinPoolName)
	})

	// 5. Stable-sort entries by (LVGName, ThinPoolName) (preserves node order within same LVG).
	slices.SortStableFunc(entries, func(a, b *CandidateEntry) int {
		if c := cmp.Compare(a.LVGName(), b.LVGName()); c != 0 {
			return c
		}
		return cmp.Compare(a.ThinPoolName(), b.ThinPoolName())
	})

	// 6. Two-pointer merge join driven by the extender response (scored).
	// Both slices are sorted by (LVGName, ThinPoolName). LVGName is cluster-unique,
	// so each pair maps to exactly one entry and one scored element.
	// Entries with no matching scored element or score <= 0 are excluded.
	compareEntryToLVG := func(e *CandidateEntry, s schext.LVMVolumeGroup) int {
		if c := cmp.Compare(e.LVGName(), s.LVGName); c != 0 {
			return c
		}
		return cmp.Compare(e.ThinPoolName(), s.ThinPoolName)
	}
	result := make([]*CandidateEntry, 0, len(scored))
	excluded := 0
	i := 0
	for _, s := range scored {
		// Advance i past entries whose key is less than (s.LVGName, s.ThinPoolName).
		for i < len(entries) && compareEntryToLVG(entries[i], s.LVMVolumeGroup) < 0 {
			excluded++
			logger.V(3).Info("candidate excluded",
				"predicate", "scoring",
				"node", entries[i].Node.NodeName,
				"lvg", entries[i].LVGName(),
				"thinPool", entries[i].ThinPoolName(),
				"reason", "no score from extender",
			)
			i++
		}

		// Scored element has no matching entry — skip.
		if i >= len(entries) || compareEntryToLVG(entries[i], s.LVMVolumeGroup) != 0 {
			continue
		}

		// Match found.
		if s.Score > 0 {
			entries[i].Score = s.Score
			result = append(result, entries[i])
		} else {
			excluded++
			logger.V(3).Info("candidate excluded",
				"predicate", "scoring",
				"node", entries[i].Node.NodeName,
				"lvg", s.LVGName,
				"thinPool", s.ThinPoolName,
				"score", s.Score,
			)
		}
		i++
	}
	// Remaining entries past the last scored element are also excluded.
	for i < len(entries) {
		excluded++
		logger.V(3).Info("candidate excluded",
			"predicate", "scoring",
			"node", entries[i].Node.NodeName,
			"lvg", entries[i].LVGName(),
			"thinPool", entries[i].ThinPoolName(),
			"reason", "no score from extender",
		)
		i++
	}

	// TODO: отсортировать в стандартный порядок

	return &ScoringStep{
		upstream: upstream,
		entries:  result,
		excluded: excluded,
		logger:   logger,
	}, nil
}

// Seq returns an iterator over the pre-scored entries.
func (s *ScoringStep) Seq() CandidateSeq {
	return func(yield func(int, *CandidateEntry) bool) {
		for i, e := range s.entries {
			if !yield(i, e) {
				return
			}
		}
	}
}

// Summary returns a chain of exclusion messages from upstream and this step.
func (s *ScoringStep) Summary() string {
	upstream := s.upstream.Summary()
	if s.excluded == 0 {
		return upstream
	}
	own := fmt.Sprintf("%d excluded: scoring (no capacity)", s.excluded)
	if upstream == "" {
		return own
	}
	return upstream + "; " + own
}

// ──────────────────────────────────────────────────────────────────────────────
// GroupScoringStep
//

// GroupScoreFn receives a group key and the candidates in that group,
// and returns a score to add to every entry in the group.
type GroupScoreFn func(key string, group []*CandidateEntry) int

// groupScoringStep eagerly collects upstream entries, groups them by a key,
// scores each group via a callback, and yields all entries with adjusted scores.
// No entries are filtered — only scores are adjusted.
type groupScoringStep struct {
	upstream SchedulingPipeline
	name     string
	entries  []*CandidateEntry
	logger   logr.Logger
}

// WithGroupScore creates a pipeline step that eagerly collects upstream
// entries, groups them by keyFn, calls scoreFn for each group, and adds
// the returned score to every entry in the group. No entries are filtered.
func WithGroupScore(
	upstream SchedulingPipeline,
	name string,
	logger logr.Logger,
	keyFn func(*CandidateEntry) string,
	scoreFn GroupScoreFn,
) SchedulingPipeline {
	// 1. Collect all upstream entries.
	var all []*CandidateEntry
	for _, e := range upstream.Seq() {
		all = append(all, e)
	}

	// 2. Group by key, preserving first-seen order.
	type group struct {
		key     string
		entries []*CandidateEntry
	}
	var groups []group
	idx := make(map[string]int) // key → index in groups
	for _, e := range all {
		k := keyFn(e)
		if i, ok := idx[k]; ok {
			groups[i].entries = append(groups[i].entries, e)
		} else {
			idx[k] = len(groups)
			groups = append(groups, group{key: k, entries: []*CandidateEntry{e}})
		}
	}

	// 3. Score each group.
	for _, g := range groups {
		score := scoreFn(g.key, g.entries)
		if score != 0 {
			for _, e := range g.entries {
				e.Score += score
			}
			logger.V(3).Info("group scored",
				"step", name,
				"key", g.key,
				"score", score,
				"count", len(g.entries),
			)
		}
	}

	return &groupScoringStep{
		upstream: upstream,
		name:     name,
		entries:  all,
		logger:   logger,
	}
}

// WithZoneScore groups candidates by zone name and scores each zone group.
func WithZoneScore(
	upstream SchedulingPipeline,
	name string,
	logger logr.Logger,
	scoreFn GroupScoreFn,
) SchedulingPipeline {
	return WithGroupScore(upstream, name, logger,
		func(e *CandidateEntry) string { return e.Node.ZoneName },
		scoreFn,
	)
}

// WithNodeScore groups candidates by node name and scores each node group.
func WithNodeScore(
	upstream SchedulingPipeline,
	name string,
	logger logr.Logger,
	scoreFn GroupScoreFn,
) SchedulingPipeline {
	return WithGroupScore(upstream, name, logger,
		func(e *CandidateEntry) string { return e.Node.NodeName },
		scoreFn,
	)
}

// Seq returns an iterator over all entries with adjusted scores.
func (s *groupScoringStep) Seq() CandidateSeq {
	return func(yield func(int, *CandidateEntry) bool) {
		for i, e := range s.entries {
			if !yield(i, e) {
				return
			}
		}
	}
}

// Summary passes through the upstream summary (group scoring does not filter).
func (s *groupScoringStep) Summary() string {
	return s.upstream.Summary()
}

// ──────────────────────────────────────────────────────────────────────────────
// Best-candidate selection
//

// SelectBest consumes all entries from the pipeline, picks the single best
// candidate according to isBetter, and returns it (or nil if the pipeline is
// empty). The summary string is taken from the pipeline for diagnostic use.
func SelectBest(p SchedulingPipeline, isBetter CandidateCompareFn) (best *CandidateEntry, summary string) {
	for _, entry := range p.Seq() {
		if best == nil || isBetter(entry, best) {
			best = entry
		}
	}
	return best, p.Summary()
}
