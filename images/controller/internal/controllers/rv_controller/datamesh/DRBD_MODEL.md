# DRBD Replication and Quorum Model

This document describes how DRBD quorum and replication work under the hood.
It covers peer classification, the quorum condition, tiebreaker logic, write
acknowledgment semantics, replica types and their quorum roles, and the
asynchronous configuration propagation mechanism.

For a high-level overview of the datamesh — configuration, layouts,
guarantees, and topology — see [README.md](README.md).

---

## 1. Peer Classification

Each DRBD node classifies every other node (peer) into one of the following
categories for quorum calculation:

| Category | Connected? | Description |
|----------|------------|-------------|
| **up_to_date** | Yes | Diskful peer with fully synchronized, current data |
| **present** | Yes | Diskful peer with disk in any non-UpToDate state (consistent, syncing, or diskless-but-not-intentional) |
| **outdated** | No | Disconnected peer, known to have stale data |
| **quorumless** | No | Disconnected peer that was a known member but itself lost quorum |
| **unknown** | No | Disconnected peer with unknown state |
| **diskless** | Yes | Connected intentional-diskless peer |
| **missing_diskless** | No | Disconnected intentional-diskless peer |

A node also classifies **itself** by the same rules (based on its own disk
state).

### allow-remote-read exclusion

Peers configured with `allow-remote-read = false` are **completely excluded**
from the quorum calculation. They are not counted in any category — not as
voters, not as diskless, not in the tiebreaker. From the quorum perspective,
they do not exist.

This mechanism is used by the SDS to make certain replica types (Access and
ShadowDiskful) invisible to quorum on their peers — see §5 and §6.

---

## 2. Voters and Quorum Condition

### Voters

Only **diskful** peers (including disconnected ones) are **voters**:

```
voters = outdated + quorumless + unknown + up_to_date + present
```

Intentional-diskless peers (TieBreaker, Access) are **not** voters. They
participate only in tiebreaker logic (§3).

### Main quorum condition

A diskful node has quorum when **both** conditions are met simultaneously:

```
(up_to_date + present) >= q       — enough peers are reachable
up_to_date >= qmr                 — enough peers have current data
```

Where **q** (quorum) and **qmr** (quorum-minimum-redundancy) are numeric
configuration parameters set by the controller. See [README.md](README.md) §4
for the formulas that derive q and qmr from FTT and GMDR.

The first condition prevents split-brain: no network partition can give both
sides enough voters. The second condition prevents serving IO when too few
data copies exist.

### Alternative path for diskless nodes

Diskless nodes (Access, TieBreaker, ShadowDiskful) cannot evaluate the main
condition because they have no disk and do not count as voters themselves.
Instead, they use an alternative quorum path:

```
have_quorum = quorate_peers >= qmr
```

Where **quorate_peers** counts connected diskful peers that are UpToDate
**and** themselves have quorum. In effect, a diskless node has quorum if and
only if it can see at least `qmr` quorate Diskful peers.

This path is used **only** by diskless nodes; diskful nodes never use it.

---

## 3. Tiebreaker Logic

When the main quorum condition (§2) fails, DRBD evaluates an additional
**tiebreaker** path. Quorum is granted via tiebreaker if **all five** of the
following conditions are true:

1. The number of voters is **even**.
2. The node is **exactly one vote short**: `up_to_date + present == q − 1`.
3. The data redundancy check is met: `up_to_date >= qmr`.
4. A **majority** of intentional-diskless peers are connected:
   `diskless >= (diskless + missing_diskless) / 2 + 1`.
5. The node **already had quorum** before this evaluation (sticky condition).

### Critical properties

- **Even voters only.** The tiebreaker never fires when the number of voters
  is odd. With odd voters, the main condition always resolves partitions
  unambiguously (strict majority).

- **Exactly one vote short.** The tiebreaker covers only the case of losing
  exactly half of the voters. It does not help when more than half are lost.

- **Respects qmr.** The tiebreaker cannot override the data redundancy
  requirement. If fewer than `qmr` nodes have current data, IO stops
  regardless of tiebreaker status.

- **Sticky.** Condition 5 prevents both sides of a network partition from
  claiming quorum simultaneously. When a partition splits even voters into two
  equal halves, only the side that had quorum *before* the partition retains
  it via the tiebreaker. The other side never had quorum, so the sticky
  condition fails, and it correctly remains without quorum.

### When tiebreaker is needed

Tiebreaker is needed for layouts with an even number of Diskful replicas where
`FTT = D / 2` — that is, the layout must tolerate losing exactly half of its
voters. The two such layouts are **2D+1TB** (FTT=1) and **4D+1TB** (FTT=2).
In these layouts, a single TieBreaker replica provides the diskless majority
needed for condition 4.

---

## 4. Protocol C and Write Acknowledgment

DRBD Protocol C provides **synchronous replication**. A write is acknowledged
to the application only after:

1. The **local disk** has completed the write, **AND**
2. **All connected peers with UpToDate disk** have completed the write.

Intentional-diskless nodes (TieBreaker, Access) have no disk and are
transparent to Protocol C — they do **not** add write latency. Only Diskful
replicas contribute to the write path.

### Consequence for data safety

The minimum number of nodes holding **all** acknowledged data at any moment
equals the number of currently connected UpToDate nodes. This number is
bounded below by the `qmr` setting: if fewer than `qmr` nodes are UpToDate,
quorum is lost and IO stops (§2). Therefore:

- While IO is being served, at least `qmr` disks hold every acknowledged
  write.
- GMDR = `qmr − 1` — the number of permanent disk losses the system can
  survive without losing any acknowledged data (see [README.md](README.md)
  §4).

### Write latency

Write latency equals the slowest disk write among all connected UpToDate
peers. When all Diskful replicas are healthy, latency = max(D parallel disk
writes). In degraded mode (some D down), fewer writes are needed. TieBreakers
and Access replicas never add latency.

---

## 5. Replica Types

The SDS defines four replica types:

| Type | Notation | Disk | Bitmap | Connections | Quorum role | Own quorum via | Purpose |
|------|----------|------|--------|-------------|-------------|----------------|---------|
| **Diskful** | **D** | Yes | Yes | Full-mesh (all replicas) | Voter | Main condition (§2) | Stores data, participates in quorum |
| **ShadowDiskful** | **sD** | Yes | Yes | Full-mesh (all replicas) | None (invisible) | quorate_peers path (§2) | Pre-syncs data before becoming D |
| **TieBreaker** | **TB** | No | No | Star (only D + sD) | Tiebreaker only (§3) | quorate_peers path (§2) | Breaks ties in even-voter quorum |
| **Access** | **A** | No | No | Star (only D + sD) | None (invisible) | quorate_peers path (§2) | IO access without local disk |

### Full-mesh vs star topology

D and sD replicas form a **fully connected mesh** — each connects to every
other replica (D, sD, TB, and A). TB and A replicas use a **star** topology —
they connect only to full-mesh members (D and sD), never to each other.

This distinction matters for configuration propagation: when a full-mesh
member is added or removed, all replicas must update their connection
configuration. When a star member is added or removed, only the full-mesh
members need to update.

### Replica type vs member type

The SDS distinguishes two type layers:

- **Replica type** — the desired role assigned at creation: D, sD, TB, or A.
  This is what the user or the controller requests.
- **Member type** — the operational state within the datamesh. Includes the
  four replica types plus two **liminal** variants: D∅ and sD∅ (see §7).
  The member type changes during transitions as the replica moves through
  intermediate states.

For detailed per-layout quorum verification and failure scenarios, see
[LAYOUTS_ANALYSIS.md](LAYOUTS_ANALYSIS.md).

---

## 6. ShadowDiskful (sD)

ShadowDiskful is a special replica type that stores data (like D) but is
**completely invisible to quorum** (unlike D). Its purpose is to pre-sync
data before being promoted to a full voting Diskful replica. This eliminates
the hours-long full resync that would otherwise occur when adding a new voter.

### Requirement

sD requires the **`non-voting` DRBD disk option**, available only in the
Flant DRBD kernel extension. On kernels without this extension, the
controller falls back to using Access replicas as vestibules instead of sD.
The procedures are functionally equivalent but sD-based paths are faster
(delta resync instead of full resync on promotion).

### Double exclusion mechanism

sD achieves full quorum invisibility through two independent mechanisms:

- **`non-voting = true`** (on sD itself) — excludes sD from its own quorum
  calculation as a voter. sD does not count itself in the `voters` set.
- **`allow-remote-read = false`** (on all connections **toward** sD) —
  excludes sD from the quorum calculation on every peer (§1). Peers do not
  see sD in any category.

This double exclusion ensures that sD is invisible **from both sides**: it
does not affect its own quorum condition (it uses the `quorate_peers` path
instead), and peers do not count it in their quorum calculations at all.

### sD vs D

sD is functionally identical to D in terms of data: it has a disk, a bitmap,
connects to all peers via full-mesh, and syncs data via Protocol C. The only
difference is quorum participation — sD does not vote and is not counted as
a voter on any peer. This means adding or removing an sD does not change the
quorum arithmetic (voter count, q, qmr) and cannot cause split-brain or
quorum disruption.

---

## 7. Liminal Types (D∅, sD∅)

D and sD each have a **liminal** variant — a distinct member type where the
replica has its target role (voter status, full-mesh connectivity) but DRBD is
configured as diskless. The backing volume is provisioned but not yet attached
to DRBD.

- **D∅** (LiminalDiskful) — a Diskful replica without disk. Still a voter,
  counted as `present` in the quorum calculation on peers.
- **sD∅** (LiminalShadowDiskful) — a ShadowDiskful replica without disk.
  Still invisible to quorum (like sD). Functionally equivalent to Access.

Liminal types are always transitional — they exist only during type
transitions (adding/removing disk) and are never a steady-state member type.

### Why liminal types exist

DRBD requires peers to enable **bitmaps** for a member before that member
attaches its disk. If a disk is attached when peers do not yet have bitmaps
allocated for it, DRBD refuses the attach operation. The liminal type solves
this ordering problem:

1. The member is created as D∅ or sD∅ — peers configure connections and
   allocate bitmaps for it (full-mesh topology, bitmap=yes).
2. All peers confirm the new configuration.
3. The member attaches its disk (D∅ → D or sD∅ → sD) — this is now safe
   because bitmaps are already in place on all peers.

The reverse (removing disk) does not have this constraint — detaching disk
first, then updating peer configuration is always safe.

### Properties comparison

| Property | D | D∅ | sD | sD∅ | TB / A |
|----------|---|-----|-----|------|--------|
| Bitmap on peers | Yes | Yes | Yes | Yes | No |
| Connections | Full-mesh | Full-mesh | Full-mesh | Full-mesh | Star (to D + sD) |
| Voter | Yes | Yes | No | No | No |
| Disk | Attached | Detached | Attached | Detached | None |
| Quorum on peers (§1) | `up_to_date` | `present` | excluded (arr=false) | excluded (arr=false) | `diskless` / excluded |
| Own quorum | Main (§2) | Main (§2) | quorate_peers | quorate_peers | quorate_peers |

**D∅ in quorum:** A connected D∅ is counted as `present` on its peers — it
contributes to `(up_to_date + present) >= q` but **not** to
`up_to_date >= qmr`. This means D∅ helps reach the voter threshold `q`
without inflating the data safety guarantee. A disconnected D∅ is counted as
`unknown` (still a voter, works against quorum).

**sD∅ in quorum:** Completely invisible, same as sD. The `allow-remote-read =
false` + `non-voting = true` exclusions apply regardless of disk state.

---

## 8. Quorum Settings by Replica Type

Different replica types use different quorum settings, which determines how
each type evaluates its own quorum condition.

### Diskful replicas (D, D∅)

D replicas (including D∅) use the layout-specific **q** and **qmr** values
from the datamesh configuration. These are the values described in
[README.md](README.md) §4:

```
q   = floor(D / 2) + 1
qmr = GMDR + 1
```

The main quorum condition (§2) is evaluated with these values. D replicas are
the only type that participates as voters in their own quorum calculation.

### Non-voting replicas (A, TB, sD, sD∅)

Access, TieBreaker, and ShadowDiskful replicas always use **q = 32** — an
impossibly high value. The DRBD metadata is configured for a maximum of 7
peers (8 replicas total), so the condition `(up_to_date + present) >= 32`
can never be satisfied. This means non-voting replicas can **never** achieve
quorum via the main condition.

Instead, their quorum is determined entirely by the **alternative diskless
path** (§2):

```
have_quorum = quorate_peers >= qmr
```

In effect, **A, TB, sD, and sD∅ have quorum if and only if they can see at
least `qmr` connected Diskful peers that are UpToDate and themselves have
quorum.**

### Why this design

All connections **toward** A and sD replicas are configured with
`allow-remote-read = false`. As described in §1, this completely excludes them
from the quorum calculation on all peers. They are not counted as voters, not
as diskless, and not in the tiebreaker.

For sD, the `non-voting = true` disk option provides additional exclusion from
the self-quorum calculation (§6).

The combined effect: A, TB, and sD replicas are **transparent to quorum** on
all peers. Adding or removing any number of them does not change voter counts,
does not affect the main quorum condition on D replicas, and cannot cause
split-brain. Only D replicas participate in the quorum arithmetic that
determines availability and data safety.

---

## 9. Configuration Propagation

The SDS controller maintains a single **target configuration** for each
datamesh volume. This configuration includes the member list, quorum
parameters (q, qmr), multiattach flag, system networks, and member addresses.
Each configuration change is assigned a monotonically increasing
**DatameshRevision**.

### Asynchronous apply model

Configuration changes are propagated asynchronously:

1. The controller publishes a new target configuration by updating the
   datamesh state and incrementing the DatameshRevision.
2. Each replica's agent detects the change, applies the new DRBD
   configuration locally, and reports back — either **success** (by updating
   its own DatameshRevision to match) or **error** (via a status condition).
3. The controller waits for confirmations from the **required subset** of
   replicas before proceeding to the next step.

The required subset depends on what changed:

- **Full-mesh member added or removed** — all replicas must confirm (everyone
  needs to update their connection configuration).
- **Star member added or removed** — only full-mesh members plus the affected
  star member must confirm.
- **Disk attach/detach** — only the affected replica must confirm (peers
  detect disk state changes automatically via DRBD protocol).
- **Quorum parameter change** — all replicas must confirm (everyone uses qmr
  in their quorum path).

### Non-atomic transitions

Configuration changes are **not atomic** across the cluster. During
propagation, different replicas may temporarily have different configurations.
Some replicas may have already applied the new configuration while others
still operate with the old one.

All transition procedures are designed to be **safe during these transient
states**. The controller publishes only configurations that are safe regardless
of which replicas apply them first. For example, when raising the quorum
threshold `q` together with adding a voter, the configuration is constructed
so that both old-q and new-q are safe for the intermediate voter count.

For the complete list of transitions and their step-by-step confirmation
rules, see [TRANSITIONS.md](TRANSITIONS.md).
