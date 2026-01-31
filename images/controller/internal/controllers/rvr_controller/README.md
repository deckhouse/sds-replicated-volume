# rvr_controller

This controller manages `ReplicatedVolumeReplica` (RVR) resources by reconciling their backing volumes (LVMLogicalVolume) and DRBD resources.

## Purpose

The controller reconciles `ReplicatedVolumeReplica` with:

1. **Metadata management** — finalizers and labels on RVR and child resources
2. **Backing volume management** — creates, resizes, and deletes LVMLogicalVolume for diskful replicas
3. **DRBD resource management** — creates, configures, resizes, and deletes DRBDResource
4. **Conditions** — reports backing volume readiness (`BackingVolumeReady`) and DRBD configuration state (`Configured`)

## Interactions

| Direction | Resource/Controller | Relationship |
|-----------|---------------------|--------------|
| ← input | ReplicatedVolume | Reads datamesh configuration (size, membership, type transitions) |
| ← input | ReplicatedStoragePool | Reads eligible nodes for eligibility verification |
| ← input | Pod (agent) | Checks agent readiness on target node before configuring DRBD |
| → manages | LVMLogicalVolume | Creates/resizes/deletes backing volumes |
| → manages | DRBDResource | Creates/configures/resizes/deletes DRBD resources |

## Algorithm

The controller reconciles individual RVRs:

```
shouldDelete = (RVR deleted) AND (only our finalizer remains)

if shouldDelete:
    delete children → remove finalizer → Done

if RVR exists:
    ensure metadata (finalizer + labels)

reconcile backing volume:
    if diskless or deleting → delete all LLVs
    else → ensure intended LLV exists and is ready

reconcile DRBD resource:
    if deleting → delete DRBDResource
    if node not assigned → Configured=False PendingScheduling
    if RV not ready → Configured=False WaitingForReplicatedVolume
    compute target DRBDR spec (type, size, peers)
    create or patch DRBDResource
    if agent not ready → Configured=False AgentNotReady
    if DRBDR pending → Configured=False ApplyingConfiguration
    if DRBDR failed → Configured=False ConfigurationFailed
    if not datamesh member → Configured=False PendingDatameshJoin
    else → Configured=True

patch status if changed
```

## Reconciliation Structure

```
Reconcile (root) [Pure orchestration]
├── getRVR
├── getDRBDR
├── getLLVs
├── getRV
├── reconcileMetadata [Target-state driven]
│   ├── isRVRMetadataInSync
│   ├── applyRVRMetadata (finalizer + labels)
│   └── patchRVR
├── reconcileBackingVolume [In-place reconciliation] ← details
│   ├── rvrShouldNotExist check
│   ├── computeActualBackingVolume
│   ├── computeIntendedBackingVolume
│   ├── createLLV / patchLLV metadata / patchLLV resize
│   ├── reconcileLLVsDeletion (cleanup obsolete)
│   └── applyRVRBackingVolumeReadyCondTrue/False
├── reconcileDRBDResource [In-place reconciliation] ← details
│   ├── rvrShouldNotExist → deleteDRBDR + applyRVRConfiguredCondFalse NotApplicable
│   ├── node not assigned → applyRVRConfiguredCondFalse PendingScheduling
│   ├── RV/datamesh not ready → applyRVRConfiguredCondFalse WaitingForReplicatedVolume
│   ├── computeIntendedType / computeTargetType / computeTargetDRBDRReconciliationCache
│   ├── createDRBDR (computeTargetDRBDRSpec) / patchDRBDR
│   ├── applyRVRDRBDRReconciliationCache
│   ├── getAgentReady → applyRVRConfiguredCondFalse AgentNotReady
│   ├── computeActualDRBDRConfigured → ApplyingConfiguration / ConfigurationFailed
│   ├── applyRVRAddresses
│   └── applyRVRConfiguredCondTrue Configured / applyRVRConfiguredCondFalse PendingDatameshJoin
├── ensureAttachmentStatus ← details
│   ├── computeIntendedType
│   ├── applyRVRAttachedCond*
│   ├── applyRVRDevicePath
│   └── applyRVRDeviceIOSuspended
├── ensureStatusPeers ← details
│   └── mirrors drbdr.Status.Peers to rvr.Status.Peers
├── ensureConditionFullyConnected ← details
│   └── applyRVRFullyConnectedCond*
├── ensureStatusBackingVolume ← details
│   └── applyRVRBackingVolumeStatus
├── ensureConditionBackingVolumeInSync ← details
│   ├── computeHasUpToDatePeer
│   ├── computeHasConnectedAttachedPeer
│   └── applyRVRBackingVolumeInSyncCond*
├── ensureQuorumStatus ← details
│   ├── ensureRVRStatusQuorumSummary
│   ├── applyRVRStatusQuorum
│   └── applyRVRReadyCond*
├── reconcileSatisfyEligibleNodesCondition [In-place reconciliation] ← details
│   ├── getNodeEligibility (from RSP)
│   └── applySatisfyEligibleNodesCond*
└── patchRVRStatus
```

Links to detailed algorithms: [`reconcileBackingVolume`](#reconcilebackingvolume-details), [`reconcileDRBDResource`](#reconciledrbdresource-details), [`ensureAttachmentStatus`](#ensureattachmentstatus-details), [`ensureStatusPeers`](#ensurestatuspeers-details), [`ensureConditionFullyConnected`](#ensureconditionfullyconnected-details), [`ensureStatusBackingVolume`](#ensurestatusbackingvolume-details), [`ensureConditionBackingVolumeInSync`](#ensureconditionbackingvolumeinsync-details), [`ensureQuorumStatus`](#ensurequorumstatus-details), [`reconcileSatisfyEligibleNodesCondition`](#reconcilesatisfyeligiblenodescondition-details)

## Algorithm Flow

High-level overview of reconciliation phases. See [Detailed Algorithms](#detailed-algorithms) for step-by-step breakdowns.

```mermaid
flowchart TD
    Start([Reconcile RVR]) --> Load[Load RVR, DRBDR, LLVs, RV]
    Load --> CheckExists{RVR exists?}

    CheckExists -->|No| Cleanup[Cleanup orphaned children]
    Cleanup --> Done1([Done])

    CheckExists -->|Yes| Meta[reconcileMetadata]
    Meta -->|Finalizer removed| Done2([Done])
    Meta --> BV[reconcileBackingVolume]
    BV --> DRBDR[reconcileDRBDResource]

    DRBDR --> StatusBlock

    subgraph StatusBlock [Status Ensure]
        Attach[ensureAttachmentStatus]
        Attach --> Peers[ensureStatusPeers]
        Peers --> PeersCond[ensureConditionFullyConnected]
        PeersCond --> BVStatus[ensureStatusBackingVolume]
        BVStatus --> BVInSync[ensureConditionBackingVolumeInSync]
        BVInSync --> Quorum[ensureQuorumStatus]
        Quorum --> SEN[reconcileSatisfyEligibleNodesCondition]
    end

    SEN --> Patch[Patch RVR status]
    Patch --> EndNode([Done])
```

## Conditions

### BackingVolumeReady

Indicates whether the backing volume (LVMLogicalVolume) is ready.

| Status | Reason | When |
|--------|--------|------|
| True | Ready | Backing volume exists and is ready |
| False | NotApplicable | Replica is diskless or being deleted |
| False | NotReady | Backing volume exists but not ready yet |
| False | PendingScheduling | Waiting for node or storage assignment |
| False | Provisioning | Creating new backing volume |
| False | ProvisioningFailed | Failed to create backing volume (validation error) |
| False | Reprovisioning | Creating new backing volume to replace existing one |
| False | ResizeFailed | Failed to resize backing volume (validation error) |
| False | Resizing | Resizing backing volume |
| False | WaitingForReplicatedVolume | Waiting for ReplicatedVolume to be ready |

### Configured

Indicates whether the replica's DRBD resource is configured.

| Status | Reason | When |
|--------|--------|------|
| True | Configured | DRBD resource is fully configured and replica is a datamesh member |
| False | AgentNotReady | Agent is not ready on the target node |
| False | ApplyingConfiguration | Waiting for agent to apply DRBD configuration |
| False | ConfigurationFailed | DRBD resource configuration failed |
| False | NotApplicable | Replica is being deleted |
| False | PendingDatameshJoin | DRBD preconfigured, waiting for datamesh membership |
| False | PendingScheduling | Waiting for node assignment |
| False | WaitingForReplicatedVolume | Waiting for ReplicatedVolume to be ready |

### Attached

Indicates whether the replica is attached (primary) and ready for I/O.

| Status | Reason | When |
|--------|--------|------|
| True | Attached | Attached and ready for I/O |
| True | DetachmentFailed | Expected detached but still attached |
| False | AttachmentFailed | Expected attached but not attached |
| False | IOSuspended | Attached but I/O is suspended |
| Unknown | AgentNotReady | Agent is not ready |
| Unknown | ApplyingConfiguration | Configuration is being applied |
| (absent) | - | No DRBDR exists or not applicable |

### BackingVolumeInSync

Indicates whether the local backing volume is in sync with peers.

| Status | Reason | When |
|--------|--------|------|
| True | InSync | Disk is fully up-to-date |
| False | Attaching | Disk is being attached |
| False | Detaching | Disk is being detached |
| False | DiskFailed | Disk failed due to I/O errors |
| False | NoDisk | No local disk attached |
| False | Synchronizing | Disk is synchronizing |
| False | SynchronizationBlocked | Sync blocked awaiting peer |
| False | UnknownState | Unknown disk state |
| Unknown | AgentNotReady | Agent is not ready |
| Unknown | ApplyingConfiguration | Configuration is being applied |
| (absent) | - | Diskless replica or no DRBDR |

### FullyConnected

Indicates whether the replica has established connections to all peers.

| Status | Reason | When |
|--------|--------|------|
| True | FullyConnected | Fully connected to all peers on all paths |
| True | ConnectedToAllPeers | All peers connected but not all paths established |
| False | NoPeers | No peers configured |
| False | NotConnected | Not connected to any peer |
| False | PartiallyConnected | Connected to some but not all peers |
| Unknown | AgentNotReady | Agent is not ready |
| (absent) | - | No DRBDR exists or not applicable |

### Ready

Indicates overall replica readiness for I/O (based on quorum state).

| Status | Reason | When |
|--------|--------|------|
| True | Ready | Ready for I/O (quorum message) |
| False | Deleting | Replica is being deleted |
| False | QuorumLost | Quorum is lost (quorum message) |
| Unknown | AgentNotReady | Agent is not ready |
| Unknown | ApplyingConfiguration | Configuration is being applied |

### SatisfyEligibleNodes

Indicates whether the replica satisfies the eligible nodes requirements from its storage pool.

| Status | Reason | When |
|--------|--------|------|
| True | Satisfied | Replica satisfies eligible nodes requirements |
| False | NodeMismatch | Node is not in the eligible nodes list |
| False | LVMVolumeGroupMismatch | Node is eligible, but LVMVolumeGroup is not allowed for this node |
| False | ThinPoolMismatch | Node and LVMVolumeGroup are eligible, but ThinPool is not allowed |
| Unknown | PendingConfiguration | Configuration not yet available (RSP not found) |
| (absent) | - | Node not yet assigned |

## Status Fields

The controller manages the following status fields on RVR:

| Field | Description | Source |
|-------|-------------|--------|
| `addresses` | DRBD addresses assigned to this replica | From DRBDR status |
| `backingVolume` | Backing volume info (size, state, LVG name, thin pool) | From DRBDR + LLV status |
| `drbdrReconciliationCache` | Cache of target configuration that DRBDR spec was last applied for | Computed |
| `deviceIOSuspended` | Whether I/O is suspended on the device | From DRBDR status |
| `devicePath` | Block device path when attached (e.g., /dev/drbd10012) | From DRBDR status |
| `peers` | Peer connectivity status | Merged from datamesh + DRBDR |
| `quorum` | Whether this replica has quorum | From DRBDR status |
| `quorumSummary` | Detailed quorum info (voting peers, thresholds) | Computed from DRBDR + peers |

### BackingVolume

The `backingVolume` field is a nested struct with information about the backing LVM logical volume. Only set for Diskful replicas.

| Field | Description |
|-------|-------------|
| `size` | Size of the backing LVM logical volume |
| `state` | Local disk state (UpToDate/Outdated/etc.) |
| `lvmVolumeGroupName` | Name of the LVM volume group |
| `lvmVolumeGroupThinPoolName` | Thin pool name (empty if thick provisioned) |

### DRBDRReconciliationCache

The `drbdrReconciliationCache` field caches the target configuration that DRBDR spec was last applied for. These fields are used to optimize controller operation by avoiding redundant computations and DRBDR spec updates. **Important**: these fields reflect the *target* revision, not the revision that DRBDR has actually transitioned to.

| Field | Description |
|-------|-------------|
| `datameshRevision` | Datamesh revision this replica was configured for |
| `drbdrGeneration` | Generation of the DRBDResource that was last targeted |
| `rvrType` | RVR type (Diskful/TieBreaker/Access) that was last targeted |

### PeerStatus

Each entry in `peers` contains:

| Field | Description |
|-------|-------------|
| `name` | Peer RVR name |
| `type` | Replica type (Diskful/TieBreaker/Access), empty if orphan |
| `attached` | Whether peer is attached (primary) |
| `connectionEstablishedOn` | System networks with established connection |
| `connectionState` | DRBD connection state |
| `backingVolumeState` | Peer's disk state |

### QuorumSummary

| Field | Description |
|-------|-------------|
| `connectedVotingPeers` | Count of connected voting peers |
| `quorum` | Quorum threshold |
| `connectedUpToDatePeers` | Count of connected UpToDate peers |
| `quorumMinimumRedundancy` | Minimum UpToDate nodes required |

## Backing Volume Management

The controller manages LVMLogicalVolume resources as backing storage for diskful replicas.

### When backing volume is needed

A backing volume is needed if **all** conditions are met:

1. **Replica type is Diskful** — diskless replicas do not need backing storage
2. **RVR is not being deleted** — no backing volume during deletion
3. **Configuration is complete** — nodeName and lvmVolumeGroupName are set

For replicas that are members of the datamesh:
- Type must be `Diskful` AND typeTransition must NOT be `ToDiskless`
- When transitioning to diskless, backing volume is removed first

### LLV naming

LVMLogicalVolume names are computed deterministically:

```
llvName = rvrName + "-" + fnv128(lvgName + thinPoolName)
```

For migration support: if an existing LLV is already referenced by DRBDResource on the same LVG/ThinPool, its name is reused.

### Size source

The backing volume size is taken from `rv.Status.Datamesh.Size` (after DRBD overhead adjustment), not directly from RV spec. This ensures consistency during resize operations when datamesh has not yet propagated the new size.

### Lifecycle

1. **Create**: When intended LLV does not exist, create it with ownerRef, finalizer, and labels
2. **Resize**: When LLV is ready but actual size < intended size, patch spec.size
3. **Delete**: Remove finalizer, then delete LLV

## Managed Metadata

| Type | Key | Managed On | Purpose |
|------|-----|------------|---------|
| Finalizer | `sds-replicated-volume.deckhouse.io/rvr-controller` | RVR | Prevent deletion while children exist |
| Finalizer | `sds-replicated-volume.deckhouse.io/rvr-controller` | LLV | Prevent premature deletion |
| Finalizer | `sds-replicated-volume.deckhouse.io/rvr-controller` | DRBDResource | Prevent premature deletion |
| Label | `sds-replicated-volume.deckhouse.io/replicated-volume` | RVR | Link to parent ReplicatedVolume |
| Label | `sds-replicated-volume.deckhouse.io/replicated-storage-class` | RVR | Link to ReplicatedStorageClass |
| Label | `sds-replicated-volume.deckhouse.io/lvm-volume-group` | RVR | Link to LVMVolumeGroup |
| Label | `sds-replicated-volume.deckhouse.io/replicated-volume` | LLV | Link to parent ReplicatedVolume |
| Label | `sds-replicated-volume.deckhouse.io/replicated-storage-class` | LLV | Link to ReplicatedStorageClass |
| OwnerRef | controller reference | LLV | Owner reference to RVR |
| OwnerRef | controller reference | DRBDResource | Owner reference to RVR |

## Watches

The controller watches six event sources:

| Resource | Events | Handler |
|----------|--------|---------|
| ReplicatedVolumeReplica | Generation changes, Finalizers changes | For() (primary) |
| LVMLogicalVolume | All fields (Status, Spec, Labels, Finalizers, OwnerRefs) | Owns() |
| DRBDResource | All fields | Owns() |
| ReplicatedVolume | DatameshRevision changes, ReplicatedStorageClassName changes | mapRVToRVRs |
| ReplicatedStoragePool | EligibleNodes changes (per-node) | rspEventHandler |
| Pod (agent) | Ready condition changes, Create/Delete | mapAgentPodToRVRs |

### RVR Predicates

- Reacts to Generation change (spec changes)
- Reacts to Finalizers change
- Skips pure Status updates and Labels/Annotations changes

### LLV Predicates

Intentionally empty: we need to react to all LLV fields (Status, Spec, Labels, Finalizers, OwnerReferences).

### DRBDResource Predicates

Intentionally empty: we need to react to all DRBDResource fields.

### RV Predicates

- Reacts to DatameshRevision changes (covers Size, membership changes, type transitions)
- Reacts to Spec.ReplicatedStorageClassName changes (for labels)
- Does not react to Create/Delete (RVRs handle their own lifecycle)

### RSP Predicates

- Reacts to eligibleNodes changes (compares old and new lists)
- On Create/Delete: always triggers
- On Update: triggers only if eligibleNodes differ

### RSP EventHandler

Custom EventHandler that computes changed nodes and enqueues only RVRs on those nodes:
- On Create: enqueues all RVRs on all eligible nodes
- On Update: computes nodes that were added/removed/modified, enqueues RVRs on those nodes
- On Delete: enqueues all RVRs that were on eligible nodes

Uses composite index to efficiently find RVRs by (replicatedVolumeName, nodeName).

### Agent Pod Predicates

- Filters to Pods in the agent namespace with label `app=agent`
- Reacts to Ready condition changes
- Reacts to Create/Delete events

## Indexes

| Index | Field | Purpose |
|-------|-------|---------|
| `IndexFieldLLVByRVROwner` | `metadata.ownerReferences.rvr` | List LVMLogicalVolumes owned by RVR |
| `IndexFieldRVRByReplicatedVolumeName` | `spec.replicatedVolumeName` | Map ReplicatedVolume events to RVRs |
| `IndexFieldRVRByNodeName` | `spec.nodeName` | Map agent Pod events to RVRs on the same node |
| `IndexFieldRVRByRVAndNode` | `spec.replicatedVolumeName+nodeName` | Find RVR by RV and node (composite) |
| `IndexFieldRVByStoragePoolName` | `status.configuration.storagePoolName` | Find RVs using a specific RSP |
| `IndexFieldPodByNodeName` | `spec.nodeName` | Find agent Pod on a specific node |

## Data Flow

```mermaid
flowchart TD
    subgraph inputs [Inputs]
        RVRSpec[RVR.spec]
        RVStatus[RV.status.datamesh]
        LLVs[LVMLogicalVolumes]
        DRBDR[DRBDResource]
        AgentPod[Agent Pod]
    end

    subgraph reconcilers [Reconcilers]
        ReconcileMeta[reconcileMetadata]
        ReconcileBV[reconcileBackingVolume]
        ReconcileDRBD[reconcileDRBDResource]
    end

    subgraph statusEnsure [Status Ensure]
        EnsureAttach[ensureAttachmentStatus]
        EnsureStatusPeers[ensureStatusPeers]
        EnsureCondFC[ensureConditionFullyConnected]
        EnsureBVStatus[ensureStatusBackingVolume]
        EnsureBVInSync[ensureConditionBackingVolumeInSync]
        EnsureQuorum[ensureQuorumStatus]
    end

    subgraph outputs [Outputs]
        RVRMeta[RVR metadata]
        RVRStatusConds[RVR.status.conditions]
        RVRStatusFields[RVR.status fields]
        LLVManaged[LVMLogicalVolume]
        DRBDRManaged[DRBDResource]
    end

    RVRSpec --> ReconcileMeta
    RVStatus --> ReconcileBV
    RVStatus --> ReconcileDRBD
    LLVs --> ReconcileBV
    DRBDR --> ReconcileDRBD
    AgentPod --> ReconcileDRBD

    ReconcileMeta --> RVRMeta
    ReconcileBV --> LLVManaged
    ReconcileBV -->|BackingVolumeReady| RVRStatusConds
    ReconcileDRBD --> DRBDRManaged
    ReconcileDRBD -->|Configured| RVRStatusConds
    ReconcileDRBD -->|drbdrReconciliationCache, addresses| RVRStatusFields

    DRBDR --> EnsureAttach
    DRBDR --> EnsureStatusPeers
    DRBDR --> EnsureCondFC
    DRBDR --> EnsureBVStatus
    DRBDR --> EnsureBVInSync
    DRBDR --> EnsureQuorum
    LLVs --> EnsureBVStatus

    EnsureAttach -->|Attached| RVRStatusConds
    EnsureAttach -->|devicePath, deviceIOSuspended| RVRStatusFields
    EnsureStatusPeers -->|peers| RVRStatusFields
    EnsureCondFC -->|FullyConnected| RVRStatusConds
    EnsureBVStatus -->|backingVolume| RVRStatusFields
    EnsureBVInSync -->|BackingVolumeInSync| RVRStatusConds
    EnsureQuorum -->|Ready| RVRStatusConds
    EnsureQuorum -->|quorum, quorumSummary| RVRStatusFields
```

---

## Detailed Algorithms

### reconcileBackingVolume Details

**Purpose**: Manages LVMLogicalVolume (LLV) lifecycle for diskful replicas — creation, resize, and deletion.

**Algorithm**:

```mermaid
flowchart TD
    Start([Start]) --> CheckDelete{RVR should not exist?}
    CheckDelete -->|Yes| DeleteAll[Delete all LLVs]
    DeleteAll --> SetNA1[BackingVolumeReady=False NotApplicable]
    SetNA1 --> End1([Done])

    CheckDelete -->|No| ComputeActual[Compute actual backing volume from DRBDR]
    ComputeActual --> CheckRV{RV exists and datamesh ready?}
    CheckRV -->|No| WaitRV[BackingVolumeReady=False WaitingForReplicatedVolume]
    WaitRV --> End2([Done])

    CheckRV -->|Yes| ComputeIntended[Compute intended backing volume]
    ComputeIntended --> CheckNeed{Backing volume needed?}
    CheckNeed -->|No| DeleteLLVs[Delete all LLVs]
    DeleteLLVs --> RemoveCond[Remove BackingVolumeReady condition]
    RemoveCond --> End3([Done])

    CheckNeed -->|Yes| CheckExists{Intended LLV exists?}
    CheckExists -->|No| Create[Create LLV]
    Create --> SetProv[BackingVolumeReady=False Provisioning]
    SetProv --> End4([Done])

    CheckExists -->|Yes| EnsureMeta[Ensure LLV metadata]
    EnsureMeta --> CheckReady{LLV ready?}
    CheckReady -->|No| SetWait[BackingVolumeReady=False waiting]
    SetWait --> End5([Done])

    CheckReady -->|Yes| CheckResize{Needs resize?}
    CheckResize -->|Yes| PatchSize[Patch LLV size]
    PatchSize --> SetResize[BackingVolumeReady=False Resizing]
    SetResize --> End6([Done])

    CheckResize -->|No| DeleteObs[Delete obsolete LLVs]
    DeleteObs --> SetReady[BackingVolumeReady=True Ready]
    SetReady --> End7([Done])
```

**Data Flow**:

| Input | Description |
|-------|-------------|
| `rvr.Spec` | Node name, LVG name, thin pool name, replica type |
| `rv.Status.Datamesh` | Target size (after DRBD overhead adjustment), membership state |
| `drbdr.Spec.LVMLogicalVolumeName` | Currently referenced LLV (actual state) |
| `llvs[]` | List of LLVs owned by this RVR |

| Output | Description |
|--------|-------------|
| `LVMLogicalVolume` | Created/patched/deleted backing volume |
| `BackingVolumeReady` condition | Reports LLV lifecycle state |
| `status.backingVolumeSize` | Actual size of the backing volume |

---

### reconcileDRBDResource Details

**Purpose**: Manages DRBDResource lifecycle — creation, configuration, resize, and deletion. Coordinates with agent for DRBD configuration.

**Algorithm**:

```mermaid
flowchart TD
    Start([Start]) --> CheckDelete{RVR should not exist?}
    CheckDelete -->|Yes| RemoveFin[Remove finalizer from DRBDR]
    RemoveFin --> Delete[Delete DRBDR]
    Delete --> SetNA[Configured=False NotApplicable]
    SetNA --> End1([Done])

    CheckDelete -->|No| CheckNode{Node assigned?}
    CheckNode -->|No| SetPending[Configured=False PendingScheduling]
    SetPending --> End2([Done])

    CheckNode -->|Yes| CheckRV{RV ready with datamesh?}
    CheckRV -->|No| SetWaitRV[Configured=False WaitingForReplicatedVolume]
    SetWaitRV --> End3([Done])

    CheckRV -->|Yes| ComputeTarget[Compute target DRBDR spec]
    ComputeTarget --> CheckDRBDRExists{DRBDR exists?}
    CheckDRBDRExists -->|No| CreateDRBDR[Create DRBDR]
    CreateDRBDR --> UpdateStatus1[Update RVR status fields]

    CheckDRBDRExists -->|Yes| CheckNeedsUpdate{Spec needs update?}
    CheckNeedsUpdate -->|Yes| PatchDRBDR[Patch DRBDR]
    PatchDRBDR --> UpdateStatus1
    CheckNeedsUpdate -->|No| UpdateStatus1

    UpdateStatus1 --> CheckAgent{Agent ready?}
    CheckAgent -->|No| SetAgentNA[Configured=False AgentNotReady]
    SetAgentNA --> End4([Done])

    CheckAgent -->|Yes| CheckState{DRBDR state?}
    CheckState -->|Pending| SetApplying[Configured=False ApplyingConfiguration]
    CheckState -->|Failed| SetFailed[Configured=False ConfigurationFailed]
    CheckState -->|True| CheckMember{Datamesh member?}

    SetApplying --> End5([Done])
    SetFailed --> End6([Done])

    CheckMember -->|No| SetPendingJoin[Configured=False PendingDatameshJoin]
    CheckMember -->|Yes| SetConfigured[Configured=True]
    SetPendingJoin --> End7([Done])
    SetConfigured --> End8([Done])
```

**Data Flow**:

| Input | Description |
|-------|-------------|
| `rvr.Spec` | Node name, replica type, LVG/thin pool for diskful |
| `rv.Status.Datamesh` | System networks, members, size, type transitions |
| `targetLLVName` | LLV name from reconcileBackingVolume |
| `agent Pod` | Agent readiness on target node |

| Output | Description |
|--------|-------------|
| `DRBDResource` | Created/patched/deleted DRBD resource |
| `Configured` condition | Reports DRBD configuration state |
| `status.effectiveType` | Current effective replica type |
| `status.datameshRevision` | Datamesh revision this replica was configured for |
| `status.drbdResourceGeneration` | DRBDR generation that was last applied |
| `status.addresses` | DRBD addresses assigned to this replica |

---

### reconcileSatisfyEligibleNodesCondition Details

**Purpose**: Verifies that the replica's node, LVMVolumeGroup, and ThinPool satisfy the eligible nodes requirements from the ReplicatedStoragePool.

**Algorithm**:

```mermaid
flowchart TD
    Start([Start]) --> CheckNode{Node assigned?}
    CheckNode -->|No| RemoveCond[Remove condition]
    RemoveCond --> End1([Done])

    CheckNode -->|Yes| CheckConfig{RV config available?}
    CheckConfig -->|No| SetUnknown1[Unknown: PendingConfiguration]
    SetUnknown1 --> End2([Done])

    CheckConfig -->|Yes| GetRSP[Get RSP eligibleNodes for node]
    GetRSP --> CheckRSPFound{RSP found?}
    CheckRSPFound -->|No| SetUnknown2[Unknown: PendingConfiguration]
    SetUnknown2 --> End3([Done])

    CheckRSPFound -->|Yes| CheckNodeInList{Node in eligibleNodes?}
    CheckNodeInList -->|No| SetFalse1[False: NodeMismatch]
    SetFalse1 --> End4([Done])

    CheckNodeInList -->|Yes| CheckDiskful{Diskful with LVG?}
    CheckDiskful -->|No| CollectWarnings[Collect warnings]

    CheckDiskful -->|Yes| CheckLVG{LVG in eligible node?}
    CheckLVG -->|No| SetFalse2[False: LVMVolumeGroupMismatch]
    SetFalse2 --> End5([Done])

    CheckLVG -->|Yes| CheckThinPool{ThinPool specified?}
    CheckThinPool -->|No| CollectWarnings

    CheckThinPool -->|Yes| CheckTPMatch{ThinPool in LVG?}
    CheckTPMatch -->|No| SetFalse3[False: ThinPoolMismatch]
    SetFalse3 --> End6([Done])

    CheckTPMatch -->|Yes| CollectWarnings
    CollectWarnings --> SetTrue[True: Satisfied + warnings]
    SetTrue --> End7([Done])
```

**Data Flow**:

| Input | Description |
|-------|-------------|
| `rvr.Spec` | Node name, LVG name, thin pool name, replica type |
| `rv.Status.Configuration.StoragePoolName` | RSP name |
| `RSP.Status.EligibleNodes` | List of eligible nodes with LVG details |

| Output | Description |
|--------|-------------|
| `SatisfyEligibleNodes` condition | Reports eligibility verification result |

---

### ensureAttachmentStatus Details

**Purpose**: Reports whether the replica is attached (primary) and ready for I/O. Updates device path and I/O suspension status.

**Algorithm**:

```mermaid
flowchart TD
    Start([Start]) --> CheckRelevant{DRBDR exists AND<br/>intended or actual attached?}
    CheckRelevant -->|No| RemoveAll[Remove condition, clear device fields]
    RemoveAll --> End1([Done])

    CheckRelevant -->|Yes| CheckAgent{Agent ready?}
    CheckAgent -->|No| SetUnknown1[Unknown: AgentNotReady]
    SetUnknown1 --> End2([Done])

    CheckAgent -->|Yes| CheckPending{Configuration pending?}
    CheckPending -->|Yes| SetUnknown2[Unknown: ApplyingConfiguration]
    SetUnknown2 --> End3([Done])

    CheckPending -->|No| EvalState{Evaluate state}

    EvalState -->|Expected attached, not attached| SetFalse1[False: AttachmentFailed]
    EvalState -->|Expected detached, still attached| SetTrue1[True: DetachmentFailed]
    EvalState -->|Attached, IO suspended| SetFalse2[False: IOSuspended]
    EvalState -->|Attached, IO ok| SetTrue2[True: Attached]

    SetFalse1 --> UpdateFields1[Clear device fields]
    SetTrue1 --> UpdateFields2[Set device fields from DRBDR]
    SetFalse2 --> UpdateFields2
    SetTrue2 --> UpdateFields2

    UpdateFields1 --> End4([Done])
    UpdateFields2 --> End5([Done])
```

**Data Flow**:

| Input | Description |
|-------|-------------|
| `drbdr.Status.ActiveConfiguration.Role` | Actual attachment state |
| `datameshMember.Role` | Intended attachment state |
| `drbdr.Status.Device` | Device path when attached |
| `drbdr.Status.DeviceIOSuspended` | I/O suspension flag |

| Output | Description |
|--------|-------------|
| `Attached` condition | Reports attachment state |
| `status.devicePath` | Block device path |
| `status.deviceIOSuspended` | I/O suspension status |

---

### ensureStatusPeers Details

**Purpose**: Mirrors DRBDR peer status to RVR status. Populates `rvr.Status.Peers` directly from `drbdr.Status.Peers`.

**Algorithm**:

```mermaid
flowchart TD
    Start([Start]) --> CheckDRBDR{DRBDR exists AND<br/>has peers?}
    CheckDRBDR -->|No| ClearPeers[Clear rvr.Status.Peers]
    ClearPeers --> End1([Done])

    CheckDRBDR -->|Yes| MirrorPeers[Mirror drbdr.Status.Peers to rvr.Status.Peers]
    MirrorPeers --> ComputeType["Compute Type from drbdr peer:<br/>Diskful → Diskful<br/>Diskless + AllowRemoteRead=false → Access<br/>Diskless + AllowRemoteRead=true → TieBreaker"]
    ComputeType --> ComputeAttached[Compute Attached from Role=Primary]
    ComputeAttached --> CopyFields[Copy ConnectionState, DiskState, Paths]
    CopyFields --> End2([Done])
```

**Data Flow**:

| Input | Description |
|-------|-------------|
| `drbdr.Status.Peers` | Peer status from DRBD |

| Output | Description |
|--------|-------------|
| `status.peers[]` | Mirrored peer status list |

---

### ensureConditionFullyConnected Details

**Purpose**: Reports peer connectivity status via the `FullyConnected` condition. Uses `rvr.Status.Addresses` to determine expected system networks.

**Algorithm**:

```mermaid
flowchart TD
    Start([Start]) --> CheckRelevant{DRBDR exists AND<br/>is member or has peers AND<br/>has addresses?}
    CheckRelevant -->|No| RemoveCond[Remove FullyConnected condition]
    RemoveCond --> End1([Done])

    CheckRelevant -->|Yes| CheckAgent{Agent ready?}
    CheckAgent -->|No| SetUnknown[Unknown: AgentNotReady]
    SetUnknown --> End2([Done])

    CheckAgent -->|Yes| CheckPeers{Any peers in rvr.Status.Peers?}
    CheckPeers -->|No| SetFalse1[False: NoPeers]
    SetFalse1 --> End3([Done])

    CheckPeers -->|Yes| CountConnections["Count fully/partially/not connected<br/>using rvr.Status.Addresses"]
    CountConnections --> EvalState{Evaluate connectivity}

    EvalState -->|All fully connected| SetTrue1[True: FullyConnected]
    EvalState -->|None connected| SetFalse2[False: NotConnected]
    EvalState -->|All connected, not all paths| SetTrue2[True: ConnectedToAllPeers]
    EvalState -->|Some connected| SetFalse3[False: PartiallyConnected]

    SetTrue1 --> End4([Done])
    SetFalse2 --> End5([Done])
    SetTrue2 --> End6([Done])
    SetFalse3 --> End7([Done])
```

**Data Flow**:

| Input | Description |
|-------|-------------|
| `rvr.Status.Peers` | Peer status (from ensureStatusPeers) |
| `rvr.Status.Addresses` | Expected system network names |
| `datamesh` | Datamesh membership check |

| Output | Description |
|--------|-------------|
| `FullyConnected` condition | Reports peer connectivity |

---

### ensureStatusBackingVolume Details

**Purpose**: Populates the `rvr.Status.BackingVolume` struct fields (size, state, LVM volume group info) from DRBDR and LLV status.

**Algorithm**:

```mermaid
flowchart TD
    Start([Start]) --> CheckDRBDR{DRBDR exists?}
    CheckDRBDR -->|No| ClearBV[Clear backingVolume]
    ClearBV --> End1([Done])

    CheckDRBDR -->|Yes| CheckActive{ActiveConfiguration exists?}
    CheckActive -->|No| ClearBV2[Clear backingVolume]
    ClearBV2 --> End2([Done])

    CheckActive -->|Yes| GetLLVName[Get lvmLogicalVolumeName from ActiveConfiguration]
    GetLLVName --> CheckEmpty{Name empty?}
    CheckEmpty -->|Yes| ApplyFromDRBDR[Apply backingVolume from DRBDR only]
    ApplyFromDRBDR --> End3([Done])

    CheckEmpty -->|No| FindLLV{Find LLV by name}
    FindLLV -->|Not found| Error[Return error: inconsistent state]
    Error --> End4([Done])

    FindLLV -->|Found| ApplyFull[Apply backingVolume with LLV info]
    ApplyFull --> End5([Done])
```

**Data Flow**:

| Input | Description |
|-------|-------------|
| `drbdr.Status.DiskState` | Local disk state from DRBD |
| `drbdr.Status.ActiveConfiguration.LVMLogicalVolumeName` | Name of the backing LLV |
| `llvs` | List of LVMLogicalVolumes on this node |
| `llv.Status.ActualSize` | Actual size of the backing LLV |

| Output | Description |
|--------|-------------|
| `status.backingVolume.size` | Size of the backing LVM logical volume |
| `status.backingVolume.state` | Local disk state (UpToDate/Outdated/etc.) |
| `status.backingVolume.lvmVolumeGroupName` | LVG name |
| `status.backingVolume.lvmVolumeGroupThinPoolName` | Thin pool name (if applicable) |

---

### ensureConditionBackingVolumeInSync Details

**Purpose**: Reports local disk synchronization state for diskful replicas via the `BackingVolumeInSync` condition.

**Algorithm**:

```mermaid
flowchart TD
    Start([Start]) --> CheckRelevant{DRBDR exists AND<br/>diskful member OR<br/>has backingVolume?}
    CheckRelevant -->|No| RemoveCond[Remove BackingVolumeInSync condition]
    RemoveCond --> End1([Done])

    CheckRelevant -->|Yes| CheckAgent{Agent ready?}
    CheckAgent -->|No| SetUnknown1[Unknown: AgentNotReady]
    SetUnknown1 --> End2([Done])

    CheckAgent -->|Yes| CheckPending{Configuration pending?}
    CheckPending -->|Yes| SetUnknown2[Unknown: ApplyingConfiguration]
    SetUnknown2 --> End3([Done])

    CheckPending -->|No| CheckActive{ActiveConfiguration exists?}
    CheckActive -->|No| Error[Return error: bug in agent]
    Error --> End6([Done])

    CheckActive -->|Yes| GetDiskState[Get drbdr.Status.DiskState]
    GetDiskState --> EvalState{Evaluate disk state}

    EvalState -->|UpToDate| SetTrue[True: InSync]
    SetTrue --> End4([Done])

    EvalState -->|Diskless| SetFalse1[False: NoDisk]
    EvalState -->|Attaching| SetFalse2[False: Attaching]
    EvalState -->|Detaching| SetFalse3[False: Detaching]
    EvalState -->|Failed| SetFalse4[False: DiskFailed]
    EvalState -->|Syncing states| CheckPeers[Check peer availability]
    EvalState -->|Unknown| SetFalse5[False: UnknownState]

    CheckPeers --> PeerCheck{Has UpToDate peer?}
    PeerCheck -->|No| SetBlocked1[False: SynchronizationBlocked]
    PeerCheck -->|Yes| IOCheck{IO available?}
    IOCheck -->|No| SetBlocked2[False: SynchronizationBlocked]
    IOCheck -->|Yes| SetSyncing[False: Synchronizing]

    SetFalse1 --> End5([Done])
    SetFalse2 --> End5
    SetFalse3 --> End5
    SetFalse4 --> End5
    SetFalse5 --> End5
    SetBlocked1 --> End5
    SetBlocked2 --> End5
    SetSyncing --> End5
```

**Data Flow**:

| Input | Description |
|-------|-------------|
| `drbdr.Status.DiskState` | Local disk state from DRBD |
| `drbdr.Status.ActiveConfiguration.Role` | Local attachment state |
| `rvr.Status.Peers` | Peer states for sync availability check |
| `rvr.Status.BackingVolume` | Current backing volume info (for relevance check) |
| `datameshMember` | Whether this replica is a datamesh member |

| Output | Description |
|--------|-------------|
| `BackingVolumeInSync` condition | Reports disk sync state |

---

### ensureQuorumStatus Details

**Purpose**: Reports quorum status and overall replica readiness.

**Algorithm**:

```mermaid
flowchart TD
    Start([Start]) --> CheckDelete{RVR being deleted<br/>or no DRBDR?}
    CheckDelete -->|Yes| SetDeleting[False: Deleting]
    SetDeleting --> ClearQuorum1[Clear quorum fields]
    ClearQuorum1 --> End1([Done])

    CheckDelete -->|No| CheckAgent{Agent ready?}
    CheckAgent -->|No| SetUnknown1[Unknown: AgentNotReady]
    SetUnknown1 --> ClearQuorum2[Clear quorum fields]
    ClearQuorum2 --> End2([Done])

    CheckAgent -->|Yes| CheckPending{Configuration pending?}
    CheckPending -->|Yes| SetUnknown2[Unknown: ApplyingConfiguration]
    SetUnknown2 --> ClearQuorum3[Clear quorum fields]
    ClearQuorum3 --> End3([Done])

    CheckPending -->|No| ComputeSummary[Compute quorumSummary from peers + DRBDR]
    ComputeSummary --> CopyQuorum[Copy quorum from drbdr.Status.Quorum]
    CopyQuorum --> BuildMessage[Build quorum message]
    BuildMessage --> CheckQuorum{Has quorum?}

    CheckQuorum -->|No| SetFalse[False: QuorumLost]
    CheckQuorum -->|Yes| SetTrue[True: Ready]

    SetFalse --> End4([Done])
    SetTrue --> End5([Done])
```

**Data Flow**:

| Input | Description |
|-------|-------------|
| `drbdr.Status.Quorum` | Quorum flag from DRBD |
| `drbdr.Status.ActiveConfiguration` | Quorum and QMR thresholds |
| `rvr.Status.Peers` | Peer states for voting/UpToDate counts |

| Output | Description |
|--------|-------------|
| `Ready` condition | Reports overall readiness |
| `status.quorum` | Quorum flag |
| `status.quorumSummary` | Detailed quorum info |
