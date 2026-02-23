# DME Migration Analysis: `reconciler_dm_access.go`

This document captures the results of analyzing what is needed to migrate
`reconciler_dm_access.go` (Access replica membership management) to the `dme`
(datamesh engine) SDK in the `dme/` sub-package.

---

## Current architecture (`reconciler_dm_access.go`)

`ensureDatameshAccessReplicas` is an `EnsureReconcileHelper` that directly
mutates `rv.Status` in a two-loop pattern:

1. **Loop 1 (backward):** iterates existing `AddReplica`/`RemoveReplica`
   transitions in reverse — checks confirmation progress, completes or updates
   messages, deletes completed transitions.
2. **Loop 2 (forward):** iterates remaining `DatameshReplicaRequests` (Access
   join/leave only) — evaluates guards and creates new transitions.

Helper functions used:

| Function | Role |
|---|---|
| `ensureDatameshAccessReplicaTransitionProgress` | Confirm/complete a single transition |
| `ensureDatameshAddReplica` | Guards + create AddReplica transition |
| `ensureDatameshRemoveReplica` | Guards + create RemoveReplica transition |
| `applyDatameshMember` | Add/update a DatameshMember |
| `removeDatameshMembers` | Remove members by ID set |
| `makeDatameshSingleStepTransition` | Construct a single-step transition |
| `computeDatameshTransitionProgressMessage` | Build progress message string |
| `applyDatameshReplicaRequestMessage` | Set message on a replica request |
| `applyDatameshTransitionStepMessage` | Set message on a transition step |
| `findRVRByID` | Find RVR by replica ID |

Called from `reconcileNormalOperation`:

```go
eo := flow.MergeEnsures(
    ensureDatameshAccessReplicas(rf.Ctx(), rv, *rvrs, rsp),
    ensureDatameshAttachments(rf.Ctx(), rv, *rvrs, rvas, rsp, &atts),
)
```

---

## DME engine architecture (target)

The `dme` package provides a pure non-I/O engine that replaces the manual
backward/forward loop pattern with:

- **Registry** — static plan store built at controller startup.
- **Engine** — per-reconciliation instance with pre-built `ReplicaContext[]`.
- **Process()** — three-phase loop: backward → membership forward → attachment forward.
- **MembershipPlanner** / **AttachmentPlanner** — interfaces for plan selection.
- **parallelismCache** — automatic parallelism enforcement.
- **Progress messages** — automatic generation from `ConfirmResult`.

---

## Components to implement

### 1. MembershipPlanner

New interface implementation needed:

```go
type MembershipPlanner interface {
    Classify(operation) TransitionType
    Plan(gctx, rctx, transitionType) (PlanID, string)
}
```

**Classify** — pure mapping:

| Operation | TransitionType |
|---|---|
| `Join` | `AddReplica` |
| `Leave` | `RemoveReplica` |
| `ChangeRole` | `ChangeReplicaType` (future) |
| `ChangeBackingVolume` | `ChangeBackingVolume` (future) |

**Plan** — selects a plan ID or returns blocked/skip:

- Determines replica type from request context (e.g., `request.Type == Access`).
- Returns `(planID, "")` when a plan is selected.
- Returns `("", blockedMsg)` when blocked at planner level.
- Returns `("", "")` when nothing to do (skip — message left unchanged).

**Skip cases** (planner returns `("", "")`):

- Join request but member already exists (transient state).
- Leave request but member does not exist (transient state).
- Leave request but member is not Access type (defensive).
- Request type not yet supported (e.g., Diskful — future plans not registered).

**Blocked cases** (planner returns `("", msg)`):

- None currently at planner level. All blocking guards are per-plan (see §2).

Alternatively, some guards that depend only on `gctx` (not on the specific plan)
could live in the planner. See §2 for the recommended split.

### 2. Guard distribution: Planner vs Plan ReplicaGuardFunc

Guards from `ensureDatameshAddReplica`:

| Guard | Current behavior | Recommended location |
|---|---|---|
| Already a member | skip (no message) | **Planner** → `("", "")` |
| RV deleting | blocked + message | **ReplicaGuardFunc** on AddReplica plan |
| VolumeAccess=Local | blocked + message | **ReplicaGuardFunc** on AddReplica plan |
| RVR must exist | skip | **Planner** → `("", "")` (engine guarantees `rctx.RVR` for ID-bearing requests, but guard defensively in planner if RVR is nil) |
| Addresses not populated | blocked + message | **ReplicaGuardFunc** on AddReplica plan |
| Member on same node | blocked + message | **ReplicaGuardFunc** on AddReplica plan |
| RSP unavailable | blocked + message | **ReplicaGuardFunc** on AddReplica plan |
| Node not in eligible nodes | blocked + message | **ReplicaGuardFunc** on AddReplica plan |

Guards from `ensureDatameshRemoveReplica`:

| Guard | Current behavior | Recommended location |
|---|---|---|
| Not a member | skip | **Planner** → `("", "")` |
| Not Access type | skip | **Planner** → `("", "")` |
| Member is attached | blocked + message | **ReplicaGuardFunc** on RemoveReplica plan |

**Rationale:** "Skip" guards (no message change, transient state) belong in the
planner because they decide *whether to attempt a transition at all*. "Blocked"
guards (message is set) belong on the plan because they are plan-specific
preconditions.

### 3. Apply callbacks

**AddReplica "access" plan — `ReplicaApplyFunc`:**

Adds a `DatameshMember` to `rv.Status.Datamesh.Members`:

```go
func(gctx *GlobalContext, rctx *ReplicaContext) {
    rvr := rctx.RVR
    eligibleNode := gctx.RSP.FindEligibleNode(rctx.NodeName)
    // Guards already verified RSP and node eligibility — safe to dereference.

    gctx.RV.Status.Datamesh.Members = append(gctx.RV.Status.Datamesh.Members,
        v1alpha1.DatameshMember{
            Name:      rvr.Name,
            Type:      v1alpha1.DatameshMemberTypeAccess,
            NodeName:  rvr.Spec.NodeName,
            Zone:      eligibleNode.ZoneName,
            Addresses: slices.Clone(rvr.Status.Addresses),
            Attached:  false,
        })
}
```

**RemoveReplica "access" plan — `ReplicaApplyFunc`:**

Removes the member from `rv.Status.Datamesh.Members`:

```go
func(gctx *GlobalContext, rctx *ReplicaContext) {
    id, _ := rctx.ID()
    members := gctx.RV.Status.Datamesh.Members
    n := 0
    for i := range members {
        if members[i].ID() != id {
            members[n] = members[i]
            n++
        }
    }
    gctx.RV.Status.Datamesh.Members = members[:n]
}
```

### 4. Confirm callbacks

**AddReplica "access" plan — `ReplicaConfirmFunc`:**

```go
func(gctx *GlobalContext, rctx *ReplicaContext, stepRevision int64) ConfirmResult {
    replicaID, _ := rctx.ID()

    // Members that Access replicas connect to (full-mesh members).
    mustConfirm := idset.FromWhere(gctx.RV.Status.Datamesh.Members, func(m DatameshMember) bool {
        return m.Type.ConnectsToAllPeers()
    }).Union(idset.Of(replicaID))

    confirmed := idset.FromWhere(gctx.RVRs, func(rvr *ReplicatedVolumeReplica) bool {
        return rvr.Status.DatameshRevision >= stepRevision
    }).Intersect(mustConfirm)

    return ConfirmResult{MustConfirm: mustConfirm, Confirmed: confirmed}
}
```

**RemoveReplica "access" plan — `ReplicaConfirmFunc`:**

Same as above, plus special case: RVR with `DatameshRevision == 0` is considered
confirmed (the replica left the datamesh and no longer tracks revision):

```go
func(gctx *GlobalContext, rctx *ReplicaContext, stepRevision int64) ConfirmResult {
    replicaID, _ := rctx.ID()

    mustConfirm := idset.FromWhere(gctx.RV.Status.Datamesh.Members, func(m DatameshMember) bool {
        return m.Type.ConnectsToAllPeers()
    }).Union(idset.Of(replicaID))

    confirmed := idset.FromWhere(gctx.RVRs, func(rvr *ReplicatedVolumeReplica) bool {
        return rvr.Status.DatameshRevision >= stepRevision
    }).Intersect(mustConfirm)

    // Special: leaving replica confirms by resetting revision to 0.
    for _, rvr := range gctx.RVRs {
        if rvr.ID() == replicaID && rvr.Status.DatameshRevision == 0 {
            confirmed.Add(replicaID)
        }
    }

    return ConfirmResult{MustConfirm: mustConfirm, Confirmed: confirmed}
}
```

**Behavioral note on `membersToConnect` stability:** In the current code,
`membersToConnect` is computed once and stable within `ensureDatameshAccessReplicas`.
In DME, the confirm callback reads `gctx.RV.Status.Datamesh.Members` which may
change during `Process()` (other transitions' apply callbacks may add/remove
members). For Access replicas this is safe: `ConnectsToAllPeers()` returns
D/D∅ members, which Access transitions never add or remove.

### 5. Step options

**AddReplica "access" step:**

```go
.ReplicaStep("✦ → A", applyAddAccessMember, confirmAccessStep).
    MessagePrefix("Joining datamesh").
    DiagnosticConditions(v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType).
    DiagnosticSkipError(func(rctx *ReplicaContext, id uint8, cond *metav1.Condition) bool {
        replicaID, _ := rctx.ID()
        return id == replicaID &&
            cond.Reason == v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonPendingDatameshJoin
    })
```

**RemoveReplica "access" step:**

```go
.ReplicaStep("A → ✕", applyRemoveAccessMember, confirmRemoveAccessStep).
    MessagePrefix("Leaving datamesh").
    DiagnosticConditions(v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType)
```

No `DiagnosticSkipError` for RemoveReplica (current code does not skip any
errors for leave transitions).

### 6. Completion messages

The `ReplicaOnComplete` callback writes the completion message directly to the
request. After `onComplete`, the transition is removed and
`rctx.MembershipTransition` is set to nil. The forward pass then sees the
request, calls the planner, which returns `("", "")` (member already exists /
no longer exists) → skip (message left unchanged). The completion message
survives.

### 7. Plan registration (full example)

```go
reg := dme.NewRegistry()

addReplica := reg.ReplicaTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica)

addReplica.Plan("access", v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership).
    ReplicaType(v1alpha1.ReplicaTypeAccess).
    ReplicaGuard(guardRVNotDeleting).
    ReplicaGuard(guardVolumeAccessNotLocal).
    ReplicaGuard(guardAddressesPopulated).
    ReplicaGuard(guardNoMemberOnSameNode).
    ReplicaGuard(guardRSPAvailable).
    ReplicaGuard(guardNodeEligible).
    ReplicaStep("✦ → A", applyAddAccessMember, confirmAccessStep).
        MessagePrefix("Joining datamesh").
        DiagnosticConditions(v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType).
        DiagnosticSkipError(skipPendingDatameshJoin).
    ReplicaOnComplete(func(gctx *dme.GlobalContext, rctx *dme.ReplicaContext) {
        if rctx.MembershipRequest != nil {
            rctx.MembershipRequest.Message = "Joined datamesh successfully"
        }
    }).
    Build()

removeReplica := reg.ReplicaTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica)

removeReplica.Plan("access", v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership).
    ReplicaType(v1alpha1.ReplicaTypeAccess).
    ReplicaGuard(guardMemberAttachedCannotRemove).
    ReplicaStep("A → ✕", applyRemoveAccessMember, confirmRemoveAccessStep).
        MessagePrefix("Leaving datamesh").
        DiagnosticConditions(v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType).
    ReplicaOnComplete(func(gctx *dme.GlobalContext, rctx *dme.ReplicaContext) {
        if rctx.MembershipRequest != nil {
            rctx.MembershipRequest.Message = "Left datamesh successfully"
        }
    }).
    Build()
```

---

## Compatibility and behavioral changes

### 8. `rspView` implements `dme.RSP`

`*rspView` already satisfies the `dme.RSP` interface — `FindEligibleNode`
signature matches exactly. No adapter needed.

### 9. Progress message format is compatible

Both `computeDatameshTransitionProgressMessage` (reconciler.go) and
`generateProgressMessage` (dme/confirm.go) produce the same format:

```
3/4 replicas confirmed revision 7. Waiting: [#2]. Errors: #2 DRBDConfigured/Reason: msg
```

The DME version takes `diagnosticConditionTypes` and `diagnosticSkipError` from
the `step` struct (registered via DSL) rather than from function arguments.

The DME `SkipErrorFunc` has an additional `rctx *ReplicaContext` parameter
compared to the current code's `func(id uint8, cond *metav1.Condition) bool`.
This is a superset — existing logic maps directly.

### 10. Parallelism enforcement (new behavior)

The current code does **not** check parallelism for Access transitions. The DME
engine enforces universal parallelism rules:

- **NonVotingMembership can overlap** with other NonVotingMembership on
  different replicas (per-member: max 1 membership transition per replica).
- **Blocked by Formation** — if a Formation transition is active, all others
  are blocked.
- **Blocked by active Quorum** — if a Quorum transition is active, all
  membership transitions are blocked.

This is a **behavioral expansion**: Access transitions will now be blocked
during Formation/Quorum transitions. This is likely more correct (safer), but
should be validated.

### 11. Backward pass scope

Current code filters transitions: `if t.Type != AddReplica && t.Type !=
RemoveReplica { continue }`.

DME backward pass: `if !e.registry.hasTransitionType(t.Type) { continue }`.

If only `AddReplica` and `RemoveReplica` are registered, behavior is identical.
When more types are registered later, they will also be processed — by design.

### 12. Calling code changes

Current:

```go
eo := flow.MergeEnsures(
    ensureDatameshAccessReplicas(rf.Ctx(), rv, *rvrs, rsp),
    ensureDatameshAttachments(rf.Ctx(), rv, *rvrs, rvas, rsp, &atts),
)
```

After migration:

```go
engine := dme.NewEngine(registry, membershipPlanner, attachmentPlanner, rv, *rvrs, rvas, rsp, features)
result := engine.Process()
// Map result to flow outcomes for patching/requeue.
```

The registry is created once at controller startup (in `controller.go` or
reconciler construction). Planners and engine are created per-reconciliation.

---

## Summary table

| # | Item | Status in DME | Action needed |
|---|---|---|---|
| 1 | `MembershipPlanner` | ❌ Not implemented | Implement `Classify` + `Plan` |
| 2 | Guard distribution | ✅ API exists | Split between planner and plan guards |
| 3 | Apply callbacks | ✅ API exists | Implement add/remove member |
| 4 | Confirm callbacks | ✅ API exists | Implement MustConfirm/Confirmed |
| 5 | RemoveReplica revision==0 confirm | ✅ API allows | Special logic in confirm callback |
| 6 | `DiagnosticSkipError` | ✅ Available | Implement callback |
| 7 | `MessagePrefix` | ✅ Available | Configure in DSL |
| 8 | `DiagnosticConditions` | ✅ Available | Configure in DSL |
| 9 | Completion messages | ✅ Works (via `ReplicaOnComplete` + skip) | Implement `onComplete` callbacks |
| 10 | `rspView` compatibility | ✅ Compatible | Nothing needed |
| 11 | Progress message format | ✅ Compatible | Nothing needed |
| 12 | Parallelism (new behavior) | ⚠️ Expansion | Verify acceptability |
| 13 | Calling code adaptation | N/A | Adapt `reconcileNormalOperation` |
| 14 | Registry + plan registration | N/A | Implement at startup |

**Recommendation:** Migrate `ensureDatameshAccessReplicas` and
`ensureDatameshAttachments` together. They share `rv.Status.DatameshTransitions`
and `DatameshRevision`, making partial migration fragile.
