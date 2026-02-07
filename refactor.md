Let's define a problem starting from "computeTargetStateActions".

Currently it is doing the following:
 - adding finalizer
 - patching status
 - executes drbd commands
 - removing finalizer

Result is abstract Action, and we need to have a complex logic here:
@images/agent/internal/controllers/drbd/reconciler.go:91-92 (flushing pending
K8S patches before executing DRBD commands, etc.) 

We can make it simpler, by structuring root "Reconcile" with the following
focused helpers:

- `getCurrentNodeDRBDR` (we already have it named getCurrentNodeDRBDResource)
- `getCurrentNode` - simply gets current Node
- `reconcileFinalizer(true)`
  - `ensureFinalizer` (adds finalizer, if resource IsUpAndNotInCleanup)
  - `patchDRBDR` (only if finalizer was added)
- `ensureAddresses`
  - `computeIntendedIPs` - what networks and IPs should we use, considering
    `drbdr.spec.systemNetworks`, current Node, but ignoring ports
  - `applyIPs` - update `drbdr.status.addresses` in a way that only intended
    networks and IPs are left. Remove addresses with unknown networks or invalid
    IPs, and add missing addresses. For existing valid addresses - keep their
    ports. For added addresses - leave port 0.
  - `computeIntendedPorts` - computes port for each IP in
    `drbdr.status.addresses`. Prefer existing port, but allocate a new one if
    port is 0. Allocation should happen with the existing port allocator, but it
    should be controller-owned, rather then application-owned, and required
    documentation should be added (see "Note: reconciler-owned" in
    controller-reconcile-helper-compute.mdc)
  - `applyPorts` - changes ports in status.
- `computeIntendedDRBDState` (we already have it as getIntendedState, but make
  sure it only covers DRBD state now)
- `getActualDRBDState` (we already have it as getActualState, but make sure it
  only covers DRBD state now)
- `computeTargetDRBDState` (returning only ExecuteDRBDAction)
- `convergeDRBDState` (execute actions to DRBD, we already have it as a "case
  ExecuteDRBDAction", but we no longer need to "flush pending K8S patches")
- `getActualDRBDState` (for second time, if DRBD state was changed)
- `ensureReportState`
- `patchDRBDRStatus`
- `reconcileFinalizer(false)` - the same as `reconcileFinalizer(true)`, but
  removes finalizer, in case if resource !IsUpAndNotInCleanup 


NOTE: we are refactoring, so changing the behaviour is not our goal. Our goal is
to change the structure of the code and align the terms we use with the rules
and the framework. You are allowed to:
 - rename, move code, simplify logic to satisfy the requirements above
 - define new function signatures and types
 - add flow framework helpers to adjust with the rules
 - fix obvious bugs


TODO: MUST DOWN NON-EXISTING RESOURCES
