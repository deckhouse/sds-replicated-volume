# Layout Procedures

This document describes step-by-step procedures for replica replacement,
layout transitions, and zone redistribution. Each procedure maintains the
three safety guarantees at every intermediate step:

1. **Never reduce GMDR/FTT** below the target level.
2. **Never interrupt IO** (maintain quorum throughout).
3. **Never create split-brain risk** (maintain safe quorum settings).

See [README.md](README.md) §6 for guarantee definitions. For per-layout
failure analysis, see [LAYOUTS_ANALYSIS.md](LAYOUTS_ANALYSIS.md). For
transition types and preconditions, see [TRANSITIONS.md](TRANSITIONS.md).
For DRBD quorum mechanics, replica types, and liminal types, see
[DRBD_MODEL.md](DRBD_MODEL.md).

Each procedure is shown at two levels:

- **DM transition sequence** — the high-level datamesh transitions with
  constraint verification at each boundary.
- **DRBD step-by-step procedure** — detailed intermediate states, quorum
  verification at each step, and wait conditions. Both "a" (without sD) and
  "b" (with sD) variants are shown where they differ.

---

## 1. Common Concepts

### Two replacement patterns

Diskful replacement follows one of two patterns, determined by the parity of
the voter count (D) **before** adding the replacement:

| D parity | Add sequence | q change? | Remove sequence |
|----------|-------------|-----------|-----------------|
| **Even** | D∅ added directly (or sD→D hot promotion) | No — q is already a safe majority | D→D∅→✕ (direct removal) |
| **Odd** | Vestibule (A or sD) + D∅ + q↑ | Yes — q must be raised | D→D∅→A + q↓→✕ (vestibule removal) |

A replacement = **add** new D (VotingMembership), then **remove** old D
(VotingMembership). These two transitions execute sequentially
(VotingMembership is serialized — see [TRANSITIONS.md](TRANSITIONS.md) §14).

### Vestibule

When adding a voter to odd D (making it even), q must be raised atomically
with the voter addition to prevent split-brain. A **vestibule** — a temporary
non-voter member — establishes DRBD connections while quorum is unaffected.
The conversion to voter + q↑ happens in a single configuration push.

Two vestibule types:

- **A vestibule** (fallback): the new member enters as Access, then converts
  to D∅ + q↑. Disk attaches afterward — full resync (hours for large
  volumes).
- **sD vestibule** (requires Flant DRBD extension): the new member enters as
  ShadowDiskful and pre-syncs data invisibly. Then disk detaches (sD→sD∅),
  converts to D∅ + q↑ (pure config change, safe in any application order),
  and re-attaches with only a **delta resync** (seconds). See
  [DRBD_MODEL.md](DRBD_MODEL.md) §6 for sD mechanics.

### Split-brain safety rule

At every step: `q = floor(voters / 2) + 1`. With odd voters, majority is
unambiguous. With even voters, the higher q prevents symmetric splits. When
voter count changes, q is raised **before** adding a voter (odd→even) and
lowered **after** removing a voter (even→odd).

### Old replica retention

The procedures below show the old replica being removed at the end. In
practice, if the old replica is still in use as an IO access point, it can be
kept as an **Access** replica instead of being removed. Access replicas are
invisible to quorum, so this does not affect any guarantees.

---

## 2. Diskful Replica Replacement

### 2.1 Replacing in 1D (GMDR=0, FTT=0)

**Pattern**: Odd D (1). Add with q↑, remove with q↓.

**DM transition sequence:**

| # | DM transition | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|------------------|---|-----|------|-----|-----|
| — | — | **D**(#0) | 1 | 1 | 0 | 0 | 0 |
| 1 | AddReplica(D) + q↑ | **D**(#0) + **D**(#1) | 2 | 1 | 0 | 1 | 0 |
| 2 | RemoveReplica(D) + q↓ | **D**(#1) | 1 | 1 | 0 | 0 | 0 |

**Constraint verification:**

| # | GMDR ≥ 0? | IO maintained? | Split-brain safe? |
|---|-----------|----------------|-------------------|
| Start | 0 ≥ 0 ✓ | q=1, 1 voter ✓ | 1 voter, q=1 ✓ |
| After 1 | 0 ≥ 0 ✓ | q=2, 2 voters UtD ✓ | 2 voters, q=2 ✓ |
| After 2 | 0 ≥ 0 ✓ | q=1, 1 voter ✓ | 1 voter, q=1 ✓ |

#### Without sD

| # | Layout | q | qmr | Action | Wait condition |
|---|--------|---|-----|--------|----------------|
| 0 | **D**(#0) | 1 | 1 | Add A(#1) | D+A: #1 connected |
| 1 | **D**(#0) + **A**(#1) | 1 | 1 | A → D∅ + raise q → 2 | All: #1 is D∅, q=2 |
| 2 | **D**(#0) + **D∅**(#1) | 2 | 1 | Attach disk on #1 | #1: UpToDate |
| 3 | **D**(#0) + **D**(#1) | 2 | 1 | Detach disk on #0 | #0: disk detached |
| 4 | **D∅**(#0) + **D**(#1) | 2 | 1 | D∅ → A + lower q → 1 | All: #0 is A, q=1 |
| 5 | **A**(#0) + **D**(#1) | 1 | 1 | Remove A(#0) | D+A: #0 removed |

**Temporary operational impact** (steps 2–4): with 2 voters and q=2, a crash
of either D or D∅ causes quorum loss → IO suspension. This wider failure
surface lasts for the full resync duration (hours for large volumes). The
FTT=0 guarantee level is unchanged — but the failure surface is wider.

#### With sD

| # | Layout | q | qmr | Action | Wait condition |
|---|--------|---|-----|--------|----------------|
| 0 | **D**(#0) | 1 | 1 | Add sD(#1) | #1: UpToDate |
| 1 | **D**(#0) + **sD**(#1) | 1 | 1 | Detach disk on #1 | #1: disk detached |
| 2 | **D**(#0) + **sD∅**(#1) | 1 | 1 | sD∅ → D∅ + raise q → 2 | All: #1 is D∅, q=2 |
| 3 | **D**(#0) + **D∅**(#1) | 2 | 1 | Attach disk on #1 | #1: UpToDate (delta) |
| 4 | **D**(#0) + **D**(#1) | 2 | 1 | Detach disk on #0 | #0: disk detached |
| 5 | **D∅**(#0) + **D**(#1) | 2 | 1 | D∅ → A + lower q → 1 | All: #0 is A, q=1 |
| 6 | **A**(#0) + **D**(#1) | 1 | 1 | Remove A(#0) | D+A: #0 removed |

The sD variant pre-syncs data during steps 0–1 (invisible to quorum). The
wider failure surface at steps 2–4 lasts only for a delta resync (seconds)
instead of a full resync (hours).

### 2.2 Replacing in 2D+1TB (GMDR=0, FTT=1)

**Pattern**: Even D (2). Direct add, direct remove. No q/qmr changes needed.

**DM transition sequence:**

| # | DM transition | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|------------------|---|-----|------|-----|-----|
| — | — | 2**D** + TB | 2 | 1 | 0 | 1 | 1 |
| 1 | AddReplica(D) | 3**D** + TB | 2 | 1 | 0 | 2 | 1 |
| 2 | RemoveReplica(D) | 2**D** + TB | 2 | 1 | 0 | 1 | 1 |

**Constraint verification:**

| # | GMDR ≥ 0? | FTT ≥ 1? | IO maintained? | Split-brain safe? |
|---|-----------|----------|----------------|-------------------|
| Start | ✓ | ✓ | 2 voters, q=2 ✓ | q=2, 2 voters + TB ✓ |
| After 1 | ✓ | ✓ | 3 voters, q=2 ✓ | q=2, 3 voters (odd) ✓ |
| After 2 | ✓ | ✓ | 2 voters, q=2 ✓ | q=2, 2 voters + TB ✓ |

#### Without sD

| # | Layout | q | qmr | Action | Wait condition |
|---|--------|---|-----|--------|----------------|
| 0 | 2**D** + TB | 2 | 1 | Add D∅(#2) | All: #2 configured and connected |
| 1 | 2**D** + **D∅**(#2) + TB | 2 | 1 | Attach disk on #2 | #2: UpToDate |
| 2 | 3**D** + TB | 2 | 1 | Detach disk on #0 | #0: disk detached |
| 3 | **D∅**(#0) + 2**D** + TB | 2 | 1 | Remove #0 | All: #0 removed |

#### With sD

| # | Layout | q | qmr | Action | Wait condition |
|---|--------|---|-----|--------|----------------|
| 0 | 2**D** + TB | 2 | 1 | Add sD(#2) | #2: UpToDate |
| 1 | 2**D** + **sD**(#2) + TB | 2 | 1 | sD → D | All: #2 is D |
| 2 | 3**D** + TB | 2 | 1 | Detach disk on #0 | #0: disk detached |
| 3 | **D∅**(#0) + 2**D** + TB | 2 | 1 | Remove #0 | All: #0 removed |

Even D — sD converts directly to D via hot `non-voting` flag change (no
detach-reattach needed). The bulk sync happens as sD with zero quorum impact.

### 2.3 Replacing in 2D (GMDR=1, FTT=0)

**Pattern**: Even D (2). Direct add, direct remove. No q/qmr changes.

**DM transition sequence:**

| # | DM transition | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|------------------|---|-----|------|-----|-----|
| — | — | 2**D** | 2 | 2 | 1 | 1 | 0 |
| 1 | AddReplica(D) | 3**D** | 2 | 2 | 1 | 2 | 0 |
| 2 | RemoveReplica(D) | 2**D** | 2 | 2 | 1 | 1 | 0 |

**Constraint verification:**

| # | GMDR ≥ 1? | IO maintained? | Split-brain safe? |
|---|-----------|----------------|-------------------|
| Start | 1 ≥ 1 ✓ | 2 voters UtD, q=2 ✓ | q=2, 2 voters ✓ |
| After 1 | 1 ≥ 1 ✓ | 3 voters, q=2 ✓ | q=2, 3 voters (odd) ✓ |
| After 2 | 1 ≥ 1 ✓ | 2 voters UtD, q=2 ✓ | q=2, 2 voters ✓ |

#### Without sD

| # | Layout | q | qmr | Action | Wait condition |
|---|--------|---|-----|--------|----------------|
| 0 | 2**D** | 2 | 2 | Add D∅(#2) | All: #2 configured and connected |
| 1 | 2**D** + **D∅**(#2) | 2 | 2 | Attach disk on #2 | #2: UpToDate |
| 2 | 3**D** | 2 | 2 | Detach disk on #0 | #0: disk detached |
| 3 | **D∅**(#0) + 2**D** | 2 | 2 | Remove #0 | All: #0 removed |

D∅ failure is tolerated at steps 1–3: if D∅(#2) crashes, utd=2(#0,#1),
voters=3, (2+0)=2 ≥ 2 ✓ — quorum maintained. This is strictly better than
the target layout's FTT=0.

#### With sD

| # | Layout | q | qmr | Action | Wait condition |
|---|--------|---|-----|--------|----------------|
| 0 | 2**D** | 2 | 2 | Add sD(#2) | #2: UpToDate |
| 1 | 2**D** + **sD**(#2) | 2 | 2 | sD → D | All: #2 is D |
| 2 | 3**D** | 2 | 2 | Detach disk on #0 | #0: disk detached |
| 3 | **D∅**(#0) + 2**D** | 2 | 2 | Remove #0 | All: #0 removed |

### 2.4 Replacing in 3D (GMDR=1, FTT=1)

**Pattern**: Odd D (3). Add with q↑, remove with q↓.

**DM transition sequence:**

| # | DM transition | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|------------------|---|-----|------|-----|-----|
| — | — | 3**D** | 2 | 2 | 1 | 2 | 1 |
| 1 | AddReplica(D) + q↑ | 4**D** | 3 | 2 | 1 | 3 | 1 |
| 2 | RemoveReplica(D) + q↓ | 3**D** | 2 | 2 | 1 | 2 | 1 |

**Constraint verification:**

| # | GMDR ≥ 1? | FTT ≥ 1? | IO maintained? | Split-brain safe? |
|---|-----------|----------|----------------|-------------------|
| Start | ✓ | ✓ | 3 voters, q=2 ✓ | q=2, 3 voters (odd) ✓ |
| After 1 | ✓ | ✓ | 4 voters, q=3 ✓ | q=3, 4 voters ✓ |
| After 2 | ✓ | ✓ | 3 voters, q=2 ✓ | q=2, 3 voters (odd) ✓ |

#### Without sD

| # | Layout | q | qmr | Action | Wait condition |
|---|--------|---|-----|--------|----------------|
| 0 | 3**D** | 2 | 2 | Add A(#3) | D+A: #3 connected |
| 1 | 3**D** + **A**(#3) | 2 | 2 | A → D∅ + raise q → 3 | All: #3 is D∅, q=3 |
| 2 | 3**D** + **D∅**(#3) | 3 | 2 | Attach disk on #3 | #3: UpToDate |
| 3 | 4**D** | 3 | 2 | Detach disk on #0 | #0: disk detached |
| 4 | **D∅**(#0) + 3**D** | 3 | 2 | D∅ → A + lower q → 2 | All: #0 is A, q=2 |
| 5 | **A**(#0) + 3**D** | 2 | 2 | Remove A(#0) | D+A: #0 removed |

FTT=1 at step 2 (4 voters, q=3, with D∅): any D fails → 2utd + 1present =
3 ≥ 3 ✓, 2 ≥ 2 ✓. D∅ fails → 3utd, 3 ≥ 3 ✓. FTT=1 maintained.

#### With sD

| # | Layout | q | qmr | Action | Wait condition |
|---|--------|---|-----|--------|----------------|
| 0 | 3**D** | 2 | 2 | Add sD(#3) | #3: UpToDate |
| 1 | 3**D** + **sD**(#3) | 2 | 2 | Detach disk on #3 | #3: disk detached |
| 2 | 3**D** + **sD∅**(#3) | 2 | 2 | sD∅ → D∅ + raise q → 3 | All: #3 is D∅, q=3 |
| 3 | 3**D** + **D∅**(#3) | 3 | 2 | Attach disk on #3 | #3: UpToDate (delta) |
| 4 | 4**D** | 3 | 2 | Detach disk on #0 | #0: disk detached |
| 5 | **D∅**(#0) + 3**D** | 3 | 2 | D∅ → A + lower q → 2 | All: #0 is A, q=2 |
| 6 | **A**(#0) + 3**D** | 2 | 2 | Remove A(#0) | D+A: #0 removed |

### 2.5 Replacing in 4D+1TB (GMDR=1, FTT=2)

**Pattern**: Even D (4). Direct add, direct remove.

**DM transition sequence:**

| # | DM transition | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|------------------|---|-----|------|-----|-----|
| — | — | 4**D** + TB | 3 | 2 | 1 | 3 | 2 |
| 1 | AddReplica(D) | 5**D** + TB | 3 | 2 | 1 | 4 | 2 |
| 2 | RemoveReplica(D) | 4**D** + TB | 3 | 2 | 1 | 3 | 2 |

**Constraint verification:**

| # | GMDR ≥ 1? | FTT ≥ 2? | IO maintained? | Split-brain safe? |
|---|-----------|----------|----------------|-------------------|
| Start | ✓ | ✓ | 4 voters, q=3 ✓ | q=3, 4 voters + TB ✓ |
| After 1 | ✓ | ✓ | 5 voters, q=3 ✓ | q=3, 5 voters (odd) ✓ |
| After 2 | ✓ | ✓ | 4 voters, q=3 ✓ | q=3, 4 voters + TB ✓ |

#### Without sD

| # | Layout | q | qmr | Action | Wait condition |
|---|--------|---|-----|--------|----------------|
| 0 | 4**D** + TB | 3 | 2 | Add D∅(#4) | All: #4 configured and connected |
| 1 | 4**D** + **D∅**(#4) + TB | 3 | 2 | Attach disk on #4 | #4: UpToDate |
| 2 | 5**D** + TB | 3 | 2 | Detach disk on #0 | #0: disk detached |
| 3 | **D∅**(#0) + 4**D** + TB | 3 | 2 | Remove #0 | All: #0 removed |

#### With sD

| # | Layout | q | qmr | Action | Wait condition |
|---|--------|---|-----|--------|----------------|
| 0 | 4**D** + TB | 3 | 2 | Add sD(#4) | #4: UpToDate |
| 1 | 4**D** + **sD**(#4) + TB | 3 | 2 | sD → D | All: #4 is D |
| 2 | 5**D** + TB | 3 | 2 | Detach disk on #0 | #0: disk detached |
| 3 | **D∅**(#0) + 4**D** + TB | 3 | 2 | Remove #0 | All: #0 removed |

### 2.6 Replacing in 4D (GMDR=2, FTT=1)

**Pattern**: Even D (4). Direct add, direct remove.

**DM transition sequence:**

| # | DM transition | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|------------------|---|-----|------|-----|-----|
| — | — | 4**D** | 3 | 3 | 2 | 3 | 1 |
| 1 | AddReplica(D) | 5**D** | 3 | 3 | 2 | 4 | 1 |
| 2 | RemoveReplica(D) | 4**D** | 3 | 3 | 2 | 3 | 1 |

**Constraint verification:**

| # | GMDR ≥ 2? | FTT ≥ 1? | IO maintained? | Split-brain safe? |
|---|-----------|----------|----------------|-------------------|
| Start | ✓ | ✓ | 4 voters, q=3 ✓ | q=3, 4 voters ✓ |
| After 1 | ✓ | ✓ | 5 voters, q=3 ✓ | q=3, 5 voters (odd) ✓ |
| After 2 | ✓ | ✓ | 4 voters, q=3 ✓ | q=3, 4 voters ✓ |

#### Without sD

| # | Layout | q | qmr | Action | Wait condition |
|---|--------|---|-----|--------|----------------|
| 0 | 4**D** | 3 | 3 | Add D∅(#4) | All: #4 configured and connected |
| 1 | 4**D** + **D∅**(#4) | 3 | 3 | Attach disk on #4 | #4: UpToDate |
| 2 | 5**D** | 3 | 3 | Detach disk on #0 | #0: disk detached |
| 3 | **D∅**(#0) + 4**D** | 3 | 3 | Remove #0 | All: #0 removed |

#### With sD

| # | Layout | q | qmr | Action | Wait condition |
|---|--------|---|-----|--------|----------------|
| 0 | 4**D** | 3 | 3 | Add sD(#4) | #4: UpToDate |
| 1 | 4**D** + **sD**(#4) | 3 | 3 | sD → D | All: #4 is D |
| 2 | 5**D** | 3 | 3 | Detach disk on #0 | #0: disk detached |
| 3 | **D∅**(#0) + 4**D** | 3 | 3 | Remove #0 | All: #0 removed |

### 2.7 Replacing in 5D (GMDR=2, FTT=2)

**Pattern**: Odd D (5). Add with q↑, remove with q↓.

**DM transition sequence:**

| # | DM transition | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|------------------|---|-----|------|-----|-----|
| — | — | 5**D** | 3 | 3 | 2 | 4 | 2 |
| 1 | AddReplica(D) + q↑ | 6**D** | 4 | 3 | 2 | 5 | 2 |
| 2 | RemoveReplica(D) + q↓ | 5**D** | 3 | 3 | 2 | 4 | 2 |

**Constraint verification:**

| # | GMDR ≥ 2? | FTT ≥ 2? | IO maintained? | Split-brain safe? |
|---|-----------|----------|----------------|-------------------|
| Start | ✓ | ✓ | 5 voters, q=3 ✓ | q=3, 5 voters (odd) ✓ |
| After 1 | ✓ | ✓ | 6 voters, q=4 ✓ | q=4, 6 voters ✓ |
| After 2 | ✓ | ✓ | 5 voters, q=3 ✓ | q=3, 5 voters (odd) ✓ |

#### Without sD

| # | Layout | q | qmr | Action | Wait condition |
|---|--------|---|-----|--------|----------------|
| 0 | 5**D** | 3 | 3 | Add A(#5) | D+A: #5 connected |
| 1 | 5**D** + **A**(#5) | 3 | 3 | A → D∅ + raise q → 4 | All: #5 is D∅, q=4 |
| 2 | 5**D** + **D∅**(#5) | 4 | 3 | Attach disk on #5 | #5: UpToDate |
| 3 | 6**D** | 4 | 3 | Detach disk on #0 | #0: disk detached |
| 4 | **D∅**(#0) + 5**D** | 4 | 3 | D∅ → A + lower q → 3 | All: #0 is A, q=3 |
| 5 | **A**(#0) + 5**D** | 3 | 3 | Remove A(#0) | D+A: #0 removed |

#### With sD

| # | Layout | q | qmr | Action | Wait condition |
|---|--------|---|-----|--------|----------------|
| 0 | 5**D** | 3 | 3 | Add sD(#5) | #5: UpToDate |
| 1 | 5**D** + **sD**(#5) | 3 | 3 | Detach disk on #5 | #5: disk detached |
| 2 | 5**D** + **sD∅**(#5) | 3 | 3 | sD∅ → D∅ + raise q → 4 | All: #5 is D∅, q=4 |
| 3 | 5**D** + **D∅**(#5) | 4 | 3 | Attach disk on #5 | #5: UpToDate (delta) |
| 4 | 6**D** | 4 | 3 | Detach disk on #0 | #0: disk detached |
| 5 | **D∅**(#0) + 5**D** | 4 | 3 | D∅ → A + lower q → 3 | All: #0 is A, q=3 |
| 6 | **A**(#0) + 5**D** | 3 | 3 | Remove A(#0) | D+A: #0 removed |

---

## 3. TieBreaker Replica Replacement

TB replicas have no data and no disk — no resync is needed. Replacement is a
simple add-then-remove using NonVotingMembership transitions that can run
concurrently with VotingMembership transitions on other members.

### 3.1 Replacing TB in 2D+1TB (GMDR=0, FTT=1)

| # | DM transition | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|------------------|---|-----|------|-----|-----|
| — | — | 2**D** + **TB**(#0) | 2 | 1 | 0 | 1 | 1 |
| 1 | AddReplica(TB) | 2**D** + **TB**(#0) + **TB**(#1) | 2 | 1 | 0 | 1 | 1 |
| 2 | RemoveReplica(TB) | 2**D** + **TB**(#1) | 2 | 1 | 0 | 1 | 1 |

**2TB tiebreaker threshold window**: with 2 TBs, the DRBD tiebreaker requires
a majority of diskless peers (`diskless >= (diskless + missing_diskless) / 2 + 1`).
With 2 TBs, that threshold is 2 — both TBs must be connected. After 1D
failure, the system depends on both TBs staying connected (vs 1 TB in the
target layout). The FTT=1 guarantee is maintained, but operational risk is
slightly higher during this window. Minimize the 2TB duration.

### 3.2 Replacing TB in 4D+1TB (GMDR=1, FTT=2)

| # | DM transition | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|------------------|---|-----|------|-----|-----|
| — | — | 4**D** + **TB**(#0) | 3 | 2 | 1 | 3 | 2 |
| 1 | AddReplica(TB) | 4**D** + **TB**(#0) + **TB**(#1) | 3 | 2 | 1 | 3 | 2 |
| 2 | RemoveReplica(TB) | 4**D** + **TB**(#1) | 3 | 2 | 1 | 3 | 2 |

Same 2TB threshold note: after 2D failures, tiebreaker depends on both TBs
staying connected.

---

## 4. Layout Transitions

Layout transitions change GMDR or FTT by exactly ±1 per step. See
[README.md](README.md) §8 for the transition graph and ordering rules.

### Transition graph

```
(0,0) 1D ———GMDR——— 2D (1,0)
  |                    |
 FTT                  FTT
  |                    |
(0,1) 2D+TB —GMDR— 3D (1,1) ———GMDR——— 4D (2,1)
                       |                    |
                      FTT                  FTT
                       |                    |
                 (1,2) 4D+TB ——GMDR—— 5D (2,2)
```

### Ordering rules

- **Upgrade** (increasing FTT or GMDR): prefer increasing GMDR first.
- **Downgrade** (decreasing FTT or GMDR): prefer decreasing FTT first.

This produces paths through pure-D layouts when possible (simpler, no TB
management):

```
Upgrade  (0,0)→(2,2):  1D → 2D → 3D → 4D → 5D
Downgrade (2,2)→(0,0): 5D → 4D → 3D → 2D → 1D
```

### DM transition summary

qmr changes are embedded within D transitions. No separate ChangeQuorum
needed (see [TRANSITIONS.md](TRANSITIONS.md) §3).

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

Edges #5, #6, #8 are structurally identical to #1, #2, #4 at higher values.
Edges #3 and #7 are single VotingMembership transitions (no q/qmr change).

### 4.1 1D ↔ 2D (GMDR: 0↔1)

Changes: D 1↔2, q 1↔2, qmr 1↔2.

#### Upgrade: 1D → 2D

| # | DM transition | Group | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|-------|------------------|---|-----|------|-----|-----|
| — | — | — | **D**(#0) | 1 | 1 | 0 | 0 | 0 |
| 1 | AddReplica(D) + q↑ + qmr↑ | Voter | 2**D** | 2 | 2 | 1 | 1 | 0 |

Single transition with embedded qmr↑ as the last step. qmr rises after the
new D is UpToDate (quorum check: 2 ≥ 2 ✓).

**Constraint**: min(source, target) = GMDR≥0, FTT≥0. After: GMDR=1 ≥ 0 ✓,
FTT=0 ≥ 0 ✓. ✓

#### Downgrade: 2D → 1D

| # | DM transition | Group | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|-------|------------------|---|-----|------|-----|-----|
| — | — | — | 2**D** | 2 | 2 | 1 | 1 | 0 |
| 1 | qmr↓ + RemoveReplica(D) + q↓ | Voter | **D**(#1) | 1 | 1 | 0 | 0 | 0 |

Single transition with embedded qmr↓ as the first step. qmr is lowered
before D removal (relaxes quorum check: 2 ≥ 1 ✓).

**Constraint**: min(source, target) = GMDR≥0, FTT≥0. After: 0 ≥ 0 ✓. ✓

### 4.2 1D ↔ 2D+TB (FTT: 0↔1)

Changes: D 1↔2, TB 0↔1, q 1↔2. No qmr change (stays 1).

#### Upgrade: 1D → 2D+TB

| # | DM transition | Group | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|-------|------------------|---|-----|------|-----|-----|
| — | — | — | **D**(#0) | 1 | 1 | 0 | 0 | 0 |
| 1 | AddReplica(D) + q↑ | Voter | 2**D** | 2 | 1 | 0 | 1 | 0 |
| 2 | AddReplica(TB) | NonVoter | 2**D** + TB | 2 | 1 | 0 | 1 | 1 |

D first, TB second. TB is useless without 2D (no tiebreaker for 1 voter).

**Constraint**: min(source, target) = GMDR≥0, FTT≥0. After step 1: FTT=0 ≥
0 ✓. After step 2: FTT=1 ≥ 0 ✓. ✓

#### Downgrade: 2D+TB → 1D

| # | DM transition | Group | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|-------|------------------|---|-----|------|-----|-----|
| — | — | — | 2**D** + TB | 2 | 1 | 0 | 1 | 1 |
| 1 | RemoveReplica(TB) | NonVoter | 2**D** | 2 | 1 | 0 | 1 | 0 |
| 2 | RemoveReplica(D) + q↓ | Voter | **D**(#1) | 1 | 1 | 0 | 0 | 0 |

TB removed first (reverse of upgrade), then D removed.

**Constraint**: min(source, target) = GMDR≥0, FTT≥0. After step 1: FTT=0 ≥
0 ✓. After step 2: 0 ≥ 0 ✓. ✓

### 4.3 2D+TB ↔ 3D (GMDR: 0↔1)

Changes: D 2↔3, TB 1↔0, qmr 1↔2. No q change (stays 2).

#### Upgrade: 2D+TB → 3D

| # | DM transition | Group | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|-------|------------------|---|-----|------|-----|-----|
| — | — | — | 2**D** + TB | 2 | 1 | 0 | 1 | 1 |
| 1 | AddReplica(D) + qmr↑ | Voter | 3**D** + TB | 2 | 2 | 1 | 2 | 1 |
| 2 | RemoveReplica(TB) | NonVoter | 3**D** | 2 | 2 | 1 | 2 | 1 |

D added with embedded qmr↑ (qmr rises after 3rd D is UpToDate: 3 ≥ 2 ✓).
TB removed after — 3 odd voters don't need tiebreaker.

**Constraint**: min(source, target) = GMDR≥0, FTT≥1. After step 1: GMDR=1 ≥
0 ✓, FTT=1 ≥ 1 ✓. After step 2: FTT=1 ≥ 1 ✓ (3 odd voters, q=2). ✓

#### Downgrade: 3D → 2D+TB

| # | DM transition | Group | Resulting layout | q | qmr | GMDR | ADR | FTT |
|---|---------------|-------|------------------|---|-----|------|-----|-----|
| — | — | — | 3**D** | 2 | 2 | 1 | 2 | 1 |
| 1 | AddReplica(TB) | NonVoter | 3**D** + TB | 2 | 2 | 1 | 2 | 1 |
| 2 | qmr↓ + RemoveReplica(D) | Voter | 2**D** + TB | 2 | 1 | 0 | 1 | 1 |

TB added first (tiebreaker needed before going to 2 even voters). D removed
with embedded qmr↓ as first step.

**Constraint**: min(source, target) = GMDR≥0, FTT≥1. After step 1: ✓
(unchanged). After step 2: GMDR=0 ≥ 0 ✓, FTT=1 ≥ 1 ✓ (2 voters + TB). ✓

**Note**: edges #5 (3D ↔ 4D), #6 (3D ↔ 4D+TB), and #8 (4D+TB ↔ 5D) are
structurally identical to #1, #2, and #4 respectively, at higher D/q/qmr
values. The same DM sequences apply — substitute the appropriate D counts
and quorum parameters.

---

## 5. Topology

### Zone-aware replacement

The §2 and §3 procedures are zone-agnostic — they describe DRBD-level steps.
In TransZonal and Zonal topologies, placement and removal guards add
zone-aware constraints (see [TRANSITIONS.md](TRANSITIONS.md) §8).

**Zone placement rule**: the scheduler places the new replica in the zone
with the fewest replicas of the target type (greedy round-robin). This
naturally places the replacement into the zone that lost the replica.

**Safety enforcement**:

- **Placement guards** block additions that would violate zone constraints
  (see [TRANSITIONS.md](TRANSITIONS.md) §8, preconditions for adding D and
  adding TB).
- **Removal guards** block removals that would violate zone-level GMDR, FTT,
  or TB coverage (see [TRANSITIONS.md](TRANSITIONS.md) §8, preconditions
  for leaving D and leaving TB).

No new procedures are needed — §2 and §3 apply as-is with zone constraints
enforced by guards.

### Zone redistribution (D ↔ TB swap)

Zone redistribution changes which zones hold which replica types while
preserving the overall layout. This is different from same-zone replacement —
here replicas intentionally move between zones.

**Ordering principle**: add capacity first, remove last. Remove old TB before
old D from a co-located zone (to avoid a state where the only TB is
co-located with D in a zone whose loss would remove both).

#### Swapping D ↔ TB in 2D+1TB (q=2, qmr=1)

**Goal**: move D from zone A to zone C, move TB from zone C to zone A.

Start: D(zone A) + D(zone B) + TB(zone C).
End: TB(zone A) + D(zone B) + D(zone C).

| # | DM transition | Group | Zone A | Zone B | Zone C | q |
|---|---------------|-------|--------|--------|--------|---|
| — | — | — | D(#0) | D(#1) | TB | 2 |
| 1 | AddReplica(D) [zone C] | Voter | D(#0) | D(#1) | D(#2)+TB | 2 |
| 2 | AddReplica(TB) [zone A] | NonVoter | D(#0)+TB(#3) | D(#1) | D(#2)+TB | 2 |
| 3 | RemoveReplica(TB) [zone C] | NonVoter | D(#0)+TB(#3) | D(#1) | D(#2) | 2 |
| 4 | RemoveReplica(D) [zone A] | Voter | TB(#3) | D(#1) | D(#2) | 2 |

Steps 2 and 3 are both NonVoter — can run in parallel per parallelism rules.
Step 4 (Voter) must wait for step 1 (Voter) to complete (serialization).

**Zone-FTT=1 verification:**

| # | Zone A lost | Zone B lost | Zone C lost |
|---|------------|------------|------------|
| Start | D(B)+TB(C): TB ✓ | D(A)+TB(C): TB ✓ | D(A)+D(B): 2≥2 ✓ |
| After 1 | D(B)+D(C)+TB(C): 2≥2 ✓ | D(A)+D(C)+TB(C): 2≥2 ✓ | D(A)+D(B): 2≥2 ✓ |
| After 2 | D(B)+D(C)+TB(C): 2≥2 ✓ | D(A)+D(C)+TB(A,C): 2≥2 ✓ | D(A)+D(B)+TB(A): 2≥2 ✓ |
| After 3 | D(B)+D(C): 2≥2 ✓ | D(A)+D(C)+TB(A): 2≥2 ✓ | D(A)+D(B)+TB(A): 2≥2 ✓ |
| After 4 | D(B)+D(C): 2≥2 ✓ | D(C)+TB(A): TB ✓ | D(B)+TB(A): TB ✓ |

Zone-FTT=1 maintained throughout. ✓

#### Swapping D ↔ TB in 4D+1TB (q=3, qmr=2)

**Goal**: move D from zone A to zone C, move TB from zone C to zone A.

Start: D+D(zone A) + D(zone B) + D+TB(zone C).
End: D+TB(zone A) + D(zone B) + D+D(zone C).

| # | DM transition | Group | Zone A | Zone B | Zone C | q |
|---|---------------|-------|--------|--------|--------|---|
| — | — | — | D+D | D | D+TB | 3 |
| 1 | AddReplica(D) [zone C] | Voter | D+D | D | D+D+TB | 3 |
| 2 | AddReplica(TB) [zone A] | NonVoter | D+D+TB | D | D+D+TB | 3 |
| 3 | RemoveReplica(TB) [zone C] | NonVoter | D+D+TB | D | D+D | 3 |
| 4 | RemoveReplica(D) [zone A] | Voter | D+TB | D | D+D | 3 |

At steps 1–3 there are 5 D voters (odd, q=3). Any single zone loss removes
at most 2D+TB, leaving ≥3D or ≥2D+TB — sufficient for quorum.

**Transient state**: during steps 2–3, zone C has D+D+TB (3 replicas).
Losing zone C removes 2D+TB, leaving 3D+TB(A) or 3D — still ≥ q=3. Safe. ✓

### Changing D distribution across zones

**D-only layouts** (3D, 4D, 5D): cross-zone move = add D in target zone (§2
add half), sync, remove D from source zone (§2 remove half).

**D+TB layouts** (2D+1TB, 4D+1TB): if the move involves the TB zone, combine
with the D↔TB swap above.

**Constraint**: the resulting distribution must satisfy
`max D per zone ≤ D − qmr`.

### 3-zone TransZonal layout chain

**4D (q=3, qmr=3) is not available in 3-zone TransZonal.** With distribution
2+1+1, losing the 2D zone leaves 2D < qmr=3 — quorum lost. TB cannot help
(tiebreaker requires `utd >= qmr`, and 2 < 3).

The available layouts form a linear chain:

```
2D+1TB (0,1) ——GMDR—— 3D (1,1) ——FTT—— 4D+1TB (1,2) ——GMDR—— 5D (2,2)
```

Each edge maps to a DM transition sequence from §4:

| # | Transition | DM upgrade | DM downgrade |
|---|-----------|-----------|-------------|
| 1 | 2D+1TB ↔ 3D | §4.3: AddReplica(D)+qmr↑, RemoveReplica(TB) | §4.3: AddReplica(TB), qmr↓+RemoveReplica(D) |
| 2 | 3D ↔ 4D+1TB | §4.2 pattern: AddReplica(D)+q↑, AddReplica(TB) | §4.2 pattern: RemoveReplica(TB), RemoveReplica(D)+q↓ |
| 3 | 4D+1TB ↔ 5D | §4.3 pattern: AddReplica(D)+qmr↑, RemoveReplica(TB) | §4.3 pattern: AddReplica(TB), qmr↓+RemoveReplica(D) |

Multi-step example — upgrade 3D → 5D in 3 zones:

| # | Layout | Distribution | Zone FTT | GMDR | FTT |
|---|--------|-------------|---------|------|-----|
| 0 | 3D (q=2, qmr=2) | 1+1+1 | 1 | 1 | 1 |
| 1 | 4D+1TB (q=3, qmr=2) | 2D \| 1D+TB \| 1D | 1 | 1 | 2 |
| 2 | 5D (q=3, qmr=3) | 2+2+1 | 1 | 2 | 2 |

Zone-FTT=1 maintained throughout. Downgrade is the reverse path.

---

## 6. Automatic Failure Recovery

The datamesh controller does not independently detect node failures. It
relies on the Kubernetes node lifecycle: a member is considered permanently
lost when its ReplicatedVolumeReplica (RVR) no longer exists.

### Recovery chain

When a node is permanently lost, the following chain executes automatically:

1. **Node removed from Kubernetes** — the Node object is deleted (by an
   administrator, cloud provider, or by Deckhouse fencing — see below).
2. **RVR cleaned up** — the RVR on the lost node is eventually removed.
3. **Orphan member detected** — the datamesh membership dispatcher sees a
   member with no corresponding RVR and auto-dispatches:
   - **ForceDetach** (if the member was attached — clears the Attached flag
     so removal can proceed).
   - **ForceRemoveReplica** (removes the dead member from the datamesh
     without waiting for confirmation from the dead node).
4. **Layout restoration** — after ForceRemove, the datamesh has fewer
   replicas than the layout requires. The normal membership dispatcher
   creates AddReplica transitions to restore the target layout. The
   scheduling controller places the new replicas on available nodes.

The entire chain — from node deletion to layout restoration — is fully
automatic and requires no manual intervention.

### Deckhouse fencing integration

Deckhouse provides a **fencing** mechanism that accelerates the detection
of permanently failed nodes. In Watchdog mode:

- A **fencing-agent** (DaemonSet) runs on fencing-enabled nodes. It keeps a
  kernel watchdog timer alive by periodically confirming Kubernetes API
  reachability. If the API is unreachable for 60 seconds, the watchdog
  triggers a kernel panic and the node halts.
- A **fencing-controller** monitors all fencing-enabled nodes. If a node is
  unreachable for more than 60 seconds, the controller deletes all pods from
  the node and then deletes the Node object itself.

This deletion of the Node object triggers the recovery chain described above.
Without fencing, the Node object remains in NotReady state indefinitely,
and automatic recovery does not start until the node is explicitly removed.

### Manual recovery

ForceDetach and ForceRemove can also be triggered manually via explicit
membership requests, without waiting for the node to be deleted from
Kubernetes. This is useful when the administrator knows a node is
permanently lost but fencing is not enabled or the node has not yet been
removed.

See [TRANSITIONS.md](TRANSITIONS.md) §2.3 (ForceRemoveReplica) and §4
(ForceDetach) for transition details and preconditions.
