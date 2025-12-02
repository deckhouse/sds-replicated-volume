# RVR Status Config Address Controller

## Description

Implemented `rvr-status-config-address-controller` for automatic address configuration (IP and port) for all `ReplicatedVolumeReplica` replicas on each cluster node.

## Key Changes

### New Controller

- **Controller**: `rvr-status-config-address-controller`
- **Reconcile resource**: `Node` (atomic processing of all RVRs on a node)
- **Purpose**: Configures `status.drbd.config.address` (IPv4 and port) for all RVRs on the current node

### Functionality

1. **Address Configuration**:
   - Extracts `InternalIP` from `Node.Status.Addresses`
   - Selects a free port from the range `[drbdMinPort, drbdMaxPort]` from ConfigMap
   - Reuses existing valid ports to avoid conflicts
   - Guarantees port uniqueness for all RVRs on the node

2. **Error Handling**:
   - Sets `AddressConfigured=False` condition when no free ports are available
   - Handles cases when node is missing `InternalIP`
   - Validates port settings from ConfigMap

3. **Watches**:
   - `Node` - primary resource for reconcile
   - `ConfigMap` (agent-config) - tracks port settings changes
   - `ReplicatedVolumeReplica` - tracks RVR changes on the current node

### API Changes

#### Added

- **Condition**: `ConditionTypeAddressConfigured` in `ReplicatedVolumeReplica.status.conditions`
  - `ReasonAddressConfigurationSucceeded` - successful address configuration
  - `ReasonNodeIPNotFound` - node InternalIP not found
  - `ReasonPortSettingsNotFound` - port settings not found
  - `ReasonNoFreePortAvailable` - no free ports available in range

- **Validation**: Rule for `ownerReferences` in `ReplicatedVolumeReplica` (CRD)

- **Methods**: 
  - `ReplicatedVolumeReplica.NodeNameSelector()` - selector for filtering by nodeName
  - `ReplicatedVolumeReplica.SetReplicatedVolume()` - sets ownerReference

#### Changed

- `ReplicatedVolumeReplica.spec.type` - made optional (removed `Required`)

#### Removed

- `ReplicatedVolumeReplica.status.drbd.config.peersInitialized` - field removed from API

### Infrastructure

1. **Dependencies**:
   - Added `github.com/onsi/ginkgo/v2` and `github.com/onsi/gomega` for unit tests
   - Added `sigs.k8s.io/controller-runtime` to `api/go.mod`

2. **Cache Optimization**:
   - Configured field selector for RVR cache - only replicas of current node
   - Using `NodeNameSelector()` for cache filtering

3. **Data Types**:
   - Changed port type from `int` to `uint` in `cluster.Settings`

### Testing

Added comprehensive unit tests using Ginkgo/Gomega:

- **Handler Tests** (`handlers_test.go`):
  - Testing `ConfigMapEnqueueHandler` and `ConfigMapUpdatePredicate`
  - Testing `ReplicatedVolumeReplicaEnqueueHandler` and `ReplicatedVolumeReplicaUpdatePredicate`
  - Coverage of various event filtering scenarios

- **Reconciler Tests** (`reconciler_test.go`):
  - Testing address configuration for multiple RVRs
  - Port reuse verification
  - Error handling (missing InternalIP, invalid port settings)
  - Port uniqueness verification
  - Port range exhaustion handling

### Refactoring

1. **Controller Architecture**:
   - Migration from typed controllers to standard controller-runtime patterns
   - Simplified reconciler structure
   - Improved logging using `logr.Logger`

2. **Port Handling**:
   - Improved port reuse logic
   - Atomic processing of all RVRs on a node in a single reconcile loop
   - Avoiding race conditions when selecting free ports

## Changed Files

### New Files
- `images/agent/internal/controllers/rvr_status_config_address/handlers.go`
- `images/agent/internal/controllers/rvr_status_config_address/handlers_test.go`
- `images/agent/internal/controllers/rvr_status_config_address/reconciler_test.go`
- `images/agent/internal/controllers/rvr_status_config_address/rvr_status_config_address_suite_test.go`
- `images/agent/internal/controllers/rvr_status_config_address/errors.go`

### Modified Files
- `images/agent/internal/controllers/rvr_status_config_address/controller.go`
- `images/agent/internal/controllers/rvr_status_config_address/reconciler.go`
- `images/agent/internal/controllers/rvr_status_config_address/request.go` → `errors.go` (renamed)
- `images/agent/internal/controllers/registry.go`
- `images/agent/cmd/manager.go`
- `images/agent/internal/cluster/settings.go`
- `api/v1alpha3/conditions.go`
- `api/v1alpha3/replicated_volume_replica.go`
- `crds/storage.deckhouse.io_replicatedvolumereplicas.yaml`
- `api/go.mod`, `api/go.sum`
- `images/agent/go.mod`

## Compatibility

- ✅ Backward compatibility maintained (new fields are optional)
- ✅ Removal of `peersInitialized` requires updating code that uses this field
- ✅ Port type change (`int` → `uint`) requires updating code that uses `cluster.Settings`

## Testing

All tests pass successfully:
- Unit tests for handlers
- Unit tests for reconciler
- Integration scenarios covered by tests

## Related Changes

- Controller refactoring for improved readability and testability
- Added ownerReferences validation in CRD

