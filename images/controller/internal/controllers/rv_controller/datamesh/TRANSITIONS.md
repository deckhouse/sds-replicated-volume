# Datamesh Transitions

This document describes all transition types used by the datamesh controller,
their preconditions, step confirmation rules, and parallelism constraints.

For a high-level overview of transitions and their role in the datamesh
lifecycle, see [README.md](README.md) §12. For step-by-step layout
procedures (replica replacement, layout transitions, topology), see
[LAYOUTS_PROCEDURES.md](LAYOUTS_PROCEDURES.md).

## How transitions work

A **transition** is a structured, multi-step operation that changes the
datamesh state. Transitions are persisted in the volume status and survive
controller restarts. Each transition has:

- **Type** — what it does (e.g., AddReplica, Attach, ChangeQuorum).
- **Group** — concurrency category (e.g., VotingMembership, Attachment).
  Determines which transitions can run in parallel (see §11).
- **Plan** — a versioned sequence of steps, selected at creation time based
  on the current state (e.g., voter parity, feature flags).
- **Steps** — ordered actions, each with an apply callback (mutates state)
  and a confirm callback (waits for replicas to apply the change).
- **Guards** — precondition checks evaluated before the transition is created.
  If any guard fails, creation is blocked until the condition is met.

The lifecycle of a transition:

1. A **dispatcher** examines the current state and decides which transition
   to create.
2. The engine checks for **slot conflicts** (another transition of a
   different type already active on the same replica slot).
3. **Guards** are evaluated in order. The first blocking guard stops creation.
4. The **tracker** checks admission rules (parallelism constraints).
5. The transition is **created** and the first step is applied.
6. On each reconciliation cycle, the engine **confirms** the current step
   (checks that the required replicas have applied the new configuration).
7. When a step is confirmed, the engine **advances** to the next step.
8. When all steps are confirmed, the transition **completes** and is removed.

Transitions are **idempotent** — applying a step multiple times has the same
effect as applying it once.

---

## 1. Membership States

Each datamesh member passes through a defined set of states during its
lifecycle. For full details on replica types, quorum roles, and connectivity
topology, see [DRBD_MODEL.md](DRBD_MODEL.md) §5–§7.

| Notation | State | Description |
|----------|-------|-------------|
| **✦** | **New** | RVR exists, not yet a datamesh member, wants to join |
| **A** | **Access** | Diskless member, invisible to quorum. IO access via DRBD. |
| **TB** | **TieBreaker** | Diskless member, participates in tiebreaker voting only. |
| **D∅** | **LiminalDiskful** | Voter with full-mesh connectivity, but disk not yet attached. Backing volume provisioned. Always transitional. |
| **D** | **Diskful** | Data-bearing voter. Disk attached, quorum participant. |
| **sD∅** | **LiminalShadowDiskful** | Non-voter with full-mesh connectivity, disk not yet attached. Functionally equivalent to A. Always transitional. |
| **sD** | **ShadowDiskful** | Diskful but invisible to quorum. Pre-syncs data before promotion to D. Requires Flant DRBD extension. |
| **✕** | **Deleted** | Left the datamesh. Terminal state. |

---

## 2. Membership Transitions

Three transition types cover all datamesh member lifecycle operations:

- **AddReplica(type)** — create a new member of the given type.
- **RemoveReplica(type)** — remove an existing member.
- **ChangeReplicaType(from, to)** — change a member's type in place.

A fourth type handles emergency removal of dead members:

- **ForceRemoveReplica(type)** — remove a dead member immediately, bypassing
  normal preconditions.

Each concrete (from, to) pair determines the plan — including liminal
intermediate states, disk attach/detach ordering, and quorum parameter
changes. Quorum changes (q↑/q↓) are steps within these transitions when the
voter count changes parity. Layout-driven qmr changes (qmr↑/qmr↓) are
embedded as extra steps: qmr↑ appended at the end of AddReplica(D), qmr↓
prepended at the beginning of RemoveReplica(D). A standalone ChangeQuorum
transition handles cases where q or qmr drift without a matching membership
change (see §6).

For step-by-step verification of each procedure with quorum analysis at every
boundary, see [LAYOUTS_PROCEDURES.md](LAYOUTS_PROCEDURES.md).

### 2.1 AddReplica

| Transition | Path | When |
|------------|------|------|
| AddReplica(A) | ✦ → A | always |
| AddReplica(TB) | ✦ → TB | always |
| AddReplica(sD) | ✦ → sD∅ → sD | Flant DRBD available |
| AddReplica(D) | ✦ → D∅ → D | even→odd voters |
| AddReplica(D) + qmr↑ | ✦ → D∅ → D → qmr↑ | even→odd voters, qmr below target |
| AddReplica(D) + q↑ | ✦ → A → D∅ + q↑ → D | odd→even voters |
| AddReplica(D) + q↑ + qmr↑ | ✦ → A → D∅ + q↑ → D → qmr↑ | odd→even voters, qmr below target |
| AddReplica(D) via sD | ✦ → sD∅ → sD → D | Flant DRBD, even→odd voters |
| AddReplica(D) via sD + qmr↑ | ✦ → sD∅ → sD → D → qmr↑ | Flant DRBD, even→odd voters, qmr below target |
| AddReplica(D) via sD + q↑ | ✦ → sD∅ → sD → sD∅ → D∅ + q↑ → D | Flant DRBD, odd→even voters |
| AddReplica(D) via sD + q↑ + qmr↑ | ✦ → sD∅ → sD → sD∅ → D∅ + q↑ → D → qmr↑ | Flant DRBD, odd→even voters, qmr below target |

**Key concepts:**

- **D∅** (liminal diskful): peers must allocate bitmaps before disk attach.
  The member is created as D∅ first, all peers confirm, then disk attaches
  (D∅ → D). See [DRBD_MODEL.md](DRBD_MODEL.md) §7.

- **Via sD**: ShadowDiskful pre-syncs data invisibly before voter promotion.
  The bulk synchronization happens while sD is invisible to quorum. Promotion
  to D requires only a delta resync (seconds, not hours). See
  [DRBD_MODEL.md](DRBD_MODEL.md) §6.

- **q↑** (odd→even voters): adding a voter to odd voters makes them even.
  With even voters, the old (low) q allows a symmetric partition — split-brain.
  q must be raised atomically with the voter addition. The A or sD vestibule
  establishes connections while quorum is unaffected, then the conversion to
  D∅ + q↑ happens in a single configuration push.

- **sD + q↑ detach-before-promote**: when sD needs q↑, direct sD→D promotion
  cannot be used — publishing sD→D + q↑ in one revision is unsafe because a
  peer might apply the new voter before raising q, creating a window with even
  voters and old (low) q where a symmetric partition causes split-brain.
  Instead: sD detaches disk (sD→sD∅), converts to D∅ + q↑ — both are
  diskless, so this is a pure config update (voter status + q) that is safe
  in any application order. Then D∅→D re-attaches with a delta resync.

- **qmr↑**: quorum minimum redundancy raised after the new D reaches
  UpToDate. This is the last step — all replicas must confirm that enough
  data copies exist before the protection level increases.

### 2.2 RemoveReplica

| Transition | Path | When |
|------------|------|------|
| RemoveReplica(A) | A → ✕ | always |
| RemoveReplica(TB) | TB → ✕ | always |
| RemoveReplica(sD) | sD → sD∅ → ✕ | Flant DRBD |
| RemoveReplica(D) | D → D∅ → ✕ | odd→even voters |
| qmr↓ + RemoveReplica(D) | qmr↓ → D → D∅ → ✕ | odd→even voters, qmr above target |
| RemoveReplica(D) + q↓ | D → D∅ → A + q↓ → ✕ | even→odd voters |
| qmr↓ + RemoveReplica(D) + q↓ | qmr↓ → D → D∅ → A + q↓ → ✕ | even→odd voters, qmr above target |

**Key concepts:**

- **qmr↓ is always the first step**: the quorum constraint must be relaxed
  before removing the voter. Otherwise, removing the voter could cause the
  system to lose quorum (fewer UpToDate copies than qmr requires).

- **q↓ uses the A vestibule** (mirror of the add path): D detaches disk
  (D→D∅), then D∅ converts to A + q↓ in a single configuration push. This
  is safe regardless of application order — A is invisible to quorum, and the
  lower q is correct for the reduced voter count.

- **Backing volume fields** are cleared when transitioning from D∅ to A
  (the member no longer needs disk storage metadata).

**Attached member strategy**: when the controller wants to remove a member
that is currently attached (serving IO), it uses ChangeReplicaType instead of
RemoveReplica: D → sD (Flant DRBD) or D → A (without Flant DRBD). The
converted member stays in the datamesh and continues serving IO. See
preconditions (§9) for the NotAttached guard that enforces this.

### 2.3 ForceRemoveReplica

Used when a member's node has permanently failed. Removes the member from the
datamesh directly from its current state, without intermediate steps and
without waiting for the dead replica to confirm.

| Transition | Path | When |
|------------|------|------|
| ForceRemoveReplica(A) | A → ✕ | always |
| ForceRemoveReplica(TB) | TB → ✕ | always |
| ForceRemoveReplica(sD) | sD → ✕ | always |
| ForceRemoveReplica(sD∅) | sD∅ → ✕ | always |
| ForceRemoveReplica(D) | D → ✕ | odd→even voters |
| ForceRemoveReplica(D) + q↓ | D → ✕ + q↓ | even→odd voters |
| ForceRemoveReplica(D∅) | D∅ → ✕ | odd→even voters |
| ForceRemoveReplica(D∅) + q↓ | D∅ → ✕ + q↓ | even→odd voters |

**Key properties:**

- **Emergency group**: ForceRemoveReplica is always admitted by the
  parallelism tracker, regardless of other active transitions (see §11).

- **CancelActiveOnCreate**: any in-progress transition for the dead member
  is cancelled before the ForceRemoveReplica is created. This prevents
  conflicts with stuck transitions that will never complete.

- **No loseVoter guards**: the normal GMDR/FTT preservation guards are
  bypassed — the node is already lost, and the operation restores the
  configuration to match reality.

- **ForceDetach required first**: if the dead member is still marked as
  attached (serving IO), a ForceDetach (see §4) must be performed first to
  clear the attachment flag before removal.

- **Dead member excluded from confirmation**: after removal, the dead
  member is no longer in the member list. Confirmation waits only for
  surviving members to apply the updated configuration.

### 2.4 ChangeReplicaType

Changes a member's type in place, without removing and re-adding it. The
member retains its identity (ID, name, node) throughout. Paths depend on the
source and target types, voter parity, and ShadowDiskful availability.

**Non-voter transitions** (NonVotingMembership group):

| Transition | Path | When |
|------------|------|------|
| ChangeReplicaType(A, TB) | A → TB | always |
| ChangeReplicaType(TB, A) | TB → A | always |
| ChangeReplicaType(A, sD) | A → sD∅ → sD | Flant DRBD |
| ChangeReplicaType(sD, A) | sD → sD∅ → A | Flant DRBD |
| ChangeReplicaType(TB, sD) | TB → sD∅ → sD | Flant DRBD |
| ChangeReplicaType(sD, TB) | sD → sD∅ → TB | Flant DRBD |

**Voter promotion** (VotingMembership group — gaining voter status):

| Transition | Path | When |
|------------|------|------|
| ChangeReplicaType(A, D) | A → D∅ → D | even→odd voters |
| ChangeReplicaType(A, D) + q↑ | A → D∅ + q↑ → D | odd→even voters |
| ChangeReplicaType(A, D) via sD | A → sD∅ → sD → D | Flant DRBD, even→odd voters |
| ChangeReplicaType(A, D) via sD + q↑ | A → sD∅ → sD → sD∅ → D∅ + q↑ → D | Flant DRBD, odd→even voters |
| ChangeReplicaType(TB, D) | TB → D∅ → D | even→odd voters |
| ChangeReplicaType(TB, D) + q↑ | TB → D∅ + q↑ → D | odd→even voters |
| ChangeReplicaType(TB, D) via sD | TB → sD∅ → sD → D | Flant DRBD, even→odd voters |
| ChangeReplicaType(TB, D) via sD + q↑ | TB → sD∅ → sD → sD∅ → D∅ + q↑ → D | Flant DRBD, odd→even voters |
| ChangeReplicaType(sD, D) | sD → D | Flant DRBD, even→odd voters |
| ChangeReplicaType(sD, D) + q↑ | sD → sD∅ → D∅ + q↑ → D | Flant DRBD, odd→even voters |

**Voter demotion** (VotingMembership group — losing voter status):

| Transition | Path | When |
|------------|------|------|
| ChangeReplicaType(D, A) | D → D∅ → A | odd→even voters |
| ChangeReplicaType(D, A) + q↓ | D → D∅ → A + q↓ | even→odd voters |
| ChangeReplicaType(D, TB) | D → D∅ → TB | odd→even voters |
| ChangeReplicaType(D, TB) + q↓ | D → D∅ → TB + q↓ | even→odd voters |
| ChangeReplicaType(D, sD) | D → sD | Flant DRBD, odd→even voters |
| ChangeReplicaType(D, sD) + q↓ | D → D∅ → sD∅ + q↓ → sD | Flant DRBD, even→odd voters |

**Notes:**

- A→TB and TB→A are star-to-star role changes — peers update quorum role,
  no connection changes needed. Both are safe while the member is attached.
- Disk-gaining transitions (→ sD, → D via sD) follow the same bitmap
  ordering as AddReplica: liminal state first, then disk attach.
- Disk-losing transitions (sD →, D →) follow the reverse: disk detach
  first, then type change.
- sD→D (even→odd, hot promotion) is a direct non-voter→voter conversion
  via the `non-voting` flag change. No disk detach/reattach needed.
- D→sD + q↓ (even→odd) uses the same detach-before-demote pattern as the
  sD + q↑ paths: D→D∅→sD∅+q↓→sD.
- Liminal types heading toward their resolved type (D∅→D, sD∅→sD) are
  skipped by the dispatcher — they indicate a transition is already in
  progress. No new dispatch is created.

---

## 3. ChangeQuorum

A standalone transition that corrects `q` (quorum threshold) and/or `qmr`
(quorum minimum redundancy) when they deviate from the correct values for the
current datamesh state. See [README.md](README.md) §4 for the formulas.

### When ChangeQuorum fires

The correct values are:

```
q   = floor(voters / 2) + 1
qmr = config.GMDR + 1   (but raising is limited by UpToDate D count)
```

ChangeQuorum is dispatched when:

- q and/or qmr are incorrect, **AND** no matching membership transition will
  handle the correction (see below).
- The discrepancy is a **diagonal** case: q needs to go up but qmr needs to
  go down (or vice versa).
- The discrepancy is **larger than ±1** (corruption or multi-step
  reconfiguration).

### When ChangeQuorum defers

Normal layout-driven qmr changes are embedded within AddReplica(D) (qmr↑ as
the last step) or RemoveReplica(D) (qmr↓ as the first step). When a pending
D Join or Leave request exists and the qmr discrepancy is exactly ±1,
ChangeQuorum defers — the membership transition handles qmr as part of its
own steps. This avoids creating two separate transitions where one suffices.

### Plan variants

ChangeQuorum is always a 2-step transition (one step per scalar):

| Plan | Step 1 | Step 2 |
|------|--------|--------|
| **lower** | set correct qmr | set correct q |
| **raise** | set correct q | set correct qmr |
| **lower-q-raise-qmr** | set correct q | set correct qmr |
| **raise-q-lower-qmr** | set correct q | set correct qmr |

**Ordering rule**: the step that **lowers** protection always goes first.
This ensures the baseline GMDR is updated before the raising step, so
external consumers never see an intermediate state where the baseline exceeds
the actual protection level.

When only one of q or qmr needs to change, the other step is a no-op (sets
the same value, confirms immediately).

---

## 4. Attachment Transitions

Attachment makes a datamesh volume **Primary** on a node — the node can serve
IO through the DRBD block device. Attachment intent is expressed through
**ReplicatedVolumeAttachment (RVA)** objects: an active (non-deleting) RVA on
a node means "this node wants to attach."

### Transition types

| Type | Scope | Group | Steps | Description |
|------|-------|-------|-------|-------------|
| Attach | Replica | Attachment | 1 | Set member.Attached = true |
| Detach | Replica | Attachment | 1 | Set member.Attached = false |
| ForceDetach | Replica | Emergency | 1 | Emergency detach for dead member |
| EnableMultiattach | Global | Multiattach | 1 | Enable dual-Primary mode |
| DisableMultiattach | Global | Multiattach | 1 | Disable dual-Primary mode |

### Attach

Sets `member.Attached = true`. The DRBD agent on the node makes the volume
Primary.

**Trigger**: an active (non-deleting) RVA exists on a node that has a
datamesh member.

**Confirmation**: subject-only — waits for the subject replica to apply the
new DatameshRevision.

### Detach

Sets `member.Attached = false`. The DRBD agent makes the volume Secondary.

**Trigger**: a member has Attached = true but no active RVA exists on the
node (all RVAs deleted or deleting).

**Confirmation**: subject-only with leaving/gone semantics — accepts normal
confirmation (DatameshRevision >= stepRevision), OR the replica left the
datamesh (DatameshRevision == 0), OR the RVR is completely gone (node died
during detach). This permissive confirmation ensures Detach completes even
when the node fails mid-detach.

### ForceDetach

Emergency detach for a dead member. Clears Attached = false so that
ForceRemoveReplica can proceed (the NotAttached guard requires
Attached = false).

**Trigger**: explicit ForceDetach request.

**Confirmation**: immediate — empty confirmation sets. The step completes
within the same reconciliation cycle without waiting for any replica. The
node is dead; there is no one to confirm.

**CancelActiveOnCreate**: cancels any in-flight Attach or Detach transition
on the same slot. This prevents conflicts with a stuck Attach that will never
complete.

### EnableMultiattach

Sets `datamesh.multiattach = true`. Enables simultaneous Primary on multiple
nodes. Required before the second (and subsequent) Attach can proceed.

**Trigger**: more than one datamesh member has an active RVA (intended
attachments > 1) AND `maxAttachments > 1` AND multiattach is currently
disabled AND no EnableMultiattach is already in progress.

**Confirmation**: all members with a backing volume (D, sD) or with
Attached = true must confirm. This ensures all data-bearing and
currently-attached members apply the multiattach flag before a second Primary
is allowed.

### DisableMultiattach

Sets `datamesh.multiattach = false`. Returns to single-Primary mode.

**Trigger**: multiattach is not needed (intended attachments ≤ 1 OR
maxAttachments ≤ 1) AND multiattach is currently enabled AND at most one
member is potentially attached (safe to toggle — no concurrent Primary
risk) AND no DisableMultiattach is already in progress.

**Confirmation**: same as EnableMultiattach (D + sD + Attached members).

---

## 5. Attachment Mechanics

### Slot management

`maxAttachments` (from the volume spec, default 1) limits how many nodes can
be attached simultaneously. The controller tracks **potentiallyAttached** — an
ID set of members that are or may still be Primary:

| Event | Action |
|-------|--------|
| Initialization | Add all members with Attached = true |
| Attachment transition created | Add replica ID (attaching or detaching — still possibly Primary) |
| Detach transition completed | Remove replica ID (detach confirmed — no longer Primary) |
| Attach transition completed | Keep replica ID (attach confirmed — settled as attached) |

The **SlotAvailable** guard blocks Attach when
`potentiallyAttached count >= maxAttachments`. The proposed replica is
excluded from the count: a Detach of the only attached member must not be
blocked by its own presence in the set.

### Multiattach readiness

For a second (or subsequent) Attach to proceed, **both** conditions must hold:

1. `multiattach = true` — the flag is set by EnableMultiattach.
2. No active Multiattach toggle — the Enable/Disable transition is confirmed.

If either condition fails, the Attach is blocked with "Waiting for multiattach
to be enabled."

### maxAttachments decrease

When `maxAttachments` decreases below the current number of attached nodes,
the controller does **not** force-detach any member. All currently-attached
members remain attached. New Attach attempts are blocked by SlotAvailable
until enough members detach naturally (via RVA deletion).

### FIFO ordering

When multiple nodes request attachment and only one slot is available:

- Candidates are sorted by the **earliest active (non-deleting) RVA**
  creation timestamp on each node. Earlier RVA → processed first → gets the
  slot.
- Detach-only candidates (no active RVA) get a zero timestamp and sort
  **first** — detach decisions are processed before attach decisions. This
  ensures slots are freed before new attachments are attempted.
- Tie-break: alphabetical node name.
- An already-attached member keeps its slot — FIFO ordering cannot preempt a
  settled attachment.

### VolumeAccess modes

VolumeAccess controls which member types can be attached (Local restricts to
members with a backing volume). For the full modes table, see
[README.md](README.md) §9.

### Typical sequences

**Single-attach: switch from node-A to node-B.**

1. RVA for node-A deleted, RVA for node-B created.
2. FIFO: Detach(node-A) sorts first (zero timestamp). Detach created.
3. Attach(node-B) blocked by SlotAvailable (potentiallyAttached includes
   node-A until Detach confirms).
4. Node-A confirms Detach → potentiallyAttached clears node-A → slot free.
5. Next cycle: Attach(node-B) proceeds.

**Multi-attach: attach two nodes.**

1. Two RVAs created (node-A and node-B). maxAttachments=2.
2. EnableMultiattach dispatched (intendedCount=2 > 1). Attach(node-A)
   proceeds (first slot, single-attach still OK).
3. Attach(node-B) blocked by tracker (multiattach toggle in progress).
4. All replicas confirm → Attach(node-A) completes, EnableMultiattach
   completes.
5. Next cycle: Attach(node-B) proceeds (multiattach confirmed, slot
   available).

**ForceDetach + ForceRemove for dead node.**

1. Node dies. RVR removed. Member still has Attached=true.
2. ForceDetach request dispatched (before normal attachment decisions).
3. CancelActiveOnCreate cancels any stuck Attach/Detach on the slot.
4. ForceDetach applies (Attached→false), confirms immediately.
5. ForceDetach completes within the same reconciliation cycle.
6. ForceRemoveReplica now proceeds (NotAttached guard passes).

---

## 6. Network Transitions

The Network transition group handles changes to member network configuration
(addresses, system networks). Unlike membership transitions that add/remove
members, network transitions repair or reconfigure the communication layer
between existing members.

All Network transitions are serialized (at most one active at a time). Address
repair takes priority over system network changes.

### 6.1 RepairNetworkAddresses

**Problem**: A member's DRBD addresses (IP, port) are recorded in the
datamesh and kept in sync with the actual addresses reported by the replica
agent, filtered by the datamesh system network list. If a node's address
changes after joining (e.g., IP reassignment, port change), or if a member is
missing an address for a datamesh network, the member record becomes stale.
Peers continue using the old address, causing connectivity failures.

When this happens, DRBD is already listening on the new IP/port — connections
on the old address are already broken and IO may already be frozen. The
controller acts aggressively to restore connectivity as fast as possible.

**Detection**: three types of mismatch trigger dispatch:

- **Missing**: a datamesh network address exists in the replica status but not
  in the member record.
- **Wrong IP/port**: both have the address, but the member has a different
  value than the replica.
- **Stale**: the member has an address for a network that is no longer in the
  datamesh system network list.

**Plan**: `repair/v1` — single step.

- **Apply**: for each member, build the target address list from the replica
  status filtered by datamesh system networks, then replace the member's
  addresses if different.
- **Confirm**: all members must confirm the new DatameshRevision **AND** all
  expected peer connections must be verified (at least one side with a ready
  agent reports the peer as Connected).

**Guard**: ReplicasMatchTargetNetworks — blocks repair if any replica's
actual addresses are not yet synchronized with the target system network list.
This ensures a stable network set before repair proceeds.

### 6.2 ChangeSystemNetworks

**Problem**: Each member can participate in multiple system networks
simultaneously — DRBD establishes independent connections on each. The set of
system networks is declared in the datamesh configuration and originates from
the ReplicatedStoragePool (RSP). When the RSP configuration changes, the
datamesh must converge to the new set.

**Detection**: the controller compares the current datamesh system network
list with the RSP target. The difference determines the plan:

```
added     = target − current     (networks to add)
removed   = current − target     (networks to remove)
remaining = current ∩ target     (networks that stay)
```

| added | removed | remaining | Plan |
|-------|---------|-----------|------|
| yes | yes | none | `migrate/v1` (full replacement, no connectivity guard) |
| yes | yes | some | `update/v1` (guarded: remaining must have connectivity) |
| yes | no | — | `add/v1` (no connectivity guard needed) |
| no | yes | — | `remove/v1` (guarded: remaining must have connectivity) |

The from/to network lists are captured at creation time and frozen on the
transition. Even if the RSP changes again mid-transition, the transition
completes to the captured target.

**Dispatcher priority**: when both RepairNetworkAddresses and
ChangeSystemNetworks are needed (e.g., an IP changed AND a new network
appeared), repair is dispatched first. ChangeSystemNetworks dispatches only
after repair completes (Network group is serialized).

#### add/v1 — Add system networks

**Step 1 (Listen)**: add new networks to the datamesh system network list.
Confirm: all replicas report addresses on the new networks.

**Step 2 (Connect)**: copy new network addresses from replica status to
member records. Confirm: all expected peer connections verified on the new
networks.

#### remove/v1 — Remove system networks

**Guard**: RemainingNetworksConnected — at least one remaining network
(current ∩ target) has full connectivity for all members.

**Step 1 (Disconnect)**: remove old networks from the datamesh system network
list and remove corresponding addresses from all members. Confirm: all
members confirm the new DatameshRevision.

#### update/v1 — Add and remove simultaneously

Used when some networks are added and some removed, but remaining networks
exist and can carry traffic during the transition.

**Guards**: RemainingNetworksConnected + NodesHaveAddedNetworks.

**Step 1 (Listen new + Disconnect old)**: remove old networks and addresses
from members, add new networks to the system network list. Confirm: all
replicas report addresses on the new networks.

**Step 2 (Connect)**: copy new network addresses to members. Confirm:
connectivity on new networks.

#### migrate/v1 — Full replacement

Used when no remaining networks exist (the intersection is empty). The old
network may already be broken. No connectivity guard is possible.

**Step 1 (Listen new)**: add new networks to the system network list.
Confirm: all replicas report addresses on the new networks.

**Step 2 (Connect new)**: copy new network addresses to members. Confirm:
connectivity on new networks.

**Step 3 (Disconnect old)**: remove old networks and addresses. Confirm: all
members confirm the DatameshRevision.

---

## 7. Resize Transitions

The Resize transition group handles volume growth when `rv.Spec.Size`
increases beyond the current `datamesh.Size`. Only growing is supported —
shrinking is not.

### ResizeVolume

| Property | Value |
|----------|-------|
| Type | ResizeVolume |
| Scope | Global |
| Group | Resize |
| Plan | `resize/v1` |
| Steps | 1 |
| DisplayName | "Resizing volume" |

**Trigger**: `datamesh.Size < rv.Spec.Size`.

**Step 1 (resize)**: sets `datamesh.Size = rv.Spec.Size`. Confirmation:
all datamesh members must confirm the new DatameshRevision.

**How it works end-to-end**: the rvr\_controller eagerly grows LVM logical
volumes before the transition starts (via `max(rv.Spec.Size, datamesh.Size)`
in `computeIntendedBackingVolume`). Once all backing volumes have grown, the
transition dispatches and sets the new datamesh size. Each rvr\_controller
then patches the corresponding DRBDResource `spec.size` to the new value.
The agent runs `drbdsetup resize`, and the rvr\_controller gates
`datameshRevision` on `drbdr.Status.Size >= drbdr.Spec.Size` (step 13c).
The standard revision-based confirm thus implicitly waits for the DRBD
resize to complete on every member.

**Preconditions**: see §10.

**Parallelism**: serialized (one at a time). Mutually exclusive with
disk-gaining membership transitions (AddReplica D/sD, ChangeReplicaType
to D/sD). See §11.

---

## 8. Step Confirmation Rules

Each multi-step transition publishes a new DatameshRevision at each step and
waits for confirmation from the affected replicas. The confirmation scope
depends on what the step changes. See
[DRBD_MODEL.md](DRBD_MODEL.md) §9 for how configuration propagation works.

**Notation**: FM = full-mesh members (D, D∅, sD, sD∅). "Subject" = the
member being transitioned.

### Subject-only confirmation

The step changes only the subject's local disk state. Peers detect the change
automatically via DRBD protocol — no configuration update needed on peers.

| Step | Why |
|------|-----|
| D∅ → D | Local disk attach; resync starts automatically |
| D → D∅ | Local disk detach; peers detect via DRBD |
| sD∅ → sD | Local disk attach |
| sD → sD∅ | Local disk detach |
| Attach | Subject becomes Primary |

### FM + subject confirmation

The step adds or removes a star member. Only full-mesh members have
connections to star members, so only they need to update. The subject itself
must also confirm (it configures its own connections).

| Step | Why |
|------|-----|
| ✦ → A | Star member added; FM peers configure new connection |
| ✦ → TB | Star member added; FM peers configure new connection |
| A → ✕ | Star member removed; FM peers remove connection |
| TB → ✕ | Star member removed; FM peers remove connection |
| A → TB | Star role change; FM peers update quorum role |
| TB → A | Star role change; FM peers update quorum role |

For removal steps (→ ✕), the leaving member confirms by resetting its
DatameshRevision to 0 (left the datamesh).

### All-members confirmation

The step changes full-mesh topology, voter status, or quorum parameters. All
members have connections to full-mesh members and are affected by voter/quorum
changes.

| Step | Why |
|------|-----|
| ✦ → D∅ | Full-mesh member added; all peers configure new connections |
| ✦ → sD∅ | Full-mesh member added; all peers configure new connections |
| D∅ → ✕ | Full-mesh member removed; all remaining peers remove connections |
| sD∅ → ✕ | Full-mesh member removed |
| sD → ✕ | Full-mesh member removed (force) |
| D → ✕ | Full-mesh member removed (force); dead member excluded |
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
| qmr↑ | All replicas use qmr in their quorum path |
| qmr↓ | All replicas use qmr in their quorum path |

For removal steps, the removed member is included in the confirmation set and
confirms by resetting DatameshRevision to 0. For force-removal, the dead
member is excluded (it cannot confirm).

**q change within a step**: some steps include an atomic q change (q↑ or q↓).
The confirmation scope is the union of the step's own scope and the q-change
scope (all D + D∅). In practice, all q-changing steps are already in the
"all members" group, so the union adds no extra replicas.

### Immediate confirmation

Used by ForceDetach — the node is dead, there is no one to wait for. Empty
confirmation sets; the step completes immediately.

### Network-specific confirmation

- **Revision + connectivity** (RepairNetworkAddresses): all members must
  confirm AND all expected peer connections must be verified via
  at-least-one-side-reports-Connected.
- **Addresses reported** (ChangeSystemNetworks Listen steps): all members must
  confirm AND each member's replica status must contain addresses for every
  added network.
- **Network connectivity** (ChangeSystemNetworks Connect steps): all members
  must confirm AND all expected connections must be verified on each added
  network specifically.

---

## 9. Preconditions: Membership

Guards are evaluated in order before a transition is created. The first
blocking guard stops the pipeline. For GMDR/FTT formulas referenced below,
see [README.md](README.md) §4. For per-layout quorum verification, see
[LAYOUTS_ANALYSIS.md](LAYOUTS_ANALYSIS.md).

> **Message composition**: the "Blocked message" column below shows the guard
> **reason** only. The full message seen by the operator is composed by the
> engine: `"{plan name} is blocked: {reason}"` — e.g.,
> `"Removing diskful replica is blocked: would violate GMDR (ADR=1, need > 1)"`.

### Preconditions for adding any member

Any AddReplica transition is blocked until **all** of the following pass:

| # | Guard | Condition | Blocked message |
|---|-------|-----------|-----------------|
| 1 | **RVNotDeleting** | RV does not have a DeletionTimestamp | "volume is being deleted" |
| 2 | **RSPAvailable** | ReplicatedStoragePool is available | "waiting for ReplicatedStoragePool to be available" |
| 3 | **NodeEligible** | Node is in the RSP eligible nodes | "node is not in eligible nodes" |
| 4 | **NodeHasAllSystemNetworks** | Node has all system networks from the datamesh configuration | *(not yet enforced — requires per-node network data in RSP)* |
| 5 | **NoMemberOnSameNode** | No existing datamesh member on the same node | "member already present on node" |
| 6 | **AddressesPopulated** | Replica has at least one address reported | "waiting for replica addresses to be populated" |

### Preconditions for adding D (gaining voter)

Any transition that adds a voter is additionally blocked by topology guards:

| # | Guard | Condition | Blocked message |
|---|-------|-----------|-----------------|
| 1 | **ZonalSameZone** | Topology is not Zonal, OR the replica's zone is the primary zone (zone with the most voters) | "zone is not the primary zone for Zonal topology" |
| 2 | **TransZonalVoterPlacement** | Topology is not TransZonal, OR after adding the voter, losing any single zone still satisfies FTT and GMDR | "adding to zone would violate zone FTT/GMDR" |

Non-topology safety is inherent: more copies and more voters can only improve
GMDR/FTT, never degrade them. Split-brain is prevented by the transitions
themselves (vestibule + atomic q↑ for odd→even voters).

### Preconditions for adding TB (gaining TieBreaker)

| # | Guard | Condition | Blocked message |
|---|-------|-----------|-----------------|
| 1 | **ZonalSameZone** | Topology is not Zonal, OR the replica's zone is the primary zone | "zone is not the primary zone for Zonal topology" |
| 2 | **TransZonalTBPlacement** | Topology is not TransZonal, OR the target zone has at most 1 D voter | "zone already has N Diskful voters (TB zone must have at most 1)" |

### Preconditions for removing any member

Any RemoveReplica transition is blocked until:

| # | Guard | Condition | Blocked message |
|---|-------|-----------|-----------------|
| 1 | **NotAttached** | Member is not attached AND no Attach/Detach transition is in progress for it | "replica is attached, detach required first" / "attach/detach transition in progress" |

### Preconditions for leaving D (losing voter)

Any transition that removes a voter (RemoveReplica(D), ChangeReplicaType(D→...))
is additionally blocked until **all** of the following pass:

| # | Guard | Condition | Blocked message |
|---|-------|-----------|-----------------|
| 1 | **VolumeAccessLocalForDemotion** | Member is not attached with volumeAccess=Local | "volumeAccess=Local requires Diskful on attached node" |
| 2 | **GMDRPreserved** | ADR > target GMDR (ADR = UpToDate D count − 1) | "would violate GMDR (ADR=N, need > M)" |
| 3 | **FTTPreserved** | D count > D_min (D_min = target FTT + target GMDR + 1) | "would violate FTT (Diskful=N, need > M)" |
| 4 | **ZoneGMDRPreserved** | Topology is not TransZonal, OR losing any zone after removal still leaves enough UpToDate D for GMDR | "would violate zone GMDR" |
| 5 | **ZoneFTTPreserved** | Topology is not TransZonal, OR losing any zone after removal still leaves enough D for quorum | "would violate zone FTT" |

**ZoneGMDRPreserved pseudocode:**

```
for each zone z:
    UpToDate_in_zone = UpToDate_D_in_zone(z) − (1 if z == removed_D_zone)
    UpToDate_surviving = (UpToDate_D_count − 1) − UpToDate_in_zone
    if UpToDate_surviving <= target_GMDR → blocked
```

**ZoneFTTPreserved pseudocode:**

```
q_after = floor((D_count − 1) / 2) + 1

for each zone z:
    D_lost = D_in_zone(z) − (1 if z == removed_D_zone)
    D_surviving = (D_count − 1) − D_lost
    TB_surviving = TB_count − TB_in_zone(z)

    if D_surviving >= q_after → ok
    elif D_surviving == q_after − 1 and TB_surviving > 0 → ok (tiebreaker)
    else → blocked
```

**When guards fail**: the transition waits. Typical resolution:
- GMDR: wait for replacement D to reach UpToDate.
- FTT: wait for replacement D to be added.
- Zone guards: wait for same-zone replacement or zone redistribution.

### Preconditions for leaving TB

| # | Guard | Condition | Blocked message |
|---|-------|-----------|-----------------|
| 1 | **TBSufficient** | TB count > TB_min (TB_min = 1 when D is even and target FTT = D/2, else 0) | "TieBreaker required for quorum (Diskful=N even, FTT=M)" |
| 2 | **ZoneTBSufficient** | Topology is not TransZonal, OR after removal, each zone that needs TB coverage still has it | "would violate zone TB coverage" |

### Preconditions for ForceRemoveReplica

| # | Guard | Condition | Blocked message |
|---|-------|-----------|-----------------|
| 1 | **NotAttached** | Member is not attached and no Attach/Detach in progress | "replica is attached, detach required first" |
| 2 | **MemberUnreachable** | No replica with a ready agent reports a Connected DRBD connection to this member | "member is reachable (connected from N replica(s): [...])" |

**MemberUnreachable detail**: a replica has a ready agent if its DRBD status
is current (not stale). The guard iterates all replicas with ready agents and
checks if any of them report a Connected connection to the target member. If
so, the member is still reachable and should not be force-removed — use
normal RemoveReplica instead.

---

## 10. Preconditions: Attachment, Network, and Resize

### Preconditions for Attach

| # | Guard | Condition | Blocked message |
|---|-------|-----------|-----------------|
| 1 | **NotDeleting** | RV does not have a DeletionTimestamp | "volume is being deleted" |
| 2 | **NodeOperational** | RSP available, node in eligible nodes, NodeReady, AgentReady | "node not eligible" / "node is not ready" / "agent is not ready" |
| 3 | **RVRReady** | RVR exists and has Ready=True condition | "waiting for replica to become Ready" |
| 4 | **MemberExists** | Datamesh member exists on this node | "waiting for replica to join datamesh" |
| 5 | **NoActiveMembership** | No membership transition in progress for this replica | "waiting for membership transition to complete" |
| 6 | **QuorumSatisfied** | At least one voter member has quorum | "quorum not satisfied: ..." |
| 7 | **SlotAvailable** | potentiallyAttached count < maxAttachments | "waiting for attachment slot (N/M occupied)" |
| 8 | **VolumeAccessLocal** | volumeAccess is not Local, OR the member has a backing volume | "no Diskful replica on this node (volumeAccess is Local)" |

### Preconditions for Detach

| # | Guard | Condition | Blocked message |
|---|-------|-----------|-----------------|
| 1 | **DeviceNotInUse** | Block device is not currently in use | "device is in use" |

### Preconditions for ForceDetach

| # | Guard | Condition | Blocked message |
|---|-------|-----------|-----------------|
| 1 | **MemberUnreachable** | No replica with a ready agent reports a Connected connection to this member | "member is reachable (connected from N replica(s): [...])" |

### Preconditions for EnableMultiattach

| # | Guard | Condition | Blocked message |
|---|-------|-----------|-----------------|
| 1 | **MaxAttachmentsAllows** | maxAttachments > 1 | "maxAttachments is 1" |

### Preconditions for DisableMultiattach

| # | Guard | Condition | Blocked message |
|---|-------|-----------|-----------------|
| 1 | **CanDisable** | potentiallyAttached count ≤ 1 | "N nodes potentially attached" |

### Preconditions for RepairNetworkAddresses

| # | Guard | Condition | Blocked message |
|---|-------|-----------|-----------------|
| 1 | **ReplicasMatchTargetNetworks** | All replicas' actual addresses match the datamesh target network set (no missing, no extra) | "replica name: system networks out of sync (missing/extra: [...])" |

### Preconditions for ChangeSystemNetworks

Guards depend on the plan variant:

| Plan | Guard | Condition |
|------|-------|-----------|
| add/v1 | **NodesHaveAddedNetworks** | Every added network is available on every node with a replica *(not yet enforced)* |
| remove/v1 | **RemainingNetworksConnected** | At least one remaining network has full connectivity for all members |
| update/v1 | **RemainingNetworksConnected** + **NodesHaveAddedNetworks** | Both conditions |
| migrate/v1 | **NodesHaveAddedNetworks** | Every added network available on every node *(not yet enforced)* |

### Preconditions for ResizeVolume

| # | Guard | Condition | Blocked message |
|---|-------|-----------|-----------------|
| 1 | **HasReadyDiskfulMember** | At least one diskful member has Ready=True | "no ready diskful member" |
| 2 | **NoActiveResync** | No diskful member has a peer in a syncing replication state | "replica NAME has peer PEER in state STATE" |
| 3 | **BackingVolumesGrown** | All diskful members' backing volume size >= target lower size | "backing volume on replica NAME not grown yet (SIZE, need >= TARGET)" |

---

## 11. Transition Parallelism

Transitions are grouped by what they affect. Parallelism rules determine which
groups can run concurrently and which are mutually exclusive.

### Transition groups

| Group | Scope | Transitions |
|-------|-------|-------------|
| **Formation** | Initial replica creation | Managed by rv_controller (not the datamesh transition engine) |
| **VotingMembership** | Voter changes | AddReplica(D), RemoveReplica(D), ChangeReplicaType(D↔other), ForceRemoveReplica(D/D∅) |
| **NonVotingMembership** | Non-voter changes | AddReplica(A/TB/sD), RemoveReplica(A/TB/sD), ChangeReplicaType(A↔TB, A↔sD, TB↔sD) |
| **Quorum** | q/qmr adjustment | ChangeQuorum |
| **Attachment** | Primary/Secondary | Attach, Detach |
| **Multiattach** | Dual-Primary toggle | EnableMultiattach, DisableMultiattach |
| **Network** | Address/network repair | RepairNetworkAddresses, ChangeSystemNetworks |
| **Resize** | Volume size changes | ResizeVolume |
| **Emergency** | Dead member recovery | ForceRemoveReplica, ForceDetach |

### Per-group rules

- **VotingMembership**: serialized — at most one active at a time. Mutually
  exclusive with Quorum (voter set must be stable for quorum computation) and
  Network (addresses must be stable before voter changes).

- **NonVotingMembership**: can run in parallel with everything — including
  VotingMembership on other members and other NonVotingMembership transitions.
  Subject to the one-membership-per-replica limit (see below).

- **Quorum**: serialized — at most one ChangeQuorum at a time. Mutually
  exclusive with VotingMembership (voter count is changing — correct q is not
  yet determined) and Network (addresses must be stable before quorum
  changes).

- **Attachment**: independent from membership and quorum. Multiple
  Attach/Detach transitions can run concurrently (one per replica). Subject to
  multiattach readiness for the second+ Attach.

- **Multiattach**: serialized (no double-toggle). Independent from membership,
  quorum, and network.

- **Network**: serialized — at most one Network transition at a time. **Blocks
  VotingMembership and Quorum** (addresses must be stable before voter/quorum
  changes). **Not blocked by VotingMembership or Quorum** — this is
  asymmetric (semi-emergency): address repair is urgent because connectivity
  may already be degraded. The repair is safe alongside in-progress
  voter/quorum transitions because it only mutates addresses, not topology or
  quorum values.

- **Resize**: serialized — at most one Resize at a time. Mutually exclusive
  with **disk-gaining** membership transitions: AddReplica(D/sD) and
  ChangeReplicaType(→D/→sD). Both resize and disk-gaining transitions lead
  to resync; running them concurrently would be unsafe. Non-disk-gaining
  membership (AddReplica A/TB, RemoveReplica, ChangeType away from disk),
  Quorum, Attachment, Multiattach, and Network are independent.

- **Formation**: blocks everything except Network and Emergency. During
  initial volume creation, no membership, quorum, or attachment transitions
  are allowed.

- **Emergency**: always admitted by the parallelism tracker, regardless of
  what else is active. Uses CancelActiveOnCreate to cancel conflicting
  transitions for the affected replica rather than blocking.

### Per-member rules

- At most **one membership** transition per replica (VotingMembership or
  NonVotingMembership — the per-replica limit applies across both groups).
- At most **one attachment** transition per replica.
- Emergency transitions cancel existing per-member transitions before creating.

### Emergency preemption

ForceRemoveReplica can run alongside in-progress VotingMembership or Quorum
transitions for other members. This is necessary because those transitions may
be stuck waiting for the dead member to confirm.

**Transient q inconsistency**: when ForceRemoveReplica(D) runs alongside an
in-progress VotingMembership that raised q, the new DatameshRevision publishes
the correct q for the updated voter count. During the brief window while
replicas converge, some may still have the old (higher) q. This is safe: a
higher q is strictly more conservative — it may suspend IO unnecessarily but
cannot cause split-brain. Normal VotingMembership transitions avoid this via
serialization; Emergency operations accept the brief inconsistency for faster
recovery.

---

*For internal architecture, data structures, and the datamesh/dmte interface,
see [INTERNALS.md](INTERNALS.md). For behavioral test cases, see
[TEST_CASES.md](TEST_CASES.md).*
