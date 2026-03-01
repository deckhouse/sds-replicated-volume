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

// Package dme provides the datamesh engine SDK for processing transitions
// (membership, attachment, quorum changes) driven by requests and intents.
//
// The engine is pure non-I/O: it mutates rv.Status in place. The caller owns
// DeepCopy for patch base and patchRVStatus after the engine runs.
package dme

import (
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
)

// Engine is the main entry point for the datamesh engine SDK.
// Created per reconciliation with the RV state and pre-built replica contexts.
type Engine struct {
	registry          *Registry
	membershipPlanner MembershipPlanner
	attachmentPlanner AttachmentPlanner
	parallelism       parallelismCache

	globalCtx      GlobalContext
	replicaCtxs    []ReplicaContext
	replicaCtxByID [32]*ReplicaContext
}

// GlobalContext holds datamesh-wide state accessible to all callbacks.
type GlobalContext struct {
	// RV is the ReplicatedVolume being reconciled (mutable).
	RV *v1alpha1.ReplicatedVolume
	// RVRs is the sorted slice of all ReplicatedVolumeReplicas for this RV.
	RVRs []*v1alpha1.ReplicatedVolumeReplica
	// RVAs is the sorted slice of all ReplicatedVolumeAttachments for this RV.
	// Sorted by NodeName (primary), CreationTimestamp (secondary), Name (tie-breaker).
	RVAs []*v1alpha1.ReplicatedVolumeAttachment
	// RSP provides node eligibility lookups. Nil if RSP is unavailable.
	RSP RSP
	// Features describes cluster-level feature availability for path resolution.
	Features FeatureFlags
}

// RSP provides node eligibility lookups for transition guards.
// Implemented by the caller's RSP view; nil means RSP is unavailable.
type RSP interface {
	FindEligibleNode(nodeName string) *v1alpha1.ReplicatedStoragePoolEligibleNode
}

// ReplicaContext holds all per-node data pre-indexed for O(1) access.
// A ReplicaContext exists for every node that has at least one of: RVR, Member, RVA, Request, or Transition.
// Nodes with only RVAs (no member/RVR) have a ReplicaContext with Member=nil and RVR=nil.
type ReplicaContext struct {
	// NodeName is the node this context represents.
	NodeName string
	// RVR is the ReplicatedVolumeReplica on this node. Nil if no RVR exists.
	RVR *v1alpha1.ReplicatedVolumeReplica
	// Member is the datamesh member on this node. Nil if not a member.
	Member *v1alpha1.DatameshMember
	// RVAs is the sub-slice of RVAs for this node (from GlobalContext.RVAs).
	// Sorted by CreationTimestamp, Name (inherits GlobalContext.RVAs order).
	RVAs []*v1alpha1.ReplicatedVolumeAttachment
	// MembershipTransition is the active membership transition for this replica. Nil if none.
	MembershipTransition *v1alpha1.ReplicatedVolumeDatameshTransition
	// AttachmentTransition is the active attachment transition for this replica. Nil if none.
	AttachmentTransition *v1alpha1.ReplicatedVolumeDatameshTransition
	// MembershipRequest is the pending membership request for this replica. Nil if none.
	MembershipRequest *v1alpha1.ReplicatedVolumeDatameshReplicaRequest

	// Output fields, populated by the engine during processing.
	// Membership message: for request.Message (progress/blocked/completed).
	membershipMessage string
	// Attachment condition output: for RVA condition.
	attachmentConditionMessage string
	attachmentConditionReason  string
}

// AttachmentConditionMessage returns the attachment condition message set by the engine.
func (rc *ReplicaContext) AttachmentConditionMessage() string { return rc.attachmentConditionMessage }

// AttachmentConditionReason returns the attachment condition reason set by the engine.
func (rc *ReplicaContext) AttachmentConditionReason() string { return rc.attachmentConditionReason }

// ID returns the replica ID, derived from Member or RVR.
// Returns (id, true) if available, (0, false) if neither Member nor RVR is set.
func (rc *ReplicaContext) ID() (uint8, bool) {
	if rc.Member != nil {
		return rc.Member.ID(), true
	}
	if rc.RVR != nil {
		return rc.RVR.ID(), true
	}
	return 0, false
}

// FeatureFlags describes cluster-level feature availability for transition path resolution.
type FeatureFlags struct {
	// ShadowDiskful indicates whether ShadowDiskful (sD) replicas are supported.
	// Requires the Flant DRBD kernel extension with the `non-voting` disk option.
	// When true, sD-based paths are used for D transitions (faster resync via pre-sync).
	// When false, A-based vestibule paths are used as fallback.
	ShadowDiskful bool
}

// NewEngine creates an Engine for one reconciliation cycle.
// Builds replica contexts from the provided state.
// rsp may be nil if RSP is unavailable.
// rvas must be sorted by NodeName (primary), CreationTimestamp (secondary), Name (tie-breaker).
func NewEngine(
	registry *Registry,
	membershipPlanner MembershipPlanner,
	attachmentPlanner AttachmentPlanner,
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	rvas []*v1alpha1.ReplicatedVolumeAttachment,
	rsp RSP,
	features FeatureFlags,
) *Engine {
	if registry == nil {
		panic("dme.NewEngine: registry must be non-nil")
	}
	if membershipPlanner == nil {
		panic("dme.NewEngine: membershipPlanner must be non-nil")
	}
	if attachmentPlanner == nil {
		panic("dme.NewEngine: attachmentPlanner must be non-nil")
	}

	e := &Engine{
		registry:          registry,
		membershipPlanner: membershipPlanner,
		attachmentPlanner: attachmentPlanner,
		globalCtx:         GlobalContext{RV: rv, RVRs: rvrs, RVAs: rvas, RSP: rsp, Features: features},
	}

	e.buildReplicaCtxs(rv, rvrs, rvas)

	return e
}

// buildReplicaCtxs creates ReplicaContext entries from all data sources.
//
// Step 1: Collect all known replica IDs → create contexts, build replicaCtxByID index.
// Step 2: Populate RVR, Member, Request, Transition by ID (O(1) each). Set NodeName from RVR/Member.
// Step 3: Assign RVAs by NodeName (linear scan per node group, max 32). Orphan RVA nodes → append.
func (e *Engine) buildReplicaCtxs(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	rvas []*v1alpha1.ReplicatedVolumeAttachment,
) {
	// Step 1: Collect all known replica IDs from ID-bearing sources.
	knownIDs := idset.FromAll(rvrs).
		Union(idset.FromAll(rv.Status.Datamesh.Members)).
		Union(idset.FromAll(rv.Status.DatameshReplicaRequests)).
		Union(idset.FromFunc(rv.Status.DatameshTransitions, func(t v1alpha1.ReplicatedVolumeDatameshTransition) (uint8, bool) {
			if t.ReplicaName != "" {
				return t.ReplicaID(), true
			}
			return 0, false
		}))

	// Allocate contexts for known IDs. +1 capacity for a possible orphan RVA node in step 3.
	e.replicaCtxs = make([]ReplicaContext, knownIDs.Len(), knownIDs.Len()+1)
	idx := 0
	for id := range knownIDs.All() {
		e.replicaCtxByID[id] = &e.replicaCtxs[idx]
		idx++
	}

	// Step 2: Populate by ID.
	// All IDs below are guaranteed to be in knownIDs (collected from the same sources).

	for _, rvr := range rvrs {
		rc := e.replicaCtxByID[rvr.ID()]
		rc.RVR = rvr
		rc.NodeName = rvr.Spec.NodeName
	}

	for i := range rv.Status.Datamesh.Members {
		m := &rv.Status.Datamesh.Members[i]
		rc := e.replicaCtxByID[m.ID()]
		rc.Member = m
		if rc.NodeName == "" {
			rc.NodeName = m.NodeName
		}
	}

	for i := range rv.Status.DatameshReplicaRequests {
		req := &rv.Status.DatameshReplicaRequests[i]
		rc := e.replicaCtxByID[req.ID()]
		rc.MembershipRequest = req
	}

	for i := range rv.Status.DatameshTransitions {
		t := &rv.Status.DatameshTransitions[i]
		if t.ReplicaName == "" {
			continue
		}

		rc := e.replicaCtxByID[t.ReplicaID()]
		switch t.Group {
		case v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership,
			v1alpha1.ReplicatedVolumeDatameshTransitionGroupEmergency:
			rc.MembershipTransition = t
		case v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment:
			rc.AttachmentTransition = t
		}
	}

	// Step 3: Assign RVAs by NodeName.
	// RVAs are sorted by NodeName — iterate groups and match by linear scan (max 32 contexts).
	// Save backing array pointer to detect reallocation from orphan appends.
	var backingBefore *ReplicaContext
	if len(e.replicaCtxs) > 0 {
		backingBefore = &e.replicaCtxs[0]
	}

	rvaIdx := 0
	for rvaIdx < len(rvas) {
		nodeName := rvas[rvaIdx].Spec.NodeName

		// Find end of this NodeName group.
		rvaEnd := rvaIdx + 1
		for rvaEnd < len(rvas) && rvas[rvaEnd].Spec.NodeName == nodeName {
			rvaEnd++
		}
		rvaSlice := rvas[rvaIdx:rvaEnd]
		rvaIdx = rvaEnd

		// Find existing context with this NodeName.
		found := false
		for i := range e.replicaCtxs {
			if e.replicaCtxs[i].NodeName == nodeName {
				e.replicaCtxs[i].RVAs = rvaSlice
				found = true
				break
			}
		}
		if !found {
			// Orphan RVA node (no member/RVR on this node) — append.
			e.replicaCtxs = append(e.replicaCtxs, ReplicaContext{
				NodeName: nodeName,
				RVAs:     rvaSlice,
			})
		}
	}

	// If append reallocated the backing array, replicaCtxByID pointers are stale — rebuild.
	if len(e.replicaCtxs) > 0 && &e.replicaCtxs[0] != backingBefore {
		for i := range e.replicaCtxs {
			if id, ok := e.replicaCtxs[i].ID(); ok {
				e.replicaCtxByID[id] = &e.replicaCtxs[i]
			}
		}
	}
}

// ReplicaContexts returns all replica contexts built by the engine.
// Used by callers after Process() to read per-replica output (e.g., attachment
// condition messages for RVA condition reconciliation).
func (e *Engine) ReplicaContexts() []ReplicaContext {
	return e.replicaCtxs
}

// Result is returned by Process.
type Result struct {
	// Changed is true if any mutation was made to rv.Status.
	Changed bool
	// Requeue is true if the caller should requeue reconciliation.
	Requeue bool
}
