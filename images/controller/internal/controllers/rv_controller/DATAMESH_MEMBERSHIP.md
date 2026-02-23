# Datamesh Member State Diagram

> **Temporary design document.**

## States

| Notation | State | Description |
|----------|-------|-------------|
| **✦** | **New** | RVR exists, not a datamesh member, wants to join |
| **A** | **Access** | Member, type=Access. Diskless, invisible to quorum. |
| **TB** | **TieBreaker** | Member, type=TieBreaker. Diskless, participates in tiebreaker voting only. |
| **D∅** | **LiminalDiskful** | Member, type=LiminalDiskful. Voter with full-mesh connectivity, but DRBD configured as diskless. Backing volume provisioned but not attached. Always transitional. |
| **D** | **Diskful** | Member, type=Diskful. Data-bearing, quorum voter. Disk attached. |
| **✕** | **Deleted** | Leaving datamesh. Terminal (RVR has DeletionTimestamp). |
| **sD** | **ShadowDiskful** | Member, type=ShadowDiskful. Diskful but invisible to quorum. Requires `non-voting` DRBD option (see LAYOUTS.md §2.1). |
| **sD∅** | **LiminalShadowDiskful** | Member, type=LiminalShadowDiskful. Non-voter with full-mesh connectivity, DRBD configured as diskless. Functionally equivalent to A. Always transitional. |

## Transitions

Three membership transitions cover all datamesh member lifecycle operations:

- **AddReplica(type)** — create a new member of the given type.
- **RemoveReplica(type)** — remove an existing member of the given type.
- **ChangeReplicaType(from, to)** — change a member's type.

Each concrete (from, to) pair determines the steps — including liminal intermediate
states, disk attach/detach, and quorum changes. Quorum changes (q↑/q↓) are steps
within these transitions when voter count changes. Layout-driven qmr changes (qmr↑/qmr↓)
are also embedded as extra steps: qmr↑ appended at the end of AddReplica(D), qmr↓
prepended at the beginning of RemoveReplica(D). Standalone ChangeQuorum is reserved
for explicit/emergency adjustments (see §ChangeQuorum).

### AddReplica

| Transition | Path | When |
|------------|------|------|
| AddReplica(A) | ✦ → A | always |
| AddReplica(TB) | ✦ → TB | always |
| AddReplica(D) | ✦ → D∅ → D | no flant-drbd, even→odd voters |
| AddReplica(D) + qmr↑ | ✦ → D∅ → D → qmr↑ | no flant-drbd, even→odd voters, eff. GMDR < target GMDR |
| AddReplica(D) + q↑ | ✦ → A → D∅ + q↑ → D | no flant-drbd, odd→even voters |
| AddReplica(D) + q↑ + qmr↑ | ✦ → A → D∅ + q↑ → D → qmr↑ | no flant-drbd, odd→even voters, eff. GMDR < target GMDR |
| AddReplica(D) via sD | ✦ → sD∅ → sD → D | flant-drbd, even→odd voters |
| AddReplica(D) via sD + qmr↑ | ✦ → sD∅ → sD → D → qmr↑ | flant-drbd, even→odd voters, eff. GMDR < target GMDR |
| AddReplica(D) via sD + q↑ | ✦ → sD∅ → sD → sD∅ → D∅ + q↑ → D | flant-drbd, odd→even voters |
| AddReplica(D) via sD + q↑ + qmr↑ | ✦ → sD∅ → sD → sD∅ → D∅ + q↑ → D → qmr↑ | flant-drbd, odd→even voters, eff. GMDR < target GMDR |
| AddReplica(sD) | ✦ → sD∅ → sD | flant-drbd |

### RemoveReplica

| Transition | Path | When |
|------------|------|------|
| RemoveReplica(A) | A → ✕ | always |
| RemoveReplica(TB) | TB → ✕ | always |
| RemoveReplica(D) | D → D∅ → ✕ | odd→even voters |
| qmr↓ + RemoveReplica(D) | qmr↓ → D → D∅ → ✕ | odd→even voters, eff. GMDR > target GMDR |
| RemoveReplica(D) + q↓ | D → D∅ → A + q↓ → ✕ | even→odd voters |
| qmr↓ + RemoveReplica(D) + q↓ | qmr↓ → D → D∅ → A + q↓ → ✕ | even→odd voters, eff. GMDR > target GMDR |
| RemoveReplica(sD) | sD → sD∅ → ✕ | flant-drbd |

**Attached member strategy**: when the controller wants to remove a member that is
currently attached (serving IO), it uses `ChangeReplicaType` instead of `RemoveReplica`:
D → sD (flant-drbd) or D → A (no flant-drbd); non-diskful → A. The converted member
stays and continues serving IO. See preconditions for leaving D (VolumeAccessLocal guard)
and preconditions for removing any member (NotAttached guard).

### ForceRemoveReplica

Used when a member's node has permanently failed. Removes the member from the
datamesh directly from its current state, without intermediate steps and without
waiting for the dead replica to confirm. All precondition guards are bypassed —
the node is already lost; the operation restores configuration to match reality.

If the member has `Attached == true`, a ForceDetach (see §ForceDetach) MUST
be performed first to clear the attachment before removal.

Any in-progress `DatameshTransitions` referencing the removed member are
cleaned up as part of the operation.

| Transition | Path | When |
|------------|------|------|
| ForceRemoveReplica(A) | A → ✕ | always |
| ForceRemoveReplica(TB) | TB → ✕ | always |
| ForceRemoveReplica(D) | D → ✕ | odd→even voters |
| ForceRemoveReplica(D) + q↓ | D → ✕ + q↓ | even→odd voters |
| ForceRemoveReplica(D∅) | D∅ → ✕ | odd→even voters |
| ForceRemoveReplica(D∅) + q↓ | D∅ → ✕ + q↓ | even→odd voters |
| ForceRemoveReplica(sD) | sD → ✕ | flant-drbd |
| ForceRemoveReplica(sD∅) | sD∅ → ✕ | flant-drbd |


### ChangeReplicaType

| Transition | Path | When |
|------------|------|------|
| ChangeReplicaType(A, TB) | A → TB | always |
| ChangeReplicaType(TB, A) | TB → A | always |
| ChangeReplicaType(A, D) | A → D∅ → D | no flant-drbd, even→odd voters |
| ChangeReplicaType(A, D) + q↑ | A → D∅ + q↑ → D | no flant-drbd, odd→even voters |
| ChangeReplicaType(A, D) via sD | A → sD∅ → sD → D | flant-drbd, even→odd voters |
| ChangeReplicaType(A, D) via sD + q↑ | A → sD∅ → sD → sD∅ → D∅ + q↑ → D | flant-drbd, odd→even voters |
| ChangeReplicaType(D, A) | D → D∅ → A | odd→even voters |
| ChangeReplicaType(D, A) + q↓ | D → D∅ → A + q↓ | even→odd voters |
| ChangeReplicaType(TB, D) | TB → D∅ → D | no flant-drbd, even→odd voters |
| ChangeReplicaType(TB, D) + q↑ | TB → D∅ + q↑ → D | no flant-drbd, odd→even voters |
| ChangeReplicaType(TB, D) via sD | TB → sD∅ → sD → D | flant-drbd, even→odd voters |
| ChangeReplicaType(TB, D) via sD + q↑ | TB → sD∅ → sD → sD∅ → D∅ + q↑ → D | flant-drbd, odd→even voters |
| ChangeReplicaType(D, TB) | D → D∅ → TB | odd→even voters |
| ChangeReplicaType(D, TB) + q↓ | D → D∅ → TB + q↓ | even→odd voters |
| ChangeReplicaType(A, sD) | A → sD∅ → sD | flant-drbd |
| ChangeReplicaType(sD, A) | sD → sD∅ → A | flant-drbd |
| ChangeReplicaType(TB, sD) | TB → sD∅ → sD | flant-drbd |
| ChangeReplicaType(sD, TB) | sD → sD∅ → TB | flant-drbd |
| ChangeReplicaType(sD, D) | sD → D | flant-drbd, even→odd voters |
| ChangeReplicaType(sD, D) + q↑ | sD → sD∅ → D∅ + q↑ → D | flant-drbd, odd→even voters |
| ChangeReplicaType(D, sD) | D → sD | flant-drbd, odd→even voters |
| ChangeReplicaType(D, sD) + q↓ | D → D∅ → sD∅ + q↓ → sD | flant-drbd, even→odd voters |


### ChangeQuorum

Standalone change of q and/or qmr. Used **only** for explicit/emergency adjustments
invoked from outside the engine. Normal layout-driven qmr changes are embedded as
steps within AddReplica(D) (qmr↑ at the end) or RemoveReplica(D) (qmr↓ at the
beginning) — see those tables above.

New values are already in `rv.Status.Datamesh.Quorum` / `.QuorumMinimumRedundancy`.

Wait for confirmation depends on what changed:

- **q-only change**: wait for **D + D∅ replicas** only. A, TB, and sD always
  use q=32 (hardcoded); a q change does not affect them.
- **qmr change** (with or without q): wait for **all replicas** (D + D∅ + sD +
  sD∅ + A + TB). All replica types use qmr in their quorum path (D in the main
  condition, A/TB/sD in the `quorate_peers` path — see LAYOUTS.md §2.3).

### Step confirmation rules

Each multi-step transition is a sequence of state-to-state steps. After each step
the controller publishes a new DatameshRevision and waits for confirmation from the
affected replicas. The table below lists all unique step transitions and their
confirmation sets.

**Notation**: FM = full-mesh members (D + D∅ + sD + sD∅). "Affected member" = the
member being transitioned. "All" = all datamesh members. For removal (→ ✕),
the removed member confirms via normal DatameshRevision mechanism (like any
other replica). For force-removal, the dead member is excluded from the wait
set (it cannot confirm).

**Affected replica only** — disk state changes (no DRBD config change on peers):

| Step | Why |
|------|-----|
| D∅ → D | Local disk attach; resync with peers starts automatically via DRBD protocol |
| D → D∅ | Local disk detach; peers detect state change automatically via DRBD protocol |
| sD∅ → sD | Local disk attach; resync with peers starts automatically via DRBD protocol |
| sD → sD∅ | Local disk detach; peers detect state change automatically via DRBD protocol |

**FM + affected member** — star member add/remove/role change (star members are
independent of each other; only full-mesh members have connections to them):

| Step | Why |
|------|-----|
| ✦ → A | Star member added; FM peers configure new connection |
| ✦ → TB | Star member added; FM peers configure new connection |
| A → ✕ | Star member removed; FM peers remove connection (A is gone — excluded) |
| TB → ✕ | Star member removed; FM peers remove connection (TB is gone — excluded) |
| A → TB | Star role change; FM peers update quorum role for this peer |
| TB → A | Star role change; FM peers update quorum role for this peer |

**All replicas** — full-mesh topology or voter/quorum changes (all members have
connections to full-mesh members and are affected by voter/arr changes):

| Step | Why |
|------|-----|
| ✦ → D∅ | Full-mesh member added; all peers configure new connections |
| ✦ → sD∅ | Full-mesh member added; all peers configure new connections |
| D∅ → ✕ | Full-mesh member removed; all remaining peers remove connections |
| sD∅ → ✕ | Full-mesh member removed; all remaining peers remove connections |
| D → ✕ | Force: full-mesh member removed; dead member excluded |
| sD → ✕ | Force: full-mesh member removed; dead member excluded |
| A → D∅ | Star → full-mesh; connections added to all peers |
| A → sD∅ | Star → full-mesh; connections added to all peers |
| TB → D∅ | Star → full-mesh; connections added to all peers |
| TB → sD∅ | Star → full-mesh; connections added to all peers |
| D∅ → A | Full-mesh → star; connections removed from non-FM peers |
| D∅ → TB | Full-mesh → star; connections removed from non-FM peers |
| sD∅ → A | Full-mesh → star; connections removed from non-FM peers |
| sD∅ → TB | Full-mesh → star; connections removed from non-FM peers |
| sD∅ → D∅ | Non-voter → voter; arr and voting changes on all peers |
| D∅ → sD∅ | Voter → non-voter; arr and voting changes on all peers |
| sD → D | Non-voter → voter; arr and voting changes on all peers |
| D → sD | Voter → non-voter; arr and voting changes on all peers |

**All replicas** — standalone qmr change (embedded in AddReplica/RemoveReplica):

| Step | Why |
|------|-----|
| qmr↑ | QMR raised; all replicas use qmr in their quorum path and must apply the new value |
| qmr↓ | QMR lowered; all replicas use qmr in their quorum path and must apply the new value |

**q change within a step**: Some steps include an atomic q change (q↑ or q↓).
When a step changes q together with the state transition, the wait set is the
**union** of the step's own wait set and the q-change wait set (D + D∅ replicas;
see §ChangeQuorum). In practice, all q-changing steps are already in the "All
replicas" group, so the union adds no extra replicas.

## Preconditions

### Preconditions for removing any member

Any `RemoveReplica(X)` transition is blocked until **all** of the following guards pass.

| # | Guard | Condition | Blocked message |
|---|-------|-----------|-----------------|
| 1 | **NotAttached** | `member.Attached == false` AND no active Detach transition for this member | "Cannot remove attached member" |

### Preconditions for leaving D

Any transition that removes a voter (D → anything, or RemoveReplica(D)) is blocked
until **all** of the following guards pass. The transition itself decides whether to
adjust q (q↓ variant or not) — the guards decide whether starting the transition is
safe at all.

| # | Guard | Condition | Blocked message |
|---|-------|-----------|-----------------|
| 1 | **VolumeAccessLocal** | `NOT (member.Attached AND volumeAccess == Local)`, skip if target is sD | "Cannot demote Diskful: volumeAccess=Local requires D on attached node" |
| 2 | **QMRReady** | `current_qmr ≤ target_GMDR + 1` | "ChangeQuorum not yet applied: qmr={q}, target={t}" |
| 3 | **GMDR** | `ADR > target_GMDR` (i.e. `UpToDate_D > qmr`) | "Would violate GMDR: ADR={p}, need > {t}" |
| 4 | **FTT** | `D_count > D_min` where `D_min = target_FTT + target_GMDR + 1` (i.e. `D_count > target_FTT + qmr`) | "Would violate FTT: D_count={d}, need > {min}" |
| 5 | **ZoneGMDR** | topology ≠ TransZonal, OR: losing any single zone after removal still leaves > target_GMDR D (see pseudocode below) | "Would violate zone GMDR: losing zone {z} would leave {n} D, need > {t}" |
| 6 | **ZoneFTT** | topology ≠ TransZonal, OR: losing any single zone after removal still leaves enough D voters for quorum (see pseudocode below) | "Would violate zone FTT: losing zone {z} would leave {d} voters, need > {min}" |

**Guard 5 (ZoneGMDR) pseudocode**:

```
for each zone z:
    UpToDate_in_zone = UpToDate_D_in_zone(z) - (1 if z == zone_of_removed_D else 0)
    UpToDate_surviving = (UpToDate_D_count - 1) - UpToDate_in_zone
    if UpToDate_surviving <= target_GMDR:  // i.e. < qmr
        blocked("losing zone %s after removal would leave %d UpToDate D, need > %d", z, UpToDate_surviving, target_GMDR)
```

**Guard 6 (ZoneFTT) pseudocode**:

Checks that after removal, losing any zone still leaves enough voters for quorum.
Must account for TB: if TB is lost with the zone, tiebreaker is unavailable.

```
q_after = floor((D_count - 1) / 2) + 1  // q the transition will set

for each zone z:
    D_lost = D_in_zone(z) - (1 if z == zone_of_removed_D else 0)
    D_surviving = (D_count - 1) - D_lost
    TB_surviving = TB_count - TB_in_zone(z)

    if D_surviving >= q_after:
        ok
    elif D_surviving == q_after - 1 and TB_surviving > 0:
        ok  // tiebreaker bridges the gap (§1.3)
    else:
        blocked("losing zone %s would leave %d D, q=%d, TB=%d", z, D_surviving, q_after, TB_surviving)
```

Inputs:
- `target_GMDR`, `target_FTT` — target FTT from `rv.Status.Configuration`
- `D_count` — current number of D voters (D + D∅)
- `UpToDate_D_count` — current number of UpToDate D replicas
- `ADR` = `UpToDate_D_count - 1` (see LAYOUTS.md §3.3)
- `D_in_zone(z)`, `UpToDate_D_in_zone(z)` — per-zone counts (TransZonal only)

**When guards fail**: the transition waits. Typical resolution:
- Guard 1 (VolumeAccessLocal): detach first, or use ChangeReplicaType(D, sD/A) strategy.
- Guard 2 (QMRReady): wait for ChangeQuorum to complete (all replicas confirmed new qmr).
- Guard 3 (GMDR): wait for replacement D to reach UpToDate.
- Guard 4 (FTT): wait for replacement D to be added, or for layout downgrade
  (ChangeQuorum lowers qmr / target_FTT first → guard passes).
- Guards 5–6 (Zone*): wait for same-zone replacement, or for zone redistribution.

### Preconditions for adding D

No blocking guards. Adding a D is always safe:

- **GMDR/FTT**: more copies + more voters → FTT can only improve, never degrade.
- **Split-brain**: prevented by the transitions themselves (vestibule + atomic q↑ for
  odd→even voters). No guard needed.
- **Zone placement** (TransZonal): advisory, not blocking. The scheduler chooses the best
  zone; in degraded scenarios any zone is acceptable.

### Preconditions for leaving TB

Any transition that removes a TB (TB → anything, or RemoveReplica(TB)) is blocked
until **all** of the following guards pass.

| # | Guard | Condition | Blocked message |
|---|-------|-----------|-----------------|
| 1 | **TBRequired** | `TB_count > TB_min` where `TB_min = 1 if D_count is even AND target_FTT = D_count / 2, else 0` (§3.4) | "TB required: D_count={d} even, FTT={ftt} = D/2" |
| 2 | **ZoneTBRequired** | topology ≠ TransZonal, OR: after removal, each zone that needs TB coverage still has it (see below) | "Would violate zone TB coverage for zone {z}" |

**Guard 2 (ZoneTBRequired) detail**: In TransZonal, TB placement ensures that losing
the TB's zone still leaves quorum (see LAYOUTS.md §11.1). Removing a TB is safe only if
another TB exists (replacement already added via §9 add-then-remove) or the layout no
longer requires TB (e.g. D count became odd after adding a D).

**When guards fail**: the transition waits. Typical resolution:
- Guard 1: add replacement TB first (§9 pattern), or add a D to make D_count odd (layout transition).
- Guard 2: add replacement TB in the same zone first.

### Preconditions for adding TB

No blocking guards. Adding a TB is always safe:

- **GMDR**: TB has no data — no impact.
- **FTT**: TB can only improve availability (tiebreaker for even-D layouts).
- **Split-brain**: TB is not a voter — no quorum impact.
- **Zone placement** (TransZonal): advisory, not blocking.

### Preconditions for ForceRemoveReplica

Any `ForceRemoveReplica(X)` transition is blocked until **all** of the following guards pass.

| # | Guard | Condition | Blocked message |
|---|-------|-----------|-----------------|
| 1 | **NotAttached** | `member.Attached == false` AND no active Attach or Detach transition for this member | "Cannot force-remove attached member; ForceDetach first" |
| 2 | **MemberUnreachable** | No replica with a ready agent has an established DRBD connection to this member (see pseudocode below) | "Force-removal blocked: member is reachable (connected from {n} replica(s))" |

**Guard 2 (MemberUnreachable) detail**:

The purpose of this guard is to prevent accidental force-removal of a member that is
actually alive and participating in the datamesh.

A replica has a **ready agent** if its `DRBDConfigured` condition reason is not
`AgentNotReady` (i.e., the agent is reporting status and `rvr.Status.Peers` is
fresh). A member is **unreachable** if no replica with a ready agent reports a
`Connected` connection state to it in `rvr.Status.Peers`.

```
for rvr in rvrs:
    if rvr.condition("DRBDConfigured").reason == "AgentNotReady":
        continue  // stale status, skip
    for peer in rvr.Status.Peers:
        if peer.Name == member.Name and peer.ConnectionState == "Connected":
            blocked("Force-removal blocked: member is reachable (connected from %s)", rvr.Name)
```

**When guards fail**: the transition waits. Typical resolution:
- Guard 1 (NotAttached): perform ForceDetach first.
- Guard 2 (MemberUnreachable): the member is still reachable — use normal
  RemoveReplica instead, or wait until the node is confirmed permanently failed.

## Transition parallelism

Transitions are grouped by what they affect:

| Group | Transitions | Affects |
|-------|-------------|---------|
| **VotingMembership** | AddReplica(D), RemoveReplica(D), ChangeReplicaType(X↔D) | voter count, q, quorum |
| **NonVotingMembership** | AddReplica(A\|TB\|sD), RemoveReplica(A\|TB\|sD), ChangeReplicaType among A/TB/sD | peer topology (non-voter) |
| **Quorum** | ChangeQuorum | q, qmr for all members |
| **Attachment** | Attach, Detach | per-member attachment state |
| **Multiattach** | EnableMultiattach, DisableMultiattach | multiattach for all members |
| **Emergency** | ForceDetach, ForceRemoveReplica | emergency recovery: detach + member removal |

Parallelism rules:

| Rule | Description |
|------|-------------|
| **One Voter at a time** | Voter transitions are serialized — only one active at a time. They change voter count and q, which must be computed sequentially. |
| **NonVoter can overlap** | Multiple non-voter transitions can run in parallel (independent star-topology members). |
| **Quorum is exclusive** | ChangeQuorum blocks all other transitions (changes q/qmr globally). |
| **Attachment can overlap** | Attach/Detach can run in parallel with each other and with membership transitions (subject to guards: NotAttached, VolumeAccessLocal). |
| **Multiattach is exclusive with Attachment** | EnableMultiattach/DisableMultiattach blocks Attach/Detach but not membership transitions. |
| **Voter blocks NonVoter of same member** | A non-voter transition on member X is blocked if a voter transition on member X is in progress (one transition per member). |
| **Formation is exclusive** | Formation blocks all other transitions, including Emergency. |
| **Emergency cancels same-member** | ForceRemoveReplica cancels any in-progress transition (from any group) for the dead member. ForceDetach cancels any in-progress Attach/Detach for the dead member. |
| **Emergency preempts VotingMembership and Quorum** | ForceRemoveReplica can run even while a Voter transition or ChangeQuorum for another member is in progress. It does not wait for them to complete (they may be stuck waiting for the dead member). The new DatameshRevision includes the correct q for the updated voter count; in-progress Voter transitions for living members continue with the adjusted state. |
| **Emergency can overlap with NonVotingMembership** | No conflict with non-voter transitions for other members. |
| **Emergency can overlap with Attachment** | ForceDetach handles attachment for the dead member. Other Attach/Detach transitions are unaffected. |
| **Emergency can overlap with Multiattach** | No conflict. ForceRemoveReplica removes the dead member from Multiattach wait sets, unblocking stuck Enable/DisableMultiattach transitions. |
| **Multiple Emergency operations** | Multiple ForceRemoveReplica operations can execute in a single reconciliation (e.g., two nodes failed). q is computed once for the final voter count. |

**Emergency preemption and transient q inconsistency**: When ForceRemoveReplica(D/D∅)
runs alongside an in-progress Voter transition that raised q, the new
DatameshRevision publishes the correct q for the updated voter count. During the
brief window while replicas converge, some may still have the old (higher) q. This
is safe: a higher q is strictly more conservative — it may suspend IO unnecessarily
but cannot cause split-brain. Normal Voter transitions avoid this via serialization;
Emergency operations accept the brief inconsistency for faster recovery.

---

## Configuration mutability (DRAFT, WIP)

### Configuration parameters

The volume configuration is defined by five parameters in `ReplicatedVolumeConfiguration`:
`replicatedStoragePoolName`, `failuresToTolerate` (FTT), `guaranteedMinimumDataRedundancy` (GMDR),
`topology`, and `volumeAccess`.

### Configuration sources

There are two ways to specify the configuration, controlled by `rv.Spec.ConfigurationMode`:

1. **Auto mode** (default, `configurationMode=Auto`) — configuration is derived from
   the referenced `ReplicatedStorageClass` (RSC). The RSC specifies either:
   - **New parameters**: `failuresToTolerate` and `guaranteedMinimumDataRedundancy`
     directly, or
   - **Legacy parameter**: `replication` (kept for backward compatibility).

   The RSC also specifies `topology`, `volumeAccess`, and the storage pool.

   When the legacy `replication` is used, it is mapped to FTT/GMDR as follows:

   | `replication` (legacy) | `failuresToTolerate` | `guaranteedMinimumDataRedundancy` | Layout |
   |------------------------|----------------------|-----------------------------------|--------|
   | None                   | 0                    | 0                                 | 1D     |
   | Availability           | 1                    | 0                                 | 2D+1TB |
   | Consistency            | 0                    | 1                                 | 2D     |
   | ConsistencyAndAvailability | 1                | 1                                 | 3D     |

2. **Manual mode** (`configurationMode=Manual`) — the RV specifies configuration
   directly in `rv.Spec.ManualConfiguration`, which contains `replicatedStoragePoolName`,
   `failuresToTolerate`, `guaranteedMinimumDataRedundancy`, `topology`, and `volumeAccess`.
   No RSC reference is used.

In both cases, `rv.Status.Configuration` always contains the resolved parameters
as a `ReplicatedVolumeConfiguration`. The legacy `replication` field never appears
in `rv.Status.Configuration`.

### Configuration lifecycle

- **During formation**: configuration is **frozen**. Changes to the source
  (RSC or ManualConfiguration) are ignored until formation completes.
- **During normal operation**: configuration is updated immediately when
  the source changes. (Temporary behavior — see below.)

### Configuration changes

**These values can change.** Two mechanisms:

1. **RSC rollout** (Auto mode) — when RSC config changes, it is gradually rolled out
   to all RVs of that RSC (rolling update). The controller must handle configuration
   changes on an already-formed datamesh (add/remove/retype members to match new
   FTT/GMDR/topology).

2. **Manual mode** — the user changes `rv.Spec.ManualConfiguration` directly.
   Same effect: configuration changes on an already-formed datamesh.

**Current vs planned behavior**: currently, `rv.Status.Configuration` is updated
immediately during normal operation. In the future, configuration changes during
normal operation will require an explicit `ReplicatedVolumeOperation` to authorize
the rollout. Without it, the controller will detect the pending change but will not
apply it until the operation is created.

### Effective layout

`rv.Status.EffectiveLayout` (FTT, GMDR) tracks the protection levels that the
datamesh **actually provides right now**. It is controller-owned state, not part
of `rv.Status.Datamesh` (which is the target configuration that replicas read
and converge to).

**Why it exists**: `rv.Status.Configuration` is the **desired** layout. When
configuration changes (e.g. FTT 1→2), the datamesh does not instantly have
2 fault tolerance — it still has 1 until the controller adds a replica and
completes the layout transition. Quorum (q) and quorum minimum redundancy (qmr)
must reflect reality, not the goal, so they are derived from EffectiveLayout.

**Monotonic ratchet semantics**:

- Values only increase (via layout transitions) until they reach the levels
  defined in Configuration.
- Once reached, they are held at that level.
- They decrease only if Configuration is lowered below the current effective
  values (explicit downgrade).

**Lifecycle**:

- **Formation**: set to Configuration values when members are first added
  (formation creates the exact layout matching configuration).
- **Formation restart**: reset to zero (datamesh is torn down).
- **Normal operation**: updated by the transition engine as part of step
  mutations. Each step that changes q or qmr updates EffectiveLayout
  atomically (not as a separate post-processing pass). For upgrades, each
  step raises the effective value toward Configuration. For downgrades, each
  step lowers it.

**Who updates EffectiveLayout**: The transition engine, inside step mutations.
Each step knows its semantics (q↑/q↓/qmr↑/qmr↓) and updates EffectiveLayout
as part of the same in-memory mutation that changes `Datamesh.Quorum` /
`Datamesh.QuorumMinimumRedundancy`. The caller (Reconcile method) does not
need to recompute EffectiveLayout after `engine.Run()` — it is already correct.

**Update rules per step type**:

- **Step with q↑** (e.g., `A → D∅ + q↑`): `EffectiveLayout.FTT` increases
  if the new voter count provides higher FTT than current effective.
- **Step with q↓** (e.g., `D∅ → A + q↓`): `EffectiveLayout.FTT` decreases
  to match the new voter count's FTT.
- **ChangeQuorum with qmr↑**: `EffectiveLayout.GMDR = new_qmr - 1`.
- **ChangeQuorum with qmr↓**: `EffectiveLayout.GMDR = new_qmr - 1`.
- **Steps without q/qmr change**: no EffectiveLayout update.

**Derived values**: `q` and `qmr` are computed from EffectiveLayout:

- `minD = effective_FTT + effective_GMDR + 1`
- `q = max(floor(voters/2) + 1, floor(minD/2) + 1)`
- `qmr = effective_GMDR + 1`
