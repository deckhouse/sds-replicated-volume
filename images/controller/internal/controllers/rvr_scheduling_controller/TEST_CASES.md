# Key scheduling test cases

This document lists the key behavioral cases that must be reviewed and used as the basis for generating tests.
Cases are grouped by Context blocks.
Non-obvious / non-trivial cases are marked with ⚡.

The required Diskful count is D = FTT + GMDR + 1. TB = 1 if D is even and FTT = D/2, else 0.

---

## Zonal Topology

1. **small-1z: D:2 — all in zone-a.**
   Cluster with 1 zone (2 nodes). 2 Diskful replicas are scheduled into the only zone, zone-a.

2. **small-1z: attachTo node-a1 — D on node-a1, TB on node-a2.**
   1 Diskful + 1 TieBreaker. attachTo points to node-a1 → D goes there, TB goes to the remaining node-a2.

3. **medium-2z: attachTo both nodes of the same zone — all D in zone-a.**
   Cluster with 2 zones. attachTo=[node-a1, node-a2] (both in zone-a) → 2D are scheduled in zone-a, zone-b is not used.

4. ⚡ **medium-2z-4n: existing D in zone-a — new D and TB also go to zone-a.**
   There is already a Diskful on node-a1. New D and TB must go to the same zone (sticky behavior).

5. ⚡ **medium-2z: existing D in different zones — inconsistent state logged, scheduling proceeds.**
   Existing Diskful replicas in zone-a and zone-b. For Zonal topology this is an inconsistent state: the reconciler logs an Info message listing the preferred zones (both zone-a and zone-b are tied) and proceeds with scheduling — no zone filter is applied, the replica is placed by score.

6. **small-1z: all nodes occupied — no candidates for TB.**
   2 existing Diskful occupy both nodes. TieBreaker receives `Scheduled=False` / `SchedulingFailed`.

7. **medium-2z: existing D in zone-a — TB also goes to zone-a.**
   Existing Diskful on node-a1 locks the zone. TieBreaker is scheduled in zone-a on a free node.

---

## TransZonal Topology

1. **large-3z: D:3 — one per zone.**
   3 zones, 3 Diskful → each zone gets exactly one replica.

2. **large-3z: existing D in zone-a,b — new D goes to zone-c.**
   Replicas already exist in zone-a and zone-b. The new Diskful goes to the least-loaded zone-c.

3. **large-3z: existing D in zone-a,b — TB goes to zone-c.**
   TransZonal balancing works for TieBreaker too: TB goes to the zone with the fewest replicas.

4. **medium-2z: existing D in zone-a — new D goes to zone-b.**
   2 zones, existing in zone-a → new one goes to zone-b for balancing.

5. ⚡ **xlarge-4z: D:3 — Diskful only in RSC zones.**
   Cluster has 4 zones (a,b,c,d), but RSC restricts to 3 (a,b,c). All 3D are scheduled only in RSC zones; zone-d is not used.

6. **medium-2z: all nodes occupied — TB is not scheduled.**
   4 existing Diskful fill all 4 nodes across 2 zones. TieBreaker receives `Scheduled=False` / `SchedulingFailed`.

7. ⚡ **FTT=2,GMDR=2: 5D across 3 zones — composite 2+2+1.**
   D=5 in 3 zones (2 nodes per zone). Round-robin distributes: all 3 zones used, max 2 replicas per zone.

8. ⚡ **FTT=1,GMDR=2: 4D across 3 zones — composite 2+1+1.**
   D=4 in 3 zones (2 nodes per zone). Round-robin distributes: all 3 zones used, max 2 replicas per zone.

9. ⚡ **FTT=1,GMDR=2: 4D+1TB across 3 zones — TB avoids zone with 2D.**
   D=4 pre-placed as 2+1+1. TB uses "fewest total replicas" → goes to a zone with 1D (not the zone with 2D).

10. ⚡ **FTT=2,GMDR=1: 4D across 4 zones — pure 1+1+1+1.**
    D=4 in 4 zones (1 node per zone). Each zone gets exactly one replica.

11. ⚡ **FTT=2,GMDR=2: 5D across 3 zones — existing 2+2, new D goes to empty zone.**
    4D pre-placed as 2+2+0 (zone-a: 2, zone-b: 2, zone-c: 0). New D goes to zone-c.

---

## Ignored Topology

1. **large-3z: D:2 — Diskful by best scores.**
   No zone constraints: 2 Diskful are scheduled on the nodes with the highest scores (node-a1=100, node-b1=90), regardless of zones.

2. **medium-2z: attachTo — prefer specified nodes.**
   attachTo=[node-a1, node-b1]. Both Diskful are scheduled exactly on those nodes.

3. **small-1z-4n: D:2, TB:2 — 4 replicas on 4 nodes.**
   All 4 replicas (2 Diskful + 2 TieBreaker) are successfully scheduled, each on a separate node.

4. **small-1z: all nodes occupied — TB is not scheduled.**
   2 existing Diskful on both nodes. TieBreaker cannot be scheduled — `SchedulingFailed`.

---

## Extender Filtering

1. ⚡ **Extender error, single Diskful → `Scheduled=False` / `ExtenderUnavailable`, Reconcile returns error.**
   1 Diskful, no TieBreaker. Mock extender returns an error. The Diskful receives `Scheduled=False` / `ExtenderUnavailable`. Reconcile returns an error (triggers retry).

2. ⚡ **Extender error, multiple Diskful → all attempted, then error.**
   3 Diskful, no TieBreaker. Extender fails on the first Diskful. The reconciler continues to attempt the remaining Diskful (each also fails with `ExtenderUnavailable`). All 3 receive `Scheduled=False` / `ExtenderUnavailable`. Reconcile returns an error after attempting all Diskful.

3. ⚡ **Extender error, Diskful + TieBreaker → Diskful fails, TieBreaker NOT attempted.**
   1 Diskful + 1 TieBreaker. Extender fails on the Diskful. Diskful receives `Scheduled=False` / `ExtenderUnavailable`. Reconcile returns an error before reaching Phase 3 (TieBreaker scheduling). TieBreaker is not scheduled in this run.

4. ⚡ **TieBreaker only, extender unavailable → TieBreaker scheduled normally.**
   No unscheduled Diskful, 1 TieBreaker. Extender is unavailable but irrelevant — TieBreaker does not use the extender. TieBreaker is scheduled successfully. Reconcile returns without error.

5. **Extender returns no scores for zone-b → nodes filtered out.**
   Mock extender returns scores only for LVGs in zone-a. The replica is scheduled strictly on node-a1 or node-a2 (zone-a); zone-b nodes are unavailable.

---

## Partial Diskful Scheduling

1. ⚡ **3 Diskful on 2 nodes → 2 scheduled, 1 `Scheduled=False`.**
   Cluster small-1z (2 nodes). 3 Diskful RVRs. The reconciler schedules 2 (greedy), the third receives `Scheduled=False` / `SchedulingFailed`. The scheduled ones get `Scheduled=True`.

---

## Deleting Replica Node Occupancy

1. ⚡ **Deleting replica (with DeletionTimestamp) blocks its node.**
   RVR with `DeletionTimestamp` + `NodeName=node-a1` + finalizer. The new replica rvr-new must be scheduled on node-a2, not node-a1 (even though node-a1 has a higher score).

---

## RVR with DeletionTimestamp

1. ⚡ **Deleting unscheduled RVR is skipped.**
   RVR with `DeletionTimestamp` but no `NodeName`. The reconciler does not attempt to schedule it; `NodeName` remains empty.

---

## Scheduled Condition Management

1. ⚡ **`Scheduled=True` is set on already-existing replicas too.**
   Existing RVR with `NodeName=node-a1` + `LVMVolumeGroupName=vg-node-a1` (fully scheduled) + a new RVR without NodeName. After Reconcile, both receive the condition `Scheduled=True` / `Scheduled`.

---

## Constraint Violation Conditions

1. ⚡ **TransZonal: eligible nodes only from zone-a, but topology=TransZonal.**
   RSC specifies 2 zones, but eligible nodes exist only in zone-a. 2 Diskful: the first is scheduled in zone-a, the second receives `Scheduled=False` / `SchedulingFailed` (no node in the second zone).

---

## Multi-LVG Node Bonus

When `volumeAccess != Any`, nodes with more than one LVG receive a +2 score
bonus. This improves resilience: if a disk fails, a second LVG on the same
node allows local migration without violating volumeAccess constraints.

Selection within a node always picks the LVG with the highest extender score.

1. ⚡ **Equal extender score → multi-LVG node wins by bonus.**
   volumeAccess=Local. Node-1: 2 LVGs (lvg-1a=9, lvg-1b=5). Node-2: 1 LVG (lvg-2a=9). Node-1 entries get +2 bonus → lvg-1a has score 11 vs lvg-2a score 9. Node-1 wins; lvg-1a is selected.

2. **Higher extender score beats multi-LVG bonus.**
   volumeAccess=Local. Node-1: 2 LVGs (best=5, +2 bonus → 7). Node-2: 1 LVG (score=10). Node-2 wins by score (10 > 7) despite fewer LVGs.

3. ⚡ **volumeAccess=Any → no multi-LVG bonus applied.**
   volumeAccess=Any. Node-1: 2 LVGs (lvg-1a=9, lvg-1b=5). Node-2: 1 LVG (lvg-2a=9). No bonus → tie at score 9. Tiebreak by alphabetical NodeName.

4. **Best LVG is selected on the winning node.**
   1 node with 3 LVGs (scores 10, 15, 5). All get +2 bonus (12, 17, 7). lvg-1b (score=17) is selected — the highest on the node.

---

## RSP Not Found

1. ⚡ **RSP does not exist → `WaitingForReplicatedVolume` condition on all RVRs.**
   Configuration points to a nonexistent RSP (`rsp-nonexistent`). Guard #3 fires: all RVRs receive `Scheduled=Unknown` / `WaitingForReplicatedVolume` with a message naming the missing RSP. Reconcile returns without error.

2. ⚡ **Empty ReplicatedStoragePoolName → `WaitingForReplicatedVolume` condition on all RVRs.**
   Configuration with empty `ReplicatedStoragePoolName`. `getRSP("")` returns NotFound → Guard #3 fires: all RVRs receive `Scheduled=Unknown` / `WaitingForReplicatedVolume`. Reconcile returns without error.

3. **RV not found → `WaitingForReplicatedVolume` condition on all RVRs.**
   RV is not found. Guard #1 fires: all RVRs receive `Scheduled=Unknown` / `WaitingForReplicatedVolume`. Reconcile returns without error.

---

## Node and LVG Readiness

1. **NodeReady=false → node is skipped.**
   2 nodes: ready (score=50) and not-ready (score=100). Despite the higher score, the not-ready node is skipped.

2. **AgentReady=false → node is skipped.**
   Similarly: agent-not-ready node with a high score is skipped.

3. **Unschedulable=true → node is skipped.**
   Node with `Unschedulable=true` and a high score is skipped.

4. **LVG Ready=false → LVG is skipped.**
   One node with 2 LVGs: vg-ready (score=50) and vg-not-ready (score=100, Ready=false). vg-ready is selected.

5. **LVG Unschedulable=true → LVG is skipped.**
   One node with 2 LVGs: schedulable (score=50) and unschedulable (score=100). The schedulable one is selected.

---

## LVMThin Storage Type

1. **LVMThin → ThinPoolName is set.**
   RSP type=LVMThin. RVR receives both `LVMVolumeGroupName` and `LVMVolumeGroupThinPoolName` from the eligible node.

2. **LVM thick → ThinPoolName is empty.**
   RSP type=LVM (thick). RVR receives only `LVMVolumeGroupName`; `LVMVolumeGroupThinPoolName` remains empty.

---

## Requeue Behavior

1. ⚡ **No capacity anywhere → Reconcile completes without error, no explicit requeue.**
   Extender returns empty scores (no capacity). Replicas receive `Scheduled=False` / `SchedulingFailed`. Reconcile completes without error and without `RequeueAfter` — re-reconciliation happens naturally when the watched resources (RSP, RVR, RV) change.

---

## Access Replica Handling

1. **Access replica is not scheduled.**
   Access RVR without NodeName. After Reconcile, NodeName is still empty.

2. ⚡ **Access replica with NodeName occupies the node.**
   Access RVR on node-a1. A new Diskful RVR must go to node-a2 — Access blocks node-a1, even though it does not go through the scheduling pipeline itself.

---

## Topology Edge Cases

1. ⚡ **TransZonal: round-robin 4 Diskful across 3 zones.**
   Cluster large-3z-3n (3 zones × 3 nodes). 4 Diskful. All 3 zones are used; one zone gets 2 replicas, the others get 1 each (round-robin balancing).

2. ⚡ **Zonal: sticky zone — 3 Diskful in one zone.**
   Cluster medium-2z-4n (2 zones × 4 nodes). 3 Diskful, no existing. All 3 are scheduled in a single zone (Zonal sticky). The specific zone depends on scores.

---

## AttachTo Bonus

1. ⚡ **+1000 bonus overrides extender scores (extender returns 0–10).**
   vg-attachto=3 (+1000=1003) vs vg-other=10. The attachTo node always wins because the +1000 bonus is far larger than the maximum extender score (10).

2. ⚡ **AttachTo bonus is not applied to TieBreaker replicas.**
   attachTo=[node-a1]. 1 TieBreaker. TieBreaker does not go through extender scoring or attachTo bonus — it is placed purely by node availability and zone preference.

---

## Zone Insufficiency

1. ⚡ **TransZonal: 2 zones × 1 node, 3 Diskful → 2 OK, 1 `SchedulingFailed`.**
   Greedy: the first 2 Diskful are scheduled (one per zone), the third gets `Scheduled=False`.

2. ⚡ **TransZonal: zone-a occupied by existing → new replicas go to zone-b, zone-c.**
   3 zones × 1 node. Existing Diskful on node-a1 occupies zone-a. 3 new Diskful: 2 are scheduled in zone-b and zone-c, 1 gets `Scheduled=False`.

3. ⚡ **Zonal: zone exhausted → `SchedulingFailed`.**
   2 zones, existing Diskful on node-a1 locks zone-a. zone-a has 2 nodes. Scheduling 2 new: the first goes to node-a2, the second gets `Scheduled=False` (no free nodes in zone-a).

---

## Zonal zone capacity penalty

Zone capacity penalty applies to Diskful + Zonal only. Zones where the number
of free nodes is less than the remaining Diskful demand receive a -800 score
penalty. D = FTT + GMDR + 1.

1. ⚡ **Zone with enough free nodes has no penalty — replicas go there.**
   FTT=1, GMDR=0 (D=2). zone-a: 2 free nodes, zone-b: 1 free node. zone-b gets -800 penalty. Both replicas are scheduled in zone-a.

2. ⚡ **No zone has enough free nodes → all penalized, best score wins.**
   FTT=1, GMDR=0 (D=2). zone-a: 1 node (score=10), zone-b: 1 node (score=8). Both zones get -800. zone-a wins by score → 1 replica OK, 1 `Scheduled=False`.

3. ⚡ **All Diskful already scheduled → no penalty applied (remainingDemand=0).**
   FTT=1, GMDR=0 (D=2), 2 Diskful already scheduled. New Diskful: no penalty step runs, zone chosen purely by score.

4. ⚡ **Existing replicas commit the zone; penalty does not override sticky.**
   FTT=1, GMDR=1 (D=3). Existing Diskful on node-a1 (zone-a, 3 nodes). zone-b: 2 nodes. remainingDemand=2. zone-a has 2 free nodes (enough), zone-b has 2 free nodes (enough) — no penalty on either. Zonal sticky filter picks zone-a (has 1 Diskful > 0). Both new replicas go to zone-a.

5. ⚡ **Penalty pushes replicas away from a small zone.**
   FTT=1, GMDR=1 (D=3). zone-a: 1 node (score=10), zone-b: 3 nodes (scores 8, 7, 6). remainingDemand=3. zone-a has 1 free node < 3 → gets -800. zone-b has 3 free nodes → no penalty. All 3 replicas go to zone-b despite zone-a having the highest individual score.

6. ⚡ **FTT=0,GMDR=0 (D=1) — no penalty when zones have ≥1 node.**
   Both zones have ≥ 1 node → no penalty. Replica scheduled normally.

7. ⚡ **FTT=1,GMDR=2 (D=4) — zone with 4 free nodes gets no penalty.**
   zone-a: 4 nodes (≥ D=4 → no penalty), zone-b: 3 nodes (< 4 → penalty). All 4 replicas go to zone-a.

8. ⚡ **FTT=1,GMDR=2 (D=4) — both zones too small → penalty, sticky locks.**
   zone-a: 2 nodes, zone-b: 3 nodes. Both < D=4 → both penalized. zone-b wins first placement (higher scores). Sticky locks to zone-b. 3 placed in zone-b, 1 fails (sticky restricts to full zone).

9. ⚡ **FTT=2,GMDR=1 (D=4) — penalty pushes away from small zone.**
   zone-a: 2 nodes (score=10), zone-b: 4 nodes (score=5). zone-a penalized (2 < 4), zone-b not. All 4 go to zone-b despite lower individual scores.

10. ⚡ **FTT=2,GMDR=2 (D=5) — zone with 5 free nodes gets no penalty.**
    zone-a: 5 nodes (≥ D=5 → no penalty), zone-b: 3 nodes (< 5 → penalty). All 5 replicas go to zone-a.

11. ⚡ **FTT=2,GMDR=2 (D=5) — partial schedule reduces remaining demand.**
    3 already scheduled in zone-a. remainingDemand=2. zone-a has 2 free nodes ≥ 2 → no penalty. Both new replicas placed.

12. ⚡ **FTT=2,GMDR=2 (D=5) — no zone has 5 nodes → sticky limits.**
    zone-a: 3 nodes, zone-b: 2 nodes. Both penalized. Sticky locks to zone-a (wins tiebreak). 3 placed in zone-a, 2 fail.

---

## Zonal reserved nodes

1. ⚡ **Reserved nodes (NodeName without LVG) commit the zone, despite better scores in another zone.**
   zone-a: 3 nodes (scores 90, 80, 100). zone-b: 2 nodes (scores 200, 190). 2 Diskful RVRs with NodeName in zone-a but no LVMVolumeGroupName (reserved). 1 new Diskful RVR. Reserved commit zone-a → the new replica goes to zone-a (node-a3), not zone-b. Reserved ones get their LVGs assigned (vg-a1, vg-a2).

2. ⚡ **TieBreaker also goes to the committed zone.**
   Same setup, but instead of a new Diskful — a TieBreaker. The TieBreaker is scheduled in zone-a (node-a3), not zone-b.

---

## TransZonal greedy scheduling

1. ⚡ **3 Diskful, 2 zones × 1 node → 2 greedy, 1 `Scheduled=False`.**
   UnscheduledRVRsCount=3. TransZonal without fail-fast: 2 replicas are scheduled greedily (one per zone), the third gets `Scheduled=False`.

2. **2 Diskful, 2 zones — everything fits.**
   Basic positive case. 2 zones × 1 node, 2 replicas → both scheduled, one per zone.

3. ⚡ **Reserved node occupies a zone → greedy scheduling on the remaining ones.**
   3 zones × 1 node. RVR reserved on node-c1 (no LVG). 2 new Diskful. Greedy: at least 1 is scheduled on node-a1 or node-b1.

4. ⚡ **Migration: existing replica in old zone that is no longer in RSP.**
   RSP zones: zone-b, zone-c (zone-a removed). Existing Diskful on node-a1 (zone-a). The new replica is scheduled in zone-b — the only available new zone.

---

## RVR with Node but No LVG

1. ⚡ **LVG selection on an already-assigned node.**
   RVR has NodeName=node-b, but LVMVolumeGroupName is empty. The reconciler does not re-schedule the node; it picks the LVG (vg-b) on node-b. Sets `Scheduled=True`.

2. ⚡ **No suitable LVG on the assigned node → `SchedulingFailed`.**
   RVR on node-b. The only LVG on node-b has Ready=false and no capacity in scores. The pipeline is narrowed to node-b, all LVGs are filtered out → `Scheduled=False` / `SchedulingFailed`.

3. ⚡ **Partially-scheduled RVR blocks its node for other replicas.**
   rvr-needs-lvg on node-a (NodeName set, LVG empty). rvr-new is completely new. rvr-needs-lvg receives vg-a. rvr-new must go to node-b, not node-a (node is occupied).

4. ⚡ **LVMThin: ThinPoolName for a partially-scheduled RVR.**
   RSP type=LVMThin. RVR with NodeName=node-a, without LVG or ThinPool. After Reconcile: receives both `LVMVolumeGroupName=vg-a` and `LVMVolumeGroupThinPoolName=thin-a`. `Scheduled=True`.

5. ⚡ **Early exit → `WaitingForReplicatedVolume` on both partially-scheduled and fully-unscheduled RVRs.**
   RV with empty ReplicatedStoragePoolName → Guard #3: RSP not found. 2 RVRs: one with NodeName (partial), one without (unscheduled). Both receive `Scheduled=Unknown` / `WaitingForReplicatedVolume` — the reconciler makes no distinction on early exit.

---

## Edge Cases: Empty Inputs

1. **No RVRs at all — Reconcile returns Done.**
   RV and RSP exist and are valid. No RVRs linked to this RV. Reconcile returns Done without error. No patches, no conditions set.

2. **All RVRs are Access — no scheduling, condition removed.**
   3 Access RVRs (some may have had `Scheduled` condition from a previous type). After Reconcile: `Scheduled` condition is removed from all Access RVRs. No scheduling pipeline runs. Reconcile returns Done.

3. **0 eligible nodes in RSP — all Diskful get `SchedulingFailed`.**
   RSP exists but `EligibleNodes` is empty. 2 Diskful RVRs. Both receive `Scheduled=False` / `SchedulingFailed`. Pipeline summary: "0 candidates (node×LVG) from 0 eligible nodes".

4. ⚡ **RVR with unknown ReplicaType — silently skipped.**
   RVR with a type that is not Diskful, TieBreaker, or Access. It is in `All` but not in any type set. It does not appear in `unscheduledDiskful` or `unscheduledTieBreaker` → silently skipped. No condition set, no error.

---

## Idempotency and Patch Behavior

1. ⚡ **Repeated Reconcile is a no-op — no patches emitted.**
   All RVRs already scheduled with correct conditions. Second Reconcile: `reconcileRVRCondition` detects no diff → skips patch. No API writes. Reconcile returns Done.

2. **Condition already set with identical value — no patch.**
   RVR already has `Scheduled=True` / `Scheduled` with the correct message. `reconcileRVRCondition` compares status/reason/message → match → returns Continue without patching.

3. ⚡ **RVR deleted concurrently — NotFound on patch is silently ignored.**
   RVR is deleted between Get and Patch. `patchRVR` / `patchRVRStatus` return NotFound → converted to nil. Reconcile continues without error.

4. ⚡ **Conflict on patch (optimistic lock) — Reconcile returns error for retry.**
   Another controller modifies the RVR between DeepCopy and Patch. Patch returns Conflict. Reconcile returns error → controller-runtime retries.

---

## Extender: NarrowReservation

1. ⚡ **NarrowReservation error (scoring OK, narrow fails).**
   `FilterAndScore` succeeds and returns scores. `NarrowReservation` fails with an error. RVR receives `Scheduled=False` / `ExtenderUnavailable` with message "failed to confirm capacity reservation". Reconcile returns error.

---

## Scheduling Order

1. ⚡ **Diskful replicas scheduled in ID order — deterministic.**
   3 Diskful RVRs (ID 0, 1, 2) on a cluster with 3 nodes. RVRs are processed in ID order. The first (ID 0) gets the best-scoring node; subsequent ones get remaining nodes. Verify deterministic assignment.

2. **Diskful before TieBreaker — TieBreaker sees updated state.**
   1 Diskful + 1 TieBreaker, 2 nodes. Diskful is scheduled in Phase 2 (takes one node). TieBreaker in Phase 3 sees the updated `OccupiedNodes` → goes to the remaining node.

---

## Access Replica Condition Cleanup

1. ⚡ **Access RVR formerly Diskful — Scheduled condition removed.**
   RVR was Diskful with `Scheduled=True`. Type changed to Access. After Reconcile: `Scheduled` condition is removed by `reconcileRVRsConditionAbsent`. NodeName/LVG remain (not cleared by this controller).

---

## Ignored Topology: No Zone Filtering

1. **Ignored topology — Zonal and TransZonal code paths not activated.**
   Cluster with 3 zones, topology=Ignored. 2 Diskful scheduled. Verify: no zone filter predicates are added to the pipeline. Replicas go to nodes with best scores regardless of zone.

---

## Pipeline Summary in Condition Message

1. **`SchedulingFailed` message contains pipeline diagnostic summary.**
   1 Diskful, all nodes not ready. Condition `Scheduled=False` / `SchedulingFailed` is set. Condition `.message` contains the pipeline summary, e.g. "4 candidates (node×LVG) from 2 eligible nodes; 4 excluded: node not ready".

---

## ReservationID

1. **ReservationID from RV annotation — used as-is.**
   RV has annotation `SchedulingReservationID=ns/pvc-123`. Extender is called with `reservationID="ns/pvc-123"`, not with the computed LLV name.

2. **ReservationID computed when annotation absent.**
   RV has no `SchedulingReservationID` annotation. Extender is called with `reservationID = rvrllvname.ComputeLLVName(rvr.Name, rvr.Spec.LVMVolumeGroupName, rvr.Spec.LVMVolumeGroupThinPoolName)`.

---

## Deleting Scheduled RVR

1. ⚡ **Deleting scheduled RVR — condition `Scheduled=True` preserved, not re-scheduled.**
   RVR with DeletionTimestamp + NodeName + LVG (fully scheduled). It is in `Scheduled` → gets `Scheduled=True` condition. It is excluded from `unscheduled` (via `Deleting`). Its node stays in `OccupiedNodes`, blocking new replicas. Verify: condition set, not re-scheduled, node blocked.
