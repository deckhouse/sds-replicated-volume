# Control-Plane Migration Process

## Overview

Migration from LINSTOR to the new control-plane in the sds-replicated-volume module.

### High-level approach
- `ModuleConfig.newControlPlane` (boolean, default `false`) enables the new control-plane
- Control-plane migration state is stored in ConfigMap `control-plane-migration` in namespace `d8-sds-replicated-volume`, field `.data.state`
- OnBeforeHelm hook (`guard-against-reset-newControlPlane`) prevents setting `newControlPlane` back to `false` if any ReplicatedVolume resources exist
- A `ValidatingAdmissionPolicy` (deployed when `newControlPlane=true`) also blocks reverting `newControlPlane` from `true` to `false` at the Kubernetes API level
- Kubernetes hook (`sync-control-plane-migration-state`) watches the ConfigMap and sets `sdsReplicatedVolume.internal.controlPlaneMigration` to trigger Helm template re-renders
- Components deployment depends on both `newControlPlane` and `internal.controlPlaneMigration`

## State Diagram (High-Level Migration Flow)

```mermaid
stateDiagram-v2
    [*] --> not_started : Initial state (newControlPlane=false)

    not_started --> stage1_started : newControlPlane=true, migrator Job starts
    stage1_started --> stage1_completed : Migrator creates RV/RVR/LLV/DRBDr/RVA
    stage1_completed --> stage2_started : Agent & Controller deployed and ready
    stage2_started --> stage2_completed : RV readiness confirmed
    stage2_completed --> stage3_started : Verification begins
    stage3_started --> all_completed : Verification passed
    all_completed --> [*] : New CSI deployed, migration Job removed

    note right of not_started
      ConfigMap: control-plane-migration
      Namespace: d8-sds-replicated-volume
      Field: .data.state
    end note
```

## Hooks Implementation

### OnBeforeHelm Hook (`guard-against-reset-newControlPlane`)

Registered with `OnBeforeHelm` (order 5). Prevents accidentally reverting to the old control-plane:

```mermaid
flowchart TD
    A[OnBeforeHelm run] --> B{newControlPlane == false?}
    B -->|No / true| F[Do nothing, allow Helm release]
    B -->|Yes / false| C{Any ReplicatedVolume\nresources exist in cluster?}
    C -->|Yes| D[Return error:\n'cannot set newControlPlane=false'\nHelm release blocked]
    C -->|No| E[Allow Helm release]
    C -->|List error| G[Log warning, allow Helm release\nCRD likely not installed yet]
```

Source: `hooks/go/085-control-plane-migration/control-plane-migration.go` — `guardAgainstResetNewControlPlane()`

### Kubernetes Hook (`sync-control-plane-migration-state`)

Watches ConfigMap `control-plane-migration` in `d8-sds-replicated-volume` namespace. Runs on Added/Modified/Deleted events and on synchronization (`ExecuteHookOnSynchronization: true`):

```mermaid
flowchart TB
    subgraph "Kubernetes Hook: sync-control-plane-migration-state"
        start["Start on ConfigMap events AND synchronization"]
        decision1{"ConfigMap snapshot\nexists?"}
        read_state["Read .data.state from ConfigMap\n(default to 'not_started' if empty)"]
        update_internal["Set sdsReplicatedVolume.internal.controlPlaneMigration\n= state"]
        do_nothing["Do nothing — ConfigMap does not exist"]

        start --> decision1
        decision1 -->|Yes| read_state --> update_internal
        decision1 -->|No| do_nothing
    end
```

Source: `hooks/go/085-control-plane-migration/control-plane-migration.go` — `syncControlPlaneMigrationState()`

## Component Deployment Matrix

Helm templates conditionally deploy components based on `newControlPlane` and `internal.controlPlaneMigration`:

| Component | Condition | Deployed when |
|-----------|-----------|---------------|
| **LINSTOR stack** (controller, satellite, affinity-controller, scheduler-extender, old controller, SPAAS, certs, metadata-backup) | `newControlPlane == false` | Only in legacy mode |
| **Old CSI driver** (linstor-csi) | `newControlPlane == false` | Only in legacy mode |
| **Webhooks** (deployment, service, ValidatingWebhookConfiguration) | `newControlPlane == true` | Always when new CP enabled |
| **ValidatingAdmissionPolicy** (blocks reverting newControlPlane) | `newControlPlane == true` | Always when new CP enabled |
| **Migration Job** (linstor-migrator) | `newControlPlane == true` AND state != `all_completed` | While migration is in progress |
| **Agent** (DaemonSet) | `newControlPlane == true` AND state NOT IN (`not_started`, `stage1_started`) | After stage 1 completed |
| **Controller** (Deployment) | `newControlPlane == true` AND state NOT IN (`not_started`, `stage1_started`) | After stage 1 completed |
| **New CSI driver** (CSIDriver, controller, RBAC, VolumeSnapshotClass) | `newControlPlane == true` AND state == `all_completed` | Only after migration fully completed |

### Deployment Timeline

```
newControlPlane=false          newControlPlane=true
        │                              │
        ▼                              ▼
┌───────────────┐              ┌───────────────────────────────────────────────────────────────────┐
│ LINSTOR stack │              │ Webhooks + ValidatingAdmissionPolicy                              │
│ Old CSI       │              ├─────────────────────────────────────────────────────┬─────────────┤
│               │              │ Migration Job                                       │ (removed)   │
│               │              ├─────────────────┬───────────────────────────────────┤             │
│               │              │                 │ Agent + Controller                │             │
│               │              │                 ├───────────────────────────────────┼─────────────┤
│               │              │                 │                                   │ New CSI     │
└───────────────┘              └─────────────────┴───────────────────────────────────┴─────────────┘
                               not_started ──► stage1_completed ──────────────────► all_completed
```

## Recovery Procedure

If migration Job fails:

1. Delete the failed Job:
   ```bash
   kubectl delete job <migration-job> -n d8-sds-replicated-volume
   ```

2. Delete the existing ConfigMap (if it exists):
   ```bash
   kubectl delete configmap control-plane-migration -n d8-sds-replicated-volume
   ```

3. Recreate the ConfigMap with the desired state:
   ```bash
   cat <<EOF | kubectl apply -f -
   apiVersion: v1
   kind: ConfigMap
   metadata:
     name: control-plane-migration
     namespace: d8-sds-replicated-volume
   data:
     state: not_started
   EOF
   ```

4. The hook will detect the new ConfigMap state and allow the module templates to recreate the Job to retry migration.

## Important Notes

- The Kubernetes hook does NOT automatically recreate the ConfigMap when it is deleted.
- If the ConfigMap is deleted, the internal state value remains unchanged until a new ConfigMap is created manually.
- `ExecuteHookOnSynchronization` is enabled (`true`), ensuring the hook runs during initial synchronization and captures the ConfigMap state properly.
- Setting `newControlPlane=true` immediately stops all LINSTOR components and the old CSI driver.
- The new CSI driver is deployed **only** after migration reaches `all_completed`.

## Migrator Workflow

The `linstor-migrator` is a standalone CLI tool (run as a Kubernetes Job) that transfers entities from LINSTOR CRs into the new control-plane CRs: ReplicatedVolume (RV), ReplicatedVolumeReplica (RVR), ReplicatedVolumeAttachment (RVA), DRBDResource (DRBDr), and LVMLogicalVolume (LLV).

The migrator is **idempotent** — it can be safely restarted at any point.

### Migrator Internal Flow

```mermaid
flowchart TD
    Start([linstor-migrator starts]) --> CheckNewCP{New control-plane\nCRD exists?}
    CheckNewCP -->|No| ErrExit([Log error and exit with code 1])
    CheckNewCP -->|Yes| CheckLinstor{LINSTOR\nCRD exists?}

    CheckLinstor -->|No| SetCompleted1[Set ConfigMap state = all_completed]
    SetCompleted1 --> GracefulExit([Log: no migration needed\nGraceful exit])

    CheckLinstor -->|Yes| EnsureCM[Ensure ConfigMap\ncontrol-plane-migration exists\nwith state = not_started]
    EnsureCM --> ReadState{Read current\nstate from ConfigMap}

    ReadState -->|all_completed| AlreadyDone([Log: migration already completed\nGraceful exit])

    ReadState -->|stage2_completed| RunStage3
    ReadState -->|stage1_completed| RunStage2

    ReadState -->|not_started /\nstage1_started /\nstage2_started /\nstage3_started| CollectPVs

    CollectPVs[List replicated PVs\nCSI driver = replicated.csi.storage.deckhouse.io]
    CollectPVs --> HasPVs{Replicated\nPVs found?}

    HasPVs -->|No| SetCompleted2[Set ConfigMap state = all_completed]
    SetCompleted2 --> GracefulExit2([Log: no replicated PVs\nGraceful exit])

    HasPVs -->|Yes| RunStage1

    subgraph "Stage 1: Resource Migration"
        RunStage1[Set state = stage1_started\nLoad LINSTOR DB, RSP, RSC,\nVolumeAttachments, existing RVRs]
        RunStage1 --> LoopPV

        LoopPV[For each replicated PV]
        LoopPV --> CreateRV[Create RV\nname = pvName\nspec: size, storageClassName, maxAttachments=1]
        CreateRV --> LoopRes[For each LINSTOR resource of this PV]
        LoopRes --> DetermineType{Resource\nflags?}

        DetermineType -->|0 = Diskful| CreateRVR_D[Create RVR\nname = pvName-nodeID\ntype = Diskful\nlvmVG, thinPool]
        DetermineType -->|388 = TieBreaker| CreateRVR_T[Create RVR\nname = pvName-nodeID\ntype = TieBreaker]
        DetermineType -->|260 = Diskless| CreateRVR_A[Create RVR\nname = pvName-nodeID\ntype = Access]

        CreateRVR_D --> CreateLLV[Create LLV\nname = pvName_00000\nactualLVName, lvmVG, type, size]
        CreateRVR_T --> CreateDRBDr
        CreateRVR_A --> CreateDRBDr
        CreateLLV --> CreateDRBDr

        CreateDRBDr[Create DRBDResource\nname = rvrName\nnodeID from LINSTOR\nmaintenance = NoResourceReconciliation\npeers from LINSTOR DB]
        CreateDRBDr --> NextRes{More LINSTOR\nresources?}
        NextRes -->|Yes| LoopRes
        NextRes -->|No| CreateRVA

        CreateRVA{VolumeAttachment\nexists for this PV?}
        CreateRVA -->|Yes| DoCreateRVA[Create RVA\nname = pvName-nodeName\nspec: rvName, nodeName]
        CreateRVA -->|No| NextPV
        DoCreateRVA --> NextPV

        NextPV{More PVs?}
        NextPV -->|Yes| LoopPV
        NextPV -->|No| Stage1Done[Set state = stage1_completed]
    end

    Stage1Done --> RunStage2

    subgraph "Stage 2: Wait for RV Readiness (stub)"
        RunStage2[Set state = stage2_started\nWait 30 seconds\nSet state = stage2_completed]
    end

    RunStage2 --> RunStage3

    subgraph "Stage 3: Verification (stub)"
        RunStage3[Set state = stage3_started\nWait 10 seconds\nSet state = all_completed]
    end

    RunStage3 --> Done([Migration finished\nGraceful exit])
```

### Resources Created per PV

For each replicated PersistentVolume, the migrator creates:

| Resource | Name Pattern | Key Fields |
|----------|-------------|------------|
| ReplicatedVolume | `<pvName>` | size, replicatedStorageClassName, maxAttachments=1 |
| ReplicatedVolumeReplica | `<pvName>-<linstorNodeID>` | replicatedVolumeName, nodeName, type (Diskful/TieBreaker/Access), lvmVG (Diskful only) |
| LVMLogicalVolume | `<pvName>_00000` | actualLVName, lvmVG, type (Thin/Thick), size (Diskful replicas only) |
| DRBDResource | `<pvName>-<linstorNodeID>` | nodeName, nodeID, type, systemNetworks, maintenance=NoResourceReconciliation, peers |
| ReplicatedVolumeAttachment | `<pvName>-<nodeName>` | replicatedVolumeName, nodeName (only if VolumeAttachment exists) |

### Idempotency

All resource creation functions use a **create-if-not-exists** pattern:
1. Attempt `Create` on the API server.
2. If `AlreadyExists` — log and skip.
3. If another error — fail immediately.

On restart, the migrator reads the current state from the ConfigMap and skips already completed stages. Within Stage 1, resources that were already created are safely skipped.
