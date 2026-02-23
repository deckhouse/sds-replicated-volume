# DRBD Volume Layouts for SDS with FTT Guarantees

## 1. DRBD Quorum and Replication Mechanics

This section describes DRBD's built-in mechanisms for quorum and data replication. These
are generic DRBD concepts, independent of our SDS specifics.

### 1.1 Peer Classification

Each DRBD peer is classified into one of these categories for quorum calculation:

| Category             | Connected? | Description                                                      |
|----------------------|------------|------------------------------------------------------------------|
| **up_to_date**       | Yes        | Diskful peer with fully synchronized, current data               |
| **present**          | Yes        | Diskful peer with disk in any non-UpToDate state (consistent, syncing, or even diskless if not intentional-diskless) |
| **outdated**         | No         | Disconnected peer, known to have stale data                      |
| **quorumless**       | No         | Disconnected peer that was a known member but itself lost quorum  |
| **unknown**          | No         | Disconnected peer with unknown state                             |
| **diskless**         | Yes        | Connected intentional-diskless peer                              |
| **missing_diskless** | No         | Disconnected intentional-diskless peer                           |

A node also classifies itself by the same rules (based on its own disk state).

Peers with `allow-remote-read = false` are **completely excluded** from the quorum
calculation — they are not counted in any category (not as voters, not as diskless, not
in tiebreaker). From quorum perspective, they do not exist.

### 1.2 Voters and Quorum Condition

Only diskful peers (including disconnected ones) are **voters**:

```
voters = outdated + quorumless + unknown + up_to_date + present
```

Intentional-diskless peers are **NOT** voters. They only participate in the tiebreaker
logic (§1.3).

Quorum is granted when **both** conditions are met:

```
(up_to_date + present) >= q       — enough peers reachable
up_to_date >= qmr                 — enough peers have current data
```

Where `q` (quorum) and `qmr` (quorum-minimum-redundancy) are numeric configuration
parameters.

There is also an alternative quorum path for diskless nodes:

```
have_quorum = quorate_peers >= qmr
```

Where `quorate_peers` counts connected diskful peers that are UpToDate **and** themselves
have quorum. This path is only used by **diskless** nodes; diskful nodes never use it.

### 1.3 Tiebreaker Logic

When the main quorum condition fails, DRBD evaluates an additional tiebreaker path.
It grants quorum if **all** of the following are true:

1. The number of voters is **even**
2. The node is **exactly one vote short**: `up_to_date + present == q − 1`
3. The redundancy check is met: `up_to_date >= qmr`
4. A **majority** of intentional-diskless peers are connected:
   `diskless >= (diskless + missing_diskless) / 2 + 1`
5. The node **already had quorum** before this evaluation (sticky condition)

**Critical properties**:

- **Even voters only** — tiebreaker never fires when the number of voters is odd.
- **Exactly one vote short** — only covers the case of losing exactly half of voters.
- **Respects qmr** — tiebreaker cannot override the data redundancy requirement.
- **Sticky** — prevents both sides of a network partition from claiming quorum
  simultaneously. Only the side that had quorum before the partition can keep it.

### 1.4 Protocol C and Write Acknowledgment

With Protocol C, a write is acknowledged to the application only after:
1. The local disk has completed the write, **AND**
2. All connected peers with UpToDate disk have completed the write.

Intentional-diskless nodes have no disk and are transparent to Protocol C — they do
**not** add write latency.

**Crucial consequence**: The minimum number of nodes holding ALL acknowledged data at any
moment equals the number of currently connected UpToDate nodes. This number is bounded
below by the `qmr` setting (because if fewer than `qmr` nodes are UpToDate, quorum is
lost and IO stops).

---

## 2. SDS Replica Model

This section describes how our SDS maps its replica types and configuration onto the
DRBD mechanisms from §1.

### 2.1 Replica Types

The SDS has four replica types:

| Type | Notation | Disk | Bitmap | Connections | Quorum role | Own quorum via | Purpose |
|------|----------|------|--------|-------------|-------------|----------------|---------|
| **Diskful** | **D** | Yes | Yes | All D + all TB + all A + all sD | Voter (up_to_date / present) | Main condition (§1.2) | Stores data |
| **ShadowDiskful** | **sD** | Yes | Yes | All D + all TB + all A + all sD | None (excluded via arr=false + non-voting) | quorate_peers path (§1.2) | Pre-sync data before becoming D |
| **TieBreaker** | **TB** | No (intentional-diskless) | No | Star: only to D + sD | Tiebreaker only (§1.3) | quorate_peers path (§1.2) | Breaks ties in even-voter quorum |
| **Access** | **A** | No (intentional-diskless) | No | Star: only to D + sD | None (excluded via arr=false) | quorate_peers path (§1.2) | IO access via DRBD |

> **sD requires the `non-voting` DRBD disk option** available only in the Flant DRBD
> kernel extension (9.2.16-flant.1+). On kernels without this extension, the controller
> falls back to using A replicas as vestibules in place of sD. Procedures in §8 provide
> both variants ("a" — without sD, "b" — with sD).

D and sD replicas form a **fully connected mesh** with all other replicas. TB and A replicas
connect **only to full-mesh members** (D and sD), never to each other.

**sD vs D**: sD is like D (has disk, bitmap, syncs data via Protocol C) but is **invisible
to quorum** from both sides — `non-voting=true` on self excludes it from its own quorum
calculation, and `allow-remote-read=false` on peers excludes it from theirs. This allows
sD to pre-sync data silently before being promoted to a full voting D replica.

**Replica types vs member types.** The SDS distinguishes two type layers:

- **Replica type** (`ReplicaType`) — the desired role assigned at creation: D, sD, TB, A.
- **Member type** (`DatameshMemberType`) — the operational state within the datamesh.
  Includes the four replica types plus two **liminal** variants: D∅ and sD∅ (see §2.2).

### 2.2 Liminal Types (D∅, sD∅)

D and sD each have a **liminal** variant — a distinct member type where the replica has
its target role (voter status, full-mesh connectivity) but DRBD is configured as diskless.
The backing volume is provisioned but not attached to DRBD.

- **D∅** (`LiminalDiskful`) — a D replica without disk. Still a voter, counted as `present`.
- **sD∅** (`LiminalShadowDiskful`) — an sD replica without disk. Still invisible to quorum
  (like sD). Functionally equivalent to A.

Liminal types are used during type transitions:

```
Without sD (fallback):
  TB or A  ──[become D∅]──[attach disk]──→  D        (gaining disk)
  D  ──[detach disk = become D∅]──[become TB or A]──→  TB or A   (losing disk)

With sD (requires non-voting):
  sD  ──[detach disk]──→  sD∅  ──[become D∅ + q↑]──→  D∅  ──[attach]──→  D
  D  ──[detach]──→  D∅  ──[become sD∅ + q↓]──→  sD∅  ──[remove or keep as sD]
```

| Property | D | D∅ | sD | sD∅ | TB / A |
|----------|---|-----|-----|------|--------|
| Bitmap on peers | Yes | Yes | Yes | Yes | No |
| Connections | All replicas | All replicas | All replicas | All replicas | Star (to D + sD) |
| Voter | Yes | Yes | No | No | No |
| Disk | Attached | Detached | Attached | Detached | None |
| Quorum on peers (§1.1) | `up_to_date` | `present` | excluded (arr=false) | excluded (arr=false) | diskless / excluded |
| Own quorum | Main (§1.2) | Main (§1.2) | quorate_peers | quorate_peers | quorate_peers |

In the quorum calculation on D replicas, a connected D∅ is counted as `present` —
it contributes to `(up_to_date + present) ≥ q`, but **not** to `up_to_date ≥ qmr`.
A disconnected D∅ is counted as `unknown` (voter, works against quorum).

D∅ replicas **help reach q** without inflating the data safety guarantee.

sD and sD∅ replicas are **completely invisible** to quorum on all peers (via arr=false +
non-voting). They neither help nor hurt.

### 2.3 Quorum Settings by Replica Type

D replicas (including D∅) use the layout-specific `q` and `qmr` values from §6.

A, TB, and sD replicas always use **q = 32** (an impossibly high value — our DRBD metadata
is configured for max 7 peers, i.e. 8 replicas total). This means A/TB/sD can **never**
satisfy the main quorum condition `(up_to_date + present) ≥ q` on their own. Their quorum
is determined entirely by the alternative `quorate_peers` path (§1.2):

```
have_quorum = quorate_peers >= qmr
```

In effect, **A, TB, and sD replicas have quorum iff they see ≥ qmr quorate D peers**.

All connections **toward** A and sD replicas are configured with `allow-remote-read = false`.
As described in §1.1, this completely excludes them from the quorum calculation on all
peers — they are not counted as voters, not as diskless, and not in the tiebreaker.

Additionally, sD replicas use the DRBD `non-voting=true` disk option, which excludes them
from their own quorum calculation as a voter (they don't count themselves in `voters`).
This double exclusion (arr=false on peers + non-voting on self) makes sD fully invisible
to quorum from both sides.

### 2.4 Configuration Propagation Mechanism

The SDS controller maintains a single **target configuration** for each volume. Replicas
apply this configuration asynchronously:

1. Controller publishes a new target configuration (updates `Datamesh` and increments
   `DatameshRevision`).
2. Each replica detects the change, applies it locally, and reports back — either
   **success** or **error**.
3. Controller waits for confirmations from the **required subset** of replicas (see §2.5)
   before proceeding to the next step.

This means configuration changes are **not atomic** across the cluster. During propagation,
different replicas may temporarily have different configurations. The procedures in §8 are
designed to be safe during these transient states.

### 2.5 Configuration Change Impact Matrix

Different changes affect different replica subsets. The controller must wait for the
appropriate subset before proceeding:

| Change | Wait for | Why |
|--------|----------|-----|
| Add/remove D replica | **All replicas** (D + sD + A + TB) | D connects to all; bitmap and topology affect everyone |
| Add/remove sD replica | **All replicas** (D + sD + A + TB) | sD connects to all; bitmap and topology affect everyone |
| Add/remove A replica | **D + sD + A replicas only** | A connects to D and sD; TB is unaffected |
| Add/remove TB replica | **D + sD + TB replicas only** | TB connects to D and sD; A is unaffected |
| D ↔ D∅ (set/clear ∅ flag) | **All replicas** (D + sD + A + TB) | Changes bitmap allocation and connection topology |
| sD ↔ sD∅ (set/clear ∅ flag) | **All replicas** (D + sD + A + TB) | sD connects to all; bitmap and topology affect everyone |
| Change `q` | **D replicas only** | A and TB always use q=32; their q never changes |
| Change `qmr` | **All replicas** (D + sD + A + TB) | A, TB, and sD use qmr for their quorum path |
| Attach disk (D∅ → D) | **D replicas only** | Local operation + resync with D peers |
| Detach disk (D → D∅) | **D replicas only** | Local operation |

---

## 3. FTT Definitions

### 3.1 beforeDataLoss (FTT-BDL)

How many **permanent disk losses** can occur at any point while the volume is serving IO,
without losing any acknowledged data.

Since Protocol C writes to all connected UpToDate peers, and `qmr` is the minimum number
of UpToDate nodes required for IO to continue:

```
FTT-BDL = qmr − 1
```

**Why**: While IO is being served, at least `qmr` nodes have every acknowledged write
(Protocol C guarantees this). Permanently destroying `qmr − 1` of those disks leaves at
least 1 node with all data. Destroying all `qmr` → data lost.

Example: with `qmr=2`, the system can operate with as few as 2 UpToDate nodes. If both
of those nodes' disks are permanently destroyed, the newest data (written while only those
2 were active) is lost. A third node that was temporarily disconnected has only older data.
So FTT-BDL = 1, not 2, even though 3 disk copies may exist when all nodes are healthy.

### 3.2 beforeUnavailability (FTT-BUA)

How many **arbitrary** node failures (diskful or tiebreaker — crash, network partition,
disk failure) the system can tolerate simultaneously while remaining available (maintaining
quorum **and** at least `qmr` accessible UpToDate disks).

### 3.3 Practical beforeDataLoss (pFTT-BDL)

The current data redundancy level: how many disks can be permanently destroyed **right now**
without any data loss. Simply equals the number of UpToDate D replicas minus 1:

```
pFTT-BDL = UpToDate_D_count − 1
```

Unlike FTT-BDL (which is the worst-case guarantee determined by `qmr`), pFTT-BDL reflects
the actual number of data copies at this moment. **pFTT-BDL ≥ FTT-BDL** always.

Example: a 2D+1TB layout (FTT-BDL=0, qmr=1) has pFTT-BDL=1 when both D replicas are
UpToDate, but FTT-BDL=0 because the system is *permitted* to continue IO with 1 copy (via
tiebreaker after a temporary failure).

### 3.4 Key Formulas (FTT-BDL / FTT-BUA → DRBD settings)

```
qmr = FTT-BDL + 1
D   = FTT-BUA + FTT-BDL + 1    (minimum diskful nodes)
q   = floor(D / 2) + 1          (minimum safe quorum value)
TB  = 1  if D is even AND FTT-BUA = D / 2, else 0
```

**Why D = FTT-BUA + FTT-BDL + 1**: After losing FTT-BUA diskful nodes (worst case), the
remaining `D − FTT-BUA` nodes must still satisfy `qmr`: `D − FTT-BUA ≥ qmr = FTT-BDL + 1`.

**Why q = floor(D/2) + 1**: This is the minimum `q` that prevents split-brain (no
partition can have both sides meet the quorum threshold). With even D, the tiebreaker
mechanism resolves ties.

**Why TB is needed**: When D is even, `q = D/2 + 1`. After losing exactly `D/2` nodes
(= FTT-BUA), only `D/2` remain, which is `q − 1` — one vote short. The tiebreaker
bridges this gap (if `qmr` is still met). Without TB, the maximum FTT-BUA for even D is
only `D/2 − 1`.

### 3.5 Valid Combinations (|FTT-BDL − FTT-BUA| ≤ 1, both ∈ {0, 1, 2})

| FTT-BDL | FTT-BUA | qmr | D | q | TB | Total nodes |
|---------|---------|-----|---|---|----|-------------|
| 0       | 0       | 1   | 1 | 1 | 0  | 1           |
| 0       | 1       | 1   | 2 | 2 | 1  | 3           |
| 1       | 0       | 2   | 2 | 2 | 0  | 2           |
| 1       | 1       | 2   | 3 | 2 | 0  | 3           |
| 1       | 2       | 2   | 4 | 3 | 1  | 5           |
| 2       | 1       | 3   | 4 | 3 | 0  | 4           |
| 2       | 2       | 3   | 5 | 3 | 0  | 5           |

---

## 4. Layout Definitions

### Notation

```
kD + nTB (q=x, qmr=y)
```

- **kD** — k diskful replicas (protocol C)
- **nTB** — n tiebreaker replicas (intentional-diskless, `--bitmap=no`)
- **D∅** — diskful replica without disk attached (see §2.2); used in transition procedures
- **sD** — shadow diskful replica (see §2.1); pre-syncs data invisibly. *Requires `non-voting` DRBD option.*
- **sD∅** — shadow diskful without disk (see §2.2); functionally equivalent to A
- **q=x** — DRBD `quorum` setting (always a numeric value)
- **qmr=y** — DRBD `quorum-minimum-redundancy` setting (always a numeric value)

If no tiebreakers: `kD (q=x, qmr=y)`.

---

## 5. Detailed Analysis of Each Layout

### 5.1 FTT-BDL=0, FTT-BUA=0 → `1D (q=1, qmr=1)`

**Configuration**: 1 diskful node. Total: **1 node**.

| Scenario | up_to_date | voters | q=1 | qmr=1 | Result |
|----------|-----------|--------|-----|--------|--------|
| All OK   | 1         | 1      | ✓   | ✓      | **Available** |
| 1D fails | 0         | 1      | ✗   | ✗      | Data loss + unavailable |

No redundancy. Single permanent disk loss = data loss. Suitable for ephemeral data.

---

### 5.2 FTT-BDL=0, FTT-BUA=1 → `2D + 1TB (q=2, qmr=1)`

**Configuration**: 2 diskful + 1 tiebreaker. Total: **3 nodes**.

Voters=2, `quorum_at=2`, `min_redundancy_at=1`.

| Scenario | up_to_date | diskless | q=2 main | Tiebreaker? | Result |
|----------|-----------|----------|----------|-------------|--------|
| All OK   | 2         | 1        | 2≥2 ✓    | —           | **Available** |
| 1D fails | 1         | 1        | 1<2 ✗    | even ✓, 1=2-1 ✓, qmr(1≥1) ✓, TB(1≥1) ✓, sticky ✓ | **Available** |
| 1TB fails| 2         | 0        | 2≥2 ✓    | —           | **Available** |
| 1D+1TB   | 1         | 0        | 1<2 ✗    | TB: 0<1 ✗   | **Unavailable** |
| 2D fail  | 0         | 1        | ✗        | ✗           | **Data loss + unavailable** |

Tolerates any single failure for availability. But `qmr=1` means: in degraded mode (1D
down), writes go to only 1 disk. Permanent loss of that disk = data lost (FTT-BDL=0).

**Network partition safety**:

| Partition         | Side A                                          | Side B              |
|-------------------|-------------------------------------------------|---------------------|
| (1D+1TB) vs (1D)  | TB saves: **Available**                         | No TB: **Unavail.** |
| (2D) vs (1TB)     | 2≥2 ✓: **Available**                            | No data             |

---

### 5.3 FTT-BDL=1, FTT-BUA=0 → `2D (q=2, qmr=2)`

**Configuration**: 2 diskful nodes. Total: **2 nodes**.

Voters=2, `quorum_at=2`, `min_redundancy_at=2`.

| Scenario | up_to_date | voters | q=2 | qmr=2 | Result |
|----------|-----------|--------|-----|--------|--------|
| All OK   | 2         | 2      | ✓   | ✓      | **Available** |
| 1D fails | 1         | 2      | ✗   | ✗      | **Unavailable** (data safe — 1 copy) |

Both nodes must be present for IO. Losing either → IO stops immediately. But since both
always have all data (`qmr=2`, Protocol C), 1 permanent disk loss is safe (FTT-BDL=1).

---

### 5.4 FTT-BDL=1, FTT-BUA=1 → `3D (q=2, qmr=2)`

**Configuration**: 3 diskful nodes. Total: **3 nodes**.

Voters=3, `quorum_at=2`, `min_redundancy_at=2`.

| Scenario | up_to_date | voters | q=2 | qmr=2 | Result |
|----------|-----------|--------|-----|--------|--------|
| All OK   | 3         | 3      | 3≥2 ✓ | 3≥2 ✓ | **Available** |
| 1D fails | 2         | 3      | 2≥2 ✓ | 2≥2 ✓ | **Available** |
| 2D fail  | 1         | 3      | 1<2 ✗ | —     | **Unavailable** (data safe — 1 copy) |

In degraded mode (1D down), IO continues with 2 UpToDate nodes (`qmr=2` met). Writes go
to 2 disks via Protocol C. Permanent loss of 1 of those 2 → the other still has all data.
Permanent loss of both → data lost. FTT-BDL=1.

**Network partition safety**:

| Partition  | Side A (2D) | Side B (1D) |
|------------|-------------|-------------|
| 2D vs 1D   | 2≥2 ✓: **Available** | 1<2 ✗: **Unavailable** |

---

### 5.5 FTT-BDL=1, FTT-BUA=2 → `4D + 1TB (q=3, qmr=2)`

**Configuration**: 4 diskful + 1 tiebreaker. Total: **5 nodes**.

Voters=4, `quorum_at=3`, `min_redundancy_at=2`.

| Scenario  | up_to_date | diskless | q=3 main | Tiebreaker? | Result |
|-----------|-----------|----------|----------|-------------|--------|
| All OK    | 4         | 1        | 4≥3 ✓    | —           | **Available** |
| 1D fails  | 3         | 1        | 3≥3 ✓    | —           | **Available** |
| 2D fail   | 2         | 1        | 2<3 ✗    | even ✓, 2=3-1 ✓, qmr(2≥2) ✓, TB(1≥1) ✓, sticky ✓ | **Available** |
| 1D+1TB    | 3         | 0        | 3≥3 ✓    | —           | **Available** |
| 2D+1TB    | 2         | 0        | 2<3 ✗    | no TB       | **Unavailable** |
| 3D fail   | 1         | 1        | 1<3 ✗    | 1≠3-1=2     | **Unavailable** |

All 2-failure scenarios:

| 2 failures | Result |
|------------|--------|
| 2D         | ✓ (tiebreaker) |
| 1D + 1TB   | ✓ (main: 3≥3) |

After losing 2D: 2 UpToDate remain (≥ `qmr=2`). Writes go to 2 disks. 1 more permanent
disk loss → 1 copy remains, data safe. FTT-BDL=1.

**Network partition safety**:

| Partition              | Side A                | Side B              |
|------------------------|-----------------------|---------------------|
| (2D+1TB) vs (2D)       | TB saves: **Avail.**  | No TB: **Unavail.** |
| (3D) vs (1D+1TB)       | 3≥3 ✓: **Avail.**    | 1<3 ✗: **Unavail.** |
| (3D+1TB) vs (1D)       | 3≥3 ✓: **Avail.**    | 1<3 ✗: **Unavail.** |

---

### 5.6 FTT-BDL=2, FTT-BUA=1 → `4D (q=3, qmr=3)`

**Configuration**: 4 diskful nodes. Total: **4 nodes**.

Voters=4, `quorum_at=3`, `min_redundancy_at=3`.

| Scenario | up_to_date | voters | q=3 | qmr=3 | Result |
|----------|-----------|--------|-----|--------|--------|
| All OK   | 4         | 4      | 4≥3 ✓ | 4≥3 ✓ | **Available** |
| 1D fails | 3         | 4      | 3≥3 ✓ | 3≥3 ✓ | **Available** |
| 2D fail  | 2         | 4      | 2<3 ✗ | 2<3 ✗ | **Unavailable** (data safe) |

In degraded mode (1D down), IO continues with 3 UpToDate nodes (`qmr=3` met). Writes go
to 3 disks. Permanent loss of 2 of those 3 → 1 copy remains. FTT-BDL=2.

**Note on tiebreaker**: Adding a TB to this layout would NOT help with 2D failure, because
the tiebreaker requires `up_to_date >= qmr` (i.e. 2 ≥ 3), which fails. With `qmr=3`, the
tiebreaker can never fire when UpToDate < 3.

**Network partition safety**:

| Partition  | Side A | Side B |
|------------|--------|--------|
| 3D vs 1D   | 3≥3 ✓: **Available** | 1<3 ✗: **Unavailable** |
| 2D vs 2D   | 2<3 ✗: **Unavailable** | 2<3 ✗: **Unavailable** |

The 2-2 partition makes both sides unavailable. This is safe (no split-brain), but means
an even network split takes down the entire volume. This is a 2-failure scenario (each side
sees 2 missing nodes), exceeding FTT-BUA=1.

---

### 5.7 FTT-BDL=2, FTT-BUA=2 → `5D (q=3, qmr=3)`

**Configuration**: 5 diskful nodes. Total: **5 nodes**.

Voters=5, `quorum_at=3`, `min_redundancy_at=3`.

| Scenario | up_to_date | voters | q=3 | qmr=3 | Result |
|----------|-----------|--------|-----|--------|--------|
| All OK   | 5         | 5      | 5≥3 ✓ | 5≥3 ✓ | **Available** |
| 1D fails | 4         | 5      | 4≥3 ✓ | 4≥3 ✓ | **Available** |
| 2D fail  | 3         | 5      | 3≥3 ✓ | 3≥3 ✓ | **Available** |
| 3D fail  | 2         | 5      | 2<3 ✗ | 2<3 ✗ | **Unavailable** (data safe) |

After losing 2D: 3 UpToDate remain (≥ `qmr=3`). Writes go to 3 disks. Permanent loss
of 2 of those 3 → 1 copy remains. FTT-BDL=2.

**No tiebreaker needed**: 5 voters is odd, so strict majority (3) always resolves
partitions unambiguously.

**Network partition safety**:

| Partition  | Side A | Side B |
|------------|--------|--------|
| 3D vs 2D   | 3≥3 ✓: **Available** | 2<3 ✗: **Unavailable** |

---

## 6. Summary Table

| FTT-BDL | pFTT-BDL | FTT-BUA | Layout       | q | qmr | D | TB | Total | Min copies during IO | Write latency (Protocol C) |
|---------|----------|---------|--------------|---|-----|---|----|-------|---------------------|---------------------------|
| 0       | 0        | 0       | **1D**       | 1 | 1   | 1 | 0  | 1     | 1                   | 1 disk write              |
| 0       | 1        | 1       | **2D+1TB**   | 2 | 1   | 2 | 1  | 3     | 1                   | max 2 disk writes (parallel) |
| 1       | 1        | 0       | **2D**       | 2 | 2   | 2 | 0  | 2     | 2                   | 2 disk writes (parallel)  |
| 1       | 2        | 1       | **3D**       | 2 | 2   | 3 | 0  | 3     | 2                   | max 3 disk writes (parallel) |
| 1       | 3        | 2       | **4D+1TB**   | 3 | 2   | 4 | 1  | 5     | 2                   | max 4 disk writes (parallel) |
| 2       | 3        | 1       | **4D**       | 3 | 3   | 4 | 0  | 4     | 3                   | max 4 disk writes (parallel) |
| 2       | 4        | 2       | **5D**       | 3 | 3   | 5 | 0  | 5     | 3                   | max 5 disk writes (parallel) |

### Column notes

- **Min copies during IO**: The minimum number of disks holding all acknowledged data
  while IO is being served. Equals `qmr`. This is the true redundancy guarantee during
  operation.
- **Write latency**: Protocol C waits for the slowest disk write among all connected
  UpToDate peers. When all D nodes are healthy, latency = max(D parallel writes). In
  degraded mode, fewer writes. Tiebreakers do not add latency.

---

## 7. Practical Recommendations

| Use Case | Recommended | Total | Why |
|----------|-------------|-------|-----|
| Development / ephemeral / externally replicated | FTT-BDL=0, FTT-BUA=0 (1D) | 1 | Minimal resources; OK if data is replicated by other means |
| Production, availability-first | FTT-BDL=0, FTT-BUA=1 (2D+1TB) | 3 | Stays available through 1 failure; 2 copies when healthy |
| Production, data-safety-first | FTT-BDL=1, FTT-BUA=0 (2D) | 2 | Always writes to 2 disks; stops on any failure |
| Production, balanced | FTT-BDL=1, FTT-BUA=1 (3D) | 3 | 2+ copies guaranteed, tolerates 1 failure |
| Production, high availability | FTT-BDL=1, FTT-BUA=2 (4D+1TB) | 5 | 2+ copies guaranteed, tolerates 2 failures |
| Mission-critical, data safety | FTT-BDL=2, FTT-BUA=1 (4D) | 4 | 3+ copies guaranteed, tolerates 1 failure |
| Mission-critical, full | FTT-BDL=2, FTT-BUA=2 (5D) | 5 | 3+ copies guaranteed, tolerates 2 failures |

### Understanding the FTT-BDL / FTT-BUA trade-off

These are **independent axes** — neither must be ≤ or ≥ the other:

- **FTT-BDL > FTT-BUA** (e.g. 1, 0): System stops IO early (before any risk to data).
  Maximum data safety, but more downtime.
- **FTT-BDL = FTT-BUA** (e.g. 1, 1): System stays available until the moment one more
  failure would risk data. Balanced.
- **FTT-BDL < FTT-BUA** (e.g. 0, 1): System prioritizes availability — stays running even
  when data redundancy has dropped to the last copy. Riskier for data, fewer outages.

---

## 8. Diskful Replica Replacement Procedures

This section describes step-by-step procedures for replacing a diskful replica for each
layout. The procedures are designed to:

1. **Never reduce FTT-BDL/FTT-BUA guarantees** below the target level
2. **Never interrupt IO** (maintain quorum throughout)
3. **Never create split-brain risk** (maintain safe quorum settings)

### Common concepts

**Old replica retention**: The procedures below show the old replica being removed at the
end. In practice, if the old replica is still in use (e.g. as an IO access point), it can
be kept as an **A** replica instead of being removed. Since A replicas are invisible to
quorum (arr=false, non-voter), this does not affect any FTT-BDL/FTT-BUA guarantees.

**Two replacement patterns** depending on D parity:

| D parity | +D∅ → voters | q change? | Vestibule? | Sections |
|----------|-------------|-----------|------------|----------|
| Even | Odd | No — q is already a safe majority | No — D∅ added directly | 8.2, 8.3, 8.5, 8.6 |
| Odd | Even | Yes — q must be raised by 1 | A (fallback) or sD (with non-voting) | 8.1, 8.4, 8.7 |

**Why odd D needs a vestibule**: Adding D∅ to odd D gives even voters. With even voters,
q = D/2 + 1 would cause a symmetric partition (D/2 vs D/2) to split-brain. So q must be
raised before adding the voter. The vestibule (A or sD) ensures connections are established
while quorum is unaffected. The q raise and D∅ conversion happen in a single fast config
push — no intermediate unsafe state.

**Split-brain safety rule**: At every step, `q = floor(voters / 2) + 1` ensures no
partition can give both sides quorum. With odd voters, majority is unambiguous. With even
voters, the higher q prevents symmetric splits.

**sD variant general principle** (requires `non-voting` DRBD option, see §2.1): In every
procedure, the "a" variant uses A as vestibule + D∅ with a full resync (hours). The "b"
variant pre-syncs data via sD (invisible to quorum), then promotes sD to D. The promotion
path depends on D parity:

- **Even D** (no q change): sD converts directly to D via hot `non-voting` flag change
  (`sD → D`). No detach-reattach needed. The bulk sync happens as sD with zero quorum
  impact; the promotion itself is instantaneous (no wider failure surface window at all).
- **Odd D** (q↑ needed): sD detaches the disk (`sD → sD∅`), converts to D∅ + raises q
  (`sD∅ → D∅ + q↑`), and re-attaches with only a **delta resync** (seconds). The direct
  `sD → D` path cannot be used because q must be raised atomically with the voter addition.

On kernels without `non-voting`, only the "a" variant is available.

### 8.1 Replacing a replica in 1D (FTT-BDL=0, FTT-BUA=0)

**Goal**: Replace D replica #0 with new D replica #1. IO must not stop. No split-brain.

#### 8.1a Without sD

New replica #1 enters as **A** (invisible to quorum), then is converted to D∅ + q raised
in a single config push. This ensures connections are established while quorum is
unaffected. The same pattern is used in reverse for removal.

| # | Layout | q | qmr | FTT-BDL | pFTT-BDL | FTT-BUA | Action | Wait condition |
|---|--------|---|-----|---------|----------|---------|--------|----------------|
| 0 | **D**(#0) | 1 | 1 | 0 | 0 | 0 | Add A(#1) | D+A replicas: #1 connected |
| 1 | **D**(#0) + **A**(#1) | 1 | 1 | 0 | 0 | 0 | Convert #1: A → D∅ + raise q → 2 | All: #1 is D∅ + D: q=2 |
| 2 | **D**(#0) + **D∅**(#1) | 2 | 1 | 0 | 0 | 0 | Attach disk on #1 | #1: `D_UP_TO_DATE` |
| 3 | **D**(#0) + **D**(#1) | 2 | 1 | 0 | 1 | 0 | Detach disk on #0 | D replicas: #0 disk detached |
| 4 | **D∅**(#0) + **D**(#1) | 2 | 1 | 0 | 0 | 0 | Convert #0: D∅ → A + lower q → 1 | All: #0 is A + D: q=1 |
| 5 | **A**(#0) + **D**(#1) | 1 | 1 | 0 | 0 | 0 | Remove A(#0) | D+A replicas: #0 removed |
| 6 | **D**(#1) | 1 | 1 | 0 | 0 | 0 | — | — |

**Temporary operational impact** (steps 2–4): With 2 voters and q=2, a crash of **either**
D or D∅ replica causes quorum loss → IO suspension. This wider failure surface lasts for
the **full resync duration** (hours for large volumes). FTT-BUA remains 0 (tolerance of 0
failures), so the **guarantee level** is unchanged.

#### 8.1b With sD

New replica #1 enters as **sD** and pre-syncs data invisibly. Then disk is detached
(sD → sD∅), converted to D∅ + q raised, and disk re-attached with only a delta resync.
The wider failure surface window is reduced from hours to seconds.

| # | Layout | q | qmr | FTT-BDL | pFTT-BDL | FTT-BUA | Action | Wait condition |
|---|--------|---|-----|---------|----------|---------|--------|----------------|
| 0 | **D**(#0) | 1 | 1 | 0 | 0 | 0 | Add sD(#1) | #1: `D_UP_TO_DATE` |
| 1 | **D**(#0) + **sD**(#1) | 1 | 1 | 0 | 0 | 0 | Detach disk on #1 | D+sD replicas: #1 disk detached |
| 2 | **D**(#0) + **sD∅**(#1) | 1 | 1 | 0 | 0 | 0 | Convert #1: sD∅ → D∅ + raise q → 2 | All: #1 is D∅ + D: q=2 |
| 3 | **D**(#0) + **D∅**(#1) | 2 | 1 | 0 | 0 | 0 | Attach disk on #1 | #1: `D_UP_TO_DATE` (delta) |
| 4 | **D**(#0) + **D**(#1) | 2 | 1 | 0 | 1 | 0 | Detach disk on #0 | D replicas: #0 disk detached |
| 5 | **D∅**(#0) + **D**(#1) | 2 | 1 | 0 | 0 | 0 | Convert #0: D∅ → A + lower q → 1 | All: #0 is A + D: q=1 |
| 6 | **A**(#0) + **D**(#1) | 1 | 1 | 0 | 0 | 0 | Remove A(#0) | D+A replicas: #0 removed |
| 7 | **D**(#1) | 1 | 1 | 0 | 0 | 0 | — | — |

**Note**: pFTT-BDL is 0 in the table (sD copies don't count as D), but data physically
exists on 2 disks during sD sync phase.

### 8.2 Replacing a replica in 2D + 1TB (FTT-BDL=0, FTT-BUA=1)

**Goal**: Replace D replica #0 with new D replica #2. Maintain FTT-BDL≥0 and FTT-BUA≥1 throughout.

#### 8.2a Without sD

This procedure is simpler than §8.1 because **no q/qmr changes are needed**. Adding a 3rd
voter to a q=2 configuration produces 3 odd voters, where q=2 is a safe majority. All
partitions resolve unambiguously (2 vs 1).

| # | Layout | q | qmr | FTT-BDL | pFTT-BDL | FTT-BUA | Action | Wait condition |
|---|--------|---|-----|---------|----------|---------|--------|----------------|
| 0 | **D**(#0) + **D**(#1) + TB | 2 | 1 | 0 | 1 | 1 | Add D∅(#2) | All replicas: #2 configured and connected |
| 1 | **D**(#0) + **D**(#1) + **D∅**(#2) + TB | 2 | 1 | 0 | 1 | 1 | Attach disk on #2 | #2: `D_UP_TO_DATE` |
| 2 | **D**(#0) + **D**(#1) + **D**(#2) + TB | 2 | 1 | 0 | 2 | 1 | Detach disk on #0 | D replicas: #0 disk detached |
| 3 | **D∅**(#0) + **D**(#1) + **D**(#2) + TB | 2 | 1 | 0 | 1 | 1 | Remove #0 | All replicas: #0 removed |
| 4 | **D**(#1) + **D**(#2) + TB | 2 | 1 | 0 | 1 | 1 | — | — |

**Quorum verification at each step** (from D replica perspective):

| # | voters | up_to_date | present | (utd+prs) ≥ q? | utd ≥ qmr? | Quorum? |
|---|--------|-----------|---------|-----------------|------------|---------|
| 0 | 2      | 2(#0,#1)  | 0       | 2 ≥ 2 ✓         | 2 ≥ 1 ✓    | ✓       |
| 1 | 3      | 2(#0,#1)  | 1(#2)   | 3 ≥ 2 ✓         | 2 ≥ 1 ✓    | ✓       |
| 2 | 3      | 3(#0,#1,#2) | 0     | 3 ≥ 2 ✓         | 3 ≥ 1 ✓    | ✓       |
| 3 | 3      | 2(#1,#2)  | 1(#0)   | 3 ≥ 2 ✓         | 2 ≥ 1 ✓    | ✓       |
| 4 | 2      | 2(#1,#2)  | 0       | 2 ≥ 2 ✓         | 2 ≥ 1 ✓    | ✓       |

**FTT-BUA=1 verification** — single-failure scenarios at step 1 (worst case during transition, 3 voters):

| Failure | up_to_date | present | (utd+prs) ≥ 2? | utd ≥ 1? | Quorum? |
|---------|-----------|---------|-----------------|----------|---------|
| #0 (D) fails | 1(#1) | 1(#2 D∅) | 2 ≥ 2 ✓ | 1 ≥ 1 ✓ | ✓ |
| #1 (D) fails | 1(#0) | 1(#2 D∅) | 2 ≥ 2 ✓ | 1 ≥ 1 ✓ | ✓ |
| #2 (D∅) fails | 2(#0,#1) | 0 | 2 ≥ 2 ✓ | 2 ≥ 1 ✓ | ✓ |
| TB fails | 2(#0,#1) | 1(#2) | 3 ≥ 2 ✓ | 2 ≥ 1 ✓ | ✓ |

**pFTT-BDL is 1 throughout** and rises to 2 at step 2 (three UpToDate).

#### 8.2b With sD

sD pre-syncs data invisibly, then converts directly to D (even D, no q change needed).

| # | Layout | q | qmr | FTT-BDL | pFTT-BDL | FTT-BUA | Action | Wait condition |
|---|--------|---|-----|---------|----------|---------|--------|----------------|
| 0 | **D**(#0) + **D**(#1) + TB | 2 | 1 | 0 | 1 | 1 | Add sD(#2) | #2: `D_UP_TO_DATE` |
| 1 | **D**(#0) + **D**(#1) + **sD**(#2) + TB | 2 | 1 | 0 | 1 | 1 | Convert #2: sD → D | All replicas: #2 is D |
| 2 | **D**(#0) + **D**(#1) + **D**(#2) + TB | 2 | 1 | 0 | 2 | 1 | Detach disk on #0 | D replicas: #0 disk detached |
| 3 | **D∅**(#0) + **D**(#1) + **D**(#2) + TB | 2 | 1 | 0 | 1 | 1 | Remove #0 | All replicas: #0 removed |
| 4 | **D**(#1) + **D**(#2) + TB | 2 | 1 | 0 | 1 | 1 | — | — |

### 8.3 Replacing a replica in 2D (FTT-BDL=1, FTT-BUA=0)

**Goal**: Replace D replica #0 with new D replica #2. Maintain FTT-BDL≥1 throughout.

#### 8.3a Without sD

Like §8.2a, **no q/qmr changes are needed**. Adding a 3rd voter to q=2 produces 3 odd
voters — a safe majority. The key difference from §8.2 is `qmr=2`: both existing D
replicas must stay UpToDate for IO to continue. This is already the baseline (FTT-BUA=0),
so the procedure does not widen the failure surface.

| # | Layout | q | qmr | FTT-BDL | pFTT-BDL | FTT-BUA | Action | Wait condition |
|---|--------|---|-----|---------|----------|---------|--------|----------------|
| 0 | **D**(#0) + **D**(#1) | 2 | 2 | 1 | 1 | 0 | Add D∅(#2) | All replicas: #2 configured and connected |
| 1 | **D**(#0) + **D**(#1) + **D∅**(#2) | 2 | 2 | 1 | 1 | 0 | Attach disk on #2 | #2: `D_UP_TO_DATE` |
| 2 | **D**(#0) + **D**(#1) + **D**(#2) | 2 | 2 | 1 | 2 | 0 | Detach disk on #0 | D replicas: #0 disk detached |
| 3 | **D∅**(#0) + **D**(#1) + **D**(#2) | 2 | 2 | 1 | 1 | 0 | Remove #0 | All replicas: #0 removed |
| 4 | **D**(#1) + **D**(#2) | 2 | 2 | 1 | 1 | 0 | — | — |

**Quorum verification at each step** (from D replica perspective):

| # | voters | up_to_date | present | (utd+prs) ≥ q? | utd ≥ qmr? | Quorum? |
|---|--------|-----------|---------|-----------------|------------|---------|
| 0 | 2      | 2(#0,#1)  | 0       | 2 ≥ 2 ✓         | 2 ≥ 2 ✓    | ✓       |
| 1 | 3      | 2(#0,#1)  | 1(#2)   | 3 ≥ 2 ✓         | 2 ≥ 2 ✓    | ✓       |
| 2 | 3      | 3(#0,#1,#2) | 0     | 3 ≥ 2 ✓         | 3 ≥ 2 ✓    | ✓       |
| 3 | 3      | 2(#1,#2)  | 1(#0)   | 3 ≥ 2 ✓         | 2 ≥ 2 ✓    | ✓       |
| 4 | 2      | 2(#1,#2)  | 0       | 2 ≥ 2 ✓         | 2 ≥ 2 ✓    | ✓       |

**FTT-BDL=1 maintained**: `qmr=2` never changes → 1 permanent loss is always survivable.

**D∅ failure is tolerated** at steps 1–3: if D∅(#2) crashes, utd=2(#0,#1), voters=3,
(2+0)=2 ≥ 2 ✓, 2 ≥ 2 ✓ → quorum maintained. This is strictly better than the target
layout's FTT-BUA=0 (where any D failure stops IO). The D∅ is a "free" addition that
cannot hurt availability.

#### 8.3b With sD

sD pre-syncs data invisibly, then converts directly to D (even D, no q change needed).

| # | Layout | q | qmr | FTT-BDL | pFTT-BDL | FTT-BUA | Action | Wait condition |
|---|--------|---|-----|---------|----------|---------|--------|----------------|
| 0 | **D**(#0) + **D**(#1) | 2 | 2 | 1 | 1 | 0 | Add sD(#2) | #2: `D_UP_TO_DATE` |
| 1 | **D**(#0) + **D**(#1) + **sD**(#2) | 2 | 2 | 1 | 1 | 0 | Convert #2: sD → D | All replicas: #2 is D |
| 2 | **D**(#0) + **D**(#1) + **D**(#2) | 2 | 2 | 1 | 2 | 0 | Detach disk on #0 | D replicas: #0 disk detached |
| 3 | **D∅**(#0) + **D**(#1) + **D**(#2) | 2 | 2 | 1 | 1 | 0 | Remove #0 | All replicas: #0 removed |
| 4 | **D**(#1) + **D**(#2) | 2 | 2 | 1 | 1 | 0 | — | — |

### 8.4 Replacing a replica in 3D (FTT-BDL=1, FTT-BUA=1)

**Goal**: Replace D replica #0 with new D replica #3. Maintain FTT-BDL≥1 and FTT-BUA≥1.

This procedure **requires q changes** because adding a 4th voter to q=2 produces 4 even
voters — a 2-2 partition would give both sides quorum (split-brain). So q must be raised
to 3 together with adding the voter.

#### 8.4a Without sD

New replica #3 enters as **A** first (connections established invisibly), then is converted
to D∅ together with raising q in a single config push. Same pattern in reverse for removal.

| # | Layout | q | qmr | FTT-BDL | pFTT-BDL | FTT-BUA | Action | Wait condition |
|---|--------|---|-----|---------|----------|---------|--------|----------------|
| 0 | **D**(#0) + **D**(#1) + **D**(#2) | 2 | 2 | 1 | 2 | 1 | Add A(#3) | D+A replicas: #3 connected |
| 1 | **D**(#0) + **D**(#1) + **D**(#2) + **A**(#3) | 2 | 2 | 1 | 2 | 1 | Convert #3: A → D∅ + raise q → 3 | All: #3 is D∅ + D: q=3 |
| 2 | **D**(#0) + **D**(#1) + **D**(#2) + **D∅**(#3) | 3 | 2 | 1 | 2 | 1 | Attach disk on #3 | #3: `D_UP_TO_DATE` |
| 3 | **D**(#0) + **D**(#1) + **D**(#2) + **D**(#3) | 3 | 2 | 1 | 3 | 1 | Detach disk on #0 | D replicas: #0 disk detached |
| 4 | **D∅**(#0) + **D**(#1) + **D**(#2) + **D**(#3) | 3 | 2 | 1 | 2 | 1 | Convert #0: D∅ → A + lower q → 2 | All: #0 is A + D: q=2 |
| 5 | **A**(#0) + **D**(#1) + **D**(#2) + **D**(#3) | 2 | 2 | 1 | 2 | 1 | Remove A(#0) | D+A replicas: #0 removed |
| 6 | **D**(#1) + **D**(#2) + **D**(#3) | 2 | 2 | 1 | 2 | 1 | — | — |

**Temporary operational impact**: Step 2 involves a **full resync** (hours). During this
time, 4 voters with q=3. FTT-BUA=1 is maintained throughout (see verification below).

#### 8.4b With sD

sD pre-syncs data invisibly, reducing the D∅ resync phase from full to delta.

| # | Layout | q | qmr | FTT-BDL | pFTT-BDL | FTT-BUA | Action | Wait condition |
|---|--------|---|-----|---------|----------|---------|--------|----------------|
| 0 | **D**(#0) + **D**(#1) + **D**(#2) | 2 | 2 | 1 | 2 | 1 | Add sD(#3) | #3: `D_UP_TO_DATE` |
| 1 | **D**(#0) + **D**(#1) + **D**(#2) + **sD**(#3) | 2 | 2 | 1 | 2 | 1 | Detach disk on #3 | D+sD replicas: #3 disk detached |
| 2 | **D**(#0) + **D**(#1) + **D**(#2) + **sD∅**(#3) | 2 | 2 | 1 | 2 | 1 | Convert #3: sD∅ → D∅ + raise q → 3 | All: #3 is D∅ + D: q=3 |
| 3 | **D**(#0) + **D**(#1) + **D**(#2) + **D∅**(#3) | 3 | 2 | 1 | 2 | 1 | Attach disk on #3 | #3: `D_UP_TO_DATE` (delta) |
| 4 | **D**(#0) + **D**(#1) + **D**(#2) + **D**(#3) | 3 | 2 | 1 | 3 | 1 | Detach disk on #0 | D replicas: #0 disk detached |
| 5 | **D∅**(#0) + **D**(#1) + **D**(#2) + **D**(#3) | 3 | 2 | 1 | 2 | 1 | Convert #0: D∅ → A + lower q → 2 | All: #0 is A + D: q=2 |
| 6 | **A**(#0) + **D**(#1) + **D**(#2) + **D**(#3) | 2 | 2 | 1 | 2 | 1 | Remove A(#0) | D+A replicas: #0 removed |
| 7 | **D**(#1) + **D**(#2) + **D**(#3) | 2 | 2 | 1 | 2 | 1 | — | — |

**Quorum verification** (from D replica perspective, both variants):

| voters | up_to_date | present | (utd+prs) ≥ q? | utd ≥ qmr? | Quorum? |
|--------|-----------|---------|-----------------|------------|---------|
| 3 (before add) | 3 | 0 | 3 ≥ 2 ✓ | 3 ≥ 2 ✓ | ✓ |
| 4 (with D∅) | 3 | 1 | 4 ≥ 3 ✓ | 3 ≥ 2 ✓ | ✓ |
| 4 (all D) | 4 | 0 | 4 ≥ 3 ✓ | 4 ≥ 2 ✓ | ✓ |
| 3 (after remove) | 3 | 0 | 3 ≥ 2 ✓ | 3 ≥ 2 ✓ | ✓ |

**FTT-BUA=1 verification** (worst case: 4 voters, q=3, with D∅):

| Failure | up_to_date | present | (utd+prs) ≥ 3? | utd ≥ 2? | Quorum? |
|---------|-----------|---------|-----------------|----------|---------|
| Any D fails | 2 | 1(D∅) | 3 ≥ 3 ✓ | 2 ≥ 2 ✓ | ✓ |
| D∅ fails | 3 | 0 | 3 ≥ 3 ✓ | 3 ≥ 2 ✓ | ✓ |

**FTT-BUA=1 throughout** in both variants.

### 8.5 Replacing a replica in 4D + 1TB (FTT-BDL=1, FTT-BUA=2)

**Goal**: Replace D replica #0 with new D replica #4. Maintain FTT-BDL≥1 and FTT-BUA≥2.

Like §8.2a/§8.3a, **no q/qmr changes are needed**. Adding a 5th voter to q=3 produces
5 odd voters, where q=3 is a safe majority. All partitions resolve unambiguously (3 vs 2).

#### 8.5a Without sD

| # | Layout | q | qmr | FTT-BDL | pFTT-BDL | FTT-BUA | Action | Wait condition |
|---|--------|---|-----|---------|----------|---------|--------|----------------|
| 0 | **D**(#0) + **D**(#1) + **D**(#2) + **D**(#3) + TB | 3 | 2 | 1 | 3 | 2 | Add D∅(#4) | All replicas: #4 configured and connected |
| 1 | 4**D** + **D∅**(#4) + TB | 3 | 2 | 1 | 3 | 2 | Attach disk on #4 | #4: `D_UP_TO_DATE` |
| 2 | 5**D** + TB | 3 | 2 | 1 | 4 | 2 | Detach disk on #0 | D replicas: #0 disk detached |
| 3 | **D∅**(#0) + 4**D** + TB | 3 | 2 | 1 | 3 | 2 | Remove #0 | All replicas: #0 removed |
| 4 | **D**(#1) + **D**(#2) + **D**(#3) + **D**(#4) + TB | 3 | 2 | 1 | 3 | 2 | — | — |

**Quorum verification at each step** (from D replica perspective):

| # | voters | up_to_date | present | (utd+prs) ≥ q? | utd ≥ qmr? | Quorum? |
|---|--------|-----------|---------|-----------------|------------|---------|
| 0 | 4      | 4         | 0       | 4 ≥ 3 ✓         | 4 ≥ 2 ✓    | ✓       |
| 1 | 5      | 4         | 1(#4)   | 5 ≥ 3 ✓         | 4 ≥ 2 ✓    | ✓       |
| 2 | 5      | 5         | 0       | 5 ≥ 3 ✓         | 5 ≥ 2 ✓    | ✓       |
| 3 | 5      | 4         | 1(#0)   | 5 ≥ 3 ✓         | 4 ≥ 2 ✓    | ✓       |
| 4 | 4      | 4         | 0       | 4 ≥ 3 ✓         | 4 ≥ 2 ✓    | ✓       |

**FTT-BUA=2 verification at step 1** (worst case, 5 voters + D∅ + TB):

| 2 failures | up_to_date | present | (utd+prs) ≥ 3? | utd ≥ 2? | Quorum? |
|------------|-----------|---------|-----------------|----------|---------|
| 2D fail    | 2         | 1(D∅)   | 3 ≥ 3 ✓         | 2 ≥ 2 ✓  | ✓       |
| 1D + 1TB   | 3         | 1(D∅)   | 4 ≥ 3 ✓         | 3 ≥ 2 ✓  | ✓       |
| 1D + D∅    | 3         | 0       | 3 ≥ 3 ✓         | 3 ≥ 2 ✓  | ✓       |
| D∅ + TB    | 4         | 0       | 4 ≥ 3 ✓         | 4 ≥ 2 ✓  | ✓       |

#### 8.5b With sD

sD pre-syncs data invisibly, then converts directly to D (even D, no q change needed).

| # | Layout | q | qmr | FTT-BDL | pFTT-BDL | FTT-BUA | Action | Wait condition |
|---|--------|---|-----|---------|----------|---------|--------|----------------|
| 0 | 4**D** + TB | 3 | 2 | 1 | 3 | 2 | Add sD(#4) | #4: `D_UP_TO_DATE` |
| 1 | 4**D** + **sD**(#4) + TB | 3 | 2 | 1 | 3 | 2 | Convert #4: sD → D | All replicas: #4 is D |
| 2 | 5**D** + TB | 3 | 2 | 1 | 4 | 2 | Detach disk on #0 | D replicas: #0 disk detached |
| 3 | **D∅**(#0) + 4**D** + TB | 3 | 2 | 1 | 3 | 2 | Remove #0 | All replicas: #0 removed |
| 4 | 4**D** + TB | 3 | 2 | 1 | 3 | 2 | — | — |

### 8.6 Replacing a replica in 4D (FTT-BDL=2, FTT-BUA=1)

**Goal**: Replace D replica #0 with new D replica #4. Maintain FTT-BDL≥2 and FTT-BUA≥1.

Like §8.5, **no q/qmr changes are needed**. Adding a 5th voter to q=3 produces 5 odd
voters, where q=3 is a safe majority.

#### 8.6a Without sD

| # | Layout | q | qmr | FTT-BDL | pFTT-BDL | FTT-BUA | Action | Wait condition |
|---|--------|---|-----|---------|----------|---------|--------|----------------|
| 0 | 4**D** | 3 | 3 | 2 | 3 | 1 | Add D∅(#4) | All replicas: #4 configured and connected |
| 1 | 4**D** + **D∅**(#4) | 3 | 3 | 2 | 3 | 1 | Attach disk on #4 | #4: `D_UP_TO_DATE` |
| 2 | 5**D** | 3 | 3 | 2 | 4 | 1 | Detach disk on #0 | D replicas: #0 disk detached |
| 3 | **D∅**(#0) + 4**D** | 3 | 3 | 2 | 3 | 1 | Remove #0 | All replicas: #0 removed |
| 4 | 4**D** | 3 | 3 | 2 | 3 | 1 | — | — |

**Quorum verification at each step** (from D replica perspective):

| # | voters | up_to_date | present | (utd+prs) ≥ q? | utd ≥ qmr? | Quorum? |
|---|--------|-----------|---------|-----------------|------------|---------|
| 0 | 4      | 4         | 0       | 4 ≥ 3 ✓         | 4 ≥ 3 ✓    | ✓       |
| 1 | 5      | 4         | 1(#4)   | 5 ≥ 3 ✓         | 4 ≥ 3 ✓    | ✓       |
| 2 | 5      | 5         | 0       | 5 ≥ 3 ✓         | 5 ≥ 3 ✓    | ✓       |
| 3 | 5      | 4         | 1(#0)   | 5 ≥ 3 ✓         | 4 ≥ 3 ✓    | ✓       |
| 4 | 4      | 4         | 0       | 4 ≥ 3 ✓         | 4 ≥ 3 ✓    | ✓       |

**FTT-BUA=1 verification at step 1** (5 voters, q=3, qmr=3):

| Failure | up_to_date | present | (utd+prs) ≥ 3? | utd ≥ 3? | Quorum? |
|---------|-----------|---------|-----------------|----------|---------|
| 1D fails | 3 | 1(D∅) | 4 ≥ 3 ✓ | 3 ≥ 3 ✓ | ✓ |
| D∅ fails | 4 | 0 | 4 ≥ 3 ✓ | 4 ≥ 3 ✓ | ✓ |

#### 8.6b With sD

sD pre-syncs data invisibly, then converts directly to D (even D, no q change needed).

| # | Layout | q | qmr | FTT-BDL | pFTT-BDL | FTT-BUA | Action | Wait condition |
|---|--------|---|-----|---------|----------|---------|--------|----------------|
| 0 | 4**D** | 3 | 3 | 2 | 3 | 1 | Add sD(#4) | #4: `D_UP_TO_DATE` |
| 1 | 4**D** + **sD**(#4) | 3 | 3 | 2 | 3 | 1 | Convert #4: sD → D | All replicas: #4 is D |
| 2 | 5**D** | 3 | 3 | 2 | 4 | 1 | Detach disk on #0 | D replicas: #0 disk detached |
| 3 | **D∅**(#0) + 4**D** | 3 | 3 | 2 | 3 | 1 | Remove #0 | All replicas: #0 removed |
| 4 | 4**D** | 3 | 3 | 2 | 3 | 1 | — | — |

### 8.7 Replacing a replica in 5D (FTT-BDL=2, FTT-BUA=2)

**Goal**: Replace D replica #0 with new D replica #5. Maintain FTT-BDL≥2 and FTT-BUA≥2.

This procedure **requires q changes** because adding a 6th voter to q=3 produces 6 even
voters — a 3-3 partition would give both sides quorum (split-brain). So q must be raised
to 4 together with adding the voter. Same pattern as §8.1 and §8.4.

#### 8.7a Without sD

New replica #5 enters as **A** first (connections established invisibly), then is converted
to D∅ together with raising q in a single config push. Same pattern in reverse for removal.

| # | Layout | q | qmr | FTT-BDL | pFTT-BDL | FTT-BUA | Action | Wait condition |
|---|--------|---|-----|---------|----------|---------|--------|----------------|
| 0 | 5**D** | 3 | 3 | 2 | 4 | 2 | Add A(#5) | D+A replicas: #5 connected |
| 1 | 5**D** + **A**(#5) | 3 | 3 | 2 | 4 | 2 | Convert #5: A → D∅ + raise q → 4 | All: #5 is D∅ + D: q=4 |
| 2 | 5**D** + **D∅**(#5) | 4 | 3 | 2 | 4 | 2 | Attach disk on #5 | #5: `D_UP_TO_DATE` |
| 3 | 6**D** | 4 | 3 | 2 | 5 | 2 | Detach disk on #0 | D replicas: #0 disk detached |
| 4 | **D∅**(#0) + 5**D** | 4 | 3 | 2 | 4 | 2 | Convert #0: D∅ → A + lower q → 3 | All: #0 is A + D: q=3 |
| 5 | **A**(#0) + 5**D** | 3 | 3 | 2 | 4 | 2 | Remove A(#0) | D+A replicas: #0 removed |
| 6 | 5**D** | 3 | 3 | 2 | 4 | 2 | — | — |

#### 8.7b With sD

sD pre-syncs data invisibly, reducing the D∅ resync phase from full to delta.

| # | Layout | q | qmr | FTT-BDL | pFTT-BDL | FTT-BUA | Action | Wait condition |
|---|--------|---|-----|---------|----------|---------|--------|----------------|
| 0 | 5**D** | 3 | 3 | 2 | 4 | 2 | Add sD(#5) | #5: `D_UP_TO_DATE` |
| 1 | 5**D** + **sD**(#5) | 3 | 3 | 2 | 4 | 2 | Detach disk on #5 | D+sD replicas: #5 disk detached |
| 2 | 5**D** + **sD∅**(#5) | 3 | 3 | 2 | 4 | 2 | Convert #5: sD∅ → D∅ + raise q → 4 | All: #5 is D∅ + D: q=4 |
| 3 | 5**D** + **D∅**(#5) | 4 | 3 | 2 | 4 | 2 | Attach disk on #5 | #5: `D_UP_TO_DATE` (delta) |
| 4 | 6**D** | 4 | 3 | 2 | 5 | 2 | Detach disk on #0 | D replicas: #0 disk detached |
| 5 | **D∅**(#0) + 5**D** | 4 | 3 | 2 | 4 | 2 | Convert #0: D∅ → A + lower q → 3 | All: #0 is A + D: q=3 |
| 6 | **A**(#0) + 5**D** | 3 | 3 | 2 | 4 | 2 | Remove A(#0) | D+A replicas: #0 removed |
| 7 | 5**D** | 3 | 3 | 2 | 4 | 2 | — | — |

**Quorum verification** (from D replica perspective, both variants):

| voters | up_to_date | present | (utd+prs) ≥ q? | utd ≥ qmr? | Quorum? |
|--------|-----------|---------|-----------------|------------|---------|
| 5 (before add) | 5 | 0 | 5 ≥ 3 ✓ | 5 ≥ 3 ✓ | ✓ |
| 6 (with D∅) | 5 | 1 | 6 ≥ 4 ✓ | 5 ≥ 3 ✓ | ✓ |
| 6 (all D) | 6 | 0 | 6 ≥ 4 ✓ | 6 ≥ 3 ✓ | ✓ |
| 5 (after remove) | 5 | 0 | 5 ≥ 3 ✓ | 5 ≥ 3 ✓ | ✓ |

**FTT-BUA=2 verification** (worst case: 6 voters, q=4, with D∅):

| 2 failures | up_to_date | present | (utd+prs) ≥ 4? | utd ≥ 3? | Quorum? |
|------------|-----------|---------|-----------------|----------|---------|
| 2D fail    | 3         | 1(D∅)   | 4 ≥ 4 ✓         | 3 ≥ 3 ✓  | ✓       |
| 1D + D∅    | 4         | 0       | 4 ≥ 4 ✓         | 4 ≥ 3 ✓  | ✓       |

**FTT-BUA=2 throughout** in both variants.

---

## 9. TieBreaker Replica Replacement Procedures

Like §8, all TB replacement procedures are designed to:

1. **Never reduce FTT-BDL/FTT-BUA guarantees** below the target level
2. **Never interrupt IO** (maintain quorum throughout)
3. **Never create split-brain risk** (maintain safe quorum settings)

### 9.1 Replacing TB in 2D + 1TB (FTT-BDL=0, FTT-BUA=1)

**Goal**: Replace TB replica #0 with new TB replica #1. Maintain FTT-BDL≥0 and FTT-BUA≥1.

TB replicas have no data and no disk — no resync is needed. The replacement is a simple
add-then-remove.

| # | Layout | q | qmr | FTT-BDL | pFTT-BDL | FTT-BUA | Action | Wait condition |
|---|--------|---|-----|---------|----------|---------|--------|----------------|
| 0 | 2**D** + **TB**(#0) | 2 | 1 | 0 | 1 | 1 | Add TB(#1) | D+TB replicas: #1 connected |
| 1 | 2**D** + **TB**(#0) + **TB**(#1) | 2 | 1 | 0 | 1 | 1 | Remove TB(#0) | D+TB replicas: #0 removed |
| 2 | 2**D** + **TB**(#1) | 2 | 1 | 0 | 1 | 1 | — | — |

**FTT-BUA=1 verification at step 1** (2D + 2TB):

| Failure | up_to_date | diskless | missing_diskless | TB majority_at | Quorum? |
|---------|-----------|----------|-----------------|---------------|---------|
| 1D fails | 1 | 2 | 0 | 2: 2≥2 ✓ → tiebreaker | ✓ |
| TB(#0) fails | 2 | 1 | 1 | 2: 1<2 — but q=2: 2≥2 ✓ (main) | ✓ |
| TB(#1) fails | 2 | 1 | 1 | 2: 1<2 — but q=2: 2≥2 ✓ (main) | ✓ |

**Note on 2TB tiebreaker threshold**: With 2 TBs, `diskless_majority_at` = 2 (need both
TBs for tiebreaker). The FTT-BUA=1 **guarantee is not affected** — a single failure
leaves all TBs connected, so the threshold is always met. However, the **operational risk**
during degraded state is higher: after 1D failure, the system depends on both TBs staying
connected (vs 1 TB in the target layout). Minimize the duration of the 2TB window.

### 9.2 Replacing TB in 4D + 1TB (FTT-BDL=1, FTT-BUA=2)

**Goal**: Replace TB replica #0 with new TB replica #1. Maintain FTT-BDL≥1 and FTT-BUA≥2.

Same pattern as §9.1 — simple add-then-remove, no data, no resync.

| # | Layout | q | qmr | FTT-BDL | pFTT-BDL | FTT-BUA | Action | Wait condition |
|---|--------|---|-----|---------|----------|---------|--------|----------------|
| 0 | 4**D** + **TB**(#0) | 3 | 2 | 1 | 3 | 2 | Add TB(#1) | D+TB replicas: #1 connected |
| 1 | 4**D** + **TB**(#0) + **TB**(#1) | 3 | 2 | 1 | 3 | 2 | Remove TB(#0) | D+TB replicas: #0 removed |
| 2 | 4**D** + **TB**(#1) | 3 | 2 | 1 | 3 | 2 | — | — |

**FTT-BUA=2 verification at step 1** (4D + 2TB):

| 2 failures | up_to_date | diskless | missing_diskless | TB majority_at | Quorum? |
|------------|-----------|----------|-----------------|---------------|---------|
| 2D fail | 2 | 2 | 0 | 2: 2≥2 ✓ → tiebreaker | ✓ |
| 1D + 1TB | 3 | 1 | 1 | — main: 3≥3 ✓ | ✓ |
| 2TB fail | 4 | 0 | 2 | — main: 4≥3 ✓ | ✓ |

Same tiebreaker threshold note as §9.1: with 2 TBs, `diskless_majority_at` = 2. The
FTT-BUA=2 guarantee is not affected. Operational risk is slightly higher during the 2TB
window — after 2D failures, tiebreaker depends on both TBs staying connected.

---

## 10. Layout Transitions

Any change of FTT-BDL or FTT-BUA is performed as a sequence of **single-parameter steps**,
where each step changes exactly one parameter by ±1. Like §8, all transitions must:

1. **Never reduce FTT-BDL/FTT-BUA** below the minimum of source and target values
2. **Never interrupt IO** (maintain quorum throughout)
3. **Never create split-brain risk** (maintain safe quorum settings)

### Transition graph

```
(0,0) 1D ——FTT-BDL—— 2D (1,0)
  |                      |
FTT-BUA              FTT-BUA
  |                      |
(0,1) 2D+TB —FTT-BDL— 3D (1,1) ——FTT-BDL—— 4D (2,1)
                         |                      |
                      FTT-BUA               FTT-BUA
                         |                      |
                   (1,2) 4D+TB —FTT-BDL— 5D (2,2)
```

Each edge is a single-step transition. Multi-step transitions (e.g. 1D → 5D) follow a
path along the edges.

### Multi-step ordering rule

When both FTT-BDL and FTT-BUA change, apply the following priority at each step:

- **Upgrade**: prefer increasing FTT-BDL first. If the edge doesn't exist
  (|FTT-BDL−FTT-BUA| > 1 constraint), increase FTT-BUA instead.
- **Downgrade**: prefer decreasing FTT-BUA first. If the edge doesn't exist, decrease
  FTT-BDL instead.

This rule produces paths through **pure-D layouts** (no TB management), which are simpler:

```
Upgrade (0,0)→(2,2):   1D →FTT-BDL→ 2D →FTT-BUA→ 3D →FTT-BDL→ 4D →FTT-BUA→ 5D
Downgrade (2,2)→(0,0): 5D →FTT-BUA→ 4D →FTT-BDL→ 3D →FTT-BUA→ 2D →FTT-BDL→ 1D
```

The alternative (FTT-BUA-first on upgrade) would go through 2D+TB and 4D+TB — adding and
removing TB twice. FTT-BDL-first avoids this entirely.

**Why FTT-BDL first on upgrade**: data safety before availability. If the process is
interrupted mid-way, the system has better data protection even if availability isn't
fully upgraded yet. **Why FTT-BUA first on downgrade**: reduce availability (the "cheaper"
guarantee) first, preserving data safety as long as possible.

### Transition summary

| # | Edge | Parameter | D | TB | q | qmr | Pattern | Covered by |
|---|------|-----------|---|----|---|-----|---------|------------|
| 1 | 1D ↔ 2D | FTT-BDL ±1 | 1↔2 | 0 | 1↔2 | 1↔2 | vestibule + qmr | §10.1 |
| 2 | 1D ↔ 2D+TB | FTT-BUA ±1 | 1↔2 | 0↔1 | 1↔2 | 1 | D + TB ordering | §10.2 |
| 3 | 2D ↔ 3D | FTT-BUA ±1 | 2↔3 | 0 | 2 | 2 | D only | §8 add/remove (§8.3a/§8.4a) |
| 4 | 2D+TB ↔ 3D | FTT-BDL ±1 | 2↔3 | 1↔0 | 2 | 1↔2 | D + TB + qmr | §10.3 |
| 5 | 3D ↔ 4D | FTT-BDL ±1 | 3↔4 | 0 | 2↔3 | 2↔3 | vestibule + qmr | Same pattern as §10.1 (§8.4a + qmr) |
| 6 | 3D ↔ 4D+TB | FTT-BUA ±1 | 3↔4 | 0↔1 | 2↔3 | 2 | D + TB ordering | Same pattern as §10.2 (§8.4a + §9) |
| 7 | 4D ↔ 5D | FTT-BUA ±1 | 4↔5 | 0 | 3 | 3 | D only | §8 add/remove (§8.6a/§8.7a) |
| 8 | 4D+TB ↔ 5D | FTT-BDL ±1 | 4↔5 | 1↔0 | 3 | 2↔3 | D + TB + qmr | Same pattern as §10.3 (§8.5a + qmr + §9) |

**Transitions #3 and #7** (D only): covered by §8 add/remove halves.

**Transitions #5, #6, #8**: structurally identical to §10.1, §10.2, §10.3 respectively,
at higher D/q/qmr values. Upgrade/downgrade ordering is the same — substitute the
referenced §8 section for the D add/remove steps.

The 3 patterns with full procedures:

### 10.1 1D ↔ 2D (FTT-BDL: 0↔1)

Changes: D 1↔2, q 1↔2, qmr 1↔2. The q change is covered by §8.1 vestibule pattern.
The new element is the **qmr change**.

**Ordering principle**: raise qmr **after** new D is UpToDate (otherwise quorum check
fails). Lower qmr **before** removing D (otherwise system loses quorum on removal).

#### Upgrade: 1D → 2D

| # | Layout | q | qmr | FTT-BDL | pFTT-BDL | FTT-BUA | Action | Wait condition |
|---|--------|---|-----|---------|----------|---------|--------|----------------|
| 0 | **D**(#0) | 1 | 1 | 0 | 0 | 0 | Add A(#1) | D+A: #1 connected |
| 1 | **D**(#0) + **A**(#1) | 1 | 1 | 0 | 0 | 0 | Convert #1: A → D∅ + raise q → 2 | All: #1 is D∅ + D: q=2 |
| 2 | **D**(#0) + **D∅**(#1) | 2 | 1 | 0 | 0 | 0 | Attach disk on #1 | #1: `D_UP_TO_DATE` |
| 3 | **D**(#0) + **D**(#1) | 2 | 1 | 0 | 1 | 0 | Raise qmr → 2 | All replicas: accepted qmr=2 |
| 4 | **D**(#0) + **D**(#1) | 2 | 2 | 1 | 1 | 0 | — | — |

Steps 0–2 are the "add" half of §8.1a. Step 3 raises qmr after both D are UpToDate —
the quorum check `utd ≥ qmr` holds (2 ≥ 2 ✓). FTT-BDL rises from 0 to 1 at this step.

#### Downgrade: 2D → 1D

| # | Layout | q | qmr | FTT-BDL | pFTT-BDL | FTT-BUA | Action | Wait condition |
|---|--------|---|-----|---------|----------|---------|--------|----------------|
| 0 | **D**(#0) + **D**(#1) | 2 | 2 | 1 | 1 | 0 | Lower qmr → 1 | All replicas: accepted qmr=1 |
| 1 | **D**(#0) + **D**(#1) | 2 | 1 | 0 | 1 | 0 | Detach disk on #0 | D replicas: #0 disk detached |
| 2 | **D∅**(#0) + **D**(#1) | 2 | 1 | 0 | 0 | 0 | Convert #0: D∅ → A + lower q → 1 | All: #0 is A + D: q=1 |
| 3 | **A**(#0) + **D**(#1) | 1 | 1 | 0 | 0 | 0 | Remove A(#0) | D+A: #0 removed |
| 4 | **D**(#1) | 1 | 1 | 0 | 0 | 0 | — | — |

Step 0 lowers qmr first — FTT-BDL drops from 1 to 0 (= target level). This is safe:
lowering qmr relaxes the quorum check (2 ≥ 1 ✓). Steps 1–3 are the "remove" half of §8.1a.

**Guarantee verification**: min(source, target) = BDL≥0, BUA≥0. Both hold at every step.
Split-brain safe: q=2 with 2 voters (partition 1-1: both <2), q=1 with 1 voter.

### 10.2 1D ↔ 2D+TB (FTT-BUA: 0↔1)

Changes: D 1↔2, TB 0↔1, q 1↔2. No qmr change (stays 1). Combines D add/remove (§8.1
pattern) with TB add/remove (§9 pattern). The key question is **ordering**.

**Ordering principle**: add D before TB (D provides data capacity; TB only helps with
tiebreaker, which is useless without enough D). Remove TB before D (reverse).

#### Upgrade: 1D → 2D+TB

| # | Layout | q | qmr | FTT-BDL | pFTT-BDL | FTT-BUA | Action | Wait condition |
|---|--------|---|-----|---------|----------|---------|--------|----------------|
| 0 | **D**(#0) | 1 | 1 | 0 | 0 | 0 | Add A(#1) | D+A: #1 connected |
| 1 | **D**(#0) + **A**(#1) | 1 | 1 | 0 | 0 | 0 | Convert #1: A → D∅ + raise q → 2 | All: #1 is D∅ + D: q=2 |
| 2 | **D**(#0) + **D∅**(#1) | 2 | 1 | 0 | 0 | 0 | Attach disk on #1 | #1: `D_UP_TO_DATE` |
| 3 | **D**(#0) + **D**(#1) | 2 | 1 | 0 | 1 | 0 | Add TB | D+TB: TB connected |
| 4 | **D**(#0) + **D**(#1) + TB | 2 | 1 | 0 | 1 | 1 | — | — |

Steps 0–2 are the "add" half of §8.1a. Step 3 adds TB — FTT-BUA rises from 0 to 1
(tiebreaker enables quorum after 1D failure).

#### Downgrade: 2D+TB → 1D

| # | Layout | q | qmr | FTT-BDL | pFTT-BDL | FTT-BUA | Action | Wait condition |
|---|--------|---|-----|---------|----------|---------|--------|----------------|
| 0 | **D**(#0) + **D**(#1) + TB | 2 | 1 | 0 | 1 | 1 | Remove TB | D+TB: TB removed |
| 1 | **D**(#0) + **D**(#1) | 2 | 1 | 0 | 1 | 0 | Detach disk on #0 | D replicas: #0 disk detached |
| 2 | **D∅**(#0) + **D**(#1) | 2 | 1 | 0 | 0 | 0 | Convert #0: D∅ → A + lower q → 1 | All: #0 is A + D: q=1 |
| 3 | **A**(#0) + **D**(#1) | 1 | 1 | 0 | 0 | 0 | Remove A(#0) | D+A: #0 removed |
| 4 | **D**(#1) | 1 | 1 | 0 | 0 | 0 | — | — |

Step 0 removes TB first — FTT-BUA drops from 1 to 0 (= target level). Steps 1–3 are the
"remove" half of §8.1a.

**Guarantee verification**: min(source, target) = BDL≥0, BUA≥0. Both hold at every step.
Split-brain safe: q=2 with 2 voters (partition 1-1: both <2), q=1 with 1 voter.

### 10.3 2D+TB ↔ 3D (FTT-BDL: 0↔1)

Changes: D 2↔3, TB 1↔0, qmr 1↔2. No q change (stays 2). Combines D add/remove (§8.2
pattern) with TB remove/add (§9) and qmr change.

**Ordering principle**: upgrade = add D, raise qmr, remove TB. Downgrade = add TB, lower
qmr, remove D.

#### Upgrade: 2D+TB → 3D

| # | Layout | q | qmr | FTT-BDL | pFTT-BDL | FTT-BUA | Action | Wait condition |
|---|--------|---|-----|---------|----------|---------|--------|----------------|
| 0 | 2**D** + TB | 2 | 1 | 0 | 1 | 1 | Add D∅(#2) | All replicas: #2 configured and connected |
| 1 | 2**D** + **D∅**(#2) + TB | 2 | 1 | 0 | 1 | 1 | Attach disk on #2 | #2: `D_UP_TO_DATE` |
| 2 | 3**D** + TB | 2 | 1 | 0 | 2 | 1 | Raise qmr → 2 | All replicas: accepted qmr=2 |
| 3 | 3**D** + TB | 2 | 2 | 1 | 2 | 1 | Remove TB | D+TB: TB removed |
| 4 | 3**D** | 2 | 2 | 1 | 2 | 1 | — | — |

Steps 0–1 are the "add" half of §8.2a (even D → odd voters, no q change). Step 2 raises
qmr after third D is UpToDate (3 ≥ 2 ✓). Step 3 removes TB — safe because 3 odd voters
don't need tiebreaker.

#### Downgrade: 3D → 2D+TB

| # | Layout | q | qmr | FTT-BDL | pFTT-BDL | FTT-BUA | Action | Wait condition |
|---|--------|---|-----|---------|----------|---------|--------|----------------|
| 0 | 3**D** | 2 | 2 | 1 | 2 | 1 | Add TB | D+TB: TB connected |
| 1 | 3**D** + TB | 2 | 2 | 1 | 2 | 1 | Lower qmr → 1 | All replicas: accepted qmr=1 |
| 2 | 3**D** + TB | 2 | 1 | 0 | 2 | 1 | Detach disk on #0 | D replicas: #0 disk detached |
| 3 | **D∅**(#0) + 2**D** + TB | 2 | 1 | 0 | 1 | 1 | Remove #0 | All replicas: #0 removed |
| 4 | 2**D** + TB | 2 | 1 | 0 | 1 | 1 | — | — |

Step 0 adds TB first — needed before removing D (tiebreaker for 2 even voters). Step 1
lowers qmr (relaxation: 3 ≥ 1 ✓). Steps 2–3 are the "remove" half of §8.2a.

**Guarantee verification**: min(source, target) = BDL≥0, BUA≥1. At every step: BDL≥0 ✓,
BUA≥1 ✓ (3 odd voters with q=2 always resolve partitions; after TB added, even 2 voters +
TB maintain BUA=1). Split-brain safe: q=2 is majority for both 3 and 2 voters.

---

## 11. Topology

The SDS supports three placement topologies:

- **Ignored** — zones are not considered. Failure domain is a single node. Replicas are
  placed on any available nodes without zone awareness. All procedures from §8–§10 apply
  directly.
- **Zonal** — all replicas are placed within a single zone. Failure domain is a single
  node. Zone placement is a constraint on node selection but does not affect quorum or
  replacement logic. Procedures from §8–§10 apply directly.
- **TransZonal** — replicas are distributed across zones. The failure domain depends on
  the FTT level:

  for some FTT-BDL/FTT-BUA combinations it is an entire zone (the layout survives loss
  of a whole zone), for others it may be composite (individual node failures across zones).

The rest of this section covers TransZonal topology — distribution of D and TB replicas
across zones, zone-aware replacement and transition procedures.

### 11.1 TransZonal Zone Requirements

In TransZonal, replicas are distributed across zones. The placement strategy depends on
how many zones are available, which determines the **failure domain** — what the FTT-BUA
guarantee covers.

Two placement strategies:

| FTT-BDL | FTT-BUA | Zone FTT-BUA | Layout | Zones required | Failure domain | Available after loss of |
|---------|---------|-------------|--------|---------------|---------------|------------------------|
| 0 | 0 | 0 | 1D (q=1, qmr=1) | 1 | — | — |
| 0 | 1 | 1 | 2D+1TB (q=2, qmr=1) | 3 | zone | 1 zone |
| 1 | 0 | 0 | 2D (q=2, qmr=2) | 2 | — | — |
| 1 | 1 | 1 | 3D (q=2, qmr=2) | 3 | zone | 1 zone |
| 1 | 2 | 1 | 4D+1TB (q=3, qmr=2) | 3 | composite | 1 zone or 2 nodes |
| 1 | 2 | 2 | 4D+1TB (q=3, qmr=2) | 5 | zone | 2 zones |
| 2 | 1 | 1 | 4D (q=3, qmr=3) | 4 | zone | 1 zone |
| 2 | 2 | 1 | 5D (q=3, qmr=3) | 3 | composite | 1 zone or 2 nodes |
| 2 | 2 | 2 | 5D (q=3, qmr=3) | 5 | zone | 2 zones |

**Pure zone failure domain** (1 replica per zone): Each D and each TB occupies its own zone.
Losing K zones loses exactly K replicas. FTT-BUA=N means "survive loss of N zones." This
requires **zones = D + TB** (= Total replicas). Applies when enough zones are available.

**Composite failure domain** (multiple replicas per zone): When fewer zones are available
(e.g. 3), replicas share zones under these constraints:

- **Max D per zone ≤ D − qmr**: ensures that losing any 1 zone still leaves ≥ qmr D
  replicas for quorum. Example: 5D with qmr=3 → max 2D per zone → distribution 2+2+1
  across 3 zones.
- **TB zone must have ≤ 1D**: if TB shares a zone with 2D, losing that zone removes 2D +
  TB — the tiebreaker is gone precisely when it's most needed. With ≤ 1D, losing the TB
  zone removes 1D + TB, leaving D−1 ≥ q for main quorum (tiebreaker not needed).

In composite mode, FTT-BUA has a **dual meaning**: "available after loss of 1 zone OR
FTT-BUA nodes."

### 11.2 Zone-Aware Replica Replacement

The §8 and §9 procedures are zone-agnostic — they describe DRBD-level steps (add D∅,
attach, detach, remove). In TransZonal topology, one additional constraint applies:

**Zone placement rule**: The new replica must be placed on a node **in the same zone** as
the replica being replaced. This preserves the zone distribution (e.g. 1+1+1 or 2+2+1)
that underpins the zone-level FTT-BUA guarantee. Moving a replica to a different zone
would change the distribution, potentially violating the guarantee.

This applies to:
- D replacement (§8) — new D∅ (or sD, or A vestibule) goes into the same zone
- TB replacement (§9) — new TB goes into the same zone

**Zone loss recovery**: If the entire zone is unavailable (all nodes in the zone are down),
replacement **waits for the zone to return**. We do not create a replacement replica in a
different zone, because that would alter the distribution. Once the zone is back, normal
§8/§9 procedures apply.

**No new procedures are needed**: §8.1–§8.7 and §9.1–§9.2 apply as-is with the zone
placement constraint. The DRBD quorum mechanics, q/qmr settings, and vestibule patterns
are unaffected by zone placement.

**Composite failure domain note**: For layouts in 3 zones with composite failure domain
(e.g. 5D 2+2+1), the zone distribution must be preserved exactly:
- Replacing a D in a 2-D zone → new D goes into that same 2-D zone (different node)
- Replacing a D in the 1-D zone → new D goes into that 1-D zone
- The distribution shape (2+2+1) must not change

### 11.3 Zone Redistribution (Swapping D and TB Between Zones)

Zone redistribution changes **which zones hold which replica types**, while preserving the
overall layout (same D count, TB count, q, qmr). This is different from §11.2 (same-zone
replacement) — here replicas intentionally move between zones.

**When needed**: rebalancing after infrastructure changes, decommissioning a zone's role,
moving TB to a zone with lower failure probability, etc.

**D-only layouts** (3D, 4D, 5D): All D replicas are identical. Swapping two D replicas
between zones produces an equivalent distribution. No special procedure needed — use §8
replacement with the target zone instead of the source zone.

**D+TB layouts** (2D+1TB, 4D+1TB): D and TB are different types, so swapping changes the
zone-level resilience profile. The procedure must maintain zone-FTT-BUA throughout.

**Ordering principle**: add capacity first, remove last. **Remove old TB before old D**
from a co-located zone (to avoid a state where the only TB is co-located with D in a zone
whose loss would remove both).

#### 11.3.1 Swapping D ↔ TB in 2D+1TB (q=2, qmr=1)

**Goal**: Move D from zone A to zone C, move TB from zone C to zone A. Maintain
zone-FTT-BUA≥1 throughout.

Start: D(zone A) + D(zone B) + TB(zone C).
End: TB(zone A) + D(zone B) + D(zone C).

| # | Layout | Zone A | Zone B | Zone C | q | qmr | FTT-BDL | FTT-BUA | Action | Wait condition |
|---|--------|--------|--------|--------|---|-----|---------|---------|--------|----------------|
| 0 | 2**D**+TB | D(#0) | D(#1) | TB | 2 | 1 | 0 | 1 | Add D∅(#2) in zone C | All replicas: #2 configured and connected |
| 1 | 2**D**+**D∅**+TB | D(#0) | D(#1) | D∅(#2)+TB | 2 | 1 | 0 | 1 | Attach disk on #2 | #2: `D_UP_TO_DATE` |
| 2 | 3**D**+TB | D(#0) | D(#1) | D(#2)+TB | 2 | 1 | 0 | 1 | Add TB(#3) in zone A | D+TB: #3 connected |
| 3 | 3**D**+2TB | D(#0)+TB(#3) | D(#1) | D(#2)+TB | 2 | 1 | 0 | 1 | Remove TB from zone C | D+TB: old TB removed |
| 4 | 3**D**+TB | D(#0)+TB(#3) | D(#1) | D(#2) | 2 | 1 | 0 | 1 | Remove D(#0) from zone A | All replicas: #0 removed |
| 5 | 2**D**+TB | TB(#3) | D(#1) | D(#2) | 2 | 1 | 0 | 1 | — | — |

**Zone-FTT-BUA=1 verification at each step**:

| # | Zone A lost | Zone B lost | Zone C lost | All ✓? |
|---|------------|------------|------------|--------|
| 0 | D(B)+TB(C): tiebreaker ✓ | D(A)+TB(C): tiebreaker ✓ | D(A)+D(B): main 2≥2 ✓ | ✓ |
| 1 | D(B)+D∅(C)+TB(C): 3 voters, (1+1)≥2 ✓ | D(A)+D∅(C)+TB(C): same ✓ | D(A)+D(B): 2≥2 ✓ | ✓ |
| 2 | D(B)+D(C)+TB(C): 2≥2 ✓ | D(A)+D(C)+TB(A)+TB(C): 2≥2 ✓ | D(A)+D(B)+TB(A): 2≥2 ✓ | ✓ |
| 3 | D(B)+D(C): 3 voters, 2≥2 ✓ | D(A)+D(C)+TB(A): 2≥2 ✓ | D(A)+D(B)+TB(A): 2≥2 ✓ | ✓ |
| 4 | D(B)+D(C): 2≥2 ✓ | D(C)+TB(A): tiebreaker ✓ | D(B)+TB(A): tiebreaker ✓ | ✓ |
| 5 | D(B)+D(C): 2≥2 ✓ | D(C)+TB(A): tiebreaker ✓ | D(B)+TB(A): tiebreaker ✓ | ✓ |

**Split-brain safety**: q=2 throughout. Voters range from 2 to 3 (odd = safe majority,
even = tiebreaker resolves).

#### 11.3.2 Swapping D ↔ TB in 4D+1TB (q=3, qmr=2)

**Goal**: Move D from zone A to zone C, move TB from zone C to zone A. Maintain
zone-FTT-BUA≥1 throughout.

Start: D+D(zone A) + D(zone B) + D+TB(zone C). (Composite 3-zone layout.)
End: D+TB(zone A) + D(zone B) + D+D(zone C).

Same principle: add D in target zone → add TB in target zone → remove old TB → remove old D.

| # | Layout | Zone A | Zone B | Zone C | q | qmr | FTT-BDL | FTT-BUA | Action | Wait condition |
|---|--------|--------|--------|--------|---|-----|---------|---------|--------|----------------|
| 0 | 4**D**+TB | D+D | D | D+TB | 3 | 2 | 1 | 2 | Add D∅(#4) in zone C | All replicas: #4 configured and connected |
| 1 | 4**D**+**D∅**+TB | D+D | D | D+D∅+TB | 3 | 2 | 1 | 2 | Attach disk on #4 | #4: `D_UP_TO_DATE` |
| 2 | 5**D**+TB | D+D | D | D+D+TB | 3 | 2 | 1 | 2 | Add TB(#5) in zone A | D+TB: #5 connected |
| 3 | 5**D**+2TB | D+D+TB | D | D+D+TB | 3 | 2 | 1 | 2 | Remove TB from zone C | D+TB: old TB removed |
| 4 | 5**D**+TB | D+D+TB | D | D+D | 3 | 2 | 1 | 2 | Remove D from zone A | All replicas: old D removed |
| 5 | 4**D**+TB | D+TB | D | D+D | 3 | 2 | 1 | 2 | — | — |

**Zone-FTT-BUA verification**: At steps 2–4 there are 5 D voters (odd, q=3 = safe
majority). Any single zone loss removes at most 2D (+ possibly TB), leaving ≥3D or ≥2D+TB
— sufficient for quorum in all cases.

**Note**: During steps 2–3 zone C has D+D+TB (3 replicas in one zone). This is a transient
state. Losing zone C at this point removes 2D+TB, leaving 3D+TB(A) or 3D — still ≥ q=3.
The procedure is safe because 5 odd voters provide enough redundancy.

### 11.4 Changing D Distribution Across Zones (Composite Layouts)

In composite layouts (3 zones), the D distribution (e.g. 2+2+1 or 2+1+1) may need to
change — either rebalancing within the same layout or during a layout transition (§10).

#### Rebalancing within the same layout

Moving a D from one zone to another while keeping the same total D count. Example: 5D
(q=3, qmr=3) changing from 2+2+1 to 1+2+2.

For **D-only layouts**: all D replicas are identical. Cross-zone move = add D∅ in target
zone (§8 add half), sync, remove D from source zone (§8 remove half). No q/qmr changes
needed if the voter count stays odd throughout.

For **D+TB layouts**: if the move doesn't involve the TB zone, same as D-only. If it
involves the TB zone, combine with §11.3 (swap D ↔ TB).

**Constraint**: the resulting distribution must still satisfy `max D per zone ≤ D − qmr`.

#### Layout transitions in 3-zone TransZonal

**4D (q=3, qmr=3) is not available in 3-zone TransZonal.** With distribution 2+1+1, losing
the 2D zone leaves 2D < qmr=3 → quorum lost. Zone-FTT-BUA=0 for the biggest zone. TB
cannot help (tiebreaker requires `up_to_date ≥ qmr`, and 2 < 3). For FTT-BDL=2 in 3
zones, use **5D (q=3, qmr=3)** directly.

The available layouts for 3-zone TransZonal with zone-FTT-BUA ≥ 1 form a **linear chain**:

```
2D+1TB (0,1) ——FTT-BDL—— 3D (1,1) ——FTT-BUA—— 4D+1TB (1,2) ——FTT-BDL—— 5D (2,2)
```

All transitions follow this chain. The §10 ordering rule ("FTT-BDL first") is naturally
satisfied where possible, and the chain avoids 4D entirely:

| # | Transition | Edge | §10 reference |
|---|-----------|------|--------------|
| 1 | 2D+1TB ↔ 3D | FTT-BDL ±1 | §10.3 |
| 2 | 3D ↔ 4D+1TB | FTT-BUA ±1 | §10.2 pattern (§8.4a + §9) |
| 3 | 4D+1TB ↔ 5D | FTT-BDL ±1 | §10.3 pattern (§8.5a + qmr + §9) |

Multi-step transitions follow the chain. Example upgrade 3D → 5D:

| # | Layout | Distribution | Zone FTT-BUA | FTT-BDL | FTT-BUA | Transition |
|---|--------|-------------|-------------|---------|---------|------------|
| 0 | 3D (q=2, qmr=2) | 1+1+1 | 1 | 1 | 1 | Transition #6: 3D → 4D+1TB (§10.2 pattern) |
| 1 | 4D+1TB (q=3, qmr=2) | 2D \| 1D+TB \| 1D | 1 | 1 | 2 | Transition #8: 4D+1TB → 5D (§10.3 pattern) |
| 2 | 5D (q=3, qmr=3) | 2+2+1 | 1 | 2 | 2 | — |

Zone-FTT-BUA=1 maintained throughout. Downgrade is the reverse path.

---

## 12. Failure Recovery

TODO — to be designed and documented.

### Questions to resolve

1. **When to initiate replacement**: after what timeout / condition does the SDS controller
   decide a replica is lost and start replacement? Immediate? After N seconds? After
   confirming permanent failure?

2. **Degraded but available** (within FTT-BUA): lost some replicas, IO continues, pFTT-BDL
   reduced.
   - Start §8 replacement to restore redundancy?
   - What if another replica fails during replacement (cascading failure)?
   - Priority: replace the replica that restores the most pFTT-BDL first?

3. **FTT-BUA exceeded** (quorum lost, IO suspended): too many replicas lost.
   - **Option A**: lower FTT-BDL/FTT-BUA (change q/qmr via §10 downgrade) to match
     current reality → resume IO → gradually restore replicas
   - **Option B**: wait for failed nodes to return / new nodes to appear → IO stays
     suspended until enough replicas are back
   - Decision criteria: how long to wait before switching from B to A?
   - Can we lower q/qmr without quorum? (state change requires quorum normally)

4. **Catastrophic loss** (all but last D): only 1 D replica remains.
   - Force downgrade to 1D (q=1, qmr=1) → resume IO from single copy
   - Gradually rebuild: 1D → 2D → 3D → ...
   - Risk: the single remaining D is now a single point of failure for both data and
     availability

5. **Zone-specific recovery**: entire zone lost.
   - §11.2 says "wait for zone return" — is this always the right strategy?
   - What if zone is permanently lost (DC fire)?
   - How to redistribute to remaining zones (violates §11.2 same-zone rule)?

6. **Split-brain after recovery**: if nodes come back with stale data after being
   partitioned, DRBD handles UUID-based resync. Any SDS-level concerns?
