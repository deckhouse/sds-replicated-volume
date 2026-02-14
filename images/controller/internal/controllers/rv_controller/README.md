# rv_controller

This controller manages `ReplicatedVolume` (RV) resources by orchestrating datamesh formation, normal operation, and deletion.

## Purpose

The controller reconciles `ReplicatedVolume` with:

1. **Configuration initialization** — copies resolved configuration from `ReplicatedStorageClass` (RSC) into RV status
2. **Datamesh formation** — creates replicas, establishes DRBD connectivity, bootstraps data synchronization
3. **Normal operation** — steady-state datamesh lifecycle (attach handling, scaling, etc.) *(TODO)*
4. **Deletion** — cleans up child resources (RVRs, RVAs) and datamesh state

## Interactions

| Direction | Resource/Controller | Relationship |
|-----------|---------------------|--------------|
| ← input | ReplicatedStorageClass | Reads configuration (replication, topology, storage pool) |
| ← input | ReplicatedStoragePool | Reads eligible nodes, system networks, zones for formation |
| ← input | ReplicatedVolumeReplica | Reads replica status (scheduling, preconfiguration, connectivity, data sync) |
| ← input | ReplicatedVolumeAttachment | Reads attachment status (for deletion decisions) |
| → manages | ReplicatedVolumeReplica | Creates/deletes during formation and deletion |
| → manages | ReplicatedVolumeAttachment | Manages finalizers; updates conditions during deletion |
| → manages | DRBDResourceOperation | Creates for data bootstrap during formation |

## Algorithm

The controller reconciles individual ReplicatedVolumes:

```
if rv deleted (NotFound):
    if orphaned RVAs exist:
        reconcileOrphanedRVAs (set conditions + remove finalizers) → Done
    else → Done

if shouldDelete (DeletionTimestamp + no attached members + no Detach transitions):
    reconcileDeletion (update RVA conditions → force-delete RVRs → clear datamesh members)
    reconcileRVAFinalizers (remove RVA finalizers — after conditions are set)
    reconcileMetadata (remove finalizer if no children left) → Done

ensure metadata (finalizer + labels)
reconcileRVAFinalizers (add/remove RVA finalizers)
reconcileRVRFinalizers (add/remove RVR finalizers)

ensure configuration from RSC
ensure datamesh pending replica transitions (sync from RVR statuses)

if configuration exists:
    if formation in progress (DatameshRevision == 0 or Formation transition active):
        reconcile formation (3-phase process)
    else:
        reconcile normal operation (TODO)

ensure ConfigurationReady condition
patch status if changed
```

## Reconciliation Structure

```
Reconcile (root) [Pure orchestration]
├── getRV
├── rv == nil → getRVAs → reconcileOrphanedRVAs (set conditions + remove finalizers)
├── getRSC, getRVAs, getRVRsSorted
├── rvShouldNotExist (DeletionTimestamp + no attached + no Detach transitions) →
│   ├── reconcileDeletion [In-place reconciliation] ← details
│   │   ├── isRVADeletionConditionsInSync / applyRVADeletionConditions
│   │   ├── deleteRVRWithForcedFinalizerRemoval (loop)
│   │   └── clear datamesh members + patchRVStatus
│   ├── reconcileRVAFinalizers [Target-state driven]
│   │   ├── add RVControllerFinalizer to non-deleting RVAs
│   │   └── remove RVControllerFinalizer from deleting RVAs (when safe)
│   │       ├── hasOtherNonDeletingRVAOnNode (duplicate check)
│   │       └── isNodeAttachedOrDetaching (datamesh state check)
│   └── reconcileMetadata [Target-state driven] (remove finalizer)
├── reconcileMetadata [Target-state driven]
│   ├── isRVMetadataInSync
│   ├── applyRVMetadata (finalizer + labels)
│   └── patchRV
├── reconcileRVAFinalizers [Target-state driven] (same as above)
├── reconcileRVRFinalizers [Target-state driven] (same as above)
├── ensureRVConfiguration
├── ensureDatameshPendingReplicaTransitions ← details
├── reconcileFormation [Pure orchestration]
│   ├── reconcileFormationPhasePreconfigure [Pure orchestration] ← details
│   │   ├── applyFormationTransition
│   │   ├── create/delete RVRs (guards for deleting/misplaced, replica count management)
│   │   ├── wait for deleting replicas cleanup
│   │   ├── safety checks (addresses, eligible nodes, spec mismatch, backing volume size)
│   │   └── reconcileFormationRestartIfTimeoutPassed
│   ├── reconcileFormationPhaseEstablishConnectivity [Pure orchestration] ← details
│   │   ├── generateSharedSecret + applyDatameshMember
│   │   ├── computeTargetQuorum
│   │   ├── verify configured, connected, ready for data bootstrap
│   │   └── reconcileFormationRestartIfTimeoutPassed
│   └── reconcileFormationPhaseBootstrapData [Pure orchestration] ← details
│       ├── createDRBDROp (new-current-uuid)
│       ├── verify operation status + UpToDate replicas
│       ├── reconcileFormationRestartIfTimeoutPassed
│       └── applyFormationTransitionAbsent (formation complete)
├── reconcileNormalOperation [Pure orchestration] (TODO)
├── ensureConditionConfigurationReady ← details
└── patchRVStatus
```

Links to detailed algorithms: [`reconcileDeletion`](#reconciledeletion-details), [`ensureDatameshPendingReplicaTransitions`](#ensuredatameshpendingreplicatransitions-details), [`reconcileFormationPhasePreconfigure`](#reconcileformationphasepreconfigure-details), [`reconcileFormationPhaseEstablishConnectivity`](#reconcileformationphaseestablishconnectivity-details), [`reconcileFormationPhaseBootstrapData`](#reconcileformationphasebootstrapdata-details), [`ensureConditionConfigurationReady`](#ensureconditionconfigurationready-details)

## Algorithm Flow

```mermaid
flowchart TD
    Start([Reconcile]) --> GetRV[Get RV]
    GetRV -->|NotFound| CheckOrphanedRVAs{Orphaned RVAs?}
    CheckOrphanedRVAs -->|No| Done1([Done])
    CheckOrphanedRVAs -->|Yes| OrphanedRVAs["reconcileOrphanedRVAs<br/>(set conditions + remove finalizers)"]
    OrphanedRVAs --> Done1
    GetRV --> LoadDeps[Load RSC, RVAs, RVRs]

    LoadDeps --> CheckDelete{rvShouldNotExist?}
    CheckDelete -->|Yes| Deletion[reconcileDeletion]
    Deletion --> RVAFinDel[reconcileRVAFinalizers]
    RVAFinDel --> MetaDel["reconcileMetadata<br/>(remove finalizer)"]
    MetaDel --> Done3([Done])

    CheckDelete -->|No| Meta[reconcileMetadata]
    Meta --> RVAFin[reconcileRVAFinalizers]
    RVAFin --> RVRFin[reconcileRVRFinalizers]
    RVRFin --> EnsureConfig[ensureRVConfiguration +<br/>ensureDatameshPendingReplicaTransitions]
    EnsureConfig --> CheckConfig{Configuration exists?}
    CheckConfig -->|No| Conditions

    CheckConfig -->|Yes| CheckForming{Formation in progress?}
    CheckForming -->|Yes| Formation[reconcileFormation]
    CheckForming -->|No| NormalOp[reconcileNormalOperation]

    Formation --> Conditions
    NormalOp --> Conditions

    Conditions[ensureConditionConfigurationReady]
    Conditions --> PatchDecision{Changed?}
    PatchDecision -->|Yes| Patch[patchRVStatus]
    PatchDecision -->|No| EndNode([Done])
    Patch --> EndNode
```

## Conditions

### ConfigurationReady

Indicates whether the RV configuration is initialized and matches the storage class.

| Status | Reason | When |
|--------|--------|------|
| True | Ready | Configuration matches storage class generation |
| False | WaitingForStorageClass | RSC not found or RSC configuration not ready |
| False | ConfigurationRolloutInProgress | ConfigurationGeneration not yet set (initial rollout) |
| False | StaleConfiguration | RV configuration generation does not match RSC generation |

## Formation Phases

Datamesh formation is a 3-phase process that creates and configures DRBD replicas. Each phase has a timeout; if progress stalls, formation restarts from scratch.

### Phase 1: Preconfigure

Creates diskful replicas and waits for them to become preconfigured (DRBD setup complete, ready for datamesh membership).

**Steps:**
1. Initialize datamesh configuration (SystemNetworkNames, Size, DatameshRevision=1)
2. Identify misplaced replicas (SatisfyEligibleNodes=False) and deleting replicas (DeletionTimestamp set)
3. Collect active diskful replicas (excluding misplaced and deleting)
4. Create missing diskful replicas only when no deleting or misplaced replicas exist (prevents zombie accumulation)
5. Remove excess/misplaced replicas
6. Wait for all deleting replicas to be fully removed (restart formation if timeout)
7. Wait for scheduling and preconfiguration (replicas split into pending scheduling / scheduling failed / preconfiguring; scheduling failure messages from RVR Scheduled=False conditions are shown inline)
8. Safety checks: addresses, eligible nodes, spec consistency, backing volume size

### Phase 2: Establish Connectivity

Adds preconfigured replicas to the datamesh and waits for DRBD peer connections.

**Steps:**
1. Generate shared secret for DRBD peer authentication
2. Add diskful replicas as datamesh members (with zone, addresses, LVG info)
3. Set quorum parameters
4. Wait for all replicas to apply DRBD configuration (DRBDConfigured=True)
5. Wait for all replicas to connect to each other (ConnectionState=Connected)
6. Wait for data bootstrap readiness (BackingVolume=Inconsistent + Replication=Established)

### Phase 3: Bootstrap Data

Triggers initial data synchronization via DRBDResourceOperation and waits for completion.

**Steps:**
1. Create DRBDResourceOperation (type: CreateNewUUID)
   - Single replica (any pool type): clear-bitmap (no peers to synchronize with)
   - Multiple replicas, thin provisioning: clear-bitmap (no full resync needed)
   - Multiple replicas, thick provisioning: force-resync (full data synchronization)
2. Wait for operation to succeed
3. Wait for all replicas to reach UpToDate state
4. Remove Formation transition (formation complete)

**Timeout calculation:**
- Base: 1 minute
- Force-resync (multi-replica thick provisioning): + volume size / 100 Mbit/s (worst-case bandwidth estimate)
- Clear-bitmap (single replica or thin provisioning): base only

### Formation Restart

When formation stalls (any safety check fails or progress timeout is exceeded), formation restarts:

1. Wait for timeout since formation started (to avoid thrashing)
2. Log error (formation timed out)
3. Delete formation DRBDResourceOperation if exists
4. Delete all replicas (with finalizer removal)
5. Reset all status fields (Configuration, DatameshRevision, Datamesh, transitions)
6. Re-initialize configuration from RSC (to avoid intermediate nil state that would trigger unnecessary RSC reconciliation)
7. Requeue for fresh start

## Managed Metadata

| Type | Key | Managed On | Purpose |
|------|-----|------------|---------|
| Finalizer | `sds-replicated-volume.deckhouse.io/rv-controller` | RV | Prevent deletion while child resources exist |
| Label | `sds-replicated-volume.deckhouse.io/replicated-storage-class` | RV | Link to ReplicatedStorageClass |
| Finalizer | `sds-replicated-volume.deckhouse.io/rv-controller` | RVA | Prevent deletion while node is attached or detaching |
| Finalizer | `sds-replicated-volume.deckhouse.io/rv-controller` | RVR | Prevent deletion while RVR is a datamesh member or leaving datamesh; force-removed during formation restart / RV deletion |
| OwnerRef | controller reference | DRBDResourceOperation | Owner reference to RV |

## Watches

| Resource | Events | Handler |
|----------|--------|---------|
| ReplicatedVolume | Generation, DeletionTimestamp, ReplicatedStorageClass label, Finalizers changes | For() (primary) |
| ReplicatedStorageClass | ConfigurationGeneration changes | mapRSCToRVs (index lookup) |
| ReplicatedVolumeAttachment | DeletionTimestamp, Finalizers, Attached condition status changes | mapRVAToRV |
| ReplicatedVolumeReplica | Conditions (Scheduled, DRBDConfigured, SatisfyEligibleNodes), DatameshPendingTransition, DatameshRevision, Addresses, BackingVolume, Peers, DeletionTimestamp, Finalizers changes | mapRVRToRV |
| DRBDResourceOperation | Create/Delete of *-formation ops, Phase changes, Generation changes | Owns() |

## Indexes

| Index | Field | Purpose |
|-------|-------|---------|
| `IndexFieldRVByReplicatedStorageClassName` | `spec.replicatedStorageClassName` | Map RSC events to RVs |
| `IndexFieldRVAByReplicatedVolumeName` | `spec.replicatedVolumeName` | List RVAs for an RV |
| `IndexFieldRVRByReplicatedVolumeName` | `spec.replicatedVolumeName` | List RVRs for an RV |

## Data Flow

```mermaid
flowchart TD
    subgraph inputs [Inputs]
        RSCStatus[RSC.status.configuration]
        RSP[RSP.status]
        RVRStatus[RVR.status]
        RVAStatus[RVA.status]
    end

    subgraph reconcilers [Reconcilers]
        ReconcileMeta[reconcileMetadata]
        ReconcileFormation[reconcileFormation]
        ReconcileDeletion[reconcileDeletion]
    end

    subgraph ensures [Ensure Helpers]
        EnsureConfig[ensureRVConfiguration]
        EnsurePending[ensureDatameshPendingReplicaTransitions]
        EnsureCondConfig[ensureConditionConfigurationReady]
    end

    subgraph outputs [Outputs]
        RVMeta[RV metadata]
        RVStatus[RV.status]
        RVRManaged[RVR create/delete]
        DRBDROp[DRBDResourceOperation]
        RVAConditions[RVA conditions]
    end

    RSCStatus --> EnsureConfig
    RSCStatus --> EnsureCondConfig
    RVRStatus --> EnsurePending
    RVRStatus --> ReconcileFormation
    RSP --> ReconcileFormation
    RVAStatus --> ReconcileDeletion

    ReconcileMeta --> RVMeta
    EnsureConfig --> RVStatus
    EnsurePending --> RVStatus
    EnsureCondConfig --> RVStatus
    ReconcileFormation --> RVStatus
    ReconcileFormation --> RVRManaged
    ReconcileFormation --> DRBDROp
    ReconcileDeletion --> RVAConditions
    ReconcileDeletion --> RVRManaged
    ReconcileDeletion --> RVStatus
```

---

## Detailed Algorithms

### reconcileDeletion Details

**Purpose:** Handles RV deletion — updates RVA conditions, removes RVR finalizers and deletes RVRs, clears datamesh members.

**Algorithm:**

```mermaid
flowchart TD
    Start([reconcileDeletion]) --> UpdateRVAs[Update RVA conditions]
    UpdateRVAs --> LoopRVA{"For each RVA"}
    LoopRVA --> CheckRVASync{Conditions in sync?}
    CheckRVASync -->|Yes| SkipRVA[Skip]
    CheckRVASync -->|No| PatchRVA["Set Attached=False WaitingForRV<br/>Set Ready=False NotAttached<br/>Remove other conditions<br/>Patch RVA status"]

    SkipRVA --> LoopRVA
    PatchRVA --> LoopRVA

    LoopRVA -->|Done| DeleteRVRs["Delete all RVRs<br/>(with forced finalizer removal)"]
    DeleteRVRs --> CheckMembers{Datamesh members exist?}
    CheckMembers -->|Yes| ClearMembers["Clear datamesh members<br/>Patch RV status"]
    CheckMembers -->|No| End([Done])
    ClearMembers --> End
```

**Data Flow:**

| Input | Output |
|-------|--------|
| `rvas` | Patched RVA conditions (Attached=False, Ready=False) |
| `rvrs` | All RVRs deleted (finalizers removed first) |
| `rv.Status.Datamesh.Members` | Cleared to nil |

---

### ensureDatameshPendingReplicaTransitions Details

**Purpose:** Synchronizes `rv.Status.DatameshPendingReplicaTransitions` with the current `DatameshPendingTransition` from each RVR. Uses a sorted merge algorithm for determinism.

**Algorithm:**

```mermaid
flowchart TD
    Start([Start]) --> SortExisting[Sort existing entries by ID]
    SortExisting --> Merge["Sorted merge:<br/>existing × rvrs"]

    Merge --> CaseRemoved["existing entry not in rvrs → removed"]
    Merge --> CaseAdded["rvr entry not in existing → added<br/>(DeepCopy transition, set FirstObservedAt)"]
    Merge --> CaseEqual["names match, transition equal → keep"]
    Merge --> CaseUpdated["names match, transition differs → update<br/>(DeepCopy transition, reset FirstObservedAt)"]

    CaseRemoved --> Result
    CaseAdded --> Result
    CaseEqual --> Result
    CaseUpdated --> Result

    Result[Assign result if changed] --> End([Return EnsureOutcome])
```

**Data Flow:**

| Input | Description |
|-------|-------------|
| `rv.Status.DatameshPendingReplicaTransitions` | Existing pending transitions |
| `rvrs[].Status.DatameshPendingTransition` | Current pending transition per RVR |

| Output | Description |
|--------|-------------|
| `rv.Status.DatameshPendingReplicaTransitions` | Synchronized list (sorted by ID) |

---

### reconcileFormationPhasePreconfigure Details

**Purpose:** Creates diskful replicas and waits for them to become preconfigured (DRBD setup complete, ready for datamesh membership). Performs safety checks before advancing.

**Algorithm:**

```mermaid
flowchart TD
    Start([Start]) --> ApplyTransition["applyFormationTransition(Preconfigure)<br/>Init: DatameshRevision=1, SystemNetworkNames, Size"]

    ApplyTransition --> FindMisplaced["Find misplaced replicas<br/>(SatisfyEligibleNodes=False)"]
    FindMisplaced --> FindDeleting["Find deleting replicas<br/>(DeletionTimestamp set)"]
    FindDeleting --> CollectDiskful["Collect active diskful replicas<br/>(exclude misplaced + deleting)"]
    CollectDiskful --> ComputeCount[computeIntendedDiskfulReplicaCount]

    ComputeCount --> CheckClean{"No deleting and<br/>no misplaced?"}
    CheckClean -->|Yes| CreateLoop{"diskful.Len < target?"}
    CreateLoop -->|Yes| CreateRVR[createRVR]
    CreateRVR -->|AlreadyExists| Requeue1([DoneAndRequeue])
    CreateRVR --> CreateLoop
    CheckClean -->|No| SkipCreate[Skip creation]
    SkipCreate --> RemoveExcess

    CreateLoop -->|No| RemoveExcess{"diskful.Len > target?"}

    RemoveExcess -->|Yes| PickCandidate["Pick least-progressed replica<br/>(not scheduled > not preconfigured > any)"]
    PickCandidate --> RemoveExcess
    RemoveExcess -->|No| DeleteUnwanted["Delete replicas not in diskful set<br/>(misplaced, excess, externally created)"]

    DeleteUnwanted --> CheckDeleting{"Any replicas still<br/>deleting?"}
    CheckDeleting -->|Yes| WaitDeleting["Wait for cleanup /<br/>restart if timeout (30s)"]

    CheckDeleting -->|No| SplitScheduling["Split waitingScheduling into<br/>pendingScheduling + schedulingFailed<br/>(Scheduled=False)"]
    SplitScheduling --> WaitReady{"All scheduled<br/>and preconfigured?"}
    WaitReady -->|No| BuildMsg["computeFormationPreconfigureWaitMessage:<br/>only non-empty groups shown,<br/>scheduling failed includes inline<br/>error from Scheduled condition"]
    BuildMsg --> WaitTimeout1[Wait / restart if timeout]

    WaitReady -->|Yes| CheckAddresses{"All have required<br/>network addresses?"}
    CheckAddresses -->|No| WaitTimeout2[Wait / restart if timeout]

    CheckAddresses -->|Yes| CheckEligible{"All on eligible nodes?"}
    CheckEligible -->|No| WaitTimeout3[Wait / restart if timeout]

    CheckEligible -->|Yes| CheckSpec{"Spec matches<br/>pending transition?"}
    CheckSpec -->|No| WaitTimeout4[Wait / restart if timeout]

    CheckSpec -->|Yes| CheckBVSize{"Backing volume<br/>size sufficient?"}
    CheckBVSize -->|No| WaitTimeout5[Wait / restart if timeout]

    CheckBVSize -->|Yes| NextPhase([→ EstablishConnectivity])
```

**Data Flow:**

| Input | Description |
|-------|-------------|
| `rv.Spec.Size` | Target volume size |
| `rv.Status.Configuration.Replication` | Replication mode (determines replica count) |
| `rsp` | Storage pool view (eligible nodes, system network names) |
| `rvrs` | Current replicas (status: scheduled, preconfigured, addresses, backing volume) |

| Output | Description |
|--------|-------------|
| `rv.Status.DatameshRevision` | Set to 1 on first entry |
| `rv.Status.Datamesh.SystemNetworkNames` | Copied from RSP |
| `rv.Status.Datamesh.Size` | Copied from RV spec |
| RVR create/delete | Replica count adjusted |
| Formation transition messages | Progress/error reporting |

---

### reconcileFormationPhaseEstablishConnectivity Details

**Purpose:** Adds preconfigured replicas to the datamesh (with shared secret and quorum), then waits for DRBD configuration, peer connections, and replication establishment.

**Algorithm:**

```mermaid
flowchart TD
    Start([Start]) --> ApplyTransition["applyFormationTransition(EstablishConnectivity)"]
    ApplyTransition --> CollectDiskful[Collect active diskful replicas]

    CollectDiskful --> CheckMembers{Datamesh members<br/>already set?}
    CheckMembers -->|No| GenSecret[generateSharedSecret]
    GenSecret --> AddMembers["Add diskful replicas as datamesh members<br/>(zone, addresses, LVG from pending transition)"]
    AddMembers --> SetQuorum[computeTargetQuorum]
    SetQuorum --> IncrRevision["DatameshRevision++"]
    IncrRevision --> ReturnChanged([Return changed])

    CheckMembers -->|Yes| VerifyMembers{"Datamesh members<br/>match active RVRs?"}
    VerifyMembers -->|No| WaitRestart1[Wait / restart if timeout]

    VerifyMembers -->|Yes| CheckConfigured{"All replicas DRBD configured<br/>for current revision?"}
    CheckConfigured -->|No| WaitRestart2[Wait / restart if timeout]

    CheckConfigured -->|Yes| CheckConnected{"All replicas connected<br/>to all peers?"}
    CheckConnected -->|No| WaitRestart3[Wait / restart if timeout]

    CheckConnected -->|Yes| CheckBootstrapReady{"All replicas ready for<br/>data bootstrap?<br/>(Inconsistent + Established)"}
    CheckBootstrapReady -->|No| WaitRestart4[Wait / restart if timeout]

    CheckBootstrapReady -->|Yes| NextPhase([→ BootstrapData])
```

**Data Flow:**

| Input | Description |
|-------|-------------|
| `rvrs` | Replica status (DRBDConfigured, peers, backing volume state) |
| `rsp.EligibleNodes` | Zone information for datamesh members |

| Output | Description |
|--------|-------------|
| `rv.Status.Datamesh.SharedSecret` | Generated DRBD shared secret |
| `rv.Status.Datamesh.Members` | Datamesh member list |
| `rv.Status.Datamesh.Quorum` | Quorum threshold |
| `rv.Status.DatameshRevision` | Incremented revision |

---

### reconcileFormationPhaseBootstrapData Details

**Purpose:** Creates a DRBDResourceOperation to trigger initial data synchronization, waits for completion, and finalizes formation.

**Algorithm:**

```mermaid
flowchart TD
    Start([Start]) --> ApplyTransition["applyFormationTransition(BootstrapData)"]
    ApplyTransition --> GetOp[getDRBDROp]
    GetOp --> CheckStale{"Operation exists but<br/>created before current<br/>formation start?"}
    CheckStale -->|Yes| DeleteStale[Delete stale operation]
    DeleteStale --> CheckExists

    CheckStale -->|No| CheckExists{Operation exists?}
    CheckExists -->|No| CreateOp["createDRBDROp<br/>Type: CreateNewUUID<br/>single/thin: clear-bitmap<br/>multi+thick: force-resync"]
    CreateOp -->|AlreadyExists| Requeue([DoneAndRequeue])
    CreateOp --> CheckStatus

    CheckExists -->|Yes| VerifyParams{"Parameters match<br/>expected?"}
    VerifyParams -->|No| WaitRestart1[Wait / restart if timeout]
    VerifyParams -->|Yes| CheckStatus

    CheckStatus{"Operation status?"}
    CheckStatus -->|Failed| WaitRestart2[Wait / restart if timeout]
    CheckStatus -->|Pending/Running| WaitTimeout[Wait / restart if dataBootstrapTimeout]
    CheckStatus -->|Succeeded| CheckUpToDate{"All replicas<br/>UpToDate?"}

    CheckUpToDate -->|No| WaitSync[Wait / restart if dataBootstrapTimeout]
    CheckUpToDate -->|Yes| Complete["applyFormationTransitionAbsent<br/>(formation complete!)"]
    Complete --> End([Continue])
```

**Data Flow:**

| Input | Description |
|-------|-------------|
| `rv.Status.Datamesh.Members` | Diskful members (target for operation, count determines single/multi-replica) |
| `rsp.Type` | LVM or LVMThin (together with replica count determines sync mode) |
| `rv.Status.Datamesh.Size` | Volume size (for force-resync timeout calculation) |

| Output | Description |
|--------|-------------|
| `DRBDResourceOperation` | Created/verified data bootstrap operation |
| `rv.Status.DatameshTransitions` | Formation transition removed on success |

---

### ensureConditionConfigurationReady Details

**Purpose:** Sets the `ConfigurationReady` condition based on RSC availability and configuration generation matching.

**Algorithm:**

```mermaid
flowchart TD
    Start([Start]) --> CheckRSC{RSC exists?}
    CheckRSC -->|No| SetWaiting1["False: WaitingForStorageClass<br/>(RSC not found)"]
    SetWaiting1 --> End([Return])

    CheckRSC -->|Yes| CheckRSCConfig{RSC has configuration?}
    CheckRSCConfig -->|No| SetWaiting2["False: WaitingForStorageClass<br/>(RSC configuration not ready)"]
    SetWaiting2 --> End

    CheckRSCConfig -->|Yes| CheckGen{ConfigurationGeneration == 0?}
    CheckGen -->|Yes| SetRollout["False: ConfigurationRolloutInProgress"]
    SetRollout --> End

    CheckGen -->|No| CheckMatch{RV generation == RSC generation?}
    CheckMatch -->|Yes| SetReady["True: Ready"]
    CheckMatch -->|No| SetStale["False: StaleConfiguration"]
    SetReady --> End
    SetStale --> End
```

**Data Flow:**

| Input | Description |
|-------|-------------|
| `rsc` | ReplicatedStorageClass (may be nil) |
| `rsc.Status.Configuration` | RSC configuration availability |
| `rsc.Status.ConfigurationGeneration` | RSC generation for comparison |
| `rv.Status.ConfigurationGeneration` | RV's stored generation |

| Output | Description |
|--------|-------------|
| `ConfigurationReady` condition | Reports configuration state |
