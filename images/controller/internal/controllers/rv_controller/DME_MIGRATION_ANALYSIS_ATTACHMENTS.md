# DME Migration Analysis: `reconciler_dm_attachments.go`

This document captures the results of analyzing what is needed to migrate
`reconciler_dm_attachments.go` (attachment scheduling and Attach/Detach/Multiattach
transition management) to the `dme` (datamesh engine) SDK.

Companion document: `DME_MIGRATION_ANALYSIS.md` (Access replica membership).

---

## Current architecture (`reconciler_dm_attachments.go`)

`ensureDatameshAttachments` is an `EnsureReconcileHelper` that orchestrates
attach/detach lifecycle in a multi-phase pipeline:

```
buildAttachmentsSummary          → pre-indexed view (members × rvrs × rvas)
computeDatameshAttachBlocked     → global attach block (RV deleting, quorum)
ensureDatameshAttachDetachTransitionProgress  → backward pass: Attach/Detach
computeDatameshPotentiallyAttached            → who is/may be attached
ensureDatameshMultiattachTransitionProgress   → backward pass: Multiattach
computeDatameshAttachmentIntents              → slot allocation, FIFO, eligibility
ensureDatameshMultiattachToggle               → create Enable/Disable Multiattach
ensureDatameshDetachTransitions               → create Detach transitions
ensureDatameshAttachTransitions               → create Attach transitions
```

Output: `*attachmentsSummary` (via `outAtts`) consumed by
`reconcileRVAConditionsFromAttachmentsSummary` for RVA condition updates.

---

## Part 1: `attachmentsSummary` / `buildAttachmentsSummary` → `Engine.buildReplicaCtxs`

### Field mapping

| `attachmentState` field | `ReplicaContext` field | Status |
|---|---|---|
| `nodeName` | `NodeName` | ✅ Direct match |
| `member` | `Member` | ✅ Direct match |
| `rvr` | `RVR` | ✅ Direct match |
| `rvas` | `RVAs` | ✅ Direct match |
| `intent` | — | Not in DME (belongs to `AttachmentPlanner`) |
| `hasActiveAttachTransition` | `AttachmentTransition != nil && Type==Attach` | ✅ Derivable |
| `hasActiveDetachTransition` | `AttachmentTransition != nil && Type==Detach` | ✅ Derivable |
| `hasActiveAddAccessTransition` | `MembershipTransition != nil && Type==AddReplica` | ✅ Derivable |
| `conditionMessage` | `attachmentConditionMessage` | ✅ Direct match |
| `conditionReason` | `attachmentConditionReason` | ✅ Direct match |
| `attachmentStateByReplicaID` | `replicaCtxByID[32]` | ✅ Direct match |

### Global summary fields

| `attachmentsSummary` field | DME equivalent | Status |
|---|---|---|
| `attachBlocked` + reason/message | Computed internally by `AttachmentPlanner` | ✅ Design-level match |
| `hasActiveEnableMultiattachTransition` | Derivable from `DatameshTransitions` | ✅ Derivable |
| `hasActiveDisableMultiattachTransition` | Derivable from `DatameshTransitions` | ✅ Derivable |
| `intendedAttachments` | Internal to `AttachmentPlanner` | ✅ Design-level match |
| `potentiallyAttached` | Internal to `AttachmentPlanner` | ✅ Design-level match |

### Conclusion

`buildAttachmentsSummary` is fully replaced by `Engine.buildReplicaCtxs()`.
The `attachmentsSummary` type becomes internal planner state. No structural gaps.

---

## Part 2: `computeDatameshAttachBlocked` → `AttachmentPlanner` internal

Global attach block (RV deleting, quorum not satisfied) is a planner-internal
decision. `AttachmentPlanner.PlanAttachments()` receives `gctx` which contains
`gctx.RV` (DeletionTimestamp) and `gctx.RVRs` (for quorum computation).

Blocked decisions are expressed per-replica via `AttachmentDecision.BlockedMessage`
/ `BlockedReason`. The planner applies the global block to all relevant replicas.

**No gap.** This is planner design, not engine API.

---

## Part 3: `computeDatameshAttachmentIntents` → `AttachmentPlanner.PlanAttachments`

This is the core scheduling logic: slot allocation, FIFO ordering, eligibility
checks, `potentiallyAttached` tracking.

### What `PlanAttachments` must implement

1. Compute `potentiallyAttached`: `member.Attached || rctx.AttachmentTransition
   is active Detach`.
2. PotentiallyAttached nodes with active RVA → decision=Attach (keep slot).
   PotentiallyAttached nodes without active RVA → decision=Detach.
3. If attach globally blocked → remaining active-RVA nodes → blocked with reason.
4. Eligibility checks via `gctx.RSP` (NodeReady, AgentReady, node eligible).
5. FIFO ordering by earliest active RVA timestamp.
6. Slot budget: `maxAttachments - occupiedSlots`.
7. Settled nodes: already attached with no transition → condition output only.
8. Detach intent nodes: member no longer needed → Detach transition.

### Data availability for planner

The planner receives `gctx *GlobalContext` and `rctxs []*ReplicaContext`.

- `gctx.RV` → `Spec.MaxAttachments`, `Status.Datamesh.Multiattach`,
  `DeletionTimestamp`, `Status.Configuration.*`
- `gctx.RVRs` → for quorum checks
- `gctx.RSP` → `FindEligibleNode` for eligibility
- Each `rctx` → `Member` (Attached flag), `RVR`, `RVAs`,
  `AttachmentTransition`, `MembershipTransition`

**All required data is accessible. No gap.**

### Settled condition output

Settled nodes (already attached, no transition needed) require condition
messages ("Volume is attached and ready to serve I/O on the node") but have
no transition and are not "blocked" in the traditional sense.

The planner returns a decision for every relevant node, including settled ones.
For settled nodes: `PlanID=""`, `BlockedMessage="Volume is attached..."`,
`BlockedReason="Attached"`. The engine writes this to `rctx` after diff check
(no patch churn because the engine only marks `Changed` when the message
actually differs from the current value).

### `relevant` filter in `processAttachmentForward`

Current engine:

```go
var relevant []*ReplicaContext
for i := range e.replicaCtxs {
    rctx := &e.replicaCtxs[i]
    if len(rctx.RVAs) > 0 || (rctx.Member != nil && rctx.Member.Attached) {
        relevant = append(relevant, rctx)
    }
}
```

This filters for nodes with RVAs or attached members. Nodes with active Detach
transitions but no RVAs (`Member.Attached == false`, detach apply already ran)
are not included — but they don't need to be: the backward pass handles
in-progress Detach transitions and writes condition messages via
`setReplicaCtxMessage`. When the detach completes, the node has no RVAs
and no RVA conditions to update.

**Conclusion:** The `relevant` filter is sufficient. No gap.

---

## Part 4: Backward pass — `ensureDatameshAttachDetachTransitionProgress` → `processBackward`

### Attach completion

Current: `rvr.Status.DatameshRevision >= step.DatameshRevision`

DME confirm callback:
```
MustConfirm = {replicaID}
Confirmed = {replicaID} if rvr.DatameshRevision >= stepRevision
```

**Direct match.**

### Detach completion (special cases)

Current completion conditions:
1. `as == nil` — attachment state not found (member removed between cycles)
2. `as.rvr == nil` — RVR not found
3. `rvr.DatameshRevision == 0` — RVR left datamesh
4. `rvr.DatameshRevision >= step.DatameshRevision` — normal confirmation

DME confirm callback receives `rctx`. In cases 1-3, the replica is effectively
"gone" and the transition should auto-complete:

```go
func(gctx *GlobalContext, rctx *ReplicaContext, stepRevision int64) ConfirmResult {
    replicaID, _ := rctx.ID()
    mustConfirm := idset.Of(replicaID)

    // Auto-confirm if member gone, replica gone, or left datamesh.
    if rctx.Member == nil || rctx.RVR == nil || rctx.RVR.Status.DatameshRevision == 0 {
        return ConfirmResult{MustConfirm: mustConfirm, Confirmed: mustConfirm}
    }

    var confirmed idset.IDSet
    if rctx.RVR.Status.DatameshRevision >= stepRevision {
        confirmed.Add(replicaID)
    }
    return ConfirmResult{MustConfirm: mustConfirm, Confirmed: confirmed}
}
```

**No structural gap.** The API supports all cases; `rctx.Member == nil` covers
the "member removed" case (DME builds `rctx` from transitions too, so the
context exists even if the member is gone).

### AttachmentConditionReason during in-progress transitions

Current code sets per-node condition messages during backward pass:
```go
as.conditionReason = "Attaching"
as.conditionMessage = "Attaching, " + progressMsg
```

DME handles this via `step.attachmentConditionReason` (set by
`.AttachmentConditionReason("Attaching")` in DSL) and `setReplicaCtxMessage`
which writes to `rctx.attachmentConditionMessage`.

The composed message format: `composeMessage(s, progress)` produces
`"<messagePrefix>, <progress>"`. With `.MessagePrefix("Attaching")` and
`.AttachmentConditionReason("Attaching")`:
- `rctx.attachmentConditionMessage` = `"Attaching, 0/1 replicas confirmed..."`
- `rctx.attachmentConditionReason` = `"Attaching"`

**Direct match.**

### AddReplica transition indexing

Current backward pass indexes `hasActiveAddAccessTransition` for Attach guards
(AddReplica still in progress → don't attach yet).

DME: `rctx.MembershipTransition` points to the active membership transition.
Attach plan guard can check: `rctx.MembershipTransition != nil &&
rctx.MembershipTransition.Type == AddReplica`. **Direct match.**

---

## Part 5: Multiattach — backward pass + toggle

### `ensureDatameshMultiattachTransitionProgress` → backward pass with global confirm

Multiattach transitions are global-scope (no `ReplicaName`). The engine's
backward pass handles them via `GlobalConfirmFunc`.

Confirm callback:
```go
func(gctx *GlobalContext, stepRevision int64) ConfirmResult {
    // Must be confirmed by all members with backing volume + potentially attached.
    mustConfirm := idset.FromWhere(gctx.RV.Status.Datamesh.Members, func(m) bool {
        return m.Type.HasBackingVolume() || m.Attached
    })
    // Add members with active Detach transitions (potentiallyAttached).
    for _, t := range gctx.RV.Status.DatameshTransitions {
        if t.Type == Detach && t.ReplicaName != "" {
            mustConfirm.Add(t.ReplicaID())
        }
    }

    confirmed := idset.FromWhere(gctx.RVRs, func(rvr) bool {
        return rvr.Status.DatameshRevision >= stepRevision
    }).Intersect(mustConfirm)

    return ConfirmResult{MustConfirm: mustConfirm, Confirmed: confirmed}
}
```

**No structural gap.** Computing `potentiallyAttached` inside the confirm
callback is verbose but straightforward.

### `ensureDatameshMultiattachToggle` → `AttachmentPlanner.PlanMultiattach`

`PlanMultiattach(gctx *GlobalContext) *MultiattachDecision` is called before
`PlanAttachments` in the engine's forward pass. This is fine because the
multiattach toggle decision does **not** require the precise `intendedAttachments`
set from `PlanAttachments`.

`PlanMultiattach` can independently determine whether multiattach is needed by
examining the same inputs available to `PlanAttachments`:

- `gctx.RV.Status.Datamesh.Members` → who is currently attached
- `gctx.RV.Status.DatameshTransitions` → active Detach transitions
  (potentiallyAttached)
- `gctx.RVAs` → who wants to attach (active RVAs by node)
- `gctx.RV.Spec.MaxAttachments` → slot limit
- `gctx.RSP` → node eligibility

The logic: count how many nodes will/should be attached (potentiallyAttached
with active RVAs + eligible new candidates within slot budget). If count > 1 and
multiattach is not yet enabled → return EnableMultiattach decision. If count ≤ 1
and potentiallyAttached ≤ 1 and multiattach is enabled → return
DisableMultiattach decision.

This may enable multiattach slightly earlier than the current code (e.g., when
a candidate is blocked by a guard like AddReplica-in-progress, the current code
does not count it in `intendedAttachments`, but `PlanMultiattach` might). This
is harmless: enabling multiattach when only 1 node actually attaches wastes no
resources, and when the second node becomes ready, multiattach is already
enabled — saving one reconcile cycle.

---

## Part 6: Forward pass — `ensureDatameshDetachTransitions` + `ensureDatameshAttachTransitions`

### Mapping to `AttachmentPlanner.PlanAttachments` + engine creation

The planner returns `[]AttachmentDecision` (parallel slice to `rctxs`):

```go
type AttachmentDecision struct {
    TransitionType  // Attach, Detach, or empty
    PlanID          // plan to use, or empty
    BlockedMessage  // human-readable block reason
    BlockedReason   // machine-readable reason
}
```

The engine creates transitions via `createReplicaTransition()`.

### Attach guards → plan `ReplicaGuardFunc` or planner logic

| Guard | Location |
|---|---|
| Already attached (settled) | **Planner**: decision = condition-only (no transition) |
| Active Attach transition exists | **Planner**: decision = no-op (backward handles it) |
| Active Detach conflict | **ReplicaGuardFunc** on Attach plan |
| Active AddReplica in progress | **ReplicaGuardFunc** on Attach plan |
| No member on node | **Planner**: decision = blocked/pending |
| No RVR on node | **Planner**: decision = blocked/pending |
| RVR not Ready | **ReplicaGuardFunc** on Attach plan |
| VolumeAccess=Local, no backing volume | **ReplicaGuardFunc** on Attach plan |
| Multiattach not enabled (for 2nd+ attach) | **ReplicaGuardFunc** on Attach plan |
| Multiattach disable in progress | **ReplicaGuardFunc** on Attach plan |
| Attach globally blocked (defensive) | **Planner**: decision = blocked |

### Detach guards → plan `ReplicaGuardFunc` or planner logic

| Guard | Location |
|---|---|
| No member (node was never attached) | **Planner**: condition-only |
| Already detached (settled) | **Planner**: condition-only |
| Active Detach transition exists | **Planner**: no-op |
| Active Attach conflict | **ReplicaGuardFunc** on Detach plan |
| Device in use | **ReplicaGuardFunc** on Detach plan |

### Multiattach guard — `break` semantics

Current code uses `break` (not `continue`) for the multiattach guard in
`ensureDatameshAttachTransitions`, stopping ALL remaining Attach transitions.
In DME, the planner handles this: if multiattach is not enabled and another
node is already attached, all additional Attach decisions are set to blocked.
**No gap — planner design.**

### Apply callbacks

**Attach `ReplicaApplyFunc`:**
```go
func(gctx *GlobalContext, rctx *ReplicaContext) {
    rctx.Member.Attached = true
}
```

**Detach `ReplicaApplyFunc`:**
```go
func(gctx *GlobalContext, rctx *ReplicaContext) {
    rctx.Member.Attached = false
}
```

Both are trivial single-field mutations. **No gap.**

### Confirm callbacks

**Attach `ReplicaConfirmFunc`:**
```go
func(gctx *GlobalContext, rctx *ReplicaContext, stepRevision int64) ConfirmResult {
    id, _ := rctx.ID()
    mustConfirm := idset.Of(id)
    var confirmed idset.IDSet
    if rctx.RVR != nil && rctx.RVR.Status.DatameshRevision >= stepRevision {
        confirmed.Add(id)
    }
    return ConfirmResult{MustConfirm: mustConfirm, Confirmed: confirmed}
}
```

**Detach `ReplicaConfirmFunc`:** (with "replica gone" handling)
```go
func(gctx *GlobalContext, rctx *ReplicaContext, stepRevision int64) ConfirmResult {
    id, _ := rctx.ID()
    mustConfirm := idset.Of(id)
    // Auto-confirm if member gone, RVR gone, or RVR left datamesh.
    if rctx.Member == nil || rctx.RVR == nil ||
        rctx.RVR.Status.DatameshRevision == 0 ||
        rctx.RVR.Status.DatameshRevision >= stepRevision {
        return ConfirmResult{MustConfirm: mustConfirm, Confirmed: mustConfirm}
    }
    return ConfirmResult{MustConfirm: mustConfirm, Confirmed: idset.IDSet(0)}
}
```

**No structural gap.**

---

## Part 7: Downstream output — `outAtts` for RVA condition reconciliation

`ensureDatameshAttachments` writes `*attachmentsSummary` to `outAtts`. The
downstream `reconcileRVAConditionsFromAttachmentsSummary` reads per-node
`conditionMessage`/`conditionReason` to update RVA conditions.

After DME migration, the equivalent data is in `ReplicaContext`:
- `rctx.AttachmentConditionMessage()`
- `rctx.AttachmentConditionReason()`

The caller accesses all contexts via `Engine.ReplicaContexts()` after
`Process()` and matches by `NodeName` to update RVA conditions.

### Condition output completeness

The engine writes `attachmentConditionMessage`/`attachmentConditionReason` in
these scenarios:
1. **Backward pass:** In-progress transitions (via `setReplicaCtxMessage`).
2. **Forward pass (blocked/settled):** planner decision with non-empty
   `BlockedMessage` → engine writes to `rctx` after diff check.
3. **Forward pass (transition created):** via `createReplicaTransition` →
   `applyAndConfirmStep` → `setReplicaCtxMessage`.

For settled nodes (scenario 2), the planner returns a decision with
`PlanID=""`, `BlockedMessage="Volume is attached..."`,
`BlockedReason="Attached"`. The engine writes this to `rctx` only if the
message differs (diff check prevents patch churn).

---

## Part 8: Plan registration (full example)

```go
// Attach
attach := reg.ReplicaTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach)
attach.Plan("attach", v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment).
    ReplicaGuard(guardAttachNoActiveDetach).
    ReplicaGuard(guardAttachNoActiveAddReplica).
    ReplicaGuard(guardAttachRVRReady).
    ReplicaGuard(guardAttachVolumeAccessLocal).
    ReplicaGuard(guardAttachMultiattachEnabled).
    ReplicaStep("Attach", applyAttach, confirmAttach).
        MessagePrefix("Attaching").
        AttachmentConditionReason("Attaching").
        DiagnosticConditions(v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType).
    Build()

// Detach
detach := reg.ReplicaTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach)
detach.Plan("detach", v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment).
    ReplicaGuard(guardDetachNoActiveAttach).
    ReplicaGuard(guardDetachNotInUse).
    ReplicaStep("Detach", applyDetach, confirmDetach).
        MessagePrefix("Detaching").
        AttachmentConditionReason("Detaching").
        DiagnosticConditions(v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType).
    Build()

// EnableMultiattach
enableMultiattach := reg.GlobalTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach)
enableMultiattach.Plan("enable", v1alpha1.ReplicatedVolumeDatameshTransitionGroupMultiattach).
    GlobalStep("Enable multiattach", applyEnableMultiattach, confirmMultiattach).
        DiagnosticConditions(v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType).
    Build()

// DisableMultiattach
disableMultiattach := reg.GlobalTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeDisableMultiattach)
disableMultiattach.Plan("disable", v1alpha1.ReplicatedVolumeDatameshTransitionGroupMultiattach).
    GlobalStep("Disable multiattach", applyDisableMultiattach, confirmMultiattach).
        DiagnosticConditions(v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType).
    Build()
```

---

## Summary

### Components to implement

| # | Component | Notes |
|---|---|---|
| 1 | `AttachmentPlanner` implementation | Core scheduling: `PlanAttachments` (intents, FIFO, slots, eligibility, settled condition output) + `PlanMultiattach` (toggle based on independent count of who will/should be attached) |
| 2 | Attach plan: guards, apply, confirm | Guards: active Detach/AddReplica conflict, RVR Ready, VolumeAccess=Local, multiattach enabled. Apply: `member.Attached = true`. Confirm: single-replica revision check. |
| 3 | Detach plan: guards, apply, confirm | Guards: active Attach conflict, device in use. Apply: `member.Attached = false`. Confirm: revision check with "replica gone" auto-confirm. |
| 4 | EnableMultiattach plan: apply, confirm | Apply: `rv.Status.Datamesh.Multiattach = true`. Confirm: members with backing volume + potentiallyAttached. |
| 5 | DisableMultiattach plan: apply, confirm | Apply: `rv.Status.Datamesh.Multiattach = false`. Confirm: same as Enable. |
| 6 | Downstream integration | `reconcileRVAConditionsFromAttachmentsSummary` reads from `Engine.ReplicaContexts()` instead of `attachmentsSummary`. |

### Cross-cutting with Access migration

Both Access and Attachments share:
- `rv.Status.DatameshTransitions` and `DatameshRevision` counter
- Backward pass processing
- Parallelism rules

**Recommendation:** Migrate both simultaneously to avoid fragile partial state.
