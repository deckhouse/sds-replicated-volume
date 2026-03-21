# E2E Agent Test Cases

## Test tree

```
TestDRBDResource
│
├── R1 — single diskful replica on one node
│   │   Creates a diskless DRBDResource, waits for Configured=True and
│   │   addresses populated. Creates an LLV, waits for Created. Patches
│   │   the DRBDResource to Diskful, waits for Configured=True. Asserts
│   │   status.size is populated and less than spec.size (DRBD metadata
│   │   overhead). Asserts the agent added its finalizer to the LLV.
│   │
│   ├── MaintenanceMode
│   │   Patches spec.maintenance=NoResourceReconciliation. Asserts
│   │   Configured=False with reason InMaintenance. Cleanup reverts;
│   │   agent reconciles back to Configured=True.
│   │
│   ├── StateDown
│   │   Patches spec.state=Down. Waits for agent to tear down DRBD and
│   │   remove its own finalizer from the DRBDResource. The LLV finalizer
│   │   is also released. Cleanup reverts to state=Up; agent brings DRBD
│   │   back up and re-adds both finalizers.
│   │
│   └── DiskfulToDiskless
│       Patches spec.type from Diskful to Diskless. Waits for
│       Configured=True. Asserts activeConfiguration.type=Diskless.
│       Cleanup reverts to Diskful; agent re-attaches the disk.
│
├── Resize — online resize of a single-node diskful replica
│       Creates a diskful DRBDResource with an LLV, promotes to Primary
│       (required by drbdsetup resize). Records initial status.size.
│       Grows the LLV to 2x AllocateSize, waits for sds-node-configurator
│       to resize. Patches DRBDR spec.size to the new size, waits for
│       Configured=True. Asserts status.size has grown.
│
├── DeleteDiskful — delete diskful replica with attached LLV
│       Creates a diskful DRBDResource with an LLV (same setup as R1).
│       Deletes the DRBDResource directly without reverting to diskless.
│       Waits for full deletion. Asserts the agent released its finalizer
│       from the LLV. Catches the bug where the agent fails to release the
│       LLV finalizer on the deletion path (intendedLLVName == attachedLLVName
│       because spec doesn't change on delete).
│
├── LLVFinalizer — LLV finalizer behavior across state/spec transitions
│   │   Creates a diskful DRBDResource with an LLV (same setup as R1).
│   │   Exercises sequences of state and spec changes, asserting the
│   │   agent releases the LLV finalizer on Down and re-acquires it on Up.
│   │
│   ├── DownUp
│   │   Down → Up. Asserts finalizer absent after Down, present after Up.
│   │
│   ├── DownUpDiskless
│   │   Down → Up+Diskless (single combined patch). Asserts finalizer
│   │   absent after Down, stays absent after Up+Diskless.
│   │
│   ├── DownUpDownUpDiskless
│   │   Down → Up → Down → Up+Diskless. Multiple cycles, asserts
│   │   correct finalizer state at each step.
│   │
│   └── DownDisklessThenUp
│       Down → DiskfulToDiskless (while Down) → Up. Asserts finalizer
│       stays absent throughout when spec is cleared while Down.
│
├── OrphanCleanup — orphan DRBD resource cleanup after force-delete
│       Creates a diskless DRBDResource, waits for Configured=True.
│       Force-deletes the K8S object (removes agent finalizer, then
│       deletes). The DRBD resource may still exist on the node.
│       Re-creates the same DRBDResource, waits for Configured=True.
│       Verifies the agent handles orphan DRBD resources left behind
│       when the K8S object disappears without the normal finalizer flow.
│
├── NonManagedResource — non-managed DRBD resource left untouched
│       Creates a DRBD resource directly on the node (via drbdsetup
│       new-resource) without the sdsrv- prefix. Waits for the scanner
│       to process events, then asserts the resource still exists.
│       Verifies the agent does not tear down DRBD resources it does
│       not own. Cleanup tears down the resource via drbdsetup down.
│
├── Rename — actualNameOnTheNode rename flow
│   │   Creates a diskless DRBDResource, waits for Configured=True.
│   │   Sets maintenance mode to guard against the agent re-creating
│   │   the DRBD resource during the rename window. Renames the DRBD
│   │   resource on the node from sdsrv-{name} to custom-{name} via
│   │   drbdsetup rename-resource. Patches actualNameOnTheNode and
│   │   clears maintenance in a single patch. Waits for the agent to
│   │   rename it back to sdsrv-{name}, clear actualNameOnTheNode,
│   │   and reach Configured=True.
│   │
│   └── MaintenanceModeSkipsRename
│       Sets maintenance mode, renames the DRBD resource on the node
│       to custom-mm-{name}, patches actualNameOnTheNode. Asserts the
│       agent skips the rename (Configured=False/InMaintenance) and
│       the DRBD resource on the node still has the custom name.
│       Cleanup reverts actualNameOnTheNode, renames DRBD back on the
│       node, then clears maintenance mode.
├── DeviceUUID — DRBD metadata protection (parallel)
│   │   Covers device-uuid decision table: never overwrite existing metadata (G1),
│   │   never attach a foreign disk (G2).
│   │
│   ├── InitialCreation — no metadata, empty status → create-md and set UUID
│   ├── Peered — two replicas, synced
│   │   ├── ReattachMatchingUUID — metadata and UUID match → reattach
│   │   ├── ZeroDiskUUID — metadata present, zero on disk, status has UUID → write to disk and attach
│   │   └── ForeignDisk — metadata present, UUID mismatch → error (foreign disk)
│   ├── AdoptFromDisk — metadata present, status empty → adopt UUID from disk
│   ├── GenerateNewUUID — metadata present, zero on disk, status empty → generate and set UUID
│   └── RecreateMetadata — no metadata, status has UUID → create-md and write status UUID
│
├── PortConvergence — port adoption and path mismatch convergence (parallel)
│   │   Requires 2 nodes. Creates 2 peered, synced diskful replicas.
│   │
│   ├── AdoptExistingPort
│   │   Force-deletes both DRBDRs (DRBD keeps running on nodes with
│   │   established connections). Recreates fresh DRBDRs (empty status).
│   │   Asserts the agent adopts existing DRBD ports into status.addresses
│   │   instead of allocating new ones. Re-peers and verifies Connected.
│   │
│   └── PathMismatchConvergence
│       Injects a wrong port into DRBD on node 0 via drbdsetup
│       (disconnect, del-path, new-path with wrong port, connect).
│       status.addresses still has the original correct port. Waits for
│       the agent to converge: adds correct path, waits for established,
│       del-paths the stale one. Asserts status shows the correct port
│       and DRBD has exactly one path per connection.
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
    ├── DRBDSetupSchema — strict schema validation of drbdsetup JSON output
    │   Runs drbdsetup status --json and drbdsetup show --json on node 0.
    │   Unmarshals both outputs into the production drbdutils Go structs
    │   with DisallowUnknownFields (rejects unknown JSON fields). Validates
    │   key fields are populated: resource name, connections (3 for 4-node
    │   full mesh), paths with non-empty host addresses. Read-only; does
    │   not modify state.
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

- **DiskfulToDiskless cleanup**: reverts diskless→diskful patch. Verifies
  the agent can re-attach a previously detached disk.

- **Peering cleanup**: reverts peer patches (restores empty peers list).
  Verifies the agent handles peer disconnect gracefully.

- **PromotePrimary cleanup**: reverts role back to Secondary.
  Verifies the agent handles demotion.

- **MaintenanceMode cleanup**: clears maintenance field. Verifies the
  agent resumes reconciliation.

- **StateDown cleanup**: reverts state back to Up. Verifies the agent
  can bring a downed resource fully back up (re-add both DRBDR and LLV
  finalizers, re-create DRBD resource, re-attach disk).

- **RemovePeer cleanup**: restores the peer list. Verifies the agent
  can re-add a previously forgotten peer.

- **Resize cleanup**: SetupPromotePrimary cleanup reverts role to Secondary.
  SetupResourcePatch cleanup reverts LLV spec.size (LV is not actually
  shrunk). SetupDisklessToDiskfulReplica cleanup deletes the DRBDResource
  and LLV entirely. The DRBDR spec.size patch has no revert because the
  API forbids decreasing spec.size.

- **DeleteDiskful**: deletes the DRBDResource directly while still diskful
  with an attached LLV. Verifies the agent releases the LLV finalizer on
  the deletion path. Parent cleanup deletes the orphaned LLV.

- **OrphanCleanup**: force-deletes a DRBDResource (bypassing agent
  finalizer flow), then re-creates it. Verifies the agent tears down the
  orphan DRBD resource on the node and brings the re-created object up
  cleanly. Cleanup deletes the re-created DRBDResource normally.

- **NonManagedResource**: creates a non-prefixed DRBD resource directly
  on the node and verifies it survives after the scanner processes events.
  Cleanup tears down the resource via drbdsetup down.

- **Rename cleanup**: reverts the combined actualNameOnTheNode +
  maintenance patch, then reverts the maintenance-mode-only patch.
  Since the agent already cleared actualNameOnTheNode and renamed
  the DRBD resource back, the reverts are effectively no-ops.

- **MaintenanceModeSkipsRename cleanup**: reverts actualNameOnTheNode,
  renames the DRBD resource back to the standard name on the node,
  then clears maintenance mode. The agent reconciles normally and
  reaches Configured=True.
- **ForeignDisk cleanup**: restores the correct device-uuid on disk after
  writing a fake UUID. Verifies the agent recovers from the error.

- **AdoptExistingPort**: force-deletes DRBDRs leaving DRBD running,
  recreates them, asserts port adoption. Cleanup deletes the recreated
  DRBDRs normally (via SetupDisklessToDiskfulReplica cleanup).

- **PathMismatchConvergence**: injects a wrong DRBD path, waits for
  the agent to converge. No extra cleanup needed — the parent
  PortConvergence scope handles DRBDR deletion.
