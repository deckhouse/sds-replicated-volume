# E2E Agent Test Cases

- [E2E Agent Test Cases](#e2e-agent-test-cases)
  - [Preconditions](#preconditions)
    - [Client](#client)
    - [PodLogMonitor](#podlogmonitor)
    - [DRBDResources](#drbdresources)
    - [DRBDResourcesPeered](#drbdresourcespeered)
  - [Test cases](#test-cases)
    - [Standalone DRBDResource (without peers)](#standalone-drbdresource-without-peers)
    - [StandaloneDRBDResourceProducesAddresses](#standalonedrbdresourceproducesaddresses)
    - [StandaloneDRBDResourceBecomesConfigured](#standalonedrbdresourcebecomesconfigured)
    - [StandaloneDiskfulResourceDiskIsUpToDate](#standalonediskfulresourcediskisuptodate)
    - [StandaloneDRBDResourceActiveConfigurationMatchesSpec](#standalonedrbdresourceactiveconfigurationmatchesspec)
    - [Peered DRBDResources](#peered-drbdresources)
    - [PeeredDRBDResourcesConfigured](#peereddrbdresourcesconfigured)
    - [PeeredDRBDResourcesPeerConnected](#peereddrbdresourcespeerconnected)
    - [PeeredDRBDResourcesReplicationEstablished](#peereddrbdresourcesreplicationestablished)
    - [PeeredDRBDResourcesPathsEstablished](#peereddrbdresourcespathsestablished)


## Preconditions

<!-- Each precondition corresponds 1-to-1 to a helper (SetupX / DiscoverX). -->
<!-- Format: -->
<!-- ### PreconditionName -->
<!-- Description of the environment state this precondition arranges. -->

### Client

K8s client is discovered from kubeconfig via `*I`. Registers `v1alpha1`,
sds-node-configurator, core, and storage schemes.

### PodLogMonitor

Agent pod log streaming is started for all agent pods matching the
`PodLogMonitorOptions` config section. Errors in logs are collected. During
cleanup, any collected errors are reported as test failures.

### DRBDResources

Reads the `DRBDResourcesOptions` config section. Discovers at least 2 nodes
and their `LVMVolumeGroup` objects. Creates `LVMLogicalVolume` objects (100Mi)
for each node. Creates `DRBDResource` objects, one per selected node:
`type=Diskful`, `lvmLogicalVolumeName` referencing the created LLV,
`size=100Mi`, `systemNetworks=["Internal"]`, unique `nodeID` (0..N-1),
`state=Up`, `role=Secondary`, `peers` empty. Each resource is polled until
`status.addresses` is populated (valid IPv4 + port > 0) and condition
`Configured=True`. Cleaned up after test (including LLVs).

### DRBDResourcesPeered

Each `DRBDResource` is patched to add all other resources as peers. Each peer
has: `name` = peer's `spec.nodeName`, `nodeID` = peer's `spec.nodeID`,
`protocol=C`, `sharedSecretAlg=DummyForTest`, and `paths` with
`systemNetworkName="Internal"` and `address` from the peer's
`status.addresses`. Each resource is polled until condition `Configured=True`.

## Test cases

<!-- Test cases are a flat list. Grouping below is for readability only. -->
<!-- Format: -->
<!-- ### TestName -->
<!-- **Preconditions:** Precondition1, Precondition2, ... -->
<!-- Description of what the test verifies. -->

### Standalone DRBDResource (without peers)

### StandaloneDRBDResourceProducesAddresses

**Preconditions:** Client, PodLogMonitor, DRBDResources

Each DRBDResource has `status.addresses` with an entry for "Internal" system
network containing a valid IPv4 and `port > 0`.

### StandaloneDRBDResourceBecomesConfigured

**Preconditions:** Client, PodLogMonitor, DRBDResources

Each DRBDResource has condition `Configured` with `status=True`.

### StandaloneDiskfulResourceDiskIsUpToDate

**Preconditions:** Client, PodLogMonitor, DRBDResources

Each diskful DRBDResource has `status.diskState = UpToDate`.

### StandaloneDRBDResourceActiveConfigurationMatchesSpec

**Preconditions:** Client, PodLogMonitor, DRBDResources

Each DRBDResource has `status.activeConfiguration` reflecting: `state=Up`,
`role=Secondary`, `type=Diskful`, `size=100Mi`, `lvmLogicalVolumeName` matching
`spec.lvmLogicalVolumeName`.

### Peered DRBDResources

### PeeredDRBDResourcesConfigured

**Preconditions:** Client, PodLogMonitor, DRBDResources,
DRBDResourcesPeered

After adding peers, each DRBDResource has condition `Configured` with
`status=True`.

### PeeredDRBDResourcesPeerConnected

**Preconditions:** Client, PodLogMonitor, DRBDResources,
DRBDResourcesPeered

Each DRBDResource reports all peers with `connectionState = Connected`.

### PeeredDRBDResourcesReplicationEstablished

**Preconditions:** Client, PodLogMonitor, DRBDResources,
DRBDResourcesPeered

Each DRBDResource reports all diskful peers with
`replicationState = Established`.

### PeeredDRBDResourcesPathsEstablished

**Preconditions:** Client, PodLogMonitor, DRBDResources,
DRBDResourcesPeered

Each DRBDResource reports all peer paths with `established = true`.
