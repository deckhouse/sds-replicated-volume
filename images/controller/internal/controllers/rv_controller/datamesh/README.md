# Datamesh

## 1. What is Datamesh

A **datamesh** is a replicated DRBD volume managed by the SDS controller. It
consists of **members** — replicas distributed across different nodes — that
replicate data synchronously using DRBD Protocol C. Every acknowledged write
is stored on all connected UpToDate disks before the application sees
success — for multi-replica layouts this provides protection against node
and disk failures.

The controller manages the full lifecycle of a datamesh:

- **Formation** — creating the initial set of replicas, establishing DRBD
  connectivity, and bootstrapping data synchronization.
- **Membership** — adding, removing, and retyping replicas as the cluster
  evolves (node failures, capacity changes, configuration updates).
- **Quorum** — adjusting quorum parameters to match the current member count
  and configuration.
- **Attachment** — making the volume Primary on a node so it can serve IO,
  and detaching when no longer needed.
- **Network** — repairing addresses when IPs change and migrating between
  system networks.
- **Resize** — growing the volume when rv.Spec.Size increases, coordinating
  backing volume growth and DRBD resize across all diskful members.
- **Failure recovery** — when a node is permanently lost (removed from
  Kubernetes), the controller automatically force-detaches and force-removes
  the dead member. Re-creating what was lost is automatic for TieBreakers
  only; a lost Diskful replica is replaced manually (see
  [Current limitations](#current-limitations)).

All operations are performed as structured, multi-step **transitions** that
maintain safety guarantees at every intermediate step. See
[TRANSITIONS.md](TRANSITIONS.md) for a complete description of all transition
types. For detailed DRBD mechanics, see [DRBD_MODEL.md](DRBD_MODEL.md).
For the automatic failure recovery chain and Deckhouse fencing integration,
see [LAYOUTS_PROCEDURES.md](LAYOUTS_PROCEDURES.md) §6.

### Current limitations

The following capabilities are described in this documentation but **not yet
implemented**. They are planned for the near term:

- **Automatic Diskful replica creation during normal operation.** After
  formation, the controller never creates a **Diskful** replica on its own.
  This covers failure recovery (replacing a Diskful member that was
  force-removed together with its node), layout changes that need more diskful
  voters (raising FTT or GMDR), and storage pool migration. To trigger the
  desired operation, it is sufficient to create a replica with the target type
  (or change the type of an existing one) — the scheduler will assign the node
  and storage, and the rest (datamesh membership, DRBD configuration, data
  synchronization) happens fully automatically. Once automatic Diskful creation
  is implemented, these scenarios will require no manual steps at all.

  What **is** created automatically after formation:

  - **Access** replicas for attachment (§9) — created on a node that requests
    an attachment and has no replica yet, and deleted again once they become
    unused (no active RVA) or redundant (another member on the same node).
  - **TieBreaker** replicas, by the layout convergence loop in `rv_controller`:
    a missing TieBreaker is created whenever the diskful count already matches
    the intended layout (heal — e.g. a TieBreaker whose RVR is already gone, or
    an adopted volume that entered normal operation at 2D under an r2
    configuration), and a TieBreaker whose RVR is being deleted gets its
    replacement created while the old one is still working (strict
    create-first, see §8).

  Convergence also **retypes** one Diskful into the missing TieBreaker on the
  r3→r2 path (3D → 2D+1TB) — a type change of an existing replica, not a new
  one. It never creates a Diskful replica and never deletes a replica or its
  data; a layout mismatch outside those cases is reported as
  `LayoutConverged=False/TransitionUnsupported` and waits for manual
  intervention. For the full decision order and the reported reasons, see the
  `rv_controller` README, "Layout convergence".

- **EventuallyLocal VolumeAccess mode.** The EventuallyLocal mode (which
  automatically migrates a Diskful replica to the node where the volume is
  attached) is not yet implemented. All underlying mechanics are fully
  ready — the only missing piece is the logic that changes the attached
  Access replica's type to Diskful (and removes one of the old Diskful
  replicas). The datamesh transition engine, scheduling, and data
  synchronization handle the rest.

All other operations described in this documentation — transitions,
preconditions, quorum management, attachment, network repair — are fully
implemented and operational.

---

## 2. Configuration

A datamesh is created from a **ReplicatedVolume** (RV). The RV's configuration
determines the layout (how many replicas, what quorum settings) and placement
(which nodes, which zones).

### Configuration modes

- **Auto mode** (default) — the RV references a **ReplicatedStorageClass**
  (RSC). The RSC specifies the configuration parameters, and the controller
  derives the RV configuration from it. What happens to volumes that already
  exist when the RSC changes is decided by the class rollout strategy
  (`rsc.spec.configurationRolloutStrategy`; a strategy the class controller has
  not defaulted yet reads as `RollingUpdate`):

  | Strategy | Effect on existing volumes |
  |----------|----------------------------|
  | **RollingUpdate** (default) | Every volume of that class receives the new configuration and starts the corresponding layout transition (§8). Volumes pick it up as they reconcile; `maxParallel` throttling is not implemented, so the rollout is not staged. |
  | **NewVolumesOnly** | Only volumes created afterwards get the new configuration. A volume that already has one keeps both its configuration and the generation it came from, and reports `ConfigurationReady=False/NewerConfigurationHeld`. Nothing gates on that condition — the volume keeps operating on its own configuration. The escape is to switch the strategy to `RollingUpdate` or to recreate the volume, never a silent replacement. |

  Generation tracking (`ConfigurationGeneration` vs `ConfigurationObservedGeneration`)
  and the RSC status freshness gate are described in the `rv_controller` README,
  "reconcileRVConfiguration".

- **Manual mode** — the configuration is specified directly on the RV, without
  an RSC reference.

### Configuration parameters

| Parameter | Description |
|-----------|-------------|
| **Storage pool** | The ReplicatedStoragePool (RSP) that provides LVM volume groups for backing volumes. |
| **FTT** (Failures To Tolerate) | How many simultaneous node failures the volume tolerates while staying available (0, 1, or 2). |
| **GMDR** (Guaranteed Minimum Data Redundancy) | How many permanent disk losses the volume survives without losing data (0, 1, or 2). |
| **Topology** | Placement strategy: Ignored, Zonal, or TransZonal (see §7). |
| **VolumeAccess** | IO access mode: Any, PreferablyLocal, or Local (see §9). |

### Legacy replication parameter

For backward compatibility, the RSC may use a single **replication** parameter
instead of separate FTT and GMDR. The mapping is:

| Replication | FTT | GMDR | Layout |
|-------------|-----|------|--------|
| None | 0 | 0 | 1D |
| Availability | 1 | 0 | 2D+1TB |
| Consistency | 0 | 1 | 2D |
| ConsistencyAndAvailability | 1 | 1 | 3D |

The legacy parameter never appears in the resolved configuration — it is always
converted to FTT and GMDR.

---

## 3. DRBD Basics

This section provides a minimal understanding of DRBD sufficient for the rest
of the document. For a complete description of DRBD quorum mechanics, peer
classification, tiebreaker logic, and replica types, see
[DRBD_MODEL.md](DRBD_MODEL.md).

**Protocol C (synchronous replication).** DRBD replicates block devices across
nodes. With Protocol C, a write is acknowledged to the application only after
the local disk *and* all connected peers with up-to-date data have completed
the write. This guarantees that all acknowledged data exists on every
connected UpToDate disk at any moment. With `qmr >= 2`, at least two disks
always hold all data while IO is served.

**Quorum.** A node may serve IO only when it has **quorum** — enough peers are
reachable and enough data copies are current. Two numeric parameters control
this:

- **q** (quorum): the minimum number of reachable voters (diskful nodes).
  Prevents split-brain — no network partition can give both sides quorum.
- **qmr** (quorum minimum redundancy): the minimum number of nodes with
  up-to-date data. Prevents serving IO when too few copies exist.

When quorum is lost (not enough voters or not enough up-to-date copies), DRBD
suspends IO on the affected node. IO resumes automatically when quorum is
restored.

**Voters and non-voters.** Only **Diskful** replicas are voters — they store
data and participate in the quorum calculation. **TieBreaker** and **Access**
replicas are diskless non-voters: they do not affect quorum calculations but
serve other purposes (tiebreaker for even-voter layouts, IO access point
without local disk).

**TieBreaker.** When the number of voters is even, a network partition can
split them into two equal halves. A TieBreaker — a lightweight diskless node —
resolves the tie: the side that retains the TieBreaker keeps quorum. The
TieBreaker adds no write latency and requires minimal resources.

---

## 4. FTT, GMDR, and ADR

Three metrics describe the protection level of a datamesh:

### GMDR — Guaranteed Minimum Data Redundancy

How many **permanent disk losses** the system can survive at any point while
serving IO, without losing any acknowledged data.

Since Protocol C writes to all connected up-to-date peers, and `qmr` is the
minimum number of up-to-date nodes required for IO, the worst case is:

```
GMDR = qmr − 1
```

With `qmr = 2`, at least 2 nodes always hold all acknowledged data. Destroying
1 disk leaves 1 copy intact. Destroying both → data lost. GMDR = 1.

### FTT — Failures To Tolerate

How many **arbitrary node failures** (crash, network partition, disk failure)
the system can tolerate simultaneously while remaining available — maintaining
quorum *and* at least `qmr` accessible up-to-date disks.

### ADR — Actual Data Redundancy

The **current** data redundancy level: how many disks can be permanently
destroyed right now without data loss. Simply the number of UpToDate Diskful
replicas minus 1:

```
ADR = UpToDate_D_count − 1
```

During normal operation, ADR ≥ GMDR always holds. ADR reflects the actual
number of data copies at this moment, while GMDR is the worst-case guarantee
determined by `qmr`.

### Key formulas

```
qmr = GMDR + 1
D   = FTT + GMDR + 1      (minimum diskful replicas)
q   = floor(D / 2) + 1    (minimum safe quorum value)
TB  = 1  if D is even AND FTT = D / 2, else 0
```

**Why D = FTT + GMDR + 1**: after losing FTT nodes (worst case), the remaining
D − FTT nodes must still satisfy qmr: D − FTT ≥ qmr = GMDR + 1.

**Why q = floor(D/2) + 1**: the minimum q that prevents split-brain. No
partition can have both sides meet the quorum threshold.

**Why TB is needed**: with even D, q = D/2 + 1. After losing exactly D/2 nodes
(= FTT), only D/2 remain — one vote short. The TieBreaker bridges this gap.

### Valid combinations

FTT and GMDR must satisfy |GMDR − FTT| ≤ 1, with both in {0, 1, 2}:

| GMDR | FTT | qmr | D | q | TB | Total nodes |
|------|-----|-----|---|---|----|-------------|
| 0 | 0 | 1 | 1 | 1 | 0 | 1 |
| 0 | 1 | 1 | 2 | 2 | 1 | 3 |
| 1 | 0 | 2 | 2 | 2 | 0 | 2 |
| 1 | 1 | 2 | 3 | 2 | 0 | 3 |
| 1 | 2 | 2 | 4 | 3 | 1 | 5 |
| 2 | 1 | 3 | 4 | 3 | 0 | 4 |
| 2 | 2 | 3 | 5 | 3 | 0 | 5 |

---

## 5. Layouts

Each valid FTT/GMDR combination produces a specific **layout** — a fixed
composition of Diskful (D) and TieBreaker (TB) replicas with corresponding
quorum settings.

### Layout summary

| GMDR | FTT | Layout | q | qmr | D | TB | Total | Min copies during IO | Write latency |
|------|-----|--------|---|-----|---|----|-------|----------------------|---------------|
| 0 | 0 | **1D** | 1 | 1 | 1 | 0 | 1 | 1 | 1 disk write |
| 0 | 1 | **2D+1TB** | 2 | 1 | 2 | 1 | 3 | 1 | max 2 parallel |
| 1 | 0 | **2D** | 2 | 2 | 2 | 0 | 2 | 2 | 2 parallel |
| 1 | 1 | **3D** | 2 | 2 | 3 | 0 | 3 | 2 | max 3 parallel |
| 1 | 2 | **4D+1TB** | 3 | 2 | 4 | 1 | 5 | 2 | max 4 parallel |
| 2 | 1 | **4D** | 3 | 3 | 4 | 0 | 4 | 3 | max 4 parallel |
| 2 | 2 | **5D** | 3 | 3 | 5 | 0 | 5 | 3 | max 5 parallel |

- **Min copies during IO**: the minimum number of disks holding all
  acknowledged data while IO is being served. Equals `qmr`.
- **Write latency**: Protocol C waits for the slowest disk among all connected
  up-to-date peers. TieBreakers do not add latency.

### Recommendations

| Use case | Recommended | Total | Why |
|----------|-------------|-------|-----|
| Development / ephemeral data | GMDR=0, FTT=0 (1D) | 1 | Single disk, no redundancy |
| Production, availability-first | GMDR=0, FTT=1 (2D+1TB) | 3 | ≈ classic RAID1: 2 copies, stays available through 1 failure |
| Production, data-safety-first | GMDR=1, FTT=0 (2D) | 2 | ≈ strict RAID1: writes only when both disks are up, always 2 copies |
| Production, balanced | GMDR=1, FTT=1 (3D) | 3 | ≈ RAID1 + hot spare: always 2+ copies, tolerates 1 failure |
| Production, high availability | GMDR=1, FTT=2 (4D+1TB) | 5 | ≈ 4-way mirror: always 2+ copies, tolerates 2 failures |
| Mission-critical, data safety | GMDR=2, FTT=1 (4D) | 4 | ≈ strict 3-way mirror: always 3+ copies, tolerates 1 failure |
| Mission-critical, full | GMDR=2, FTT=2 (5D) | 5 | ≈ 3-way mirror + spares: always 3+ copies, tolerates 2 failures |

### Understanding the GMDR / FTT trade-off

FTT and GMDR are **independent axes** — neither must be ≤ or ≥ the other:

- **GMDR > FTT** (e.g. 1, 0): system stops IO early (before any risk to data).
  Maximum data safety, but more downtime.
- **GMDR = FTT** (e.g. 1, 1): system stays available until the moment one more
  failure would risk data. Balanced.
- **GMDR < FTT** (e.g. 0, 1): system prioritizes availability — stays running
  even when data redundancy has dropped to the last copy. Riskier for data,
  fewer outages.

For detailed per-layout quorum verification and failure analysis, see
[LAYOUTS_ANALYSIS.md](LAYOUTS_ANALYSIS.md).

---

## 6. Guarantees

The datamesh controller maintains three safety constraints during **all**
operations — replica replacement, layout transitions, emergency recovery,
and normal membership changes:

1. **Never reduce GMDR/FTT below the target level.** The actual protection
   level at every intermediate step must be at least as strong as the
   minimum of the source and target configurations. A replica is not removed
   until enough copies exist to absorb its loss.

2. **Never interrupt IO.** Quorum must be maintained throughout every
   transition. Steps are ordered so that quorum-critical changes (voter
   additions, quorum parameter adjustments) happen atomically — the system
   is safe regardless of which replicas apply the new configuration first.

3. **Never create split-brain risk.** The quorum threshold `q` is always
   set to `floor(voters / 2) + 1`, ensuring no partition can give both sides
   quorum. When the voter count changes, `q` is raised *before* adding a
   voter (odd→even) and lowered *after* removing a voter (even→odd).

These guarantees are enforced through **precondition guards** — checks that
run before each transition is created. A guard that fails blocks the
transition until the condition is met (e.g., "wait for replacement replica
to reach UpToDate before removing the old one").

For the full list of preconditions, see [TRANSITIONS.md](TRANSITIONS.md).

---

## 7. Topology

The **topology** parameter controls how replicas are distributed across
availability zones.

### Ignored (default)

No zone awareness. Replicas are placed on the best-scoring nodes regardless
of zone. The failure domain is a single node.

### Zonal

All replicas are placed within a **single zone**. The failure domain is a
single node (same as Ignored), but placement is restricted to one zone. A
placement guard ensures new replicas go to the zone that already has the most
voters (sticky behavior).

### TransZonal

Replicas are distributed **across zones**. The failure domain depends on the
layout and the number of available zones:

- **Pure zone failure domain** (1 replica per zone): each D and TB occupies
  its own zone. Losing K zones loses exactly K replicas. Requires
  `zones = D + TB`.
- **Composite failure domain** (multiple replicas per zone): when fewer zones
  are available, replicas share zones under constraints. The layout survives
  loss of one zone, but FTT may cover individual node failures across zones.

### TransZonal zone requirements

| FTT | GMDR | Min zones | Failure domain |
|-----|------|-----------|----------------|
| 0 | 0 | 1 | — |
| 0 | 1 | 2 | — |
| 1 | 0 | 3 | zone |
| 1 | 1 | 3 | zone |
| 1 | 2 | 4 | zone |
| 2 | 1 | 3 (composite) or 5 (pure) | composite or zone |
| 2 | 2 | 3 (composite) or 5 (pure) | composite or zone |

**Composite constraints:**

- Max D per zone ≤ D − qmr (ensures losing any zone still leaves ≥ qmr D).
- TB zone must have ≤ 1D (losing the TB zone should not also remove
  multiple voters).

**Note:** 4D (GMDR=2, FTT=1) is **not available** in 3-zone TransZonal. With
distribution 2+1+1, losing the 2D zone leaves 2D < qmr=3.

For zone-aware replacement procedures and zone redistribution, see
[LAYOUTS_PROCEDURES.md](LAYOUTS_PROCEDURES.md).

---

## 8. Layout Transitions

When the FTT or GMDR configuration changes, the datamesh transitions from the
current layout to the new one. Layouts form a graph where each edge changes
exactly one parameter by ±1:

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

Multi-step transitions (e.g., 1D → 5D) follow a path along the edges. At each
step, exactly one parameter changes.

### Ordering rules

- **Upgrade** (increasing FTT or GMDR): prefer increasing GMDR first. This
  ensures data safety improves before availability. If the GMDR edge does not
  exist (would violate |GMDR−FTT| ≤ 1), increase FTT instead.
- **Downgrade** (decreasing FTT or GMDR): prefer decreasing FTT first. This
  reduces availability (the "cheaper" guarantee) first, preserving data safety
  as long as possible.

This ordering produces paths through **pure-D layouts** (no TB management) when
possible, which are simpler:

```
Upgrade  (0,0)→(2,2):  1D → 2D → 3D → 4D → 5D
Downgrade (2,2)→(0,0): 5D → 4D → 3D → 2D → 1D
```

All three safety guarantees (§6) are maintained at every intermediate step.

### Replacing a TieBreaker: strict create-first

Deleting a live TieBreaker (node drain, manual `kubectl delete rvr`) does not remove it from the
datamesh right away. The RVR keeps the controller finalizer, so it stays a working DRBD peer while
the datamesh replaces it — **the replacement first becomes operational, only then is the old one
released**. There is never a moment without tiebreak protection.

| State | What happens |
|-------|--------------|
| Old TieBreaker terminating, no replacement | Layout convergence creates a replacement RVR; `LayoutConverged=False/Converging` |
| Replacement created, not scheduled | `Converging`; a **current** `Scheduled=False` (no free eligible node) → `CannotConverge` with the scheduler's message |
| Replacement joined the datamesh, not operational yet | `Converging`; the old TieBreaker is still held by `guardTBSufficient` |
| Replacement operational | The guard lets the old one leave; the layout is honestly reported as `2D+2TB` while it does |
| Old TieBreaker gone | `2D+1TB`, `Converged` |

"Operational" means more than membership: the replacement must have applied the current datamesh
revision, report `DRBDConfigured=True` for its current spec, and have every connection to the
data-bearing members confirmed `Connected` by a fresh reporter. Joining alone only proves that the
agents applied the configuration revision, not that DRBD connected.

Two consequences worth knowing:

- **The window holds two TieBreakers.** That is legal for DRBD (diskless peers are generic, none of
  them is a voter), but a tiebreak then requires connectivity with *both* diskless peers — which is
  precisely why the switch waits for the new one to be operational instead of overlapping blindly.
- **No free eligible node → the deletion waits.** The terminating TieBreaker still occupies its
  node, so the scheduler has nowhere to put the replacement; the volume reports `CannotConverge`
  and keeps running on the terminating-but-alive TieBreaker. To finish the deletion on a full
  cluster, remove the finalizer from the terminating RVR: it becomes an orphan, is force-removed
  without TieBreaker guards, and a replacement is then scheduled onto the freed node (recipe in
  `debug_and_problem_solving.md`).

Guard details are in [TRANSITIONS.md](TRANSITIONS.md) § "TieBreaker replacement"; the creation
side lives in `rv_controller` (see its README, "Layout convergence").

For step-by-step transition procedures, see
[LAYOUTS_PROCEDURES.md](LAYOUTS_PROCEDURES.md).

---

## 9. Attachment

A volume is **attached** on a node when the DRBD device is in Primary mode —
the node can serve IO through the block device. Attachment is controlled by
**ReplicatedVolumeAttachment** (RVA) objects: the presence of an active RVA
means "this node wants to attach".

### maxAttachments

`maxAttachments` (default: 1) limits how many nodes can be attached
simultaneously. When a single node is attached, the volume operates in
single-Primary mode. When multiple nodes need attachment, the controller
automatically enables **multiattach** (DRBD dual-Primary), waits for all
replicas to confirm the mode change, and then proceeds with the additional
attachments.

### VolumeAccess modes

| Mode | Who can attach | Description |
|------|---------------|-------------|
| **Any** | Any member type | IO may go through a diskless replica (Access, TieBreaker) — reads are served from a remote peer over the network. |
| **PreferablyLocal** | Any member type | Same as Any for attachment purposes. The scheduler prefers placing Diskful replicas on nodes that will attach, but does not enforce it. |
| **Local** | Only Diskful members | IO goes through the local disk — no network hop for reads. Access replicas are not created on attach nodes in this mode. |

#### Local and membership changes

`volumeAccess=Local` constrains **attachment**, not membership. Two distinct guards express that:

- `guardVolumeAccessNotLocal` (defined in `membership_plan_access.go`) blocks the creation of an
  **Access** replica under Local, because an Access replica exists precisely to serve I/O
  remotely. It is carried by the Access plans and by `sd-to-tb/v1`.
- `guardVolumeAccessLocalForDemotion` (in `loseVoterGuardsCommon`) blocks demoting the **attached**
  Diskful member under Local — the attached node must keep its local disk.

The D→TB plans (`d-to-tb/v1`, `d-to-tb-q-down/v1`) deliberately carry **only** the second guard: a
TieBreaker serves no I/O in any access mode, so demoting an *unattached* Diskful under Local is
legitimate. This is the r3→r2 migration path; blocking it was a bug (the blanket guard had been
copied over from the Access plans by analogy).

`sd-to-tb/v1` keeps `guardVolumeAccessNotLocal` on purpose: a ShadowDiskful *has* a backing volume
and can serve a Local attachment, while the demotion-safety protocol (intent/gate/extender) is
designed for Diskful only, so dropping the guard there would open an unprotected path to losing
local I/O. Extending that protocol to ShadowDiskful is out of scope.

Guards are evaluated at **dispatch** time. Closing the race where an attachment appears between a
controller-side decision and dispatch (a dispatch-time workload/RVA guard, execution-record revoke,
rollback/repick) is specified separately in the **RVR authorization design contract**
(`docs/rvr-authorization-design-contract.md` in the project workspace) and is not implemented in
the guard layer described here.

### FIFO ordering

When multiple nodes request attachment and only one slot is available, earlier
requests are processed first (by RVA creation timestamp). An already-attached
member keeps its slot — FIFO ordering cannot preempt a settled attachment.

### maxAttachments decrease

When `maxAttachments` is decreased below the current number of attached nodes,
the controller does **not** force-detach any member. All currently-attached
members remain attached. New attachments are blocked until enough members
detach naturally (via RVA deletion).

For detailed attach/detach mechanics, slot management, ForceDetach, and
preconditions, see [TRANSITIONS.md](TRANSITIONS.md).

---

## 10. Effective Layout

The **effective layout** reports the protection level that the datamesh
**actually provides right now**, based on observable cluster state. It is
recomputed every reconciliation cycle — independently of configuration
changes or transition progress.

### Why it exists

The configured layout (FTT, GMDR) is the **desired** protection level. When
configuration changes (e.g., FTT 1→2), the datamesh does not instantly have
2 fault tolerance — it still has 1 until new replicas are added and synced.
The effective layout tells the administrator what the cluster actually
delivers.

### Effective FTT and GMDR

```
effective_FTT  = min(reachable_D − q + TB_bonus, UpToDate_D − qmr)
effective_GMDR = min(UpToDate_D, qmr) − 1
```

Where:
- **reachable_D** = voters whose agent confirms quorum, or stale voters seen
  as Connected by at least one peer with a ready agent.
- **UpToDate_D** = voters with UpToDate backing volume, or stale voters seen
  as UpToDate by a peer.
- **TB_bonus** = 1 when reachable_D is even and at least one TieBreaker is
  reachable; 0 otherwise.

When no voter members exist or no agent has fresh data, FTT and GMDR are
reported as unavailable (nil).

### Negative values

**FTT and GMDR can be negative.** This indicates degradation:

- **Negative FTT**: the system has already lost more voters than it can
  tolerate. Quorum may be lost on some nodes.
- **Negative GMDR**: the number of UpToDate copies is below the quorum
  minimum redundancy. The system may be serving IO with fewer data copies
  than configured.

### Baseline GMDR

A separate **baseline GMDR** tracks the guaranteed minimum that the datamesh
has committed to. It is updated when quorum minimum redundancy (qmr) changes
are confirmed by all replicas:

```
baseline_GMDR = min(qmr − 1, configured_GMDR)
```

The baseline ensures that external consumers (e.g., scheduling decisions) can
rely on a stable minimum without waiting for the per-cycle effective layout
recomputation.

### Observable counts

The effective layout also exposes raw counts for diagnostics: total/reachable/
UpToDate voters, total/reachable TieBreakers, stale agent count, and a
human-readable summary message.

---

## 11. Configuration Mutability

Not all configuration parameters can change at any time. The mutability rules
depend on the datamesh lifecycle phase.

### During formation

While the initial set of replicas is still being created, the configuration is
**frozen**. Changes to the RSC or to the RV spec are ignored until formation
completes. This prevents a moving target that could lead to
partially-created layouts with inconsistent quorum settings.

### During normal operation

After formation, configuration changes are accepted and the controller starts
transitioning to the new layout. Specifically:

- **FTT / GMDR changes** trigger a layout transition (§8). The transition
  follows the edge-by-edge path through the layout graph, maintaining safety
  at every intermediate step. Steps that need an additional **Diskful** voter
  wait for that replica to be created manually (see
  [Current limitations](#current-limitations)); a missing TieBreaker is created
  by convergence, and the r3→r2 step retypes a Diskful into it.
- **Storage pool changes** are accepted. New replicas are created on the new
  pool. Existing replicas remain on their current pool
  (`SatisfyEligibleNodes` will report the mismatch). Full migration of
  existing replicas between pools is not yet implemented.
- **Topology changes** are accepted: the new zone requirements apply to every
  placement decision from that moment on (scheduling of new replicas, zone
  guards). Redistributing the **existing** Diskful replicas across zones needs
  new Diskful replicas and therefore follows the same manual path (see
  [Current limitations](#current-limitations) and
  [LAYOUTS_PROCEDURES.md](LAYOUTS_PROCEDURES.md) §5).
- **VolumeAccess changes** affect future attachment decisions and new replica
  creation. Existing replicas of incompatible types are not automatically
  converted — they are removed through normal cleanup when no longer needed
  (e.g., Access replicas deleted when no active RVA exists on their node).

### RSC rollout

Which volumes receive an RSC configuration change is decided by the class
rollout strategy (§2): under `RollingUpdate` every RV referencing that RSC
does, under `NewVolumesOnly` only volumes created afterwards. A volume that
receives the update detects the drift during reconciliation and begins a
transition to the new layout. Multiple RVs may transition concurrently —
`maxParallel` throttling is not implemented.

---

## 12. Transitions Overview

The datamesh controller manages all changes through structured
**transitions** — declarative multi-step operations that are persisted, can
survive controller restarts, and maintain safety at every intermediate step.

### How transitions work

1. A **dispatcher** examines the current state and decides which transition
   to create (e.g., "we have fewer Diskful replicas than the layout requires
   — create an AddReplica(D) transition").
2. A **guard** verifies preconditions before the transition starts (e.g.,
   "there is no other conflicting membership transition in progress").
3. The transition is created as a multi-step **plan**. Each step encodes
   an action to take and a confirmation to wait for.
4. Steps are applied in order. After applying a step, the controller waits
   for the **confirm callback** to report success before moving to the next
   step.
5. When all steps are confirmed, the transition completes and is removed.

Transitions are **idempotent** — applying a step multiple times has the same
effect as applying it once. The controller reconciles periodically and simply
re-checks what needs to happen next.

### Transition groups

| Group | Scope | Examples |
|-------|-------|---------|
| **Formation** | Initial replica creation | Managed by rv_controller (not the datamesh transition engine) |
| **VotingMembership** | Adding, removing, or retyping voters (D) | AddReplica(D), RemoveReplica(D), ChangeReplicaType(D to/from other), ForceRemoveReplica(D) |
| **NonVotingMembership** | Adding, removing, or retyping non-voters | AddReplica(A/TB/sD), RemoveReplica(A/TB/sD), ChangeReplicaType among non-voters |
| **Quorum** | Adjusting quorum parameters | ChangeQuorum |
| **Attachment** | Primary/Secondary mode changes | Attach, Detach |
| **Multiattach** | Dual-Primary mode toggle | EnableMultiattach, DisableMultiattach |
| **Network** | IP address and network updates | RepairNetworkAddresses, ChangeSystemNetworks |
| **Resize** | Volume size changes | ResizeVolume |
| **Emergency** | Recovery from damaged states | ForceRemoveReplica, ForceDetach |

### Parallelism rules

Some transitions can run concurrently; others must be serialized:

- **VotingMembership**: serialized — at most one active at a time.
  Mutually exclusive with Quorum and Network.
- **NonVotingMembership**: can run in parallel with everything (including
  VotingMembership and other NonVotingMembership), subject to
  one-membership-per-replica limit.
- **Quorum**: serialized. Mutually exclusive with VotingMembership and
  Network.
- **Attachment**: independent from membership and quorum. One attachment
  transition per replica. Second+ Attach requires multiattach to be
  enabled and confirmed.
- **Multiattach**: serialized (no double-toggle). Independent from
  membership and quorum.
- **Network**: serialized. Blocks VotingMembership and Quorum (addresses
  must be stable before voter/quorum changes). Not blocked by
  VotingMembership or Quorum (semi-emergency: repair is urgent).
- **Resize**: serialized — at most one active at a time. Mutually exclusive
  with disk-gaining membership transitions (AddReplica D/sD,
  ChangeReplicaType to D/sD). Independent from Quorum, Attachment,
  Multiattach, and Network.
- **Formation**: blocks everything except Network and Emergency.
- **Emergency**: always admitted. Cancels conflicting transitions for the
  affected replica (CancelActiveOnCreate) rather than blocking them.

### Conflict detection

When a transition is about to be created, the engine checks for
**conflicts** — active transitions in the same or mutually-exclusive groups.
If a conflict exists, the creation is skipped and the engine waits for the
conflicting transition to finish.

For the complete list of transitions, their preconditions, step structures,
confirm rules, and parallelism constraints, see
[TRANSITIONS.md](TRANSITIONS.md).

---

*For internal architecture, data structures, and the datamesh/dmte interface,
see [INTERNALS.md](INTERNALS.md). For behavioral test cases, see
[TEST_CASES.md](TEST_CASES.md).*
