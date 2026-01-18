# node_controller

This controller manages the `storage.deckhouse.io/sds-replicated-volume-node` label on cluster nodes.

## Purpose

The `storage.deckhouse.io/sds-replicated-volume-node` label determines which nodes should run the sds-replicated-volume agent.
The controller automatically adds this label to nodes that match at least one `ReplicatedStorageClass` (RSC),
and removes it from nodes that do not match any RSC.

## Reconciliation Structure

```
Reconcile (root)
├── getRSCs             — fetch all RSCs
├── getNodes            — fetch all Nodes
├── computeTargetNodes  — compute which nodes should have the label
└── reconcileNode       — per-node label reconciliation (loop)
```

## Algorithm

The controller uses the **resolved configuration** from `rsc.status.configuration` (not `rsc.spec`).
RSCs that do not yet have a configuration are skipped.

A node is considered matching an RSC if **both** conditions are met (AND):

1. **Zones**: if the RSC configuration has `zones` specified — the node's `topology.kubernetes.io/zone` label must be in that list;
   if `zones` is not specified — the condition is satisfied for any node.

2. **NodeLabelSelector**: if the RSC configuration has `nodeLabelSelector` specified — the node must match this selector;
   if `nodeLabelSelector` is not specified — the condition is satisfied for any node.

An RSC configuration without `zones` and without `nodeLabelSelector` matches all cluster nodes.

A node receives the label if it matches at least one RSC (OR between RSCs).

## Algorithm Flow

```mermaid
flowchart TD
    Start([Reconcile]) --> GetRSCs[Get all RSCs]
    GetRSCs --> GetNodes[Get all Nodes]
    GetNodes --> ComputeTarget[computeTargetNodes]

    ComputeTarget --> LoopStart{For each Node}
    LoopStart --> CheckConfig{RSC has<br>configuration?}
    CheckConfig -->|No| SkipRSC[Skip RSC]
    CheckConfig -->|Yes| CheckZones{Node in<br>RSC zones?}
    SkipRSC --> NextRSC
    CheckZones -->|No| NextRSC[Next RSC]
    CheckZones -->|Yes| CheckSelector{Node matches<br>nodeLabelSelector?}
    CheckSelector -->|No| NextRSC
    CheckSelector -->|Yes| MatchFound[Node matches RSC]
    MatchFound --> MarkTrue[targetNodes = true]
    NextRSC --> MoreRSCs{More RSCs?}
    MoreRSCs -->|Yes| CheckConfig
    MoreRSCs -->|No, no match| MarkFalse[targetNodes = false]
    MarkTrue --> NextNode
    MarkFalse --> NextNode[Next Node]
    NextNode --> MoreNodes{More Nodes?}
    MoreNodes -->|Yes| LoopStart
    MoreNodes -->|No| ReconcileLoop

    ReconcileLoop{For each Node} --> CheckInSync{Label in sync?}
    CheckInSync -->|Yes| DoneNode([Skip])
    CheckInSync -->|No| PatchNode[Patch Node label]
    PatchNode --> DoneNode
    DoneNode --> MoreNodes2{More Nodes?}
    MoreNodes2 -->|Yes| ReconcileLoop
    MoreNodes2 -->|No| Done([Done])
```

## Data Flow

```mermaid
flowchart TD
    subgraph inputs [Inputs]
        RSCs[RSCs<br>status.configuration]
        Nodes[Nodes<br>labels]
    end

    subgraph compute [Compute]
        ComputeTarget[computeTargetNodes]
        NodeMatch[nodeMatchesRSC]
    end

    subgraph reconcile [Reconcile]
        ReconcileNode[reconcileNode]
    end

    subgraph output [Output]
        NodeLabel[Node labels<br>storage.deckhouse.io/<br>sds-replicated-volume-node]
    end

    RSCs -->|zones<br>nodeLabelSelector| ComputeTarget
    Nodes -->|topology.kubernetes.io/zone<br>other labels| ComputeTarget

    ComputeTarget --> NodeMatch
    NodeMatch -->|targetNodes map| ReconcileNode

    Nodes --> ReconcileNode
    ReconcileNode -->|add/remove label| NodeLabel
```
