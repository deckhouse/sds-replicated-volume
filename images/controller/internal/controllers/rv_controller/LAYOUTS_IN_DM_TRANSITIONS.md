# DRBD Volume Layout Procedures as Datamesh Transitions

This document re-expresses the DRBD-level procedures from LAYOUTS.md §8–§11 as sequences
of datamesh membership transitions defined in DATAMESH_MEMBERSHIP.md. For each procedure,
the three safety constraints are verified at every transition boundary:

1. **Never reduce GMDR/FTT** below the target level
2. **Never interrupt IO** (maintain quorum throughout)
3. **Never create split-brain risk** (maintain safe quorum settings)

Discrepancies between the LAYOUTS.md step-by-step procedures and the DM transition paths
are highlighted inline with `> **Discrepancy**` markers and summarized in §12.

## Transition Mapping Reference

| LAYOUTS.md action | DM transition | Group |
|-------------------|---------------|-------|
| Add A / TB | AddReplica(A), AddReplica(TB) | NonVotingMembership |
| Add D∅, attach → D (even→odd voters) | AddReplica(D) | VotingMembership |
| Add D∅, attach → D + raise qmr | AddReplica(D) + qmr↑ | VotingMembership |
| Add A → D∅+q↑, attach → D (odd→even voters) | AddReplica(D) + q↑ | VotingMembership |
| Add A → D∅+q↑, attach → D + raise qmr | AddReplica(D) + q↑ + qmr↑ | VotingMembership |
| Add sD∅ → sD → D (even→odd, flant-drbd) | AddReplica(D) via sD | VotingMembership |
| Add sD∅ → sD → D + raise qmr | AddReplica(D) via sD + qmr↑ | VotingMembership |
| Add sD∅ → sD → sD∅ → D∅+q↑ → D (odd→even, flant-drbd) | AddReplica(D) via sD + q↑ | VotingMembership |
| Add sD∅ → sD → sD∅ → D∅+q↑ → D + raise qmr | AddReplica(D) via sD + q↑ + qmr↑ | VotingMembership |
| Remove A / TB | RemoveReplica(A), RemoveReplica(TB) | NonVotingMembership |
| D → D∅ → remove (odd→even voters) | RemoveReplica(D) | VotingMembership |
| Lower qmr + D → D∅ → remove | qmr↓ + RemoveReplica(D) | VotingMembership |
| D → D∅ → A+q↓ → remove (even→odd voters) | RemoveReplica(D) + q↓ | VotingMembership |
| Lower qmr + D → D∅ → A+q↓ → remove | qmr↓ + RemoveReplica(D) + q↓ | VotingMembership |
| Raise / lower qmr (standalone, explicit only) | ChangeQuorum | Quorum (exclusive) |

**Parallelism rules** (from DATAMESH_MEMBERSHIP.md):

- **Voter** transitions are serialized (one at a time).
- **NonVoter** transitions can run in parallel with each other and with Voter transitions
  (different members).
- **ChangeQuorum** is exclusive — blocks all other transitions.

---

## 8. Diskful Replica Replacement

### Structural Patterns

Two patterns cover all 7 layouts, determined by the parity of D (voter count) before
adding the replacement:

| D parity | DM add sequence (without sD) | DM add sequence (with sD) | DM remove sequence |
|----------|------------------------------|---------------------------|--------------------|
| **Even** | AddReplica(D) | AddReplica(D) via sD | RemoveReplica(D) |
| **Odd** | AddReplica(D) + q↑ | AddReplica(D) via sD + q↑ | RemoveReplica(D) + q↓ |

A replacement = **add** new D (Voter), then **remove** old D (Voter). These two Voter
transitions execute sequentially (Voter serialization rule).

The **remove** side is the same regardless of sD. Only the **add** side differs.

### 8.1 Replacing in 1D (GMDR=0, FTT=0)

**Pattern**: Odd D (1). Add with q↑, remove with q↓.

#### 8.1a Without sD

| # | DM transition | Path | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|------|------------------|---|-----|---------|----------|---------|
| — | — | — | **D**(#0) | 1 | 1 | 0 | 0 | 0 |
| 1 | AddReplica(D) + q↑ | ✦ → A → D∅ + q↑ → D | **D**(#0) + **D**(#1) | 2 | 1 | 0 | 1 | 0 |
| 2 | RemoveReplica(D) + q↓ | D → D∅ → A + q↓ → ✕ | **D**(#1) | 1 | 1 | 0 | 0 | 0 |

**Constraint verification**:

| # | GMDR ≥ 0? | IO maintained? | Split-brain safe? |
|---|---------------|----------------|-------------------|
| Start | 0 ≥ 0 ✓ | q=1, 1 voter ✓ | 1 voter, q=1 ✓ |
| After 1 | 0 ≥ 0 ✓ | q=2, 2 voters UtD ✓ | 2 voters, q=2 ✓ |
| After 2 | 0 ≥ 0 ✓ | q=1, 1 voter ✓ | 1 voter, q=1 ✓ |

Matches LAYOUTS.md §8.1a. No discrepancy. ✓

**Note on internal steps**: During AddReplica(D) + q↑, after the A → D∅ + q↑ step but
before disk attach completes, there are 2 voters (D + D∅) with q=2. A crash of either
causes quorum loss. This matches the "temporary operational impact" noted in LAYOUTS.md —
the **guarantee level** (FTT=0) is unchanged, but the failure surface is wider during
full resync.

#### 8.1b With sD

| # | DM transition | Path | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|------|------------------|---|-----|---------|----------|---------|
| — | — | — | **D**(#0) | 1 | 1 | 0 | 0 | 0 |
| 1 | AddReplica(D) via sD + q↑ | ✦ → sD∅ → sD → sD∅ → D∅ + q↑ → D | **D**(#0) + **D**(#1) | 2 | 1 | 0 | 1 | 0 |
| 2 | RemoveReplica(D) + q↓ | D → D∅ → A + q↓ → ✕ | **D**(#1) | 1 | 1 | 0 | 0 | 0 |

Same boundary states as §8.1a. Constraint verification identical. ✓

Odd D → both LAYOUTS.md and DM use the sD∅ → D∅ + q↑ path. No discrepancy. ✓

---

### 8.2 Replacing in 2D + 1TB (GMDR=0, FTT=1)

**Pattern**: Even D (2). Add directly, remove directly.

#### 8.2a Without sD

| # | DM transition | Path | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|------|------------------|---|-----|---------|----------|---------|
| — | — | — | **D**(#0) + **D**(#1) + TB | 2 | 1 | 0 | 1 | 1 |
| 1 | AddReplica(D) | ✦ → D∅ → D | 3**D** + TB | 2 | 1 | 0 | 2 | 1 |
| 2 | RemoveReplica(D) | D → D∅ → ✕ | **D**(#1) + **D**(#2) + TB | 2 | 1 | 0 | 1 | 1 |

**Constraint verification**:

| # | GMDR ≥ 0? | FTT ≥ 1? | IO maintained? | Split-brain safe? |
|---|---------------|---------------|----------------|-------------------|
| Start | 0 ≥ 0 ✓ | 1 ≥ 1 ✓ | 2 voters, q=2 ✓ | q=2, 2 voters + TB ✓ |
| After 1 | 0 ≥ 0 ✓ | 1 ≥ 1 ✓ | 3 voters, q=2 ✓ | q=2, 3 voters (odd) ✓ |
| After 2 | 0 ≥ 0 ✓ | 1 ≥ 1 ✓ | 2 voters, q=2 ✓ | q=2, 2 voters + TB ✓ |

FTT=1 at transition 1 boundary (3D + TB): any single failure leaves ≥2 voters with
q=2 (main quorum) or 1 voter + TB (tiebreaker). ✓

Matches LAYOUTS.md §8.2a. No discrepancy. ✓

#### 8.2b With sD

| # | DM transition | Path | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|------|------------------|---|-----|---------|----------|---------|
| — | — | — | **D**(#0) + **D**(#1) + TB | 2 | 1 | 0 | 1 | 1 |
| 1 | AddReplica(D) via sD | ✦ → sD∅ → sD → D | 3**D** + TB | 2 | 1 | 0 | 2 | 1 |
| 2 | RemoveReplica(D) | D → D∅ → ✕ | **D**(#1) + **D**(#2) + TB | 2 | 1 | 0 | 1 | 1 |

Same boundary states as §8.2a. Constraint verification identical. ✓

---

### 8.3 Replacing in 2D (GMDR=1, FTT=0)

**Pattern**: Even D (2). Add directly, remove directly.

#### 8.3a Without sD

| # | DM transition | Path | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|------|------------------|---|-----|---------|----------|---------|
| — | — | — | **D**(#0) + **D**(#1) | 2 | 2 | 1 | 1 | 0 |
| 1 | AddReplica(D) | ✦ → D∅ → D | 3**D** | 2 | 2 | 1 | 2 | 0 |
| 2 | RemoveReplica(D) | D → D∅ → ✕ | **D**(#1) + **D**(#2) | 2 | 2 | 1 | 1 | 0 |

**Constraint verification**:

| # | GMDR ≥ 1? | IO maintained? | Split-brain safe? |
|---|---------------|----------------|-------------------|
| Start | 1 ≥ 1 ✓ | 2 voters UtD, q=2 ✓ | q=2, 2 voters ✓ |
| After 1 | 1 ≥ 1 ✓ | 3 voters, q=2 ✓ | q=2, 3 voters (odd) ✓ |
| After 2 | 1 ≥ 1 ✓ | 2 voters UtD, q=2 ✓ | q=2, 2 voters ✓ |

Matches LAYOUTS.md §8.3a. No discrepancy. ✓

#### 8.3b With sD

| # | DM transition | Path | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|------|------------------|---|-----|---------|----------|---------|
| — | — | — | **D**(#0) + **D**(#1) | 2 | 2 | 1 | 1 | 0 |
| 1 | AddReplica(D) via sD | ✦ → sD∅ → sD → D | 3**D** | 2 | 2 | 1 | 2 | 0 |
| 2 | RemoveReplica(D) | D → D∅ → ✕ | **D**(#1) + **D**(#2) | 2 | 2 | 1 | 1 | 0 |

Same boundary states. Constraint verification identical. ✓

---

### 8.4 Replacing in 3D (GMDR=1, FTT=1)

**Pattern**: Odd D (3). Add with q↑, remove with q↓.

#### 8.4a Without sD

| # | DM transition | Path | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|------|------------------|---|-----|---------|----------|---------|
| — | — | — | 3**D** | 2 | 2 | 1 | 2 | 1 |
| 1 | AddReplica(D) + q↑ | ✦ → A → D∅ + q↑ → D | 4**D** | 3 | 2 | 1 | 3 | 1 |
| 2 | RemoveReplica(D) + q↓ | D → D∅ → A + q↓ → ✕ | 3**D** | 2 | 2 | 1 | 2 | 1 |

**Constraint verification**:

| # | GMDR ≥ 1? | FTT ≥ 1? | IO maintained? | Split-brain safe? |
|---|---------------|---------------|----------------|-------------------|
| Start | 1 ≥ 1 ✓ | 1 ≥ 1 ✓ | 3 voters, q=2 ✓ | q=2, 3 voters (odd) ✓ |
| After 1 | 1 ≥ 1 ✓ | 1 ≥ 1 ✓ | 4 voters, q=3 ✓ | q=3, 4 voters (3-1 partition safe) ✓ |
| After 2 | 1 ≥ 1 ✓ | 1 ≥ 1 ✓ | 3 voters, q=2 ✓ | q=2, 3 voters (odd) ✓ |

FTT=1 at transition 1 boundary (4D, q=3): any single D failure leaves 3 voters with
3 ≥ 3 ✓ (main quorum), and utd=3 ≥ qmr=2 ✓. ✓

Matches LAYOUTS.md §8.4a. No discrepancy. ✓

#### 8.4b With sD

| # | DM transition | Path | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|------|------------------|---|-----|---------|----------|---------|
| — | — | — | 3**D** | 2 | 2 | 1 | 2 | 1 |
| 1 | AddReplica(D) via sD + q↑ | ✦ → sD∅ → sD → sD∅ → D∅ + q↑ → D | 4**D** | 3 | 2 | 1 | 3 | 1 |
| 2 | RemoveReplica(D) + q↓ | D → D∅ → A + q↓ → ✕ | 3**D** | 2 | 2 | 1 | 2 | 1 |

Odd D → both documents use sD∅ → D∅ + q↑ path. No discrepancy. ✓

---

### 8.5 Replacing in 4D + 1TB (GMDR=1, FTT=2)

**Pattern**: Even D (4). Add directly, remove directly.

#### 8.5a Without sD

| # | DM transition | Path | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|------|------------------|---|-----|---------|----------|---------|
| — | — | — | 4**D** + TB | 3 | 2 | 1 | 3 | 2 |
| 1 | AddReplica(D) | ✦ → D∅ → D | 5**D** + TB | 3 | 2 | 1 | 4 | 2 |
| 2 | RemoveReplica(D) | D → D∅ → ✕ | 4**D** + TB | 3 | 2 | 1 | 3 | 2 |

**Constraint verification**:

| # | GMDR ≥ 1? | FTT ≥ 2? | IO maintained? | Split-brain safe? |
|---|---------------|---------------|----------------|-------------------|
| Start | 1 ≥ 1 ✓ | 2 ≥ 2 ✓ | 4 voters, q=3 ✓ | q=3, 4 voters + TB ✓ |
| After 1 | 1 ≥ 1 ✓ | 2 ≥ 2 ✓ | 5 voters, q=3 ✓ | q=3, 5 voters (odd) ✓ |
| After 2 | 1 ≥ 1 ✓ | 2 ≥ 2 ✓ | 4 voters, q=3 ✓ | q=3, 4 voters + TB ✓ |

FTT=2 at transition 1 boundary (5D + TB): 2D fail → 3 voters, 3 ≥ 3 ✓, utd=3 ≥ 2 ✓.
1D + TB fail → 4 voters, 4 ≥ 3 ✓. ✓

Matches LAYOUTS.md §8.5a. No discrepancy. ✓

#### 8.5b With sD

| # | DM transition | Path | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|------|------------------|---|-----|---------|----------|---------|
| — | — | — | 4**D** + TB | 3 | 2 | 1 | 3 | 2 |
| 1 | AddReplica(D) via sD | ✦ → sD∅ → sD → D | 5**D** + TB | 3 | 2 | 1 | 4 | 2 |
| 2 | RemoveReplica(D) | D → D∅ → ✕ | 4**D** + TB | 3 | 2 | 1 | 3 | 2 |

No discrepancy. ✓

---

### 8.6 Replacing in 4D (GMDR=2, FTT=1)

**Pattern**: Even D (4). Add directly, remove directly.

#### 8.6a Without sD

| # | DM transition | Path | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|------|------------------|---|-----|---------|----------|---------|
| — | — | — | 4**D** | 3 | 3 | 2 | 3 | 1 |
| 1 | AddReplica(D) | ✦ → D∅ → D | 5**D** | 3 | 3 | 2 | 4 | 1 |
| 2 | RemoveReplica(D) | D → D∅ → ✕ | 4**D** | 3 | 3 | 2 | 3 | 1 |

**Constraint verification**:

| # | GMDR ≥ 2? | FTT ≥ 1? | IO maintained? | Split-brain safe? |
|---|---------------|---------------|----------------|-------------------|
| Start | 2 ≥ 2 ✓ | 1 ≥ 1 ✓ | 4 voters, q=3 ✓ | q=3, 4 voters ✓ |
| After 1 | 2 ≥ 2 ✓ | 1 ≥ 1 ✓ | 5 voters, q=3 ✓ | q=3, 5 voters (odd) ✓ |
| After 2 | 2 ≥ 2 ✓ | 1 ≥ 1 ✓ | 4 voters, q=3 ✓ | q=3, 4 voters ✓ |

Matches LAYOUTS.md §8.6a. No discrepancy. ✓

#### 8.6b With sD

| # | DM transition | Path | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|------|------------------|---|-----|---------|----------|---------|
| — | — | — | 4**D** | 3 | 3 | 2 | 3 | 1 |
| 1 | AddReplica(D) via sD | ✦ → sD∅ → sD → D | 5**D** | 3 | 3 | 2 | 4 | 1 |
| 2 | RemoveReplica(D) | D → D∅ → ✕ | 4**D** | 3 | 3 | 2 | 3 | 1 |

No discrepancy. ✓

---

### 8.7 Replacing in 5D (GMDR=2, FTT=2)

**Pattern**: Odd D (5). Add with q↑, remove with q↓.

#### 8.7a Without sD

| # | DM transition | Path | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|------|------------------|---|-----|---------|----------|---------|
| — | — | — | 5**D** | 3 | 3 | 2 | 4 | 2 |
| 1 | AddReplica(D) + q↑ | ✦ → A → D∅ + q↑ → D | 6**D** | 4 | 3 | 2 | 5 | 2 |
| 2 | RemoveReplica(D) + q↓ | D → D∅ → A + q↓ → ✕ | 5**D** | 3 | 3 | 2 | 4 | 2 |

**Constraint verification**:

| # | GMDR ≥ 2? | FTT ≥ 2? | IO maintained? | Split-brain safe? |
|---|---------------|---------------|----------------|-------------------|
| Start | 2 ≥ 2 ✓ | 2 ≥ 2 ✓ | 5 voters, q=3 ✓ | q=3, 5 voters (odd) ✓ |
| After 1 | 2 ≥ 2 ✓ | 2 ≥ 2 ✓ | 6 voters, q=4 ✓ | q=4, 6 voters (4-2 safe) ✓ |
| After 2 | 2 ≥ 2 ✓ | 2 ≥ 2 ✓ | 5 voters, q=3 ✓ | q=3, 5 voters (odd) ✓ |

FTT=2 at transition 1 boundary (6D, q=4): 2D fail → 4 voters, 4 ≥ 4 ✓, utd=4 ≥ 3 ✓. ✓

Matches LAYOUTS.md §8.7a. No discrepancy. ✓

#### 8.7b With sD

| # | DM transition | Path | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|------|------------------|---|-----|---------|----------|---------|
| — | — | — | 5**D** | 3 | 3 | 2 | 4 | 2 |
| 1 | AddReplica(D) via sD + q↑ | ✦ → sD∅ → sD → sD∅ → D∅ + q↑ → D | 6**D** | 4 | 3 | 2 | 5 | 2 |
| 2 | RemoveReplica(D) + q↓ | D → D∅ → A + q↓ → ✕ | 5**D** | 3 | 3 | 2 | 4 | 2 |

Odd D → both documents use sD∅ → D∅ + q↑ path. No discrepancy. ✓

---

## 9. TieBreaker Replica Replacement

TB replicas have no data and no disk. Replacement is a simple add-then-remove using
NonVoter transitions that can run concurrently with Voter transitions on other members.

### 9.1 Replacing TB in 2D + 1TB (GMDR=0, FTT=1)

| # | DM transition | Path | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|------|------------------|---|-----|---------|----------|---------|
| — | — | — | 2**D** + **TB**(#0) | 2 | 1 | 0 | 1 | 1 |
| 1 | AddReplica(TB) | ✦ → TB | 2**D** + **TB**(#0) + **TB**(#1) | 2 | 1 | 0 | 1 | 1 |
| 2 | RemoveReplica(TB) | TB → ✕ | 2**D** + **TB**(#1) | 2 | 1 | 0 | 1 | 1 |

**Constraint verification**:

| # | GMDR ≥ 0? | FTT ≥ 1? | IO maintained? | Split-brain safe? |
|---|---------------|---------------|----------------|-------------------|
| Start | ✓ | ✓ | ✓ | ✓ |
| After 1 | ✓ | ✓ (2TB: both needed for tiebreaker in degraded) | ✓ | ✓ |
| After 2 | ✓ | ✓ | ✓ | ✓ |

**Note on 2TB window**: With 2 TBs, `diskless_majority_at` = 2. After 1D failure, the
system depends on both TBs staying connected (vs 1 TB in steady state). The FTT=1
guarantee is maintained, but operational risk is higher. Minimize 2TB duration.

Matches LAYOUTS.md §9.1. No discrepancy. ✓

### 9.2 Replacing TB in 4D + 1TB (GMDR=1, FTT=2)

| # | DM transition | Path | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|------|------------------|---|-----|---------|----------|---------|
| — | — | — | 4**D** + **TB**(#0) | 3 | 2 | 1 | 3 | 2 |
| 1 | AddReplica(TB) | ✦ → TB | 4**D** + **TB**(#0) + **TB**(#1) | 3 | 2 | 1 | 3 | 2 |
| 2 | RemoveReplica(TB) | TB → ✕ | 4**D** + **TB**(#1) | 3 | 2 | 1 | 3 | 2 |

**Constraint verification**:

| # | GMDR ≥ 1? | FTT ≥ 2? | IO maintained? | Split-brain safe? |
|---|---------------|---------------|----------------|-------------------|
| All steps | ✓ | ✓ | ✓ | ✓ |

Same 2TB threshold note as §9.1: after 2D failures, tiebreaker depends on both TBs.

Matches LAYOUTS.md §9.2. No discrepancy. ✓

---

## 10. Layout Transitions

Layout transitions change GMDR or FTT by exactly ±1 per step. Each transition
is expressed as an ordered sequence of DM transitions. The DM parallelism rules and
guards (from DATAMESH_MEMBERSHIP.md) enforce safe ordering.

### Transition summary in DM terms

qmr changes are embedded as steps within AddReplica(D) (qmr↑ at end) or
RemoveReplica(D) (qmr↓ at start). No separate ChangeQuorum transitions needed.

| # | Edge | Upgrade DM sequence | Downgrade DM sequence |
|---|------|--------------------|-----------------------|
| 1 | 1D ↔ 2D | AddReplica(D)+q↑+qmr↑ | qmr↓+RemoveReplica(D)+q↓ |
| 2 | 1D ↔ 2D+TB | AddReplica(D)+q↑, AddReplica(TB) | RemoveReplica(TB), RemoveReplica(D)+q↓ |
| 3 | 2D ↔ 3D | AddReplica(D) | RemoveReplica(D) |
| 4 | 2D+TB ↔ 3D | AddReplica(D)+qmr↑, RemoveReplica(TB) | AddReplica(TB), qmr↓+RemoveReplica(D) |
| 5 | 3D ↔ 4D | AddReplica(D)+q↑+qmr↑ | qmr↓+RemoveReplica(D)+q↓ |
| 6 | 3D ↔ 4D+TB | AddReplica(D)+q↑, AddReplica(TB) | RemoveReplica(TB), RemoveReplica(D)+q↓ |
| 7 | 4D ↔ 5D | AddReplica(D) | RemoveReplica(D) |
| 8 | 4D+TB ↔ 5D | AddReplica(D)+qmr↑, RemoveReplica(TB) | AddReplica(TB), qmr↓+RemoveReplica(D) |

Transitions #5, #6, #8 are structurally identical to #1, #2, #4 at higher D/q/qmr values.
Transitions #3 and #7 are single Voter transitions (no q/qmr change, even D).
Transitions #1, #4, #5, #8 embed qmr changes — one fewer transition per edge vs before.

### DM parallelism analysis

With qmr changes embedded in AddReplica(D)/RemoveReplica(D), the sequencing is simpler:
no separate ChangeQuorum transitions to coordinate.

| Transition type | Sequencing constraint | Reason |
|-----------------|----------------------|--------|
| Voter → Voter | Strictly sequential | Voter serialization rule |
| Voter → NonVoter | Can overlap (different members) | Different groups |
| NonVoter → Voter | Can overlap (different members) | Different groups |

In practice, DM guards enforce safe ordering even when transitions could theoretically
overlap:

- **RemoveReplica(TB)**: gated by `TBRequired` guard. The guard passes only when TB is no
  longer needed (e.g. D count became odd after AddReplica(D) completes).
- **RemoveReplica(D)**: gated by `GMDR`, `FTT`, `ZoneFTT-*` guards. These pass
  only when enough D voters exist (e.g. after replacement AddReplica(D) completes).

> **Discrepancy (parallelism)**: LAYOUTS.md procedures are strictly sequential.
> DM parallelism rules allow NonVoter transitions (AddReplica(TB), RemoveReplica(TB)) to
> overlap with Voter transitions on different members. In practice, guards enforce the
> same logical ordering. **Criticality**: Low — only affects execution speed, not safety.

### 10.1 1D ↔ 2D (GMDR: 0↔1)

Changes: D 1↔2, q 1↔2, qmr 1↔2.

#### Upgrade: 1D → 2D

| # | DM transition | Group | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|-------|------------------|---|-----|---------|----------|---------|
| — | — | — | **D**(#0) | 1 | 1 | 0 | 0 | 0 |
| 1 | AddReplica(D) + q↑ + qmr↑ | Voter | **D**(#0) + **D**(#1) | 2 | 2 | 1 | 1 | 0 |

Single transition with embedded qmr↑ as the last step. The qmr raise happens after the
new D is UpToDate (quorum check `utd ≥ qmr` passes: 2 ≥ 2 ✓).

**Constraint verification**:

| # | GMDR ≥ min(0,1)=0? | FTT ≥ min(0,0)=0? | IO? | Split-brain? |
|---|------------------------|------------------------|-----|-------------|
| After 1 | 1 ≥ 0 ✓ | 0 ≥ 0 ✓ | q=2, 2 UtD ✓ | q=2, 2 voters ✓ |

Matches LAYOUTS.md §10.1 upgrade. ✓

#### Downgrade: 2D → 1D

| # | DM transition | Group | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|-------|------------------|---|-----|---------|----------|---------|
| — | — | — | **D**(#0) + **D**(#1) | 2 | 2 | 1 | 1 | 0 |
| 1 | qmr↓ + RemoveReplica(D) + q↓ | Voter | **D**(#1) | 1 | 1 | 0 | 0 | 0 |

Single transition with embedded qmr↓ as the first step. The qmr is lowered before the
D removal steps begin, relaxing the quorum check. No QMRReady guard needed — the
transition handles qmr↓ internally.

**Constraint verification**:

| # | GMDR ≥ min(1,0)=0? | FTT ≥ min(0,0)=0? | IO? | Split-brain? |
|---|------------------------|------------------------|-----|-------------|
| After 1 | 0 ≥ 0 ✓ | 0 ≥ 0 ✓ | q=1, 1 voter ✓ | q=1, 1 voter ✓ |

Matches LAYOUTS.md §10.1 downgrade. ✓

---

### 10.2 1D ↔ 2D+TB (FTT: 0↔1)

Changes: D 1↔2, TB 0↔1, q 1↔2. No qmr change (stays 1).

#### Upgrade: 1D → 2D+TB

| # | DM transition | Group | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|-------|------------------|---|-----|---------|----------|---------|
| — | — | — | **D**(#0) | 1 | 1 | 0 | 0 | 0 |
| 1 | AddReplica(D) + q↑ | Voter | **D**(#0) + **D**(#1) | 2 | 1 | 0 | 1 | 0 |
| 2 | AddReplica(TB) | NonVoter | **D**(#0) + **D**(#1) + TB | 2 | 1 | 0 | 1 | 1 |

**Ordering**: AddReplica(D) is Voter, AddReplica(TB) is NonVoter. DM parallelism allows
overlap. However, TB is useless without 2D (no tiebreaker for 1 voter). Logically,
AddReplica(D) should complete first. The DM `TBRequired` guard is not relevant here
(that's for removal). No guard enforces ordering — the controller should sequence
AddReplica(D) before AddReplica(TB) explicitly.

> **Discrepancy (parallelism)**: AddReplica(TB) could theoretically start before
> AddReplica(D) completes per DM rules. No guard prevents this. Safe (adding TB to 1D is
> harmless), but useless. LAYOUTS.md sequences them correctly: D first, TB second.
> **Criticality**: Low — adding TB early is safe, just wasteful.

**Constraint verification**:

| # | GMDR ≥ 0? | FTT ≥ min(0,1)=0? | IO? | Split-brain? |
|---|---------------|------------------------|-----|-------------|
| After 1 | ✓ | 0 ≥ 0 ✓ | q=2, 2 UtD ✓ | q=2, 2 voters ✓ |
| After 2 | ✓ | 1 ≥ 0 ✓ | q=2, 2 UtD ✓ | q=2, 2 voters + TB ✓ |

Matches LAYOUTS.md §10.2 upgrade. ✓

#### Downgrade: 2D+TB → 1D

| # | DM transition | Group | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|-------|------------------|---|-----|---------|----------|---------|
| — | — | — | **D**(#0) + **D**(#1) + TB | 2 | 1 | 0 | 1 | 1 |
| 1 | RemoveReplica(TB) | NonVoter | **D**(#0) + **D**(#1) | 2 | 1 | 0 | 1 | 0 |
| 2 | RemoveReplica(D) + q↓ | Voter | **D**(#1) | 1 | 1 | 0 | 0 | 0 |

**Ordering**: RemoveReplica(TB) is NonVoter, RemoveReplica(D)+q↓ is Voter — can overlap.
But `TBRequired` guard: target_FTT=0, D_count=2 even, target_FTT=0 ≠ D_count/2=1,
so TB_min=0. TB_count=1 > 0 ✓ — guard passes immediately. RemoveReplica(D)+q↓ has guards
(GMDR, FTT) that pass once layout permits removal. Logically, TB should be
removed first (reverse of upgrade).

**Constraint verification**:

| # | GMDR ≥ 0? | FTT ≥ min(1,0)=0? | IO? | Split-brain? |
|---|---------------|------------------------|-----|-------------|
| After 1 | ✓ | 0 ≥ 0 ✓ | q=2, 2 UtD ✓ | q=2, 2 voters ✓ |
| After 2 | ✓ | 0 ≥ 0 ✓ | q=1, 1 voter ✓ | q=1, 1 voter ✓ |

Matches LAYOUTS.md §10.2 downgrade. ✓

---

### 10.3 2D+TB ↔ 3D (GMDR: 0↔1)

Changes: D 2↔3, TB 1↔0, qmr 1↔2. No q change (stays 2).

#### Upgrade: 2D+TB → 3D

| # | DM transition | Group | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|-------|------------------|---|-----|---------|----------|---------|
| — | — | — | 2**D** + TB | 2 | 1 | 0 | 1 | 1 |
| 1 | AddReplica(D) + qmr↑ | Voter | 3**D** + TB | 2 | 2 | 1 | 2 | 1 |
| 2 | RemoveReplica(TB) | NonVoter | 3**D** | 2 | 2 | 1 | 2 | 1 |

**Ordering**:
- Step 1 (Voter) embeds qmr↑ as the last step. The new D must be UpToDate before qmr
  is raised (quorum check `utd ≥ qmr`: 3 ≥ 2 ✓).
- Step 2 (NonVoter): `TBRequired` guard: D=3 odd → TB_min=0 → guard passes after
  step 1 completes (D count is odd, TB no longer needed).

**Constraint verification**:

| # | GMDR ≥ min(0,1)=0? | FTT ≥ min(1,1)=1? | IO? | Split-brain? |
|---|------------------------|------------------------|-----|-------------|
| After 1 | 1 ≥ 0 ✓ | 1 ≥ 1 ✓ (3 voters, q=2) | ✓ | q=2, 3 voters (odd) ✓ |
| After 2 | 1 ≥ 0 ✓ | 1 ≥ 1 ✓ (3 odd, no TB needed) | ✓ | q=2, 3 voters (odd) ✓ |

Matches LAYOUTS.md §10.3 upgrade. ✓

#### Downgrade: 3D → 2D+TB

| # | DM transition | Group | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|-------|------------------|---|-----|---------|----------|---------|
| — | — | — | 3**D** | 2 | 2 | 1 | 2 | 1 |
| 1 | AddReplica(TB) | NonVoter | 3**D** + TB | 2 | 2 | 1 | 2 | 1 |
| 2 | qmr↓ + RemoveReplica(D) | Voter | 2**D** + TB | 2 | 1 | 0 | 1 | 1 |

**Ordering**:
- Step 1 (NonVoter): TB added before D removal — ensures tiebreaker is available for
  the 2-voter layout that will result.
- Step 2 (Voter) embeds qmr↓ as the first step. The qmr is lowered before D removal
  begins. No separate ChangeQuorum transition needed; no QMRReady guard needed.

**Constraint verification**:

| # | GMDR ≥ min(1,0)=0? | FTT ≥ min(1,1)=1? | IO? | Split-brain? |
|---|------------------------|------------------------|-----|-------------|
| After 1 | 1 ≥ 0 ✓ | 1 ≥ 1 ✓ | ✓ | q=2, 3 voters (odd) ✓ |
| After 2 | 0 ≥ 0 ✓ | 1 ≥ 1 ✓ (2 voters + TB) | ✓ | q=2, 2 voters + TB ✓ |

Matches LAYOUTS.md §10.3 downgrade. ✓

---

### 10.4 3D ↔ 4D (GMDR: 1↔2) — same pattern as §10.1

Changes: D 3↔4, q 2↔3, qmr 2↔3. Structurally identical to §10.1 (vestibule + qmr).

#### Upgrade: 3D → 4D

| # | DM transition | Group | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|-------|------------------|---|-----|---------|----------|---------|
| — | — | — | 3**D** | 2 | 2 | 1 | 2 | 1 |
| 1 | AddReplica(D) + q↑ + qmr↑ | Voter | 4**D** | 3 | 3 | 2 | 3 | 1 |

**Constraint verification**:

| # | GMDR ≥ min(1,2)=1? | FTT ≥ min(1,1)=1? | IO? | Split-brain? |
|---|------------------------|------------------------|-----|-------------|
| After 1 | 2 ≥ 1 ✓ | 1 ≥ 1 ✓ | q=3, 4 UtD ✓ | q=3, 4 voters ✓ |

✓

#### Downgrade: 4D → 3D

| # | DM transition | Group | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|-------|------------------|---|-----|---------|----------|---------|
| — | — | — | 4**D** | 3 | 3 | 2 | 3 | 1 |
| 1 | qmr↓ + RemoveReplica(D) + q↓ | Voter | 3**D** | 2 | 2 | 1 | 2 | 1 |

**Constraint verification**:

| # | GMDR ≥ min(2,1)=1? | FTT ≥ min(1,1)=1? | IO? | Split-brain? |
|---|------------------------|------------------------|-----|-------------|
| After 1 | 1 ≥ 1 ✓ | 1 ≥ 1 ✓ | q=2, 3 voters ✓ | q=2, 3 voters (odd) ✓ |

✓

---

### 10.5 3D ↔ 4D+TB (FTT: 1↔2) — same pattern as §10.2

Changes: D 3↔4, TB 0↔1, q 2↔3. No qmr change (stays 2).

#### Upgrade: 3D → 4D+TB

| # | DM transition | Group | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|-------|------------------|---|-----|---------|----------|---------|
| — | — | — | 3**D** | 2 | 2 | 1 | 2 | 1 |
| 1 | AddReplica(D) + q↑ | Voter | 4**D** | 3 | 2 | 1 | 3 | 1 |
| 2 | AddReplica(TB) | NonVoter | 4**D** + TB | 3 | 2 | 1 | 3 | 2 |

**Constraint verification**:

| # | GMDR ≥ 1? | FTT ≥ min(1,2)=1? | IO? | Split-brain? |
|---|---------------|------------------------|-----|-------------|
| After 1 | ✓ | 1 ≥ 1 ✓ | q=3, 4 voters ✓ | q=3, 4 voters ✓ |
| After 2 | ✓ | 2 ≥ 1 ✓ | q=3, 4 voters ✓ | q=3, 4 voters + TB ✓ |

✓

#### Downgrade: 4D+TB → 3D

| # | DM transition | Group | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|-------|------------------|---|-----|---------|----------|---------|
| — | — | — | 4**D** + TB | 3 | 2 | 1 | 3 | 2 |
| 1 | RemoveReplica(TB) | NonVoter | 4**D** | 3 | 2 | 1 | 3 | 1 |
| 2 | RemoveReplica(D) + q↓ | Voter | 3**D** | 2 | 2 | 1 | 2 | 1 |

`TBRequired` guard for step 1: target_FTT=1, D_count=4 even, target_FTT=1 ≠
D_count/2=2. TB_min=0. TB_count=1 > 0 ✓.

**Constraint verification**:

| # | GMDR ≥ 1? | FTT ≥ min(2,1)=1? | IO? | Split-brain? |
|---|---------------|------------------------|-----|-------------|
| After 1 | ✓ | 1 ≥ 1 ✓ | q=3, 4 voters ✓ | q=3, 4 voters ✓ |
| After 2 | ✓ | 1 ≥ 1 ✓ | q=2, 3 voters ✓ | q=2, 3 voters (odd) ✓ |

✓

---

### 10.6 4D+TB ↔ 5D (GMDR: 1↔2) — same pattern as §10.3

Changes: D 4↔5, TB 1↔0, qmr 2↔3. No q change (stays 3).

#### Upgrade: 4D+TB → 5D

| # | DM transition | Group | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|-------|------------------|---|-----|---------|----------|---------|
| — | — | — | 4**D** + TB | 3 | 2 | 1 | 3 | 2 |
| 1 | AddReplica(D) + qmr↑ | Voter | 5**D** + TB | 3 | 3 | 2 | 4 | 2 |
| 2 | RemoveReplica(TB) | NonVoter | 5**D** | 3 | 3 | 2 | 4 | 2 |

**Constraint verification**:

| # | GMDR ≥ min(1,2)=1? | FTT ≥ min(2,2)=2? | IO? | Split-brain? |
|---|------------------------|------------------------|-----|-------------|
| After 1 | 2 ≥ 1 ✓ | 2 ≥ 2 ✓ (5 odd + TB) | ✓ | q=3, 5 voters (odd) ✓ |
| After 2 | 2 ≥ 1 ✓ | 2 ≥ 2 ✓ (5 odd, no TB needed) | ✓ | q=3, 5 voters (odd) ✓ |

✓

#### Downgrade: 5D → 4D+TB

| # | DM transition | Group | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|-------|------------------|---|-----|---------|----------|---------|
| — | — | — | 5**D** | 3 | 3 | 2 | 4 | 2 |
| 1 | AddReplica(TB) | NonVoter | 5**D** + TB | 3 | 3 | 2 | 4 | 2 |
| 2 | qmr↓ + RemoveReplica(D) | Voter | 4**D** + TB | 3 | 2 | 1 | 3 | 2 |

**Constraint verification**:

| # | GMDR ≥ min(2,1)=1? | FTT ≥ min(2,2)=2? | IO? | Split-brain? |
|---|------------------------|------------------------|-----|-------------|
| After 1 | 2 ≥ 1 ✓ | 2 ≥ 2 ✓ | ✓ | q=3, 5 voters (odd) ✓ |
| After 2 | 1 ≥ 1 ✓ | 2 ≥ 2 ✓ (4 voters + TB) | ✓ | q=3, 4 voters + TB ✓ |

✓

---

## 11. Topology

### 11.1 Zone Requirements

No changes from LAYOUTS.md §11.1. Zone requirements and placement strategies (pure zone
vs composite failure domain) are topology-level constraints that apply to all DM transitions
below.

### 11.2 Zone-Aware Replica Replacement

The §8 and §9 DM transition sequences apply as-is with one additional constraint:

**Zone placement rule**: The node for `AddReplica(D)`, `AddReplica(D) via sD`, or
`AddReplica(TB)` must be selected from **the same zone** as the replica being replaced.
This preserves the zone distribution that underpins zone-level FTT guarantees.

This is a **scheduler constraint** on node selection within each DM transition, not a new
transition type.

**Zone loss recovery**: If the entire zone is unavailable, replacement waits for the zone
to return. The DM guards (`ZoneGMDR`, `ZoneFTT`) prevent removals that would
violate zone-level guarantees.

No new procedures are needed. No discrepancy with LAYOUTS.md §11.2. ✓

### 11.3 Zone Redistribution (Swapping D and TB Between Zones)

Zone redistribution changes which zones hold which replica types while preserving the
overall layout. This involves cross-zone moves, not same-zone replacements.

**D-only layouts** (3D, 4D, 5D): use §8 replacement with the target zone. No special
DM transition sequence needed.

**D+TB layouts** (2D+1TB, 4D+1TB): D and TB swap between zones. Requires coordinated
add/remove of both types.

#### 11.3.1 Swapping D ↔ TB in 2D+1TB (q=2, qmr=1)

**Goal**: Move D from zone A to zone C, move TB from zone C to zone A.

Start: D(zone A) + D(zone B) + TB(zone C).
End: TB(zone A) + D(zone B) + D(zone C).

| # | DM transition | Group | Zone A | Zone B | Zone C | q | GMDR | FTT |
|---|---------------|-------|--------|--------|--------|---|---------|---------|
| — | — | — | D(#0) | D(#1) | TB | 2 | 0 | 1 |
| 1 | AddReplica(D) [zone C] | Voter | D(#0) | D(#1) | D(#2)+TB | 2 | 0 | 1 |
| 2 | AddReplica(TB) [zone A] | NonVoter | D(#0)+TB(#3) | D(#1) | D(#2)+TB | 2 | 0 | 1 |
| 3 | RemoveReplica(TB) [zone C] | NonVoter | D(#0)+TB(#3) | D(#1) | D(#2) | 2 | 0 | 1 |
| 4 | RemoveReplica(D) [zone A] | Voter | TB(#3) | D(#1) | D(#2) | 2 | 0 | 1 |

**DM parallelism**: Steps 2 and 3 are both NonVoter, different members — can run in
parallel per DM rules. Step 4 (Voter) must wait for step 1 (Voter) to complete (Voter
serialization). Step 4 is also gated by `ZoneFTT` guard, which passes only after the
new TB is in zone A (step 2 complete).

> **Discrepancy (parallelism)**: LAYOUTS.md §11.3.1 shows steps 2 and 3 as sequential.
> DM allows them in parallel (both NonVoter, different members). **Criticality**: Low.

**Zone-FTT=1 verification at each transition boundary**:

| # | Zone A lost | Zone B lost | Zone C lost | All ✓? |
|---|------------|------------|------------|--------|
| Start | D(B)+TB(C): tiebreaker ✓ | D(A)+TB(C): tiebreaker ✓ | D(A)+D(B): 2≥2 ✓ | ✓ |
| After 1 | D(B)+D(C)+TB(C): 2≥2 ✓ | D(A)+D(C)+TB(C): 2≥2 ✓ | D(A)+D(B): 2≥2 ✓ | ✓ |
| After 2 | D(B)+D(C)+TB(C): 2≥2 ✓ | D(A)+D(C)+TB(A,C): 2≥2 ✓ | D(A)+D(B)+TB(A): 2≥2 ✓ | ✓ |
| After 3 | D(B)+D(C): 3 voters, 2≥2 ✓ | D(A)+D(C)+TB(A): 2≥2 ✓ | D(A)+D(B)+TB(A): 2≥2 ✓ | ✓ |
| After 4 | D(B)+D(C): 2≥2 ✓ | D(C)+TB(A): tiebreaker ✓ | D(B)+TB(A): tiebreaker ✓ | ✓ |

Split-brain safe throughout: q=2, voters range from 2 to 3 (odd=safe, even+TB=safe). ✓

#### 11.3.2 Swapping D ↔ TB in 4D+1TB (q=3, qmr=2)

**Goal**: Move D from zone A to zone C, move TB from zone C to zone A.

Start: D+D(zone A) + D(zone B) + D+TB(zone C).
End: D+TB(zone A) + D(zone B) + D+D(zone C).

| # | DM transition | Group | Zone A | Zone B | Zone C | q | GMDR | FTT |
|---|---------------|-------|--------|--------|--------|---|---------|---------|
| — | — | — | D+D | D | D+TB | 3 | 1 | 2 |
| 1 | AddReplica(D) [zone C] | Voter | D+D | D | D+D+TB | 3 | 1 | 2 |
| 2 | AddReplica(TB) [zone A] | NonVoter | D+D+TB | D | D+D+TB | 3 | 1 | 2 |
| 3 | RemoveReplica(TB) [zone C] | NonVoter | D+D+TB | D | D+D | 3 | 1 | 2 |
| 4 | RemoveReplica(D) [zone A] | Voter | D+TB | D | D+D | 3 | 1 | 2 |

**DM parallelism**: Same as §11.3.1 — steps 2 and 3 can overlap.

**Zone-FTT verification**: At steps 1-3 there are 5 D voters (odd, q=3 = safe
majority). Any single zone loss removes at most 2D+TB, leaving ≥3D or ≥2D+TB — sufficient
for quorum in all cases.

After step 4: 4D+TB with D+TB | D | D+D distribution. Losing any zone leaves ≥2D or
≥D+TB — D+TB zone loss leaves 3D (3≥3 ✓), any other zone loss leaves 2D+TB (tiebreaker
or main quorum). ✓

**Transient state**: During steps 2-3, zone C has D+D+TB (3 replicas). Losing zone C
removes 2D+TB, leaving 3D+TB(A) or 3D — still ≥ q=3. Safe. ✓

---

### 11.4 Changing D Distribution Across Zones (Composite Layouts)

#### Rebalancing within the same layout

Moving D between zones: use §8 replacement DM transitions with the target zone for add
and source zone for remove.

- **D-only layouts**: `AddReplica(D)` [target zone], then `RemoveReplica(D)` [source zone].
  For odd D, use `+q↑`/`+q↓` variants.
- **D+TB layouts**: if the move involves the TB zone, combine with §11.3 swap procedure.

**Constraint**: resulting distribution must satisfy `max D per zone ≤ D − qmr`.

#### Layout transitions in 3-zone TransZonal

**4D (q=3, qmr=3) is not available in 3-zone TransZonal** (see LAYOUTS.md §11.4).

Available layouts form a linear chain:

```
2D+1TB (0,1) ——GMDR—— 3D (1,1) ——FTT—— 4D+1TB (1,2) ——GMDR—— 5D (2,2)
```

Each edge maps to a DM transition sequence from §10:

| # | Transition | DM sequence (upgrade) | DM sequence (downgrade) |
|---|-----------|----------------------|------------------------|
| 1 | 2D+1TB ↔ 3D | §10.3: AddReplica(D)+qmr↑, RemoveReplica(TB) | §10.3: AddReplica(TB), qmr↓+RemoveReplica(D) |
| 2 | 3D ↔ 4D+1TB | §10.5: AddReplica(D)+q↑, AddReplica(TB) | §10.5: RemoveReplica(TB), RemoveReplica(D)+q↓ |
| 3 | 4D+1TB ↔ 5D | §10.6: AddReplica(D)+qmr↑, RemoveReplica(TB) | §10.6: AddReplica(TB), qmr↓+RemoveReplica(D) |

Zone-FTT=1 is maintained at every transition boundary — verified by the per-transition
constraint checks in §10.3, §10.5, §10.6 above. The DM `ZoneFTT` guard provides
runtime enforcement.

Multi-step example — upgrade 3D → 5D in 3 zones:

| # | Layout | Distribution | Zone FTT | GMDR | FTT | DM transitions |
|---|--------|-------------|-------------|---------|---------|----------------|
| 0 | 3D (q=2, qmr=2) | 1+1+1 | 1 | 1 | 1 | §10.5: AddReplica(D)+q↑, AddReplica(TB) |
| 1 | 4D+1TB (q=3, qmr=2) | 2D \| 1D+TB \| 1D | 1 | 1 | 2 | §10.6: AddReplica(D)+qmr↑, RemoveReplica(TB) |
| 2 | 5D (q=3, qmr=3) | 2+2+1 | 1 | 2 | 2 | — |

Zone-FTT=1 maintained throughout. Downgrade is the reverse path.

---

## 12. Discrepancy Summary

| # | Location | Discrepancy | Criticality | Resolution |
|---|----------|------------|-------------|------------|
| 1 | §10.2 upgrade, §10.5 upgrade | AddReplica(TB) (NonVoter) can overlap with AddReplica(D) (Voter) per DM rules; LAYOUTS.md sequences them strictly | **Low** | Safe either way — adding TB early is harmless. No guard prevents it. Controller should sequence D-first for logical clarity, but not a safety issue. |
| 2 | §11.3.1, §11.3.2 | AddReplica(TB) and RemoveReplica(TB) (both NonVoter, different members) can run in parallel per DM rules; LAYOUTS.md sequences them strictly | **Low** | Safe either way — DM guards (ZoneFTT) gate the subsequent RemoveReplica(D) until zone coverage is established. Parallel execution is an optimization opportunity. |
