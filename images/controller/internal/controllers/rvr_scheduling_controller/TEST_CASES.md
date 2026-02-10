# Key scheduling test cases

This document lists the key behavioral cases that must be reviewed and used as the basis for generating tests.
Cases are grouped by Context blocks.
Non-obvious / non-trivial cases are marked with ⚡.

---

## Zonal Topology (8 cases)

1. **small-1z: D:2 — all in zone-a.**
   Cluster with 1 zone (2 nodes). 2 Diskful replicas are scheduled into the only zone, zone-a.

2. **small-1z: attachTo node-a1 — D on node-a1, TB on node-a2.**
   1 Diskful + 1 TieBreaker. attachTo points to node-a1 → D goes there, TB goes to the remaining node-a2.

3. **medium-2z: attachTo both nodes of the same zone — all D in zone-a.**
   Cluster with 2 zones. attachTo=[node-a1, node-a2] (both in zone-a) → 2D are scheduled in zone-a, zone-b is not used.

4. ⚡ **medium-2z-4n: existing D in zone-a — new D and TB also go to zone-a.**
   There is already a Diskful on node-a1. New D and TB must go to the same zone (sticky behavior).

5. ⚡ **medium-2z: existing D in different zones — TopologyConstraintsFailed.**
   Existing Diskful replicas in zone-a and zone-b. For Zonal topology this is a conflict: the new replica cannot be scheduled and receives `Scheduled=False` / `TopologyConstraintsFailed`.

6. **small-1z: all nodes occupied — no candidates for TB.**
   2 existing Diskful occupy both nodes. TieBreaker receives `Scheduled=False` / `NoAvailableNodes`.

7. ⚡ **medium-2z: TB without any Diskful — no candidates.**
   No Diskful at all (neither existing nor to-be-scheduled). In a multi-zone cluster the zone for TieBreaker is undefined → `Scheduled=False` / `NoAvailableNodes`.

8. **medium-2z: existing D in zone-a — TB also goes to zone-a.**
   Existing Diskful on node-a1 locks the zone. TieBreaker is scheduled in zone-a on a free node.

---

## TransZonal Topology (6 cases)

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
   4 existing Diskful fill all 4 nodes across 2 zones. TieBreaker receives `Scheduled=False` / `NoAvailableNodes`.

---

## Ignored Topology (4 cases)

1. **large-3z: D:2 — Diskful by best scores.**
   No zone constraints: 2 Diskful are scheduled on the nodes with the highest scores (node-a1=100, node-b1=90), regardless of zones.

2. **medium-2z: attachTo — prefer specified nodes.**
   attachTo=[node-a1, node-b1]. Both Diskful are scheduled exactly on those nodes.

3. **small-1z-4n: D:2, TB:2 — 4 replicas on 4 nodes.**
   All 4 replicas (2 Diskful + 2 TieBreaker) are successfully scheduled, each on a separate node.

4. **small-1z: all nodes occupied — TB is not scheduled.**
   2 existing Diskful on both nodes. TieBreaker cannot be scheduled — `NoAvailableNodes`.

---

## Extender Filtering (2 cases)

1. ⚡ **Extender error → `Scheduled=False` / `ExtenderUnavailable`.**
   Mock extender returns an error (simulating unavailability). The replica is not scheduled and receives `Scheduled=False` / `ExtenderUnavailable`. Reconcile itself completes **without error** — the problem is isolated to one replica.

2. **Extender returns no scores for zone-b → nodes filtered out.**
   Mock extender returns scores only for LVGs in zone-a. The replica is scheduled strictly on node-a1 or node-a2 (zone-a); zone-b nodes are unavailable.

---

## Partial Diskful Scheduling (1 case)

1. ⚡ **3 Diskful on 2 nodes → 2 scheduled, 1 `Scheduled=False`.**
   Cluster small-1z (2 nodes). 3 Diskful RVRs. The reconciler schedules 2 (greedy), the third receives `Scheduled=False` / `NoAvailableNodes`. The scheduled ones get `Scheduled=True`.

---

## Deleting Replica Node Occupancy (1 case)

1. ⚡ **Deleting replica (with DeletionTimestamp) blocks its node.**
   RVR with `DeletionTimestamp` + `NodeName=node-a1` + finalizer. The new replica rvr-new must be scheduled on node-a2, not node-a1 (even though node-a1 has a higher score).

---

## RVR with DeletionTimestamp (1 case)

1. ⚡ **Deleting unscheduled RVR is skipped.**
   RVR with `DeletionTimestamp` but no `NodeName`. The reconciler does not attempt to schedule it; `NodeName` remains empty.

---

## Scheduled Condition Management (1 case)

1. ⚡ **`Scheduled=True` is set on already-existing replicas too.**
   Existing RVR with `NodeName=node-a1` + `LVMVolumeGroupName=vg-node-a1` (fully scheduled) + a new RVR without NodeName. After Reconcile, both receive the condition `Scheduled=True` / `ReplicaScheduled`.

---

## RV Not Ready (1 case)

1. ⚡ **RV without finalizer → error + `SchedulingPending`.**
   RV exists but has no finalizer (RV controller has not reconciled it yet). The reconciler returns an error and sets `Scheduled=False` / `SchedulingPending` on all RVRs.

---

## Constraint Violation Conditions (1 case)

1. ⚡ **TransZonal: eligible nodes only from zone-a, but topology=TransZonal.**
   RSC specifies 2 zones, but eligible nodes exist only in zone-a. 2 Diskful: the first is scheduled in zone-a, the second receives `Scheduled=False` (no node in the second zone — `NoAvailableNodes` or `TopologyConstraintsFailed`).

---

## Multi-LVG Selection (5 cases)

1. ⚡ **Equal BestScore → node with more LVGs wins.**
   Node-1: 2 LVGs (lvg-1a=9, lvg-1b=5). Node-2: 1 LVG (lvg-2a=9). BestScore is equal (9), but Node-1 has 2 LVGs > 1 → Node-1 wins. lvg-1a is selected (best score on the node).

2. ⚡ **Equal BestScore and LVGCount → higher SumScore wins.**
   Node-1: 2 LVGs (9+5=14). Node-2: 2 LVGs (9+2=11). BestScore=9, LVGCount=2 — identical. Sum: 14 > 11 → Node-1.

3. **Different BestScore → best score wins.**
   Node-1: 2 LVGs (best=9). Node-2: 1 LVG (best=10). Despite having more LVGs, Node-2 wins by BestScore (10 > 9).

4. ⚡ **LVGs without capacity are filtered out before counting.**
   Node-1: 3 LVGs, but lvg-1c is not in scores (no capacity). After filtering: Node-1 has 2 suitable LVGs vs Node-2 with 1 → Node-1 wins.

5. **Best LVG is selected on the winning node.**
   1 node with 3 LVGs (scores 10, 15, 5). lvg-1b (score=15) is selected — the best on the node.

---

## RV Validation Errors (2 cases)

1. **Size=0 → error + `SchedulingPending`.**
   RV has `Size: "0"`. Reconcile returns an error. RVR receives `Scheduled=False` / `SchedulingPending`.

2. **Empty ReplicatedStorageClassName → error + `SchedulingPending`.**
   RV with empty `ReplicatedStorageClassName`. Reconcile returns an error. RVR receives `Scheduled=False` / `SchedulingPending`.

---

## RSP Not Found (3 cases)

1. ⚡ **RSP does not exist → error, RVR status is NOT touched.**
   Configuration points to a nonexistent RSP (`rsp-nonexistent`). Reconcile returns an error containing "not found". RVR status remains empty — an I/O error must not modify status.

2. ⚡ **Empty StoragePoolName → error + `SchedulingPending`.**
   Configuration with empty `StoragePoolName`. Error: "no storage pool configured". RVR receives `Scheduled=False` / `SchedulingPending` (unlike RSP-not-found — here status IS updated).

3. **RV not found → Done without error.**
   RV is not found. Reconcile returns `Done` with no error and no requeue.

---

## Node and LVG Readiness (5 cases)

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

## LVMThin Storage Type (2 cases)

1. **LVMThin → ThinPoolName is set.**
   RSP type=LVMThin. RVR receives both `LVMVolumeGroupName` and `LVMVolumeGroupThinPoolName` from the eligible node.

2. **LVM thick → ThinPoolName is empty.**
   RSP type=LVM (thick). RVR receives only `LVMVolumeGroupName`; `LVMVolumeGroupThinPoolName` remains empty.

---

## Requeue Behavior (1 case)

1. ⚡ **Scheduling failure → `RequeueAfter(5s)`.**
   Extender returns empty scores (no capacity anywhere). Reconcile completes **without error**, but with `RequeueAfter(5s)`.

---

## Access Replica Handling (2 cases)

1. **Access replica is not scheduled.**
   Access RVR without NodeName. After Reconcile, NodeName is still empty.

2. ⚡ **Access replica with NodeName occupies the node.**
   Access RVR on node-a1. A new Diskful RVR must go to node-a2 — Access blocks node-a1, even though it does not go through the scheduling pipeline itself.

---

## Topology Edge Cases (2 cases)

1. ⚡ **TransZonal: round-robin 4 Diskful across 3 zones.**
   Cluster large-3z-3n (3 zones × 3 nodes). 4 Diskful. All 3 zones are used; one zone gets 2 replicas, the others get 1 each (round-robin balancing).

2. ⚡ **Zonal: sticky zone — 3 Diskful in one zone.**
   Cluster medium-2z-4n (2 zones × 4 nodes). 3 Diskful, no existing. All 3 are scheduled in a single zone (Zonal sticky). The specific zone depends on scores.

---

## AttachTo Bonus (2 cases)

1. ⚡ **+1000 bonus overrides a small score difference.**
   vg-attachto=50 (+1000=1050) vs vg-other=60. The attachTo node wins.

2. ⚡ **Bonus does not override a significantly higher score.**
   vg-attachto=50 (+1000=1050) vs vg-other=2000. The node with the much higher capacity score wins.

---

## Zone Insufficiency (3 cases)

1. ⚡ **TransZonal: 2 zones × 1 node, 3 Diskful → 2 OK, 1 `NoAvailableNodes`.**
   Greedy: the first 2 Diskful are scheduled (one per zone), the third gets `Scheduled=False`.

2. ⚡ **TransZonal: zone-a occupied by existing → new replicas go to zone-b, zone-c.**
   3 zones × 1 node. Existing Diskful on node-a1 occupies zone-a. 3 new Diskful: 2 are scheduled in zone-b and zone-c, 1 gets `Scheduled=False`.

3. ⚡ **Zonal: zone exhausted → `NoAvailableNodes`.**
   2 zones, existing Diskful on node-a1 locks zone-a. zone-a has 2 nodes. Scheduling 2 new: the first goes to node-a2, the second gets `Scheduled=False` (no free nodes in zone-a).

---

## Zonal soft zone preference / UnscheduledRVRsCount (7 cases)

1. ⚡ **L1: one zone has enough nodes — both replicas go there.**
   zone-a: 2 nodes, zone-b: 1 node. UnscheduledRVRsCount=2. zone-a has 2 >= required(2) → both replicas are scheduled in zone-a.

2. ⚡ **L2: no zone has enough nodes → best-effort by score.**
   zone-a: 1 node (score=100), zone-b: 1 node (score=90). Required=2, neither zone fits. zone-a is chosen by score → 1 replica OK, 1 `Scheduled=False`.

3. ⚡ **L2: 3 replicas, both zones have 2 nodes → 2 OK, 1 unscheduled.**
   zone-a: 2 nodes (scores 100, 90), zone-b: 2 nodes (85, 80). Required=3, neither zone fits. zone-a by score → 2 replicas OK, 1 `Scheduled=False`.

4. ⚡ **L3: UnscheduledRVRsCount=0 → zone chosen purely by score.**
   2 zones × 1 node, UnscheduledRVRsCount=0. 2 Diskful: 1 is scheduled in the best zone, 1 `Scheduled=False` (only 1 node in the chosen zone).

5. ⚡ **L2: reserved nodes reduce available count.**
   zone-a: 2 nodes (1 reserved), zone-b: 1 node. Required=2. After subtracting reserved: zone-a=1, zone-b=1. Neither fits. zone-a by score → 1 OK, 1 `Scheduled=False`.

6. ⚡ **Existing replicas in zone: continue scheduling until exhausted.**
   Existing on node-a1 (zone-a). UnscheduledRVRsCount=2. zone-a is committed by existing. First new → node-a2, second → `Scheduled=False` (zone-a exhausted).

7. ⚡ **attachTo narrows to a zone with 1 node — soft fallback.**
   zone-a: 1 node (node-a1, attachTo target), zone-b: 3 nodes. attachTo locks zone-a. Required=2, zone-a has 1 node. Best-effort: 1 replica on node-a1, 1 `Scheduled=False`.

---

## Zonal reserved nodes (2 cases)

1. ⚡ **Reserved nodes (NodeName without LVG) commit the zone, despite better scores in another zone.**
   zone-a: 3 nodes (scores 90, 80, 100). zone-b: 2 nodes (scores 200, 190). 2 Diskful RVRs with NodeName in zone-a but no LVMVolumeGroupName (reserved). 1 new Diskful RVR. Reserved commit zone-a → the new replica goes to zone-a (node-a3), not zone-b. Reserved ones get their LVGs assigned (vg-a1, vg-a2).

2. ⚡ **TieBreaker also goes to the committed zone.**
   Same setup, but instead of a new Diskful — a TieBreaker. The TieBreaker is scheduled in zone-a (node-a3), not zone-b.

---

## TransZonal greedy scheduling (4 cases)

1. ⚡ **3 Diskful, 2 zones × 1 node → 2 greedy, 1 `Scheduled=False`.**
   UnscheduledRVRsCount=3. TransZonal without fail-fast: 2 replicas are scheduled greedily (one per zone), the third gets `Scheduled=False`.

2. **2 Diskful, 2 zones — everything fits.**
   Basic positive case. 2 zones × 1 node, 2 replicas → both scheduled, one per zone.

3. ⚡ **Reserved node occupies a zone → greedy scheduling on the remaining ones.**
   3 zones × 1 node. RVR reserved on node-c1 (no LVG). 2 new Diskful. Greedy: at least 1 is scheduled on node-a1 or node-b1.

4. ⚡ **Migration: existing replica in old zone that is no longer in RSP.**
   RSP zones: zone-b, zone-c (zone-a removed). Existing Diskful on node-a1 (zone-a). The new replica is scheduled in zone-b — the only available new zone.

---

## RVR with Node but No LVG (5 cases)

1. ⚡ **LVG selection on an already-assigned node.**
   RVR has NodeName=node-b, but LVMVolumeGroupName is empty. The reconciler does not re-schedule the node; it picks the LVG (vg-b) on node-b. Sets `Scheduled=True`.

2. ⚡ **`NoAvailableLVGOnNode` — no suitable LVG on the assigned node.**
   RVR on node-b. The only LVG on node-b has Ready=false and no capacity in scores. The replica receives `Scheduled=False` / `NoAvailableLVGOnNode`.

3. ⚡ **Partially-scheduled RVR blocks its node for other replicas.**
   rvr-needs-lvg on node-a (NodeName set, LVG empty). rvr-new is completely new. rvr-needs-lvg receives vg-a. rvr-new must go to node-b, not node-a (node is occupied).

4. ⚡ **LVMThin: ThinPoolName for a partially-scheduled RVR.**
   RSP type=LVMThin. RVR with NodeName=node-a, without LVG or ThinPool. After Reconcile: receives both `LVMVolumeGroupName=vg-a` and `LVMVolumeGroupThinPoolName=thin-a`. `Scheduled=True`.

5. ⚡ **Early exit → `Scheduled=False` on both partially-scheduled and fully-unscheduled RVRs.**
   RV with empty StoragePoolName → early exit with error. 2 RVRs: one with NodeName (partial), one without (unscheduled). Both receive `Scheduled=False` — the reconciler makes no distinction on early exit.

---

**Total: ~68 test cases.**
