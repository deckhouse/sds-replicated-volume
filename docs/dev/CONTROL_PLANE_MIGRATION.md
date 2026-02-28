# Control-Plane Migration Process

## Overview

Migration from LINSTOR to the new control-plane in the sds-replicated-volume module. 

### High-level approach
- Control plane migration state is stored in ConfigMap
- Kubernetes hook ('sds-control-plane-migration-state-sync') watches control-plane-migration ConfigMap and updates internal values to trigger module template re-renders (runs on every module reconciliation)
- Components deployment depends on the state of ModuleConfig[newControlPlane] and sdsReplicatedVolume.internal.controlPlaneMigration

## State Diagram (High-Level Migration Flow)

```mermaid
stateDiagram-v2
    [*] --> not_started : Initial state newControlPlane=false
    not_started --> migration_enabled : newControlPlane=true
    
    migration_enabled --> all_completed : ConfigMap state all_completed (fresh install)
    migration_enabled --> prepare_mig : ConfigMap state not_started (migration)
    
    prepare_mig --> stage1_started : LINSTOR running migration starts
    
    stage1_started --> stage1_completed : Job Pass1 resource creation
    
    stage1_completed --> stage2_started : Agent & Controller ready
    
    stage2_started --> all_completed : Job Pass2 verification & activation
    
    all_completed --> [*] : All components on new control plane

    note right of not_started
      Initial state:
        ModuleConfig.newControlPlane: false
        internal.controlPlaneMigration: not_started
        ConfigMap[control-plane-migration].state: not_started
    end note
```

## Kubernetes Hook ('sds-control-plane-migration-state-sync')

The Kubernetes hook serves as a single synchronization point between the ConfigMap state and internal module values:

```mermaid
flowchart TB
    subgraph "Kubernetes Hook: sds-control-plane-migration-state-sync"
        start[Start on module reconciliation]
        check_new_cp["Check ModuleConfig[newControlPlane]"]
        decision1{"newControlPlane? true/false"}
        read_state["Read ConfigMap.data.state"]
        update_internal["Update internal.controlPlaneMigration\n= ConfigMap.data.state"]
        no_action["No action\nWait for ConfigMap changes"]
        
        start --> check_new_cp --> decision1
        decision1 -->|true| read_state --> update_internal
        decision1 -->|false| no_action
    end
```

## Migration Job Sequence

```mermaid
sequenceDiagram
    participant User as "User/Admin"
    participant DH as "Deckhouse"
    participant ConfigMap as "ConfigMap[control-plane-migration]"
    participant K8sHook as "sds-control-plane-migration-state-sync hook"
    participant Templates as "Module Templates"
    participant MigrJob as "Migration Job"
    participant LINSTOR as "LINSTOR Components"
    participant AgentCtrl as "Agent & Controller"

    Note over User,DH: Pre-migration: Module installed with newControlPlane=false
    
    User->>DH: Set ModuleConfig[newControlPlane: true]
    DH->>K8sHook: Run hook - check newControlPlane=true
    K8sHook->>ConfigMap: Read .data.state=not_started
    K8sHook->>K8sHook: Set internal.controlPlaneMigration=not_started
    K8sHook-->>DH: Hook completed
    DH->>Templates: Re-render based on internal values
    
    Templates->>MigrJob: Create Migration Job (since newControlPlane=true and internal state < all_completed)
    
    MigrJob->>ConfigMap: Set state = stage1_started
    ConfigMap-->>K8sHook: Change detected
    K8sHook->>K8sHook: Sync to internal.controlPlaneMigration=stage1_started
    K8sHook->>Templates: Trigger re-render
    Templates->>LINSTOR: Check that components stopped
    MigrJob->>Templates: Wait until LINSTOR components stopped
    MigrJob->>Templates: Pass 1: Create RV, RVR, LLV, RVA, DRBDr (in Maintenance mode)
    MigrJob->>ConfigMap: Set state = stage1_completed
    ConfigMap-->>K8sHook: Change detected
    K8sHook->>K8sHook: Sync to internal.controlPlaneMigration=stage1_completed
    K8sHook->>Templates: Trigger re-render
    Templates->>AgentCtrl: Start Agent & Controller
    AgentCtrl-->>MigrJob: Ready signal (async)
    MigrJob->>ConfigMap: Set state = stage2_started
    ConfigMap-->>K8sHook: Change detected
    K8sHook->>K8sHook: Sync to internal.controlPlaneMigration=stage2_started
    K8sHook->>Templates: Trigger re-render
    MigrJob->>Templates: Pass 2: Verify resources consistency
    MigrJob->>Templates: Disable Maintenance on DRBDr
    MigrJob->>ConfigMap: Set state = all_completed
    ConfigMap-->>K8sHook: Change detected
    K8sHook->>K8sHook: Sync to internal.controlPlaneMigration=all_completed
    K8sHook->>Templates: Trigger re-render
    Templates->>Templates: Deploy CSI components

    Note over MigrJob,Templates: On failure: 1. User deletes failed Job 2. User resets ConfigMap.state to not_started 3. User recreates Job to retry
```

## Summary Table

| Phase | ConfigMap.state | internal.controlPlaneMigration | LINSTOR/CSI | Agent/Controller | CSI | Migration Job | Notes |
|-------|-----------------|--------------------------------|---------|------------------|-----|---------------|-------|
| Initial (newControlPlane=false) | not_started | not_started | Running | Stopped | Running | Not created | Normal LINSTOR operation |
| Fresh Install (newControlPlane=true, already completed) | all_completed | all_completed | Stopped | Running | Running | Not created | New install - no migration needed |
| Stage 1 Started | stage1_started | stage1_started | Stopped | Waiting | Waiting | Running Pass 1 | Pass 1: Create resources in Maintenance |
| Stage 1 Completed | stage1_completed | stage1_completed | Stopped | Starting | Waiting | Waiting for readiness | Resources created, waiting for Agent/Controller |
| Stage 2 Started | stage2_started | stage2_started | Stopped | Running | Waiting | Running Pass 2 | Pass 2: Verify consistency |
| All Completed | all_completed | all_completed | Stopped | Running | Running | Completed | Migration finished, new control plane active |

## Recovery Procedure

If migration Job fails:

1. Delete failed Job:
   ```bash
   kubectl delete job <migration-job> -n d8-sds-replicated-volume
   ```

2. Reset ConfigMap state:
   ```bash
   kubectl patch configmap control-plane-migration \
     -n d8-sds-replicated-volume \
     -p '{"data":{"state":"not_started"}}'
   ```

3. Recreate Job to retry migration.
