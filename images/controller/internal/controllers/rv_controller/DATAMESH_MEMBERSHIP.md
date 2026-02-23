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
prepended at the beginning of RemoveReplica(D). A standalone ChangeQuorum transition
handles cases where q or qmr are incorrect but no matching membership transition is
pending (see §ChangeQuorum).

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

If the member is still attached, a ForceDetach (see §ForceDetach) MUST be
performed first to clear the attachment before removal.

Any in-progress transitions referencing the removed member are cleaned up as
part of the operation.

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

Standalone change of q and/or qmr, dispatched automatically when the published
quorum values diverge from the correct values for the current datamesh state.

Normal layout-driven qmr changes are embedded within AddReplica(D) (qmr↑) or
RemoveReplica(D) (qmr↓). When a pending D Join or Leave request exists and the
qmr discrepancy is exactly ±1, ChangeQuorum defers to the membership transition
that will handle qmr as part of its own steps. ChangeQuorum fires only when:

- q and/or qmr are incorrect **and** no matching membership transition is pending, or
- q and qmr diverge in different directions ("diagonal" case, e.g. q needs to
  go up but qmr needs to go down), or
- the discrepancy is larger than ±1 (corruption or multi-step reconfiguration).

**Correct q** is always `floor(voters / 2) + 1`. **Correct qmr** targets
`target_GMDR + 1` but is constrained: lowering is always safe; raising is limited
by the number of UpToDate D replicas (cannot require more copies than exist).

ChangeQuorum is a 2-step transition (one step per scalar):

| Plan | Step 1 | Step 2 |
|------|--------|--------|
| **lower** | set correct q | set correct qmr |
| **raise** | set correct qmr | set correct q |
| **lower-q-raise-qmr** | set correct q | set correct qmr |
| **raise-q-lower-qmr** | set correct qmr | set correct q |

The step that **lowers** protection goes first (so that the baseline GMDR is
updated before the raising step). This ensures external consumers never see an
intermediate state where the baseline exceeds the actual protection level.

Wait for confirmation depends on what changed:

- **q-only change**: wait for **D + D∅ replicas** only. A, TB, and sD always
  use q=32 (hardcoded); a q change does not affect them.
- **qmr change** (with or without q): wait for **all replicas** (D + D∅ + sD +
  sD∅ + A + TB). All replica types use qmr in their quorum path (D in the main
  condition, A/TB/sD in the quorate-peers path — see LAYOUTS.md §2.3).

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
| 1 | **NotAttached** | The member is not attached, and no Detach transition is in progress for it | "Cannot remove attached member" |

### Preconditions for leaving D

Any transition that removes a voter (D → anything, or RemoveReplica(D)) is blocked
until **all** of the following guards pass. The transition itself decides whether to
adjust q (q↓ variant or not) — the guards decide whether starting the transition is
safe at all.

| # | Guard | Condition | Blocked message |
|---|-------|-----------|-----------------|
| 1 | **VolumeAccessLocal** | The member is not attached with volumeAccess=Local (skipped when target is sD) | "Cannot demote Diskful: volumeAccess=Local requires D on attached node" |
| 2 | **GMDR** | Actual Data Redundancy exceeds target GMDR (UpToDate D count > qmr) | "Would violate GMDR: ADR={p}, need > {t}" |
| 3 | **FTT** | D count exceeds the minimum (D_min = target FTT + target GMDR + 1) | "Would violate FTT: D_count={d}, need > {min}" |
| 4 | **ZoneGMDR** | Topology is not TransZonal, OR: losing any single zone after removal still leaves enough UpToDate D to satisfy GMDR (see pseudocode below) | "Would violate zone GMDR: losing zone {z} would leave {n} D, need > {t}" |
| 5 | **ZoneFTT** | Topology is not TransZonal, OR: losing any single zone after removal still leaves enough D voters for quorum (see pseudocode below) | "Would violate zone FTT: losing zone {z} would leave {d} voters, need > {min}" |

**Guard 4 (ZoneGMDR) pseudocode**:

```
for each zone z:
    UpToDate_in_zone = UpToDate_D_in_zone(z) - (1 if z == zone_of_removed_D else 0)
    UpToDate_surviving = (UpToDate_D_count - 1) - UpToDate_in_zone
    if UpToDate_surviving <= target_GMDR:
        blocked
```

**Guard 5 (ZoneFTT) pseudocode**:

Checks that after removal, losing any zone still leaves enough voters for quorum.
Must account for TB: if TB is lost with the zone, tiebreaker is unavailable.

```
q_after = floor((D_count - 1) / 2) + 1

for each zone z:
    D_lost = D_in_zone(z) - (1 if z == zone_of_removed_D else 0)
    D_surviving = (D_count - 1) - D_lost
    TB_surviving = TB_count - TB_in_zone(z)

    if D_surviving >= q_after:
        ok
    elif D_surviving == q_after - 1 and TB_surviving > 0:
        ok  // tiebreaker bridges the gap (§1.3)
    else:
        blocked
```

Inputs:
- target_GMDR, target_FTT — from the resolved configuration
- D_count — current number of D voters (D + D∅)
- UpToDate_D_count — current number of UpToDate D replicas
- ADR = UpToDate_D_count − 1 (see LAYOUTS.md §3.3)
- D_in_zone(z), UpToDate_D_in_zone(z) — per-zone counts (TransZonal only)

**When guards fail**: the transition waits. Typical resolution:
- Guard 1 (VolumeAccessLocal): detach first, or use ChangeReplicaType(D, sD/A) strategy.
- Guard 2 (GMDR): wait for replacement D to reach UpToDate.
- Guard 3 (FTT): wait for replacement D to be added, or for layout downgrade.
- Guards 4–5 (Zone*): wait for same-zone replacement, or for zone redistribution.

### Preconditions for adding D

Any transition that adds a voter (AddReplica(D), ChangeReplicaType(X, D)) is blocked
until **all** of the following topology guards pass.

| # | Guard | Condition | Blocked message |
|---|-------|-----------|-----------------|
| 1 | **ZonalSameZone** | Topology is not Zonal, OR: the replica's zone is the primary zone (zone with most voters) | "Zonal: replica zone {z} differs from primary zone {p}" |
| 2 | **TransZonalVoterPlacement** | Topology is not TransZonal, OR: after adding the voter, losing any single zone still satisfies FTT and GMDR | "Would violate zone FTT/GMDR after placement" |

Non-topology safety is guaranteed by the transitions themselves:

- **GMDR/FTT**: more copies + more voters → FTT can only improve, never degrade.
- **Split-brain**: prevented by the transitions themselves (vestibule + atomic q↑ for
  odd→even voters). No guard needed.

### Preconditions for leaving TB

Any transition that removes a TB (TB → anything, or RemoveReplica(TB)) is blocked
until **all** of the following guards pass.

| # | Guard | Condition | Blocked message |
|---|-------|-----------|-----------------|
| 1 | **TBRequired** | TB count exceeds the minimum required (TB_min = 1 when D count is even and target FTT = D_count / 2, else 0; see §3.4) | "TB required: D_count={d} even, FTT={ftt} = D/2" |
| 2 | **ZoneTBRequired** | Topology is not TransZonal, OR: after removal, each zone that needs TB coverage still has it (see below) | "Would violate zone TB coverage for zone {z}" |

**Guard 2 (ZoneTBRequired) detail**: In TransZonal, TB placement ensures that losing
the TB's zone still leaves quorum (see LAYOUTS.md §11.1). Removing a TB is safe only if
another TB exists (replacement already added via §9 add-then-remove) or the layout no
longer requires TB (e.g. D count became odd after adding a D).

**When guards fail**: the transition waits. Typical resolution:
- Guard 1: add replacement TB first (§9 pattern), or add a D to make D_count odd (layout transition).
- Guard 2: add replacement TB in the same zone first.

### Preconditions for adding TB

Any transition that adds a TB (AddReplica(TB), ChangeReplicaType(X, TB)) is blocked
until **all** of the following topology guards pass.

| # | Guard | Condition | Blocked message |
|---|-------|-----------|-----------------|
| 1 | **ZonalSameZone** | Topology is not Zonal, OR: the replica's zone is the primary zone (zone with most voters) | "Zonal: replica zone {z} differs from primary zone {p}" |
| 2 | **TransZonalTBPlacement** | Topology is not TransZonal, OR: the target zone has at most 1 D voter (TB should be placed in a zone that does not already have a majority of voters) | "TransZonal: zone {z} has {n} D voters, TB should go to a zone with ≤1" |

Non-topology safety is inherent:

- **GMDR**: TB has no data — no impact.
- **FTT**: TB can only improve availability (tiebreaker for even-D layouts).
- **Split-brain**: TB is not a voter — no quorum impact.

### Preconditions for ForceRemoveReplica

Any `ForceRemoveReplica(X)` transition is blocked until **all** of the following guards pass.

| # | Guard | Condition | Blocked message |
|---|-------|-----------|-----------------|
| 1 | **NotAttached** | The member is not attached, and no Attach or Detach transition is in progress for it | "Cannot force-remove attached member; ForceDetach first" |
| 2 | **MemberUnreachable** | No replica with a ready agent has an established DRBD connection to this member (see detail below) | "Force-removal blocked: member is reachable (connected from {n} replica(s))" |

**Guard 2 (MemberUnreachable) detail**:

The purpose of this guard is to prevent accidental force-removal of a member that is
actually alive and participating in the datamesh.

A replica has a **ready agent** if its DRBD-configured status condition does not
indicate agent-not-ready (i.e., the agent is reporting fresh status and its peer
list is current). A member is **unreachable** if no replica with a ready agent
reports a Connected connection state to it in its peer list.

```
for each replica with a ready agent:
    for each peer in the replica's peer list:
        if peer refers to the target member and connection state is Connected:
            blocked — the member is reachable
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

The volume configuration is defined by five parameters: storage pool name,
failuresToTolerate (FTT), guaranteedMinimumDataRedundancy (GMDR), topology,
and volumeAccess.

### Configuration sources

There are two ways to specify the configuration, controlled by the configuration mode:

1. **Auto mode** (default) — configuration is derived from the referenced
   ReplicatedStorageClass (RSC). The RSC specifies either:
   - **New parameters**: FTT and GMDR directly, or
   - **Legacy parameter**: replication (kept for backward compatibility).

   The RSC also specifies topology, volumeAccess, and the storage pool.

   When the legacy replication parameter is used, it is mapped to FTT/GMDR as follows:

   | replication (legacy)       | FTT | GMDR | Layout |
   |----------------------------|-----|------|--------|
   | None                       | 0   | 0    | 1D     |
   | Availability               | 1   | 0    | 2D+1TB |
   | Consistency                | 0   | 1    | 2D     |
   | ConsistencyAndAvailability | 1   | 1    | 3D     |

2. **Manual mode** — the RV specifies configuration directly in its manual
   configuration section, which contains the same five parameters (storage pool,
   FTT, GMDR, topology, volumeAccess). No RSC reference is used.

In both cases, the resolved configuration in the volume status always contains
the effective parameters. The legacy replication field never appears in the
resolved configuration.

### Configuration lifecycle

- **During formation**: configuration is **frozen**. Changes to the source
  (RSC or manual configuration) are ignored until formation completes.
- **During normal operation**: configuration is updated immediately when
  the source changes. (Temporary behavior — see below.)

### Configuration changes

**These values can change.** Two mechanisms:

1. **RSC rollout** (Auto mode) — when RSC config changes, it is gradually rolled out
   to all RVs of that RSC (rolling update). The controller must handle configuration
   changes on an already-formed datamesh (add/remove/retype members to match new
   FTT/GMDR/topology).

2. **Manual mode** — the user changes the manual configuration directly.
   Same effect: configuration changes on an already-formed datamesh.

**Current vs planned behavior**: currently, the resolved configuration is updated
immediately during normal operation. In the future, configuration changes during
normal operation will require an explicit ReplicatedVolumeOperation to authorize
the rollout. Without it, the controller will detect the pending change but will not
apply it until the operation is created.

### Effective layout

The effective layout (FTT, GMDR) reflects the protection levels that the datamesh
**actually provides right now**. It is recomputed every reconciliation cycle from
the current cluster state — not from step mutations or transition progress.

**Why it exists**: the resolved configuration is the **desired** layout. When
configuration changes (e.g. FTT 1→2), the datamesh does not instantly have
2 fault tolerance — it still has 1 until new replicas are added and synced.
The effective layout reports what the cluster actually delivers.

**FTT and GMDR can be negative.** This indicates degradation: fewer healthy
voters or UpToDate copies than the quorum settings require. A negative FTT
means the system has already lost more voters than it can tolerate; a negative
GMDR means the number of UpToDate copies is below the quorum minimum redundancy.

**Computation**: effective FTT and GMDR are derived from:

- **reachable voters** — voters whose agent reports kernel-confirmed quorum, or
  (for stale agents) that are seen as Connected by at least one peer with a
  ready agent
- **UpToDate voters** — voters whose agent reports an UpToDate backing volume,
  or (for stale agents) that are seen as UpToDate by at least one peer
- **reachable TBs** — TBs whose agent is ready, or seen as Connected by a peer
- **q** and **qmr** — the currently published quorum and quorum minimum redundancy

Formulas:

- `effective_FTT = min(reachable_D − q + TB_bonus, UpToDate_D − qmr)`
  where TB_bonus = 1 when reachable_D is even and at least one TB is reachable
- `effective_GMDR = min(UpToDate_D, qmr) − 1`

When no voter members exist or no agent has fresh data, FTT and GMDR are
unavailable (nil).

**Observable counts**: the effective layout also exposes raw counts for
diagnostics: total/reachable/UpToDate voters, total/reachable TBs, stale agent
count, and a human-readable summary message.

**Baseline GMDR**: a separate scalar tracks the guaranteed minimum GMDR that
the datamesh has committed to. It is updated when qmr changes are confirmed by
replicas, using the formula: `baseline_GMDR = min(qmr − 1, target_GMDR)`.
The baseline ensures that external consumers (e.g. scheduling decisions) can
rely on a stable minimum without waiting for the per-cycle effective layout
recomputation.

**Lifecycle**:

- **Formation**: baseline GMDR is set from the configuration when members are
  first added. Effective layout is computed from initial cluster state.
- **Formation restart**: baseline GMDR is reset to zero.
- **Normal operation**: effective layout is recomputed each cycle. Baseline GMDR
  is updated by transition step callbacks when qmr changes are confirmed.
