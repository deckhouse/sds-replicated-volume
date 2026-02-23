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

- **AddMember(type)** — create a new member of the given type.
- **RemoveMember(type)** — remove an existing member of the given type.
- **ChangeMemberType(from, to)** — change a member's type.

Each concrete (from, to) pair determines the phases — including liminal intermediate
states, disk attach/detach, and quorum changes. Quorum changes (q↑/q↓) are phases
within these transitions when voter count changes. Standalone qmr changes during
layout transitions use a separate ChangeQuorum operation (see §ChangeQuorum).

### AddMember

| Transition | Path | When |
|------------|------|------|
| AddMember(A) | ✦ → A | always |
| AddMember(TB) | ✦ → TB | always |
| AddMember(D) | ✦ → D∅ → D | no flant-drbd, even→odd voters |
| AddMember(D) + q↑ | ✦ → A → D∅ + q↑ → D | no flant-drbd, odd→even voters |
| AddMember(D) via sD | ✦ → sD∅ → sD → D | flant-drbd, even→odd voters |
| AddMember(D) via sD + q↑ | ✦ → sD∅ → sD → sD∅ → D∅ + q↑ → D | flant-drbd, odd→even voters |
| AddMember(sD) | ✦ → sD∅ → sD | flant-drbd |

### RemoveMember

| Transition | Path | When |
|------------|------|------|
| RemoveMember(A) | A → ✕ | always |
| RemoveMember(TB) | TB → ✕ | always |
| RemoveMember(D) | D → D∅ → ✕ | odd→even voters |
| RemoveMember(D) + q↓ | D → D∅ → A + q↓ → ✕ | even→odd voters |
| RemoveMember(sD) | sD → sD∅ → ✕ | flant-drbd |

**Attached member strategy**: when the controller wants to remove a member that is
currently attached (serving IO), it uses `ChangeMemberType` instead of `RemoveMember`:
D → sD (flant-drbd) or D → A (no flant-drbd); non-diskful → A. The converted member
stays and continues serving IO. See preconditions for leaving D (VolumeAccessLocal guard)
and preconditions for removing any member (NotAttached guard).

### ChangeMemberType

| Transition | Path | When |
|------------|------|------|
| ChangeMemberType(A, TB) | A → TB | always |
| ChangeMemberType(TB, A) | TB → A | always |
| ChangeMemberType(A, D) | A → D∅ → D | no flant-drbd, even→odd voters |
| ChangeMemberType(A, D) + q↑ | A → D∅ + q↑ → D | no flant-drbd, odd→even voters |
| ChangeMemberType(A, D) via sD | A → sD∅ → sD → D | flant-drbd, even→odd voters |
| ChangeMemberType(A, D) via sD + q↑ | A → sD∅ → sD → sD∅ → D∅ + q↑ → D | flant-drbd, odd→even voters |
| ChangeMemberType(D, A) | D → D∅ → A | odd→even voters |
| ChangeMemberType(D, A) + q↓ | D → D∅ → A + q↓ | even→odd voters |
| ChangeMemberType(TB, D) | TB → D∅ → D | no flant-drbd, even→odd voters |
| ChangeMemberType(TB, D) + q↑ | TB → D∅ + q↑ → D | no flant-drbd, odd→even voters |
| ChangeMemberType(TB, D) via sD | TB → sD∅ → sD → D | flant-drbd, even→odd voters |
| ChangeMemberType(TB, D) via sD + q↑ | TB → sD∅ → sD → sD∅ → D∅ + q↑ → D | flant-drbd, odd→even voters |
| ChangeMemberType(D, TB) | D → D∅ → TB | odd→even voters |
| ChangeMemberType(D, TB) + q↓ | D → D∅ → TB + q↓ | even→odd voters |
| ChangeMemberType(A, sD) | A → sD∅ → sD | flant-drbd |
| ChangeMemberType(sD, A) | sD → sD∅ → A | flant-drbd |
| ChangeMemberType(TB, sD) | TB → sD∅ → sD | flant-drbd |
| ChangeMemberType(sD, TB) | sD → sD∅ → TB | flant-drbd |
| ChangeMemberType(sD, D) | sD → D | flant-drbd, even→odd voters |
| ChangeMemberType(sD, D) + q↑ | sD → sD∅ → D∅ + q↑ → D | flant-drbd, odd→even voters |
| ChangeMemberType(D, sD) | D → sD | flant-drbd, odd→even voters |
| ChangeMemberType(D, sD) + q↓ | D → D∅ → sD∅ + q↓ → sD | flant-drbd, even→odd voters |


### ChangeQuorum

Standalone change of q and/or qmr. Used for standalone qmr changes during layout
transitions and for emergency q adjustments.

New values are already in `rv.Status.Datamesh.Quorum` / `.QuorumMinimumRedundancy`.

### Preconditions for removing any member

Any `RemoveMember(X)` transition is blocked until **all** of the following guards pass.

| # | Guard | Condition | Blocked message |
|---|-------|-----------|-----------------|
| 1 | **NotAttached** | `member.Attached == false` AND no active Detach transition for this member | "Cannot remove attached member" |

### Preconditions for leaving D

Any transition that removes a voter (D → anything, or RemoveMember(D)) is blocked
until **all** of the following guards pass. The transition itself decides whether to
adjust q (q↓ variant or not) — the guards decide whether starting the transition is
safe at all.

| # | Guard | Condition | Blocked message |
|---|-------|-----------|-----------------|
| 1 | **VolumeAccessLocal** | `NOT (member.Attached AND volumeAccess == Local)`, skip if target is sD | "Cannot demote Diskful: volumeAccess=Local requires D on attached node" |
| 2 | **QMRReady** | `current_qmr ≤ target_FTT-BDL + 1` | "ChangeQuorum not yet applied: qmr={q}, target={t}" |
| 3 | **FTT-BDL** | `pFTT-BDL > target_FTT-BDL` (i.e. `UpToDate_D > qmr`) | "Would violate FTT-BDL: pFTT-BDL={p}, need > {t}" |
| 4 | **FTT-BUA** | `D_count > D_min` where `D_min = target_FTT-BUA + target_FTT-BDL + 1` (i.e. `D_count > target_FTT-BUA + qmr`) | "Would violate FTT-BUA: D_count={d}, need > {min}" |
| 5 | **ZoneFTT-BDL** | topology ≠ TransZonal, OR: losing any single zone after removal still leaves > target_FTT-BDL D (see pseudocode below) | "Would violate zone FTT-BDL: losing zone {z} would leave {n} D, need > {t}" |
| 6 | **ZoneFTT-BUA** | topology ≠ TransZonal, OR: losing any single zone after removal still leaves enough D voters for quorum (see pseudocode below) | "Would violate zone FTT-BUA: losing zone {z} would leave {d} voters, need > {min}" |

**Guard 5 (ZoneFTT-BDL) pseudocode**:

```
for each zone z:
    UpToDate_in_zone = UpToDate_D_in_zone(z) - (1 if z == zone_of_removed_D else 0)
    UpToDate_surviving = (UpToDate_D_count - 1) - UpToDate_in_zone
    if UpToDate_surviving <= target_FTT-BDL:  // i.e. < qmr
        blocked("losing zone %s after removal would leave %d UpToDate D, need > %d", z, UpToDate_surviving, target_FTT-BDL)
```

**Guard 6 (ZoneFTT-BUA) pseudocode**:

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
- `target_FTT-BDL`, `target_FTT-BUA` — target FTT from `rv.Status.Configuration`
- `D_count` — current number of D voters (D + D∅)
- `UpToDate_D_count` — current number of UpToDate D replicas
- `pFTT-BDL` = `UpToDate_D_count - 1` (see LAYOUTS.md §3.3)
- `D_in_zone(z)`, `UpToDate_D_in_zone(z)` — per-zone counts (TransZonal only)

**When guards fail**: the transition waits. Typical resolution:
- Guard 1 (VolumeAccessLocal): detach first, or use ChangeMemberType(D, sD/A) strategy.
- Guard 2 (QMRReady): wait for ChangeQuorum to complete (all replicas confirmed new qmr).
- Guard 3 (FTT-BDL): wait for replacement D to reach UpToDate.
- Guard 4 (FTT-BUA): wait for replacement D to be added, or for layout downgrade
  (ChangeQuorum lowers qmr / target_FTT_BUA first → guard passes).
- Guards 5–6 (Zone*): wait for same-zone replacement, or for zone redistribution.

### Preconditions for adding D

No blocking guards. Adding a D is always safe:

- **FTT-BDL/FTT-BUA**: more copies + more voters → FTT can only improve, never degrade.
- **Split-brain**: prevented by the transitions themselves (vestibule + atomic q↑ for
  odd→even voters). No guard needed.
- **Zone placement** (TransZonal): advisory, not blocking. The scheduler chooses the best
  zone; in degraded scenarios any zone is acceptable.

### Preconditions for leaving TB

Any transition that removes a TB (TB → anything, or RemoveMember(TB)) is blocked
until **all** of the following guards pass.

| # | Guard | Condition | Blocked message |
|---|-------|-----------|-----------------|
| 1 | **TBRequired** | `TB_count > TB_min` where `TB_min = 1 if D_count is even AND target_FTT-BUA = D_count / 2, else 0` (§3.4) | "TB required: D_count={d} even, FTT-BUA={bua} = D/2" |
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

- **FTT-BDL**: TB has no data — no impact.
- **FTT-BUA**: TB can only improve availability (tiebreaker for even-D layouts).
- **Split-brain**: TB is not a voter — no quorum impact.
- **Zone placement** (TransZonal): advisory, not blocking.

### Transition parallelism

Transitions are grouped by what they affect:

| Group | Transitions | Affects |
|-------|-------------|---------|
| **Voter** | AddMember(D), RemoveMember(D), ChangeMemberType(X↔D) | voter count, q, quorum |
| **NonVoter** | AddMember(A\|TB\|sD), RemoveMember(A\|TB\|sD), ChangeMemberType among A/TB/sD | peer topology (non-voter) |
| **Quorum** | ChangeQuorum | q, qmr for all members |
| **IO** | Attach, Detach | per-member IO state |
| **Multiattach** | EnableMultiattach, DisableMultiattach | multiattach for all members |

Parallelism rules:

| Rule | Description |
|------|-------------|
| **One Voter at a time** | Voter transitions are serialized — only one active at a time. They change voter count and q, which must be computed sequentially. |
| **NonVoter can overlap** | Multiple non-voter transitions can run in parallel (independent star-topology members). |
| **Quorum is exclusive** | ChangeQuorum blocks all other transitions (changes q/qmr globally). |
| **IO can overlap** | Attach/Detach can run in parallel with each other and with membership transitions (subject to guards: NotAttached, VolumeAccessLocal). |
| **Multiattach is exclusive with IO** | EnableMultiattach/DisableMultiattach blocks Attach/Detach but not membership transitions. |
| **Voter blocks NonVoter of same member** | A non-voter transition on member X is blocked if a voter transition on member X is in progress (one transition per member). |
| **Formation is exclusive** | Formation blocks all other transitions. |

---

## Configuration mutability (DRAFT, WIP)

### Current model

`replication`, `topology`, and `volumeAccess` are defined in the ReplicatedStorageClass (RSC)
and copied into `rv.Status.Configuration` at RV creation. The controller reads them from there.

**These values can change.** Two planned mechanisms:

1. **RSC rollout** — when RSC config changes, it is gradually rolled out to all RVs
   of that RSC (rolling update). The controller must handle configuration changes
   on an already-formed datamesh (add/remove/retype members to match new replication/topology).

2. **Manual mode on RV** — in the future, an RV can specify `replication`, `topology`,
   and `volumeAccess` directly (with an RSP reference instead of RSC). Same effect:
   these values may change at any time.

### Reconciliation modes

The controller operates in one of two modes with respect to member management:

**Managed mode** (default): the controller owns the intended member composition
(driven by `replication` + `topology`). It creates/deletes/retypes RVRs to converge
to the intended composition. However, **the storage administrator can influence the outcome** by:
- changing RVR `spec.type` — the controller accommodates if compatible with replication/topology;
- deleting an RVR — the controller creates a replacement;
- creating an RVR — the controller integrates it if compatible, ignores or deletes if not.

**Manual mode** (future): `replication` and `topology` are not reconciled. The controller
fully follows administrator-created RVRs and their types. It only manages quorum to match
the actual member composition. In this mode, the storage administrator is responsible
for creating the right number and types of replicas.

In both modes, the same set of membership transitions is used. The difference is only
in **what drives the intent**: configuration vs user-specified RVRs.

## Interaction with Attach/Detach

Member transitions are mostly independent from Attach/Detach/Multiattach. Two exceptions:

1. **Cannot remove while attached or detaching.**
   RemoveAccess/RemoveTieBreaker/RemoveShadowDiskful is blocked if the member has
   `Attached=true` or an active Detach transition.
   (Diskful cannot be removed directly — it must be demoted first.)

2. **Cannot demote Diskful while attached, if volumeAccess=Local.**
   DemoteFromDiskful is blocked if the member has `Attached=true`
   and `volumeAccess=Local`, because Local requires a Diskful replica on the attached node.

All other member transitions (Add*, Promote*, DemoteFromTieBreaker) do not conflict
with Attach/Detach/Multiattach.
