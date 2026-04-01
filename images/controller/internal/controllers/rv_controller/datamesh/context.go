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

package datamesh

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
)

// ──────────────────────────────────────────────────────────────────────────────
// globalContext
//

// globalContext holds datamesh-wide state accessible to all callbacks.
//
// Fields are private — all access is within the datamesh package (guards,
// apply, confirm, dispatchers). The reconciler interacts via buildContexts
// and targeted getters after engine processing.
type globalContext struct {
	// deletionTimestamp is the RV's DeletionTimestamp. Nil if not deleting.
	deletionTimestamp *metav1.Time
	// configuration is the resolved RV configuration (read-only during engine processing).
	configuration v1alpha1.ReplicatedVolumeConfiguration

	// datamesh holds mutable datamesh scalar parameters (q, qmr, multiattach).
	// Apply callbacks mutate these; the reconciler writes them back to rv.Status after engine processing.
	datamesh datameshContext

	// baselineGMDR is the committed GMDR level: min(qmr-1, config.GMDR).
	// Mutated by step callbacks after replicas confirm qmr changes;
	// written back to rv.Status.BaselineGuaranteedMinimumDataRedundancy.
	baselineGMDR byte

	// size is the target usable size from rv.Spec.Size (read-only during engine processing).
	size resource.Quantity

	// maxAttachments is the maximum number of simultaneously attached nodes (from rv.Spec).
	maxAttachments byte
	// replicatedStorageClassName is the RSC name (from rv.Spec), used in user-facing messages.
	// Empty in Manual configuration mode.
	replicatedStorageClassName string

	// rsp provides node eligibility lookups. Nil if RSP is unavailable.
	rsp RSP
	// features describes cluster-level feature availability for path resolution.
	features FeatureFlags

	// changeSystemNetworksTransition points to the active ChangeSystemNetworks
	// transition. Nil if no such transition exists. Read-only — must not be
	// mutated by callbacks. Set/cleared by the concurrency tracker in Add/Remove.
	// Provides access to FromSystemNetworkNames and ToSystemNetworkNames.
	changeSystemNetworksTransition *v1alpha1.ReplicatedVolumeDatameshTransition

	// replicas is the per-ID index (0-31). Nil for unused IDs.
	replicas [32]*ReplicaContext
	// allReplicas is the full backing slice including orphan RVA contexts.
	// Used by dispatchers for iteration and by guards/confirm for cross-replica checks.
	allReplicas []ReplicaContext

	// Lazy-computed cached values (computed on first access, cached for the reconciliation cycle).
	quorumSatisfied  *bool
	quorumDiagnostic string

	// Transition tracking state, maintained incrementally by the concurrency tracker
	// (Add/Remove). Read by guards for admission checks and by the tracker's CanAdmit.

	// Global scope groups (bool — at most one active).
	hasFormationTransition   bool
	hasQuorumTransition      bool
	hasMultiattachTransition bool
	hasNetworkTransition     bool
	hasResizeTransition      bool

	// diskGainingMembershipTransitions tracks replicas with active membership
	// transitions that add a disk-bearing member (AddReplica D/sD or ChangeReplicaType → D/sD).
	// Used for mutual exclusion with Resize.
	diskGainingMembershipTransitions idset.IDSet

	// Replica scope groups (idset — which replicas have active transitions).
	votingMembershipTransitions    idset.IDSet
	nonVotingMembershipTransitions idset.IDSet
	attachmentTransitions          idset.IDSet
	emergencyTransitions           idset.IDSet

	// potentiallyAttached tracks members that are or may still be attached
	// (Primary in DRBD terms). Maintained incrementally:
	//   - Init: members with Attached == true (from member state).
	//   - Add(Attachment): always adds (attaching or detaching — still possibly Primary).
	//   - Remove(Detach): removes (detach confirmed — no longer Primary).
	//   - Remove(Attach): keeps (attach confirmed — settled as attached).
	potentiallyAttached idset.IDSet
}

// isQuorumSatisfied returns true if at least one voter member has quorum.
// Result is lazy-computed on first call and cached for the reconciliation cycle.
func (gctx *globalContext) isQuorumSatisfied() bool {
	if gctx.quorumSatisfied == nil {
		ok, diag := computeQuorumCheck(gctx)
		gctx.quorumSatisfied = &ok
		gctx.quorumDiagnostic = diag
	}
	return *gctx.quorumSatisfied
}

// computeQuorumCheck checks if any voter member has quorum.
// Returns (true, "") if satisfied, or (false, diagnostic) with a human-readable
// explanation of why quorum is not met.
func computeQuorumCheck(gctx *globalContext) (bool, string) {
	var noQuorum, voterAgentNotReady idset.IDSet
	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		if rc.member == nil || !rc.member.Type.IsVoter() || rc.rvr == nil {
			continue
		}

		agentNotReady := obju.StatusCondition(rc.rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType).
			ReasonEqual(v1alpha1.ReplicatedVolumeReplicaCondReadyReasonAgentNotReady).
			Eval()
		if agentNotReady {
			voterAgentNotReady.Add(rc.id)
			continue
		}

		if rc.rvr.Status.Quorum != nil && *rc.rvr.Status.Quorum {
			return true, ""
		}

		noQuorum.Add(rc.id)
	}

	// Build diagnostic message.
	switch {
	case !noQuorum.IsEmpty() && !voterAgentNotReady.IsEmpty():
		return false, fmt.Sprintf("no quorum on [%s]; agent not ready on [%s]", noQuorum, voterAgentNotReady)
	case !noQuorum.IsEmpty():
		return false, fmt.Sprintf("no quorum on [%s]", noQuorum)
	case !voterAgentNotReady.IsEmpty():
		return false, fmt.Sprintf("agent not ready on [%s]", voterAgentNotReady)
	default:
		return false, "no voter members"
	}
}

// RSP provides node eligibility lookups for transition guards.
// Implemented by the caller's RSP view; nil means RSP is unavailable.
type RSP interface {
	FindEligibleNode(nodeName string) *v1alpha1.ReplicatedStoragePoolEligibleNode
	GetSystemNetworkNames() []string
}

// datameshContext holds mutable datamesh scalar parameters.
// Apply callbacks mutate these during engine processing.
// The reconciler writes them back to rv.Status after engine processing.
type datameshContext struct {
	// quorum is the current quorum value (q).
	quorum byte
	// quorumMinimumRedundancy is the current qmr value.
	quorumMinimumRedundancy byte
	// multiattach indicates whether multiattach is enabled.
	multiattach bool
	// systemNetworkNames is the current system network name list.
	systemNetworkNames []string
	// size is the current datamesh usable size (rv.Status.Datamesh.Size).
	size resource.Quantity
}

// ──────────────────────────────────────────────────────────────────────────────
// ReplicaContext
//

// ReplicaContext holds all per-node data pre-indexed for O(1) access.
// A ReplicaContext exists for every node that has at least one of: RVR, Member,
// Request, or Transition. Nodes with only RVAs (no member/RVR) have a
// ReplicaContext with member=nil, rvr=nil, and are not in the globalContext's ID index.
type ReplicaContext struct {
	// gctx is a back-pointer to the parent globalContext.
	// Used by lazy-computed fields (e.g., getEligibleNode) to access
	// shared state (RSP, configuration) without extra function arguments.
	gctx *globalContext

	// id is the replica ID (0-31), set during construction.
	// For orphan RVA contexts (no member/RVR/request/transition), id is unused.
	id uint8
	// name is the replica name (e.g., "rv-1-5"), resolved at construction
	// from the best available source. Empty for orphan contexts.
	name string

	// nodeName is the node this context represents.
	nodeName string
	// rvr is the ReplicatedVolumeReplica on this node. Nil if no RVR exists.
	rvr *v1alpha1.ReplicatedVolumeReplica
	// member is the datamesh member on this node. Nil if not a member.
	// Points into rv.Status.Datamesh.Members for existing members (stable
	// because apply callbacks never mutate the slice — they set rctx.member
	// to nil or to a new heap-allocated object). After engine processing,
	// the reconciler rebuilds the Members slice from all contexts.
	member *v1alpha1.DatameshMember
	// rvas is the sub-slice of RVAs for this node.
	// Sorted by CreationTimestamp, Name (inherits input order).
	rvas []*v1alpha1.ReplicatedVolumeAttachment
	// membershipRequest is the pending membership request for this replica. Nil if none.
	membershipRequest *v1alpha1.ReplicatedVolumeDatameshReplicaRequest

	// membershipTransition is the active membership transition for this replica.
	// Set by the engine via slot accessors. Nil if none.
	membershipTransition *dmte.Transition
	// attachmentTransition is the active attachment transition for this replica.
	// Set by the engine via slot accessors. Nil if none.
	attachmentTransition *dmte.Transition

	// Output fields, populated by slot accessors during engine processing.
	membershipMessage          string
	attachmentConditionMessage string
	attachmentConditionReason  string

	// Lazy-computed cached fields.
	eligibleNode        *v1alpha1.ReplicatedStoragePoolEligibleNode
	eligibleNodeChecked bool
}

// ──────────────────────────────────────────────────────────────────────────────
// ReplicaContext — dmte.ReplicaCtx interface
//

// ID returns the replica ID (0-31). Used by the engine for internal indexing
// and for looking up replica contexts by transition.ReplicaID().
func (rc *ReplicaContext) ID() uint8 { return rc.id }

// Name returns the replica name (e.g., "rv-1-5"). Written to
// transition.ReplicaName when creating replica-scoped transitions.
// The implementation resolves the name from the best available source.
// May return empty if no name is available (orphan contexts).
func (rc *ReplicaContext) Name() string { return rc.name }

// Exists reports whether this replica has observable state for diagnostics.
// When false, the engine reports "Replica not found" in progress messages.
// Generation() and Conditions() are only called when Exists() returns true.
func (rc *ReplicaContext) Exists() bool { return rc.rvr != nil }

// Generation returns the replica's metadata generation.
// Used by the engine to filter stale conditions in diagnostic messages.
func (rc *ReplicaContext) Generation() int64 {
	return rc.rvr.Generation
}

// Conditions returns the replica's status conditions.
// Used by the engine for diagnostic error reporting in progress messages.
func (rc *ReplicaContext) Conditions() []metav1.Condition {
	return rc.rvr.Status.Conditions
}

// ──────────────────────────────────────────────────────────────────────────────
// ReplicaContext — output accessors (for reconciler after engine processing)
//

// NodeName returns the node name this context represents.
func (rc *ReplicaContext) NodeName() string { return rc.nodeName }

// RVR returns the ReplicatedVolumeReplica on this node. Nil if no RVR exists.
func (rc *ReplicaContext) RVR() *v1alpha1.ReplicatedVolumeReplica { return rc.rvr }

// RVAs returns the RVAs for this node (sorted by CreationTimestamp, Name).
func (rc *ReplicaContext) RVAs() []*v1alpha1.ReplicatedVolumeAttachment { return rc.rvas }

// AttachmentConditionMessage returns the attachment condition message
// set by the engine via the attachment slot accessor.
func (rc *ReplicaContext) AttachmentConditionMessage() string { return rc.attachmentConditionMessage }

// AttachmentConditionReason returns the attachment condition reason
// set by the engine via the attachment slot accessor.
func (rc *ReplicaContext) AttachmentConditionReason() string { return rc.attachmentConditionReason }

// ──────────────────────────────────────────────────────────────────────────────
// ReplicaContext — lazy-computed cached accessors
//

// getEligibleNode returns the RSP eligible node for this replica's node.
// Lazy-computed: looked up on first call, cached for the reconciliation cycle.
// Returns nil if RSP is unavailable or the node is not eligible.
func (rc *ReplicaContext) getEligibleNode() *v1alpha1.ReplicatedStoragePoolEligibleNode {
	if !rc.eligibleNodeChecked {
		rc.eligibleNodeChecked = true
		if rc.gctx.rsp != nil && rc.nodeName != "" {
			rc.eligibleNode = rc.gctx.rsp.FindEligibleNode(rc.nodeName)
		}
	}
	return rc.eligibleNode
}

// hasActiveRVA returns true if this replica has at least one non-deleting RVA.
func (rc *ReplicaContext) hasActiveRVA() bool {
	for _, rva := range rc.rvas {
		if rva.DeletionTimestamp == nil {
			return true
		}
	}
	return false
}

// earliestActiveRVATimestamp returns the CreationTimestamp of the earliest
// active (non-deleting) RVA. Returns zero time if no active RVA exists.
// Relies on rvas being sorted by CreationTimestamp within each node
// (guaranteed by input sort order in buildContexts).
func (rc *ReplicaContext) earliestActiveRVATimestamp() metav1.Time {
	for _, rva := range rc.rvas {
		if rva.DeletionTimestamp == nil {
			return rva.CreationTimestamp
		}
	}
	return metav1.Time{}
}

// ──────────────────────────────────────────────────────────────────────────────
// provider
//

// provider provides access to global and per-replica contexts.
// Implements dmte.Contextprovider[*globalContext, *ReplicaContext].
// Value type with value receivers for performance (stenciling).
type provider struct {
	global *globalContext
}

// Global returns the global context.
func (p provider) Global() *globalContext { return p.global }

// Replica returns the replica context for the given ID (0-31).
// Returns nil if the replica context is not available.
func (p provider) Replica(id uint8) *ReplicaContext { return p.global.replicas[id] }

// ──────────────────────────────────────────────────────────────────────────────
// buildContexts
//

// buildContexts creates a provider with globalContext and ReplicaContexts
// from the provided state. Called once per reconciliation cycle.
//
// Step 1: Collect all known replica IDs, create contexts, build [32] index.
// Step 2: Populate RVR, Member, Request by ID. Set nodeName from RVR/Member.
//
//	Members point into rv.Status.Datamesh.Members (stable because apply
//	callbacks never mutate the slice — they set rctx.member to nil or to
//	a new heap-allocated object).
//
// Step 3: Assign RVAs by nodeName. Orphan RVA nodes appended.
//
// Transitions are NOT populated on ReplicaContexts — the engine does that
// via slot accessors in NewEngine.
//
// rvas must be sorted by NodeName (primary), CreationTimestamp (secondary),
// Name (tie-breaker).
func buildContexts(
	rv *v1alpha1.ReplicatedVolume,
	rsp RSP,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	rvas []*v1alpha1.ReplicatedVolumeAttachment,
	features FeatureFlags,
) provider {
	if rv.Status.Configuration == nil {
		panic("datamesh.buildContexts: rv.Status.Configuration must be non-nil")
	}

	gctx := &globalContext{
		deletionTimestamp: rv.DeletionTimestamp.DeepCopy(),
		configuration:     *rv.Status.Configuration,
		datamesh: datameshContext{
			quorum:                  rv.Status.Datamesh.Quorum,
			quorumMinimumRedundancy: rv.Status.Datamesh.QuorumMinimumRedundancy,
			multiattach:             rv.Status.Datamesh.Multiattach,
			systemNetworkNames:      rv.Status.Datamesh.SystemNetworkNames,
			size:                    rv.Status.Datamesh.Size,
		},
		size:                       rv.Spec.Size,
		baselineGMDR:               rv.Status.BaselineGuaranteedMinimumDataRedundancy,
		maxAttachments:             ptr.Deref(rv.Spec.MaxAttachments, 1),
		replicatedStorageClassName: rv.Spec.ReplicatedStorageClassName,
		rsp:                        rsp,
		features:                   features,
	}

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
	gctx.allReplicas = make([]ReplicaContext, knownIDs.Len(), knownIDs.Len()+1)
	idx := 0
	for id := range knownIDs.All() {
		gctx.allReplicas[idx].gctx = gctx
		gctx.allReplicas[idx].id = id
		gctx.replicas[id] = &gctx.allReplicas[idx]
		idx++
	}

	// Step 2: Populate by ID.
	for _, rvr := range rvrs {
		rc := gctx.replicas[rvr.ID()]
		rc.rvr = rvr
		rc.nodeName = rvr.Spec.NodeName
		rc.name = rvr.Name
	}

	for i := range rv.Status.Datamesh.Members {
		m := &rv.Status.Datamesh.Members[i]
		rc := gctx.replicas[m.ID()]
		rc.member = m
		if rc.nodeName == "" {
			rc.nodeName = m.NodeName
		}
		if rc.name == "" {
			rc.name = m.Name
		}
	}

	for i := range rv.Status.DatameshReplicaRequests {
		req := &rv.Status.DatameshReplicaRequests[i]
		rc := gctx.replicas[req.ID()]
		rc.membershipRequest = req
		if rc.name == "" {
			rc.name = req.Name
		}
	}

	// Step 3: Assign RVAs by nodeName.
	// RVAs are sorted by NodeName — iterate groups and match by linear scan (max 32 contexts).
	// Save backing array pointer to detect reallocation from orphan appends.
	var backingBefore *ReplicaContext
	if len(gctx.allReplicas) > 0 {
		backingBefore = &gctx.allReplicas[0]
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

		// Find existing context with this nodeName.
		found := false
		for i := range gctx.allReplicas {
			if gctx.allReplicas[i].nodeName == nodeName {
				gctx.allReplicas[i].rvas = rvaSlice
				found = true
				break
			}
		}
		if !found {
			// Orphan RVA node (no member/RVR on this node) — append.
			gctx.allReplicas = append(gctx.allReplicas, ReplicaContext{
				gctx:     gctx,
				nodeName: nodeName,
				rvas:     rvaSlice,
			})
		}
	}

	// If append reallocated the backing array, replicas pointers are stale — rebuild.
	if len(gctx.allReplicas) > 0 && &gctx.allReplicas[0] != backingBefore {
		// Clear and rebuild the ID index.
		gctx.replicas = [32]*ReplicaContext{}
		for i := range gctx.allReplicas {
			rc := &gctx.allReplicas[i]
			if rc.name != "" {
				// Has an ID-bearing source — re-index.
				gctx.replicas[rc.id] = rc
			}
		}
	}

	return provider{global: gctx}
}

// ──────────────────────────────────────────────────────────────────────────────
// Writeback
//

// writebackMembersFromContexts writes member changes from replica contexts back to
// rv.Status.Datamesh.Members.
//
// In-place field mutations (e.g., future ChangeReplicaType changing member.Type
// through the pointer) are already visible in the original slice and require
// no action from this function.
//
// Structural changes (members added, removed, or replaced) are detected via
// pointer comparison and applied by compacting the original slice and appending
// new members. Original member order is preserved; new members are appended
// at the end. The original backing array is reused when possible.
//
// Change detection for patching is the caller's responsibility (DeepCopy base
// + merge patch diff).
func writebackMembersFromContexts(rv *v1alpha1.ReplicatedVolume, gctx *globalContext) {
	orig := rv.Status.Datamesh.Members

	// Step 1: active member IDs from the replicas index.
	var activeIDs idset.IDSet
	for id := range uint8(32) {
		rc := gctx.replicas[id]
		if rc != nil && rc.member != nil {
			activeIDs.Add(id)
		}
	}

	// Step 2: walk orig, build origIDs + check pointer stability.
	var origIDs idset.IDSet
	allPointersMatch := true
	for i := range orig {
		id := orig[i].ID()
		origIDs.Add(id)
		rc := gctx.replicas[id]
		if rc == nil || rc.member != &orig[i] {
			allPointersMatch = false
		}
	}

	// Fast path: same set + same pointers → nothing to do.
	if activeIDs == origIDs && allPointersMatch {
		return
	}

	// Compact: keep orig members whose pointers still match (surviving in-place).
	var survivingIDs idset.IDSet
	n := 0
	for i := range orig {
		id := orig[i].ID()
		rc := gctx.replicas[id]
		if rc != nil && rc.member == &orig[i] {
			survivingIDs.Add(id)
			if n != i {
				orig[n] = orig[i]
			}
			n++
		}
	}
	rv.Status.Datamesh.Members = orig[:n]

	// Append new/replaced members.
	newIDs := activeIDs.Difference(survivingIDs)
	for id := range newIDs.All() {
		rc := gctx.replicas[id]
		rv.Status.Datamesh.Members = append(rv.Status.Datamesh.Members, *rc.member)
	}
}

// writebackDatameshFromContext writes mutable datamesh scalar parameters from
// the datameshContext back to rv.Status.Datamesh.
func writebackDatameshFromContext(rv *v1alpha1.ReplicatedVolume, gctx *globalContext) {
	rv.Status.Datamesh.Quorum = gctx.datamesh.quorum
	rv.Status.Datamesh.QuorumMinimumRedundancy = gctx.datamesh.quorumMinimumRedundancy
	rv.Status.Datamesh.Multiattach = gctx.datamesh.multiattach
	rv.Status.Datamesh.SystemNetworkNames = gctx.datamesh.systemNetworkNames
	rv.Status.Datamesh.Size = gctx.datamesh.size
}

// writebackBaselineGMDRFromContext writes the baseline GMDR from the
// globalContext back to rv.Status.BaselineGuaranteedMinimumDataRedundancy.
func writebackBaselineGMDRFromContext(rv *v1alpha1.ReplicatedVolume, gctx *globalContext) {
	rv.Status.BaselineGuaranteedMinimumDataRedundancy = gctx.baselineGMDR
}

// updateMemberZonesFromRSP updates member Zone from RSP eligible nodes.
// Called once after buildContexts and before engine processing, so that
// guards and dispatchers see up-to-date zone information.
// Returns true if any member zone was changed.
func updateMemberZonesFromRSP(gctx *globalContext) bool {
	if gctx.rsp == nil {
		return false
	}
	changed := false
	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		if rc.member == nil || rc.nodeName == "" {
			continue
		}
		en := rc.gctx.rsp.FindEligibleNode(rc.nodeName)
		if en == nil {
			continue
		}
		if rc.member.Zone != en.ZoneName {
			rc.member.Zone = en.ZoneName
			changed = true
		}
	}
	return changed
}

// writebackRequestMessagesFromContexts writes membership messages from replica
// contexts back to rv.Status.DatameshReplicaRequests[].Message.
//
// membershipRequest pointers point into rv.Status.DatameshReplicaRequests,
// so mutations are applied in-place. Returns true if any message was changed.
func writebackRequestMessagesFromContexts(gctx *globalContext) bool {
	changed := false
	for id := range uint8(32) {
		rc := gctx.replicas[id]
		if rc == nil || rc.membershipRequest == nil {
			continue
		}
		if rc.membershipRequest.Message != rc.membershipMessage {
			rc.membershipRequest.Message = rc.membershipMessage
			changed = true
		}
	}
	return changed
}
