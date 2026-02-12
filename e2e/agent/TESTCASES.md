# E2E Agent Test Cases

## Preconditions

<!-- Each precondition corresponds 1-to-1 to a helper (SetupX / DiscoverX / InitializeX). -->
<!-- Format: -->
<!-- ### PreconditionName -->
<!-- Description of the environment state this precondition arranges. -->

### Client

K8s client is initialized from kubeconfig. Registers `v1alpha1` and core
schemes.

### PodLogMonitor

Agent pod log streaming is started for all agent pods. Errors in logs are
collected. During cleanup, any collected errors are reported as test failures.

### Nodes

At least 2 nodes are discovered from the cluster by names from environment
configuration.

### LVGs

For each selected node, an existing `LVMVolumeGroup` is discovered on that
node. LVG names come from environment configuration.

### LLVs

`LVMLogicalVolume` objects (100Mi) are created for each diskful node in the
discovered LVGs. Cleaned up after test.

### DRBDResourcesCreated

`DRBDResource` objects are created, one per selected node: `type=Diskful`,
`lvmLogicalVolumeName` referencing the created LLV, `size=100Mi`,
`systemNetworks=["Internal"]`, unique `nodeID` (0..N-1), `state=Up`,
`role=Secondary`, `peers` empty. Each resource is polled until
`status.addresses` is populated (valid IPv4 + port > 0) and condition
`Configured=True`. Cleaned up after test.

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

**Preconditions:** Client, PodLogMonitor, Nodes, LVGs, LLVs,
DRBDResourcesCreated

Each DRBDResource has `status.addresses` with an entry for "Internal" system
network containing a valid IPv4 and `port > 0`.

### StandaloneDRBDResourceBecomesConfigured

**Preconditions:** Client, PodLogMonitor, Nodes, LVGs, LLVs,
DRBDResourcesCreated

Each DRBDResource has condition `Configured` with `status=True`.

### StandaloneDiskfulResourceDiskIsUpToDate

**Preconditions:** Client, PodLogMonitor, Nodes, LVGs, LLVs,
DRBDResourcesCreated

Each diskful DRBDResource has `status.diskState = UpToDate`.

### StandaloneDRBDResourceActiveConfigurationMatchesSpec

**Preconditions:** Client, PodLogMonitor, Nodes, LVGs, LLVs,
DRBDResourcesCreated

Each DRBDResource has `status.activeConfiguration` reflecting: `state=Up`,
`role=Secondary`, `type=Diskful`, `size=100Mi`, `lvmLogicalVolumeName` matching
`spec.lvmLogicalVolumeName`.

### Peered DRBDResources

### PeeredDRBDResourcesConfigured

**Preconditions:** Client, PodLogMonitor, Nodes, LVGs, LLVs,
DRBDResourcesCreated, DRBDResourcesPeered

After adding peers, each DRBDResource has condition `Configured` with
`status=True`.

### PeeredDRBDResourcesPeerConnected

**Preconditions:** Client, PodLogMonitor, Nodes, LVGs, LLVs,
DRBDResourcesCreated, DRBDResourcesPeered

Each DRBDResource reports all peers with `connectionState = Connected`.

### PeeredDRBDResourcesReplicationEstablished

**Preconditions:** Client, PodLogMonitor, Nodes, LVGs, LLVs,
DRBDResourcesCreated, DRBDResourcesPeered

Each DRBDResource reports all diskful peers with
`replicationState = Established`.

### PeeredDRBDResourcesPathsEstablished

**Preconditions:** Client, PodLogMonitor, Nodes, LVGs, LLVs,
DRBDResourcesCreated, DRBDResourcesPeered

Each DRBDResource reports all peer paths with `established = true`.
