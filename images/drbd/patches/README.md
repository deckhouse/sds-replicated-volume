# Quorum Improvements Patch

## Description

This patch contains improvements to the quorum logic in DRBD, aimed at more correct handling of cluster states, especially for diskless nodes and peer operations.

## Main Changes

### 1. `drbd_main.c` - Checking for `quorum_min_redundancy` changes
- Added a check for changes to the `quorum_min_redundancy` parameter in the `set_resource_options()` function
- When this parameter changes, the resource state is now forcibly recalculated

### 2. `drbd_nl.c` - Improved `forget-peer` handling
- Added removal of the peer from cluster members (`resource->members`)
- When a peer is disconnected and quorum is enabled, state recalculation is performed with the `CS_FORCE_RECALC` flag
- This ensures correct quorum state update when a peer is removed

### 3. `drbd_receiver.c` - Handling quorum changes for diskless nodes
- Improved handling of peer quorum state changes for diskless nodes
- Added tracking of `PEER_QUORATE` state changes
- For diskless nodes, quorum depends on the peer's quorum state, so when the peer's quorum state changes, the state is forcibly recalculated
- Added `CS_HARD` flag when disconnecting to perform a more strict state recalculation

### 4. `drbd_state.c` - Simplification and fixes to quorum calculation logic
- Simplified quorum calculation logic: removed complex logic with partition checks
- Added debug output of quorum state information via `drbd_info()`
- Fixed quorum check: `qd.quorate_peers` is now compared with `min_redundancy_at` instead of a simple truth check
- Added `min_redundancy_at` check to the tie-breaker condition for even number of nodes
- Allowed forced state recalculation (`CS_FORCE_RECALC`) to transition to `suspend-io` state when quorum is lost

## Affected Files

- `drbd/drbd_main.c`
- `drbd/drbd_nl.c`
- `drbd/drbd_receiver.c`
- `drbd/drbd_state.c`
