# Control-Plane Migration Process

## Overview

Migration from LINSTOR to the new control-plane in the sds-replicated-volume module. 

### High-level approach
- Control plane migration state is stored in ConfigMap
- OnBeforeHelm hook reads state from ConfigMap and writes to internal values
- Kubernetes hook watches ConfigMap and updates internal values to trigger module template re-renders
- Components deployment depends on the state of ModuleConfig[newControlPlane] and sdsReplicatedVolume.internal.controlPlaneMigration

## State Diagram (High-Level Migration Flow)

```mermaid
stateDiagram-v2
    [*] --> not_started : Module update newControlPlane=false
    not_started --> new_control_plane_set : newControlPlane=true
    
    new_control_plane_set --> all_completed : ConfigMap doesn't exist fresh install
    new_control_plane_set --> stage1_ready : ConfigMap exists with state not_started
    
    stage1_ready : LINSTOR stopping
    stage1_ready --> stage1_started
    
    stage1_started --> stage1_completed : Job Pass1 resource creation
    
    stage1_completed : Agent & Controller starting
    stage1_completed --> stage2_started : Agent & Controller ready
    
    stage2_started --> all_completed : Job Pass2 verification & activation
    
    all_completed --> [*] : All components running on new control plane

    note right of not_started
      Before update:
        ModuleConfig.newControlPlane: false
        internal.controlPlaneMigration: not_started
        ConfigMap[control-plane-migration].state: not_started
    end note
```



## Migration Job Sequence

```mermaid
sequenceDiagram
    participant User as "User/Admin"
    participant DH as "Deckhouse"
    participant ConfigMap as "ConfigMap[control-plane-migration]"
    participant OBH as "OnBeforeHelm Hook"
    participant K8sHook as "Kubernetes Hook"
    participant Templates as "Module Templates"
    participant MigrJob as "Migration Job"
    participant LINSTOR as "LINSTOR Components"
    participant AgentCtrl as "Agent & Controller"

    Note over User,DH: Pre-migration: Module installed with newControlPlane=false
    
    User->>DH: Set ModuleConfig[newControlPlane: true]
    DH->>OBH: Run OnBeforeHelm hook
    OBH->>ConfigMap: Check if exists
    alt ConfigMap exists
        OBH->>ConfigMap: Read .data.state
        OBH->>OBH: Set internal.controlPlaneMigration = value from ConfigMap
    else ConfigMap doesn't exist
        OBH->>OBH: Set internal.controlPlaneMigration = all_completed
    end
    OBH-->>DH: Hook completed
    DH->>Templates: Re-render based on internal values
    Templates->>LINSTOR: Stop LINSTOR components
    Templates->>MigrJob: Create Migration Job (if internal < all_completed)
    
    MigrJob->>ConfigMap: Set state = stage1_started
    ConfigMap-->>K8sHook: Change detected
    K8sHook->>K8sHook: Sync to internal.controlPlaneMigration
    K8sHook->>Templates: Trigger re-render
    Templates->>Templates: Confirm LINSTOR is stopped
    MigrJob->>Templates: Wait until LINSTOR is stopped
    MigrJob->>Templates: Pass 1: Create RV, RVR, LLV, RVA, DRBDr (in Maintenance mode)
    MigrJob->>ConfigMap: Set state = stage1_completed
    ConfigMap-->>K8sHook: Change detected
    K8sHook->>K8sHook: Sync to internal.controlPlaneMigration
    K8sHook->>Templates: Trigger re-render
    Templates->>AgentCtrl: Start Agent & Controller
    AgentCtrl-->>MigrJob: Ready signal (async)
    MigrJob->>ConfigMap: Set state = stage2_started
    ConfigMap-->>K8sHook: Change detected
    K8sHook->>K8sHook: Sync to internal.controlPlaneMigration
    K8sHook->>Templates: Trigger re-render
    MigrJob->>Templates: Pass 2: Verify resources consistency
    MigrJob->>Templates: Disable Maintenance on DRBDr
    MigrJob->>ConfigMap: Set state = all_completed
    ConfigMap-->>K8sHook: Change detected
    K8sHook->>K8sHook: Sync to internal.controlPlaneMigration
    K8sHook->>Templates: Trigger re-render
    Templates->>Templates: Deploy CSI components

    Note over MigrJob,Templates: On failure: 1. User deletes failed Job 2. User resets ConfigMap.state to not_started 3. User recreates Job to retry
```

## Summary Table

| Phase | ConfigMap.state | internal.controlPlaneMigration | LINSTOR | Agent/Controller | CSI | Migration Job | Notes |
|-------|-----------------|--------------------------------|---------|------------------|-----|---------------|-------|
| Initial (newControlPlane=false) | not_started | not_started | Running | Running | Running | Not created | Normal LINSTOR operation |
| Triggered (fresh install) | - | all_completed | Stopped | Running | Running | Not created | New install - no migration needed |
| Stage 1 Ready | not_started | stage1_started | N/A | N/A | N/A | Starting | LINSTOR to be stopped before Pass 1 |
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