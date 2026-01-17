# Implement DRBD controller

## Existing codebase

New package path: `images/agent/internal/controllers/drbd`. It already contains
the right structure - `drbd_scanner` runnable, which triggers `drbd_controller`
with generic events, but both scanner and controller are not implemented (dummy
code).

Style rules: `.cursor/rules`. Good example of a controller, which follows the
rules: `images/controller/internal/controllers/rv_controller`. It's
super-important to follow the rules, otherwise the code will not pass code
review.

Old version of a controller: `images/agent/internal/controllers/drbd_config`. It
should be ignored, do not allow it to influence your new package.

Old version of a scanner: `images/agent/internal/scanner`. It still has useful
parts about how to listen DRBD events, but most of functionality is now
deprecated, since we will implement a new scanner with `Runnable` interface of
manager, and a lot of infrastructure is not needed anymore: restarts, panic
recovery, logging, etc. Also we no longer need starting scanner in
`images/agent/cmd/main.go`, and entrypoint can be simplified significantly.
Also, manager queue will be merging duplicate requests, so we no longer need
cooldown and batching.

`images/agent/pkg/drbdsetup` - package, where drbdsetup commands should be
implemented. Anoother command example is `images/agent/pkg/drbdadm`.

`docs/drbd/*.txt` - man pages you would definetely need when working with
drbd-utils commands.

`api/v1alpha1/drbd_resource.go` - our main custom resource, which will be
reconciled.

## New package design

`drbd_scanner` listens for DRBD events and triggers reconciliation of every
`DRBDResource` in `drbd_controller` via generic event.

`drbd_controller` watches `DRBDResource` and triggers reconciliation if
predicates pass. Predicates should check that the `DRBDResource` is from current
node.

## Reconcilation algorithm

1. Take desired state (everything from spec)
2. Calculate intended state (validate the desired state)
3. Collect actual state (mostly with `drbdsetup show` and `drbdsetup status` commands)
4. Compare intended with actual and calcluate target
   If target non-zero:
    1. apply target
    2. refresh actual
5. Put actual to report

Properties in `drbdr.spec`, have different desired, intended, target states, and
they result in different things in actual state, and therefore, produce
different output to the report state. Let's define them:

<TBD>

 - `systemNetworks`
 - `quorum` and `quorumMinimumRedundancy`
 - `state`
 - `size`
 - `role`
 - `allowTwoPrimaries`
 - `type`
 - `lvmLogicalVolumeName`
 - `nodeID`
 - `peers`
 - `peers[*].name`
 - `peers[*].type`
 - `peers[*].allowRemoteRead`
 - `peers[*].nodeID`
 - `peers[*].protocol`
 - `peers[*].sharedSecret`
 - `peers[*].sharedSecretAlg`
 - `peers[*].pauseSync`
 - `peers[*].paths`
 - `peers[*].sharedSecret`
 - `peers[*].sharedSecret`

===========

