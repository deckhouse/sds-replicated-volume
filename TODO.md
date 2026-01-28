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
drbd-utils commands, and also `*_output.txt` files demonstrate sample output of
programs.

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
2. Calculate the intended state (i.e. validate the desired state). Calculation
   result may be cached in variables in status.
3. Collect actual state (mostly with `drbdsetup show` and `drbdsetup status`
   commands)
4. Compare intended with actual and calculate target
   If target non-zero:
    1. apply target
    2. refresh actual
5. Put actual to report

Properties in `drbdr.spec`, have different desired, intended, target states, and
they result in different things in actual state, and therefore, produce
different output to the report state. Let's define them:

 - `systemNetworks`
   - intended state: `address`. The only valid value `Internal` means that we
     should use address from `Node.status.addresses[type="InternalIP"].address`.
     This address should be cached in `drbdr.status.addresses`, and taken from
     there, if it's set.
   - intended state: `port`. Port is the lowest free port on interface
     `address`, in range from 7000 to 8000. This should also be cached in
     `drbdr.status.addresses`.
   - actual state is the address:port used on the current node, according to
     `drbdsetup show`

Other properties - try to figure out by youself, feel free to leave your
converns as TODO comments. Don't change the API schema.

===========

