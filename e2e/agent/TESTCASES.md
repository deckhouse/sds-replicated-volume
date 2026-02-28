# E2E Agent Test Cases

## Test tree

```
TestDRBDResource
│
├── R1 — single diskful replica on one node
│   │   Creates a diskless DRBDResource, waits for Configured=True and
│   │   addresses populated. Creates an LLV, waits for Created. Patches
│   │   the DRBDResource to Diskful, waits for Configured=True. Asserts
│   │   the agent added its finalizer to the LLV.
│   │
│   ├── MaintenanceMode
│   │   Patches spec.maintenance=NoResourceReconciliation. Asserts
│   │   Configured=False with reason InMaintenance. Cleanup reverts;
│   │   agent reconciles back to Configured=True.
│   │
│   └── StateDown
│       Patches spec.state=Down. Waits for agent to tear down DRBD and
│       remove its own finalizer from the DRBDResource. The LLV finalizer
│       is intentionally kept (resource may come back Up). Cleanup reverts
│       to state=Up; agent brings DRBD back up and re-adds its finalizer.
│
├── R2 — two peered, synced replicas (parallel with R3, R4)
│   │   Creates 2 diskful replicas on separate nodes. Links them as
│   │   full-mesh peers (protocol C, shared secret). Runs CreateNewUUID
│   │   with ClearBitmap to establish initial sync. Waits for both
│   │   replicas to reach DiskState=UpToDate.
│   │
│   ├── PromotePrimary
│   │   │   Patches replica 0 to role=Primary. Asserts Configured=True
│   │   │   and activeConfiguration.role=Primary.
│   │   │
│   │   └── DemoteToSecondary
│   │       Patches replica 0 back to role=Secondary. Asserts
│   │       Configured=True and activeConfiguration.role=Secondary.
│   │       Cleanup reverts to Primary; agent promotes again.
│   │
│   └── RemovePeer
│       Patches replica 0 to spec.peers=[]. Asserts Configured=True
│       and status.peers is empty (agent disconnected and forgot the
│       peer). Cleanup restores the peer; agent reconnects.
│
├── R3 — three peered, synced replicas (parallel with R2, R4)
│   │   Same as R2 but with 3 nodes. Tests full-mesh peering at scale.
│   │
│   └── PromotePrimary
│       │
│       └── DemoteToSecondary
│
└── R4 — four peered, synced replicas (parallel with R2, R3)
    │   Same as R2 but with 4 nodes. Maximum replica count test.
    │
    └── PromotePrimary
        │
        └── DemoteToSecondary
```

## What cleanup tests

Every subtest's cleanup exercises a teardown path:

- **DisklessToDiskfulReplica cleanup** (LIFO): reverts diskful→diskless
  patch, deletes the LLV, deletes the DRBDResource. Verifies the agent
  handles disk detach, DRBD teardown, and finalizer removal.

- **Peering cleanup**: reverts peer patches (restores empty peers list).
  Verifies the agent handles peer disconnect gracefully.

- **PromotePrimary cleanup**: reverts role back to Secondary.
  Verifies the agent handles demotion.

- **MaintenanceMode cleanup**: clears maintenance field. Verifies the
  agent resumes reconciliation.

- **StateDown cleanup**: reverts state back to Up. Verifies the agent
  can bring a downed resource fully back up (re-add finalizer, re-create
  DRBD resource, re-attach disk).

- **RemovePeer cleanup**: restores the peer list. Verifies the agent
  can re-add a previously forgotten peer.
