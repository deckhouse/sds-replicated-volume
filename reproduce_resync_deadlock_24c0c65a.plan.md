---
name: Reproduce resync deadlock
overview: Minimal recipe to reproduce the DRBD resync deadlock on a clean 4-node cluster with sds-replicated-volume deployed.
todos: []
isProject: false
---

# Reproducing the DRBD Resync Deadlock

## Prerequisites

- 4 worker nodes with LVGs (~50Gi each), sds-replicated-volume agent deployed
- Clean cluster: 0 DRBDResources, 0 LLVs

## Recipe

### 1. Create 400 R4 DRBD resource groups (1600 DRBDResources, 1200 LLVs)

- 400 groups, 4 replicas each (one per node)
- Each group has 1 diskless replica (rotating: group `r` has diskless on node `r % 4`)
- 3 diskful replicas with 100Mi LLVs
- All peered, one replica per group promoted to Primary
- Wait until all 1200 diskful replicas are UpToDate

### 2. Down all DRBD resources on one node

From inside the agent pod on e.g. ubuntu-0:

```bash
for r in $(drbdsetup status | grep "^[^ ]" | grep -v "No currently" | cut -d" " -f1); do
  drbdsetup down "$r"
done
```

This downs ~400 DRBD resources (300 diskful + 100 diskless) on that node. The agent's DRBD scanner detects the down events and triggers reconciliation for all 400 resources simultaneously.

### 3. Wait 90 seconds

The reconciler (20 concurrent workers) brings all 400 resources back up. The 300 diskful resources come back as Inconsistent and all initiate resync from their UpToDate peers on the other 3 nodes simultaneously.

### 4. Check for stuck resources

```bash
kubectl get drbdr -o json | python3 -c '
import sys, json
data = json.load(sys.stdin)
for i in data["items"]:
    ds = i.get("status",{}).get("diskState","")
    if ds in ("Inconsistent","Outdated"):
        print(f"{i["metadata"]["name"]}  node={i["spec"]["nodeName"]}  diskState={ds}")
'
```

Expect 5-15% of the 300 diskful resources to be stuck in Inconsistent with `SyncTarget done:0.00 received:0 sent:0`.

## Why it happens

300 diskful resources come back Inconsistent simultaneously and all request resync from 3 source nodes. Each source node gets ~100 incoming resync requests at once. DRBD's kernel-level resync scheduler saturates: some SyncTarget connections are established but the source never starts sending data. On the receiving node, DRBD serializes per-volume resyncs (only one SyncSource per volume) using the `dependency` flag. The queued resyncs wait for the stuck active ones to complete, which never happens.

## Verification

Confirm a stuck resource shows:

```
disk:Inconsistent
  peer-X: SyncTarget peer-disk:UpToDate done:0.00 received:0 sent:0
  peer-Y: PausedSyncT resync-suspended:dependency
```

The active SyncTarget has zero bytes transferred -- the source is overloaded and never starts the data phase.
