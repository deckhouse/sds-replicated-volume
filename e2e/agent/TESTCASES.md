# Test Cases: Agent

- [Test Cases: Agent](#test-cases-agent)
  - [How to use this file](#how-to-use-this-file)
    - [Format](#format)
    - [Conventions](#conventions)
  - [drbdr (DRBDResource)](#drbdr-drbdresource)
    - [References](#references)
    - [Notes](#notes)
    - [TC-DRBDR-001: Diskful resource lifecycle (up and down)](#tc-drbdr-001-diskful-resource-lifecycle-up-and-down)
    - [TC-DRBDR-002: Two-node diskful replication](#tc-drbdr-002-two-node-diskful-replication)
    - [TC-DRBDR-003: Promote to Primary](#tc-drbdr-003-promote-to-primary)
    - [TC-DRBDR-004: Maintenance mode skips DRBD actions](#tc-drbdr-004-maintenance-mode-skips-drbd-actions)


## How to use this file

### Format

Each test case follows:

- **ID / Title** — unique identifier and short summary.
- **Given** — preconditions and initial cluster state.
- **When** — actions to perform (sequential, numbered).
- **Then** — observable assertions (each prefixed with field path or resource).

### Conventions

- `wait(condition)` — poll until condition is true or timeout.
- Field paths use dot notation from the resource root: `.status.diskState`, `.spec.state`.
- `Configured` refers to condition type `Configured` in `.status.conditions[]`.

---

## drbdr (DRBDResource)

### References

<!-- AI: read these packages/docs before implementing tests -->

| What                    | Package / Path                                            |
|-------------------------|-----------------------------------------------------------|
| API types & conditions  | `api/v1alpha1`                                            |
| drbdr controller        | `images/agent/internal/controllers/drbdr`                 |
| E2E client              | `e2e/agent/client`                                        |
| sds-node-configurator   | `github.com/deckhouse/sds-node-configurator/api/v1alpha1` (external: LVMLogicalVolume, LVMVolumeGroup) |
| drbdsetup guide         | `docs/drbd/DRBDSETUP COMMAND CALLER'S GUIDE.md`          |

### Notes

- DRBDResource is cluster-scoped; its `.spec.nodeName` pins it to a node.
- The agent runs per-node; only the agent on `.spec.nodeName` reconciles it.
- Diskful resources require a pre-existing LVMLogicalVolume (LLV) and LVMVolumeGroup from the sds-node-configurator module.
- DRBD resource name on the node is `sdsrv-<k8s-name>`.
- Stable port allocation (7000-7999) is managed by the agent's PortCache.

---

### TC-DRBDR-001: Diskful resource lifecycle (up and down)

**Given:** Node with an available LVMLogicalVolume. No pre-existing DRBDResource for this LLV.

**When:**
1. Create DRBDResource: `state=Up`, `type=Diskful`, `lvmLogicalVolumeName=<llv>`, `nodeID=0`, `systemNetworks` matching the node.
2. `wait(Configured=True)`
3. Set `.spec.state` = `Down`.
4. `wait(Configured=True AND .status.activeConfiguration.state=Down)` or resource is finalized.

**Then (after step 2):**
- `Configured` = True, reason = `Configured`.
- `.status.activeConfiguration.state` = `Up`.
- `.status.activeConfiguration.type` = `Diskful`.
- `.status.activeConfiguration.lvmLogicalVolumeName` = `<llv>`.
- `.status.diskState` = `UpToDate`.
- `.status.device` is non-empty (e.g. `/dev/drbd<N>`).
- `.status.addresses` has entries matching `systemNetworks`.
- `AgentFinalizer` present on DRBDResource.
- `AgentFinalizer` present on LLV.

**Then (after step 4):**
- DRBD resource is torn down on the node (`drbdsetup status` returns nothing).
- `AgentFinalizer` removed from DRBDResource.
- `AgentFinalizer` removed from LLV.

---

### TC-DRBDR-002: Two-node diskful replication

**Given:** Two nodes (A, B) each with an available LLV. Nodes share at least one `systemNetwork`.

**When:**
1. Create DRBDResource-A on node A: `state=Up`, `type=Diskful`, `nodeID=0`, with peer entry for node B (`nodeID=1`, `protocol=C`, paths with B's address).
2. Create DRBDResource-B on node B: `state=Up`, `type=Diskful`, `nodeID=1`, with peer entry for node A (`nodeID=0`, `protocol=C`, paths with A's address).
3. `wait(both Configured=True)`
4. `wait(both .status.diskState=UpToDate)`

**Then:**
- Both resources: `Configured` = True.
- Both resources: `.status.diskState` = `UpToDate`.
- Both resources: `.status.peers` has 1 entry with `connectionState` = `Connected`, `replicationState` = `Established`, `diskState` = `UpToDate`.
- Both resources: `.status.quorum` is set.

---

### TC-DRBDR-003: Promote to Primary

**Given:** TC-DRBDR-002 completed (two synced diskful replicas, both Secondary).

**When:**
1. Set `.spec.role` = `Primary` on DRBDResource-A.
2. `wait(Configured=True on A)`

**Then:**
- A: `.status.activeConfiguration.role` = `Primary`.
- A: `.status.device` is non-empty.
- B: `.status.peers[nodeID=0].role` = `Primary` (eventually).

---

### TC-DRBDR-004: Maintenance mode skips DRBD actions

**Given:** A single diskful DRBDResource in state=Up, Configured=True.

**When:**
1. Set `.spec.maintenance` = `NoResourceReconciliation`.
2. Set `.spec.role` = `Primary` (a change that would normally trigger a DRBD action).
3. `wait(Configured=False)`

**Then:**
- `Configured` = False, reason = `InMaintenance`.
- `.status.activeConfiguration.role` remains `Secondary` (DRBD action was not executed).
- Clearing `.spec.maintenance` and waiting: `Configured` becomes True and role changes to `Primary`.
