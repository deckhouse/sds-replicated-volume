# rsp_controller

> **TODO(systemnetwork): IMPORTANT!** This controller does not yet support custom SystemNetworkNames.
> Currently only "Internal" (default node network) is allowed by API validation.
> When systemnetwork feature stabilizes, the controller must:
> - Watch NetworkNode resources
> - Filter eligible nodes based on configured networks availability
> - Add NetworkNode predicates for Ready condition changes
>
> See `controller.go` for detailed TODO comments.

This controller manages the `ReplicatedStoragePool` status fields by aggregating information from LVMVolumeGroups, Nodes, and agent Pods.

## Purpose

The controller reconciles `ReplicatedStoragePool` status with:

1. **Eligible nodes** — nodes that can host volumes of this storage pool
2. **Eligible nodes revision** — for quick change detection
3. **Ready condition** — describing the current state

## Interactions

| Direction | Resource/Controller | Relationship |
|-----------|---------------------|--------------|
| ← input | LVMVolumeGroup | Reads LVGs referenced by RSP spec |
| ← input | Node | Reads nodes matching selector |
| ← input | Pod (agent) | Reads agent pod readiness |
| → used by | rsc_controller | RSC uses `RSP.Status.EligibleNodes` for validation |
| → used by | node_controller | Reads `RSP.Status.EligibleNodes` to manage node labels |

## Algorithm

A node is eligible if **all** conditions are met:

```
eligible = matchesNodeLabelSelector
       AND matchesZones
       AND (nodeReady OR withinGracePeriod)
```

For each eligible node, the controller also records LVG readiness and agent readiness.

## Reconciliation Structure

```
Reconcile (root)
├── getRSP                                    — fetch the RSP
├── getLVGsByRSP                              — fetch LVGs referenced by RSP
├── validateRSPAndLVGs                        — validate RSP/LVG configuration
├── getSortedNodes                            — fetch nodes (filtered by selector)
├── getAgentReadiness                         — fetch agent pods and compute readiness
├── computeActualEligibleNodes                — compute eligible nodes list
├── applyEligibleNodesAndIncrementRevisionIfChanged
├── applyReadyCondTrue/applyReadyCondFalse    — set Ready condition
└── patchRSPStatus                            — persist status changes
```

## Algorithm Flow

```mermaid
flowchart TD
    Start([Reconcile]) --> GetRSP[Get RSP]
    GetRSP -->|NotFound| Done1([Done])
    GetRSP --> GetLVGs[Get LVGs by RSP]

    GetLVGs -->|Error| Fail1([Fail])
    GetLVGs -->|Some NotFound| SetLVGNotFound[Ready=False<br>LVMVolumeGroupNotFound]
    GetLVGs --> ValidateRSP[Validate RSP and LVGs]

    SetLVGNotFound --> PatchStatus1[Patch status]
    PatchStatus1 --> Done2([Done])

    ValidateRSP -->|Invalid| SetInvalidLVG[Ready=False<br>InvalidLVMVolumeGroup]
    ValidateRSP --> ValidateSelector[Validate NodeLabelSelector]

    SetInvalidLVG --> PatchStatus2[Patch status]
    PatchStatus2 --> Done3([Done])

    ValidateSelector -->|Invalid| SetInvalidSelector[Ready=False<br>InvalidNodeLabelSelector]
    ValidateSelector --> ValidateZones[Validate Zones]

    SetInvalidSelector --> PatchStatus3[Patch status]
    PatchStatus3 --> Done4([Done])

    ValidateZones -->|Invalid| SetInvalidZones[Ready=False<br>InvalidNodeLabelSelector]
    ValidateZones --> GetNodes[Get Nodes<br>filtered by selector]

    SetInvalidZones --> PatchStatus4[Patch status]
    PatchStatus4 --> Done5([Done])

    GetNodes --> GetAgentReadiness[Get Agent Readiness]
    GetAgentReadiness --> ComputeEligible[Compute Eligible Nodes]

    ComputeEligible --> ApplyEligible[Apply eligible nodes<br>Increment revision if changed]
    ApplyEligible --> SetReady[Ready=True]

    SetReady --> Changed{Changed?}
    Changed -->|Yes| PatchStatus5[Patch status]
    Changed -->|No| CheckGrace{Grace period<br>expiration?}
    PatchStatus5 --> CheckGrace

    CheckGrace -->|Yes| Requeue([RequeueAfter])
    CheckGrace -->|No| Done6([Done])
```

## Conditions

### Ready

Indicates whether the storage pool eligible nodes have been calculated successfully.

| Status | Reason | When |
|--------|--------|------|
| True | Ready | Eligible nodes calculated successfully |
| False | LVMVolumeGroupNotFound | Some LVMVolumeGroups not found |
| False | InvalidLVMVolumeGroup | RSP/LVG validation failed (e.g., thin pool not found) |
| False | InvalidNodeLabelSelector | NodeLabelSelector or Zones parsing failed |

## Eligible Nodes Details

A node is considered eligible for an RSP if **all** conditions are met (AND):

1. **NodeLabelSelector** — if the RSP has `nodeLabelSelector` specified, the node must match this selector; if not specified, the condition is satisfied for any node

2. **Zones** — if the RSP has `zones` specified, the node's `topology.kubernetes.io/zone` label must be in that list; if `zones` is not specified, the condition is satisfied for any node

3. **Ready status** — if the node has been `NotReady` longer than `spec.eligibleNodesPolicy.notReadyGracePeriod`, it is excluded from the eligible nodes list

> **Note:** Nodes are filtered by NodeLabelSelector and Zones before being passed to the eligible nodes computation. Nodes without matching LVMVolumeGroups are still included as they can serve as client-only or tiebreaker nodes.

For each eligible node, the controller records:

- **NodeName** — Kubernetes node name
- **ZoneName** — from `topology.kubernetes.io/zone` label
- **NodeReady** — current node readiness status
- **Unschedulable** — from `node.spec.unschedulable`
- **AgentReady** — whether the sds-replicated-volume agent pod on this node is ready
- **LVMVolumeGroups** — list of matching LVGs with:
  - **Name** — LVMVolumeGroup resource name
  - **ThinPoolName** — thin pool name (for LVMThin storage pools)
  - **Unschedulable** — from `storage.deckhouse.io/lvmVolumeGroupUnschedulable` annotation
  - **Ready** — LVG Ready condition status (and thin pool ready status for LVMThin)

## Managed Metadata

This controller manages `RSP.Status` fields only and does not create external labels, annotations, or finalizers.

| Type | Key | Managed On | Purpose |
|------|-----|------------|---------|
| Status field | `status.eligibleNodes` | RSP | List of eligible nodes |
| Status field | `status.eligibleNodesRevision` | RSP | Change detection counter |
| Status field | `status.conditions[Ready]` | RSP | Controller health condition |

## Watches

| Resource | Events | Handler |
|----------|--------|---------|
| ReplicatedStoragePool | Generation changes | Direct (primary) |
| Node | Label changes, Ready condition, spec.unschedulable | Index + selector matching |
| LVMVolumeGroup | Generation, unschedulable annotation, Ready condition, ThinPools[].Ready | Index by LVG name |
| Pod (agent) | Ready condition changes, namespace + label filter | Index by node name |

## Indexes

| Index | Field | Purpose |
|-------|-------|---------|
| RSP by eligible node name | `status.eligibleNodes[].nodeName` | Find RSPs where a node is eligible |
| RSP by LVMVolumeGroup name | `spec.lvmVolumeGroups[].name` | Find RSPs referencing a specific LVG |

## Data Flow

```mermaid
flowchart TD
    subgraph inputs [Inputs]
        RSP[RSP.spec]
        Nodes[Nodes]
        LVGs[LVMVolumeGroups]
        AgentPods[Agent Pods]
    end

    subgraph compute [Compute]
        BuildSelector[Build node selector<br>from NodeLabelSelector + Zones]
        BuildLVGMap[buildLVGByNodeMap]
        GetAgent[getAgentReadiness]
        ComputeEligible[computeActualEligibleNodes]
    end

    subgraph status [Status Output]
        EN[status.eligibleNodes]
        ENRev[status.eligibleNodesRevision]
        Conds[status.conditions]
    end

    RSP --> BuildSelector
    RSP --> BuildLVGMap
    Nodes --> BuildSelector
    BuildSelector -->|filtered nodes| ComputeEligible

    LVGs --> BuildLVGMap
    BuildLVGMap --> ComputeEligible

    AgentPods --> GetAgent
    GetAgent --> ComputeEligible

    ComputeEligible --> EN
    ComputeEligible --> ENRev
    ComputeEligible -->|Ready| Conds
```
