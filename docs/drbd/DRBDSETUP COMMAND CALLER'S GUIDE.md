# DRBDSETUP COMMAND CALLER'S GUIDE
- [DRBDSETUP COMMAND CALLER'S GUIDE](#drbdsetup-command-callers-guide)
  - [PREREQUISITES](#prerequisites)
  - [ERROR REPORTING FOR AUTOMATION](#error-reporting-for-automation)
    - [Special Module Loading Behavior](#special-module-loading-behavior)
  - [OPTION SYNTAX RULES](#option-syntax-rules)
    - [Boolean Options](#boolean-options)
    - [Flag Options](#flag-options)
    - [Enum Numeric String Options](#enum-numeric-string-options)
    - [Hidden Mandatory Option: `--_name`](#hidden-mandatory-option---_name)
  - [RESOURCE MANAGEMENT](#resource-management)
    - [`new-resource`](#new-resource)
    - [`resource-options`](#resource-options)
    - [`rename-resource`](#rename-resource)
    - [`down`](#down)
    - [`del-resource`](#del-resource)
  - [DEVICE MANAGEMENT](#device-management)
    - [`new-minor`](#new-minor)
    - [`del-minor`](#del-minor)
    - [`resize`](#resize)
    - [`new-current-uuid`](#new-current-uuid)
  - [DISK MANAGEMENT](#disk-management)
    - [`attach`](#attach)
    - [`disk-options`](#disk-options)
    - [`detach`](#detach)
  - [ROLE MANAGEMENT](#role-management)
    - [`primary`](#primary)
    - [`secondary`](#secondary)
  - [CONNECTION/PEER MANAGEMENT](#connectionpeer-management)
    - [`new-peer`](#new-peer)
    - [`del-peer`](#del-peer)
    - [`forget-peer`](#forget-peer)
    - [`new-path`](#new-path)
    - [`del-path`](#del-path)
    - [`connect`](#connect)
    - [`disconnect`](#disconnect)
    - [`net-options`](#net-options)
    - [`peer-device-options`](#peer-device-options)
  - [INSPECTION AND MONITORING](#inspection-and-monitoring)
    - [`show`](#show)
    - [`status`](#status)
    - [`events2`](#events2)
  - [DRBDMETA OPERATIONS](#drbdmeta-operations)
    - [`drbdmeta create-md`](#drbdmeta-create-md)
    - [`drbdmeta dump-md`](#drbdmeta-dump-md)
  - [TYPICAL COMMAND SEQUENCES](#typical-command-sequences)

## PREREQUISITES

Before calling any `drbdsetup` or `drbdmeta` command that operates on a minor
number, ensure the lock directory exists:
```bash
mkdir -p /var/run/drbd/lock
```

This directory is required by all commands that operate on a minor number
(`new-minor`, `attach`, `detach`, `disk-options`, `resize`, `new-current-uuid`,
etc.) and by all `drbdmeta` operations. Without it, these commands fail with
exit code `20`:
```
open(/var/run/drbd/lock/drbd-147-<minor>): No such file or directory
```

The lock prevents `drbdsetup` and `drbdmeta` from racing on the same minor. The
path is compiled into the binary at build time (default: `/var/run/drbd/lock/`).
It is not runtime-configurable and cannot be disabled.

Commands that do NOT require the lock directory: `new-resource`, `down`,
`primary`, `secondary`, `new-peer`, `del-peer`, `new-path`, `del-path`,
`connect`, `disconnect`, `show`, `status`, `events2`, and other read-only or
resource/peer-scoped commands.

---

## ERROR REPORTING FOR AUTOMATION

`drbdsetup` uses three different failure styles, and automation should treat
them differently:

- **Kernel configuration errors** print `Failure: (<code>) <message>` and
  usually exit with code `10`.
- **Kernel state-change errors** print `State change failed: (<code>) <message>`
  and exit with code `5`, `11`, `16`, or `17`.
- **Userspace/local errors** usually exit with code `20` and often have **no
  kernel error code** at all; examples include invalid CLI syntax, bad address
  parsing, netlink send/receive failures, multicast subscription failure, and
  initial module-load failure.
- **Module disappeared while running** is a special userspace case:
  `ERR_MODULE_UNLOADED` maps to exit code `121`.

Many kernel failures also include extra lines under `additional info from
kernel:`. For automation, the most reliable fields are:

- process exit code
- the numeric code inside `Failure: (<n>)` or `State change failed: (<n>)`
- any stable kernel info-text that follows

This guide documents errors **per command** below, because the same exit code
often covers many different situations.

### Special Module Loading Behavior

When DRBD kernel module cannot be loaded (`modprobe drbd` fails):
- **Teardown commands** (`down`, `secondary`, `disconnect`, `detach`) → Exit `0`
  (idempotent - already "down")
- **All other commands** → Exit `20` (cannot operate without module)

---

## OPTION SYNTAX RULES

**CRITICAL:** Different option types require different syntax!

### Boolean Options
```bash
# Method 1: Use equals sign
--auto-promote=yes
--auto-promote=no

# Method 2: Use prefix for "no"
--auto-promote        # sets to yes
--no-auto-promote     # sets to no

# ❌ WRONG - will cause "Excess arguments" error:
--auto-promote yes
--auto-promote no
```

### Flag Options
```bash
# Flags are presence-only switches.
# They do not take yes/no values.
--force
--overwrite-data-of-peer
--tentative
--diskless
--discard-my-data
--clear-bitmap
--force-resync
--assume-peer-has-space
--assume-clean
```

### Enum Numeric String Options
```bash
# Both syntaxes work:
--protocol=C              # With equals
--protocol C              # With space

--timeout=50              # With equals
--timeout 50              # With space

--on-no-data-accessible=suspend-io    # With equals
--on-no-data-accessible suspend-io    # With space
```

**Best Practice:** Use `=` for all options to avoid confusion and ensure
consistency.

### Hidden Mandatory Option: `--_name`

**Required for:** `new-peer` command only

**Error if omitted:** 
```
Failure: (126) UnknownMandatoryTag
additional info from kernel:
name missing
```

**Value:** A unique identifier for the connection (e.g., peer hostname or
explicit connection name)

**Example:**
```bash
drbdsetup new-peer myres 2 --_name=peer-node --protocol=C
```

**Why it exists:** The DRBD 9 kernel protocol requires a connection name field
marked as DRBD_GENLA_F_MANDATORY. Higher-level tooling often auto-generates this
from configuration, but when calling `drbdsetup` directly, you must provide it
explicitly.

## RESOURCE MANAGEMENT

### `new-resource`
**Purpose:** Create a new DRBD resource

**Synopsis:**
```bash
drbdsetup new-resource <resource_name> <node_id> [options]
```

**Arguments:**
- `resource_name` - Name of the resource to create
- `node_id` - Numeric node ID (required, must be unique in cluster)

**Options** (resource-options):
- `--cpu-mask=<mask>` (STRING) - CPU affinity mask
- `--on-no-data-accessible={io-error|suspend-io}` (ENUM)
  - Action when no data accessible
- `--auto-promote={yes|no}` (BOOLEAN)
  - Enable automatic promotion (default: yes)
- `--peer-ack-window=<bytes>` (NUMERIC) - Peer acknowledgement window
- `--peer-ack-delay=<ms>` (NUMERIC) - Peer acknowledgement delay (milliseconds)
- `--twopc-timeout=<1/10s>` (NUMERIC) - Two-phase commit timeout
- `--twopc-retry-timeout=<1/10s>` (NUMERIC) - Two-phase commit retry timeout
- `--auto-promote-timeout=<1/10s>` (NUMERIC) - Auto-promote timeout
- `--max-io-depth=<num>` (NUMERIC) - Maximum I/O queue depth (nr_requests)
- `--quorum={off|majority|all|<1-32>}` (ENUM_NUM) - Quorum setting -
`--on-no-quorum={io-error|suspend-io}` (ENUM) - Action when no quorum -
`--quorum-minimum-redundancy={off|majority|all|<1-32>}`
  (ENUM_NUM) - Minimum redundancy
- `--on-suspended-primary-outdated={disconnect|force-secondary}`
  (ENUM) - Action on suspended primary outdated
- `--quorum-dynamic-voters={yes|no}` (BOOLEAN) - Controls the "last man standing"
  quorum optimization. When enabled (default: yes), absent nodes that are known to
  be outdated or quorumless are excluded from voters, allowing the remaining
  partition to keep quorum. When disabled, all known nodes always count as voters
  regardless of state, enforcing strict majority quorum.
  **Flant extension** (available since DRBD kernel module 9.2.16-flant.1)

**Exit Codes and Errors:**
- `0` - Resource created successfully
- `10` - Kernel error. Match stderr:
  - `Failure: (126) UnknownMandatoryTag`
  - `Failure: (162) Invalid configuration request`
  - `Failure: (122) kmalloc() failed. Out of memory?`
  - optional `additional info from kernel:` lines such as `required attribute
    missing`, `unknown mandatory attribute`, `invalid attribute value`
- `20` - Local `drbdsetup` error, no kernel `(n)` code. Match stderr such as:
  - `Missing argument '...'`
  - `Excess arguments:`
  - `error sending config command`
  - `error receiving config reply`
  - `Could not connect to 'drbd' generic netlink family`

**Notes:**
- Must be first command when setting up a resource
- Node ID must be unique across all nodes in the cluster

---

### `resource-options`
**Purpose:** Change options of an existing resource

**Synopsis:**
```bash
drbdsetup resource-options <resource_name> [options]
```

**Arguments:**
- `resource_name` - Name of existing resource

**Options:**
- Same as `new-resource` (see above)
- `--set-defaults` (FLAG)
  - Explicitly reset unspecified changeable options to their defaults

**Exit Codes and Errors:**
- `0` - Options updated successfully
- `10` - Kernel error. Match stderr:
  - `Failure: (158) Unknown resource`
  - `Failure: (126) UnknownMandatoryTag`
  - `Failure: (129) Interrupted by Signal`
  - `Failure: (122) kmalloc() failed. Out of memory?`
  - `Failure: (162) Invalid configuration request`
  - optional `additional info from kernel:` lines describing the rejected
    attribute
- `20` - Local `drbdsetup` error, no kernel `(n)` code. Match stderr such as:
  - `Missing argument '...'`
  - `Excess arguments:`
  - `error sending config command`
  - `error receiving config reply`

**Notes:**
- Can change options on a running resource (most options)
- Some options may require disconnection/detachment

---

### `rename-resource`
**Purpose:** Rename a resource on the local node

**Synopsis:**
```bash
drbdsetup rename-resource <resource_name> <new_name>
```

**Arguments:**
- `resource_name` - Current resource name
- `new_name` - New resource name

**Options:** None

**Exit Codes and Errors:**
- `0` - Resource renamed successfully
- `10` - Kernel error. Match stderr:
  - `Failure: (158) Unknown resource`
  - `Failure: (126) UnknownMandatoryTag`
  - `Failure: (162) Invalid configuration request`
  - `Failure: (174) Already exists`
  - `Failure: (122) kmalloc() failed. Out of memory?`
  - optional `additional info from kernel:` lines such as `name missing`
  - current kernel info-text may also say: `Cannot rename to ...: a resource
    with that name already exists`
  - trust the numeric `(n)` first
- `20` - Local `drbdsetup` error, no kernel `(n)` code. Match stderr such as:
  - `Missing argument 'new_name'`
  - `Excess arguments:`
  - `error sending config command`
  - `error receiving config reply`

**Notes:**
- **Local operation only** - doesn't affect peer nodes
- DRBD protocol has no concept of resource names
- **Strongly recommended:** Run same command on all nodes for consistency
- Emits a `rename` event on the `events2` stream
- Resource can have different names on different nodes (technically possible but
  not recommended)

---

### `down`
**Purpose:** Completely tear down a resource (all-in-one shutdown)

**Synopsis:**
```bash
drbdsetup down [<resource_name>|all]
```

**Arguments:**
- `resource_name` - Name of resource to tear down
- `all` - Tear down all resources

**Options:** None

**Exit Codes and Errors:**
- `0` - Resource torn down successfully
  - may also succeed with warning-only output such as `Resource unknown`
  - initial module-load failure is also treated as success for `down`
- `10` - Kernel error. Match stderr:
  - `Failure: (129) Interrupted by Signal`
- `11` - State-change error. Match stderr:
  - `State change failed: (-12) Device is held open by someone`
  - `State change failed: (-10) State change was refused by peer node`
  - `State change failed: (-19) Concurrent state changes detected and aborted`
  - `State change failed: (-23) Timeout in operation`
- `17` - State-change error. Match stderr:
  - `State change failed: (-2) Need access to UpToDate data`
- `20` - Local `drbdsetup` error, no kernel `(n)` code. Match stderr such as:
  - `error sending config command`
  - `error receiving config reply`

**What it does (internally):**
1. Lists all devices in the resource
2. Sends `DRBD_ADM_DOWN` netlink command
3. Unregisters all minors
4. Unregisters the resource
5. Removes all volumes, connections, and the resource itself

**Notes:**
- **Convenience command** - does everything in one shot
- Succeeds even if DRBD module not loaded
- If no argument is given, drbdsetup treats it as `all`
- Preferred over manual teardown sequence
- Equivalent to: disconnect + detach + del-minor (for all) + del-resource

---

### `del-resource`
**Purpose:** Remove a resource (after all minors and connections are removed)

**Synopsis:**
```bash
drbdsetup del-resource <resource_name>
```

**Arguments:**
- `resource_name` - Name of resource to remove

**Options:** None

**Exit Codes and Errors:**
- `0` - Resource removed successfully
- `10` - Kernel error. Match stderr:
  - `Failure: (158) Unknown resource`
  - `Failure: (125) Device has a net-config (use disconnect first)`
  - `Failure: (159) Resource still in use (delete all minors first)`
  - `Failure: (162) Invalid configuration request`
- `20` - Local `drbdsetup` error, no kernel `(n)` code. Match stderr such as:
  - `Missing argument '...'`
  - `error sending config command`
  - `error receiving config reply`

**Notes:**
- All volumes (`del-minor`) and connections (`del-peer`) must be removed first
- Prefer using `down` instead, which does everything in one command

---

## DEVICE MANAGEMENT

### `new-minor`
**Purpose:** Create a new DRBD device/volume within a resource

**Synopsis:**
```bash
drbdsetup new-minor <resource_name> <minor> <volume> [options]
```

**Arguments:**
- `resource_name` - Parent resource name
- `minor` - Device minor number (creates /dev/drbd<minor>)
- `volume` - Volume number within resource

**Options** (device-options):
- `--max-bio-size=<bytes>` (NUMERIC) - Maximum BIO size
- `--diskless` (FLAG) - Mark as intentionally diskless
- `--block-size=<bytes>` (NUMERIC) - Block size for the device

**Exit Codes and Errors:**
- `0` - Minor created successfully
- `10` - Kernel error. Match stderr:
  - `Failure: (158) Unknown resource`
  - `Failure: (126) UnknownMandatoryTag`
  - `Failure: (161) Minor or volume exists already (delete it first)`
  - `Failure: (162) Invalid configuration request`
  - `Failure: (122) kmalloc() failed. Out of memory?`
  - userspace may also print before the `Failure:` line:
    - `new-minor <resource> <minor> <volume>: sysfs node`
      `'/sys/devices/virtual/block/drbd<minor>'` `(already? still?) exists`
- `20` - Local `drbdsetup` error, no kernel `(n)` code. Match stderr such as:
  - `Missing argument '...'`
  - `error sending config command`
  - `error receiving config reply`

**Notes:**
- Minor number must be unique system-wide
- Checks for existing sysfs nodes to avoid kernel issues
- Resource must exist before creating minors
- When `--diskless` is used, skip the `attach` command. The device operates as a
  diskless client reading and writing via peers only.

**Verifying intentional diskless state:**
- `drbdsetup show`: volume block contains `disk none;` (vs. no `disk` line for
  temporarily diskless)
- `drbdsetup show --json`: volume object contains `"disk": "none"` (vs. key
  absent for temporarily diskless)
- `drbdsetup status --json`: device object contains `"client": true"` (vs.
  `"client": false"`)
- `drbdsetup status` (text): shows `client:yes` when verbose or non-TTY output
  (vs. `client:no`)
- `drbdsetup events2`: shows `client:yes` (vs. `client:no`)

---

### `del-minor`
**Purpose:** Remove a replicated device/volume from a resource

**Synopsis:**
```bash
drbdsetup del-minor <minor>
```

**Arguments:**
- `minor` - Device minor number to remove

**Options:** None

**Exit Codes and Errors:**
- `0` - Minor removed successfully
- `10` - Kernel error. Match stderr:
  - `Failure: (127) Device minor not allocated`
  - `Failure: (129) Interrupted by Signal`
  - `Failure: (160) Minor still configured (down it first)`
- `20` - Local `drbdsetup` error, no kernel `(n)` code. Match stderr such as:
  - `open(/var/run/drbd/lock/drbd-147-<minor>): No such file or directory`
  - `error sending config command`
  - `error receiving config reply`

**Notes:**
- Disk must be detached first (`detach`)
- Acquires the per-minor lock (requires `/var/run/drbd/lock/` directory)

---

### `resize`
**Purpose:** Resize a replicated device after growing backing devices

**Synopsis:**
```bash
drbdsetup resize <minor> [options]
```

**Arguments:**
- `minor` - Device minor number

**Options:**
- `--size=<sectors>` (NUMERIC) - New size
- `--assume-peer-has-space` (FLAG) - Do not wait for peer space confirmation
- `--assume-clean` (FLAG) - Assume new space is identical on all nodes
- `--al-stripes=<num>` (NUMERIC) - Change activity log stripes during resize
- `--al-stripe-size-kB=<num>` (NUMERIC) - Change activity log stripe size during
  resize

**Exit Codes and Errors:**
- `0` - Resize completed successfully
- `10` - Kernel error. Match stderr:
  - `Failure: (127) Device minor not allocated`
  - `Failure: (126) UnknownMandatoryTag`
  - `Failure: (129) Interrupted by Signal`
  - `Failure: (138) Device does not have a disk-config`
  - `Failure: (130) Resize not allowed during resync.`
  - `Failure: (111) Low.dev. smaller than requested DRBD-dev. size.`
  - `Failure: (131) Need one Primary node to resize.`
  - `Failure: (153) Protocol version 93 required to use --assume-clean`
  - `Failure: (122) kmalloc() failed. Out of memory?`
  - `Failure: (167) Resulting AL area too big`
  - `Failure: (168) Resulting AL are too small`
  - `Failure: (140) vmalloc() failed. Out of memory?`
  - `Failure: (169) Resulting AL does not fit into available meta data space`
  - `Failure: (170) Implicit device shrinking not allowed. See kernel log.`
- `11` - State-change error. Match stderr:
  - `State change failed: (-21) Interrupted state change`
- `20` - Local `drbdsetup` error, no kernel `(n)` code. Match stderr such as:
  - `open(/var/run/drbd/lock/drbd-147-<minor>): No such file or directory`
  - `error sending config command`
  - `error receiving config reply`

**Notes:**
- Run this command after growing backing devices on all nodes
- By default, new space is marked out-of-sync and will be resynced
- **--assume-clean is dangerous:** Only use if you know new space is identical
  (e.g., extended with zeros on all nodes)
- **--assume-peer-has-space is risky:** Peer may run out of space during writes
- One node must be Primary for resize to work
- Cannot resize during active resync

### `new-current-uuid`
**Purpose:** Generate a new current UUID

**Synopsis:**
```bash
drbdsetup new-current-uuid <minor> [options]
```

**Arguments:**
- `minor` - Device minor number

**Options:**
- `--clear-bitmap` (FLAG) - Clear sync bitmap
- `--force-resync` (FLAG) - Force resync from this node

**Exit Codes and Errors:**
- `0` - New UUID generated successfully
- `10` - Kernel error. Match stderr:
  - `Failure: (127) Device minor not allocated`
  - `Failure: (126) UnknownMandatoryTag`
  - `Failure: (129) Interrupted by Signal`
  - `Failure: (138) Device does not have a disk-config`
  - `Failure: (151) Need to be StandAlone`
  - `Failure: (118) IO error(s) occurred during initial access to meta-data.`
  - `Failure: (162) Invalid configuration request`
- `20` - Local `drbdsetup` error, no kernel `(n)` code. Match stderr such as:
  - `open(/var/run/drbd/lock/drbd-147-<minor>): No such file or directory`
  - `error sending config command`
  - `error receiving config reply`

**Notes:**
- **Three use cases:**
  1. **Skip initial resync:** `--clear-bitmap` on "Just Created" metadata
  2. **Force initial resync:** `--force-resync` on "Just Created" metadata
  3. **Bootstrap single node:** Create additional nodes from a copy
- Requires "Just Created" metadata for `--clear-bitmap` and `--force-resync`
- **--clear-bitmap scenario:** After metadata has been initialized on all nodes,
  do the initial handshake, then call with `--clear-bitmap`
- **Bootstrap scenario:** On the running primary: `new-current-uuid
  --clear-bitmap`, copy disk/metadata, then `new-current-uuid`
- Changes data generation identifiers and therefore resync decisions

## DISK MANAGEMENT

### `attach`
**Purpose:** Attach a backing device and meta-data device to a volume

**Synopsis:**
```bash
drbdsetup attach <minor> <lower_dev> <meta_dev> <meta_idx> [options]
```

**Arguments:**
- `minor` - Device minor number
- `lower_dev` - Path to backing block device (e.g., /dev/sda1)
- `meta_dev` - Path to meta-data block device (or "internal")
- `meta_idx` - Meta-data index (or "internal", "flexible")

**Options** (disk-options + changeable):
- `--size=<sectors>` (NUMERIC)
  - DRBD device size
  - Default unit: sectors (512 bytes each); suffixes `K/M/G` accepted
- `--on-io-error={pass_on|call-local-io-error|detach}`
  (ENUM) - I/O error handling
- `--disk-barrier={yes|no}` (BOOLEAN) - Use disk barriers -
`--disk-flushes={yes|no}` (BOOLEAN) - Use disk flushes - `--disk-drain={yes|no}`
(BOOLEAN) - Drain before barrier - `--md-flushes={yes|no}` (BOOLEAN) - Flush
meta-data
- `--resync-after=<minor>` (NUMERIC) - Resync after this other minor
- `--al-extents=<num>` (NUMERIC) - Activity log extents (default: 1237)
- `--al-updates={yes|no}` (BOOLEAN) - Enable activity log updates -
`--discard-zeroes-if-aligned={yes|no}` (BOOLEAN) - Discard optimization -
`--disable-write-same={yes|no}` (BOOLEAN) - Disable WRITE_SAME
- `--disk-timeout=<1/10s>` (NUMERIC) - Disk timeout
- `--read-balancing={prefer-local|prefer-remote|` `round-robin|least-pending|`
  `when-congested-remote|*K-striping}` (ENUM)
  - Read balancing policy
- `--rs-discard-granularity=<bytes>` (NUMERIC) - Resync discard granularity
- `--non-voting={yes|no}` (BOOLEAN) - Exclude this volume from quorum voting
  while still replicating data normally. A non-voting disk reverses sync direction
  to always be a sync target, preventing it from overwriting voting peers' data.
  The self-side counterpart to `allow-remote-read=no` (which excludes a peer on the
  connection side). Default: no.
  **Flant extension** (available since DRBD kernel module 9.2.16-flant.1)

**Exit Codes and Errors:**
- `0` - Device attached successfully
- `10` - Kernel error. Match stderr:
  - `Failure: (127) Device minor not allocated`
  - `Failure: (126) UnknownMandatoryTag`
  - `Failure: (116) Lower device / meta device / index combination invalid.`
  - `Failure: (132) The resync-after minor number is invalid`
  - `Failure: (133) This would cause a resync-after dependency cycle`
  - `Failure: (104) Can not open backing device.`
  - `Failure: (105) Can not open meta device.`
  - `Failure: (124) Device is attached to a disk (use detach first)`
  - `Failure: (165) Unclean meta-data found.`
  - `Failure: (119) No valid meta-data signature found.`
  - `Failure: (118) IO error(s) occurred during initial access to meta-data.`
  - `Failure: (111) Low.dev. smaller than requested DRBD-dev. size.`
  - `Failure: (112) Meta device too small.`
  - `Failure: (150) Can only attach to the data we lost last (see kernel log).`
  - `Failure: (170) Implicit device shrinking not allowed. See kernel log.`
  - `Failure: (140) vmalloc() failed. Out of memory?`
  - `Failure: (122) kmalloc() failed. Out of memory?`
  - `Failure: (162) Invalid configuration request`
  - optional `additional info from kernel:` lines with attach-specific details
- `11` - State-change error. Match stderr:
  - `State change failed: (-10) State change was refused by peer node`
  - `State change failed: (-26) Intentional diskless peer` `may not attach a
    disk`
- `20` - Local `drbdsetup` error, no kernel `(n)` code. Match stderr such as:
  - `open(/var/run/drbd/lock/drbd-147-<minor>): No such file or directory`
  - `error sending config command`
  - `error receiving config reply`

**Notes:**
- Backing device must not be in use
- Meta-data must be initialized (`drbdmeta create-md`)
- Device sizes validated against meta-data
- **drbdmeta create-md:** For direct `drbdmeta` use, pass the peer count as a
  **positional argument**, not `--max-peers=N`. Example: `drbdmeta 0 v09
  /dev/vg0/lv0 internal create-md --force 31`.

---

### `disk-options`
**Purpose:** Change disk options on an attached device

**Synopsis:**
```bash
drbdsetup disk-options <minor> [options]
```

**Arguments:**
- `minor` - Device minor number

**Options:**
- Same changeable disk options as `attach` (see above)
- `--set-defaults` (FLAG)
  - Explicitly reset unspecified changeable options to their defaults

**Exit Codes and Errors:**
- `0` - Options changed successfully
- `10` - Kernel error. Match stderr:
  - `Failure: (127) Device minor not allocated`
  - `Failure: (138) Device does not have a disk-config`
  - `Failure: (126) UnknownMandatoryTag`
  - `Failure: (129) Interrupted by Signal`
  - `Failure: (132) The resync-after minor number is invalid`
  - `Failure: (133) This would cause a resync-after dependency cycle`
  - `Failure: (122) kmalloc() failed. Out of memory?`
  - `Failure: (162) Invalid configuration request`
- `20` - Local `drbdsetup` error, no kernel `(n)` code. Match stderr such as:
  - `open(/var/run/drbd/lock/drbd-147-<minor>): No such file or directory`
  - `error sending config command`
  - `error receiving config reply`

**Notes:**
- Can change most options online
- Some changes may require specific states

---

### `detach`
**Purpose:** Detach the backing device from a replicated device

**Synopsis:**
```bash
drbdsetup detach <minor> [options]
```

**Arguments:**
- `minor` - Device minor number

**Options:**
- `--force` (FLAG) - Force detach, fails pending I/O immediately
- `--diskless` (FLAG)
  - Mark volume as permanently diskless (intentional diskless client)

**Exit Codes and Errors:**
- `0` - Device detached successfully
  - initial module-load failure is also treated as success for `detach`
- `10` - Kernel error. Match stderr:
  - `Failure: (127) Device minor not allocated`
  - `Failure: (126) UnknownMandatoryTag`
  - `Failure: (129) Interrupted by Signal`
  - `Failure: (162) Invalid configuration request`
- `11` - State-change error. Match stderr:
  - `State change failed: (-25) No quorum`
- `17` - State-change error. Match stderr:
  - `State change failed: (-2) Need access to UpToDate data`
- `20` - Local `drbdsetup` error, no kernel `(n)` code. Match stderr such as:
  - `open(/var/run/drbd/lock/drbd-147-<minor>): No such file or directory`
  - `error sending config command`
  - `error receiving config reply`

**Notes:**
- Without --force: waits for pending I/O to complete
- With `--force`, returns immediately, pending I/O fails, and the device enters
  `Failed` until I/O completes
- `--diskless` converts from "temporarily diskless" to "intentionally diskless
  client"
- Cannot detach if it would leave no up-to-date copy of the data

---

## ROLE MANAGEMENT

### `primary`
**Purpose:** Change the role of a node in a resource to primary

**Synopsis:**
```bash
drbdsetup primary <resource_name> [options]
```

**Arguments:**
- `resource_name` - Resource name

**Options:**
- `--force` (FLAG) - Force promotion even without up-to-date data
- `--overwrite-data-of-peer` (FLAG)
  - Alias for `--force` (backward compatibility)

**Exit Codes and Errors:**
- `0` - Role changed to primary successfully
- `10` - Kernel error. Match stderr:
  - `Failure: (126) UnknownMandatoryTag`
  - `Failure: (129) Interrupted by Signal`
  - `Failure: (158) Unknown resource`
  - `Failure: (162) Invalid configuration request`
- `11` - State-change error. Match stderr:
  - `State change failed: (-1) Multiple primaries not allowed by config`
  - `State change failed: (-7) Refusing to be Primary while peer is not
    outdated`
  - `State change failed: (-10) State change was refused by peer node`
  - `State change failed: (-12) Device is held open by someone`
  - `State change failed: (-19) Concurrent state changes detected and aborted`
  - `State change failed: (-22) Peer may not become primary while device is
    opened read-only`
  - `State change failed: (-23) Timeout in operation`
  - `State change failed: (-24) Primary nodes must be strongly connected among
    each other`
  - `State change failed: (-25) No quorum`
- `17` - State-change error. Match stderr:
  - `State change failed: (-2) Need access to UpToDate data`
- `20` - Local `drbdsetup` error, no kernel `(n)` code. Match stderr such as:
  - `error sending config command`
  - `error receiving config reply`

**Notes:**
- Allows mounting or opening devices for writing
- DRBD normally allows only one primary node at a time (unless
  --allow-two-primaries is set)
- **--force is dangerous:** Can lead to split-brain scenarios and data
  divergence
- When force-promoting an Inconsistent device, verify data integrity before use
- If --allow-two-primaries is set, external coordination (cluster manager)
  required

---

### `secondary`
**Purpose:** Change the role of a node in a resource to secondary

**Synopsis:**
```bash
drbdsetup secondary <resource_name> [options]
```

**Arguments:**
- `resource_name` - Resource name

**Options:**
- `--force` (FLAG) - Force demotion, terminates all pending and new I/O with
  errors

**Exit Codes and Errors:**
- `0` - Role changed to secondary successfully
  - initial module-load failure is also treated as success for `secondary`
- `10` - Kernel error. Match stderr:
  - `Failure: (126) UnknownMandatoryTag`
  - `Failure: (129) Interrupted by Signal`
  - `Failure: (158) Unknown resource`
  - `Failure: (162) Invalid configuration request`
- `11` - State-change error. Match stderr:
  - `State change failed: (-10) State change was refused by peer node`
  - `State change failed: (-12) Device is held open by someone`
  - `State change failed: (-19) Concurrent state changes detected and aborted`
  - `State change failed: (-23) Timeout in operation`
- `20` - Local `drbdsetup` error, no kernel `(n)` code. Match stderr such as:
  - `error sending config command`
  - `error receiving config reply`

**Notes:**
- Fails if any device in the resource is in use (mounted, open for writing)
- **--force is dangerous:** Returns immediately but causes all I/O to fail with
  errors
- After forced demotion, unmount any filesystems and wait for
  `force-io-failures` flag to clear (check via `drbdsetup status` or `events2`)
- Device cannot be used until `force-io-failures` returns to `no`

---

## CONNECTION/PEER MANAGEMENT

### `new-peer`
**Purpose:** Make a peer node known to the resource

**Synopsis:**
```bash
drbdsetup new-peer <resource_name> <peer_node_id> --_name=<connection_name> [options]
```

**Arguments:**
- `resource_name` - Resource name
- `peer_node_id` - Peer's node ID

**Required Options:**
- `--_name=<connection_name>` (STRING) **[MANDATORY]** - Connection name
  identifier

**Immutable Options:**
- `--transport=<name>` (STRING) - Transport type (tcp, rdma, etc.)
- `--load-balance-paths={yes|no}` (BOOLEAN) - Load balance across multiple paths

**Changeable Options:**
- `--protocol={A|B|C}` (ENUM) - Replication protocol
- `--timeout=<1/10s>` (NUMERIC) - Network timeout
- `--max-epoch-size=<num>` (NUMERIC) - Maximum epoch size
- `--connect-int=<seconds>` (NUMERIC) - Connection retry interval
- `--ping-int=<seconds>` (NUMERIC) - Keepalive ping interval
- `--sndbuf-size=<bytes>` (NUMERIC) - Send buffer size
- `--rcvbuf-size=<bytes>` (NUMERIC) - Receive buffer size
- `--ko-count=<num>` (NUMERIC) - Keepalive timeout count
- `--allow-two-primaries={yes|no}` (BOOLEAN) - Allow dual-primary
- `--cram-hmac-alg=<algorithm>` (STRING) - HMAC algorithm for authentication
- `--shared-secret=<secret>` (STRING) - Shared secret for authentication
- `--after-sb-0pri={disconnect|discard-younger-primary|discard-older-primary|discard-zero-changes|discard-least-changes|discard-local|discard-remote}`
(ENUM) - Split-brain 0-primary recovery -
`--after-sb-1pri={disconnect|consensus|violently-as0p|discard-secondary|call-pri-lost-after-sb}`
(ENUM) - Split-brain 1-primary recovery -
`--after-sb-2pri={disconnect|violently-as0p|call-pri-lost-after-sb}` (ENUM) -
Split-brain 2-primary recovery - `--always-asbp={yes|no}` (BOOLEAN) - Always
apply after-split-brain policies -
`--rr-conflict={disconnect|violently|call-pri-lost|retry-connect|auto-discard}`
(ENUM) - Concurrent writes resolution
- `--ping-timeout=<1/10s>` (NUMERIC) - Ping timeout
- `--data-integrity-alg=<algorithm>` (STRING) - Data integrity algorithm
- `--tcp-cork={yes|no}` (BOOLEAN) - TCP_CORK optimization -
`--on-congestion={block|pull-ahead|disconnect}` (ENUM) - Congestion handling
- `--congestion-fill=<bytes>` (NUMERIC) - Congestion fill threshold
- `--congestion-extents=<num>` (NUMERIC) - Congestion extents threshold
- `--csums-alg=<algorithm>` (STRING) - Checksum algorithm
- `--csums-after-crash-only={yes|no}` (BOOLEAN) - Only checksum after crash
- `--verify-alg=<algorithm>` (STRING) - Online verify algorithm
- `--use-rle={yes|no}` (BOOLEAN) - Use run-length encoding
- `--socket-check-timeout=<1/10s>` (NUMERIC) - Socket check timeout
- `--fencing={dont-care|resource-only|resource-and-stonith}` (ENUM) - Fencing
  policy
- `--max-buffers=<num>` (NUMERIC) - Maximum buffers
- `--allow-remote-read={yes|no}` (BOOLEAN) - Allow reading from secondary -
`--tls={yes|no}` (BOOLEAN) - Enable TLS
- `--tls-keyring=<keyring>` (KEY_SERIAL) - TLS keyring ID
- `--tls-privkey=<key>` (KEY_SERIAL) - TLS private key ID
- `--tls-certificate=<cert>` (KEY_SERIAL) - TLS certificate ID
- `--rdma-ctrl-rcvbuf-size=<bytes>` (NUMERIC) - RDMA control receive buffer
- `--rdma-ctrl-sndbuf-size=<bytes>` (NUMERIC) - RDMA control send buffer

**Exit Codes and Errors:**
- `0` - Peer created successfully
- `10` - Kernel error. Match stderr:
  - `Failure: (126) UnknownMandatoryTag`
  - `Failure: (129) Interrupted by Signal`
  - `Failure: (158) Unknown resource`
  - `Failure: (162) Invalid configuration request`
  - `Failure: (174) Already exists`
  - `Failure: (122) kmalloc() failed. Out of memory?`
  - `Failure: (172) Failed to create transport (drbd_transport_xxx module
    missing?)`
  - `Failure: (139) Protocol C required`
  - `Failure: (154) Fencing policy resource-and-stonith only with prot B or C
    allowed`
  - `Failure: (155) on-congestion policy pull-ahead only with prot A allowed`
  - `Failure: (144) CSUMSAlgNotAvail`
  - `Failure: (146) VERIFYAlgNotAvail`
  - `Failure: (141) The 'data-integrity-alg' you specified is not known in the
    kernel. (Maybe you need to modprobe it, or modprobe hmac?)`
  - `Failure: (120) The 'cram-hmac-alg' you specified is not known in the
    kernel. (Maybe you need to modprobe it, or modprobe hmac?)`
  - `Failure: (125) Device has a net-config (use disconnect first)`
  - optional `additional info from kernel:` lines such as `name missing`
- `20` - Local `drbdsetup` error, no kernel `(n)` code. Match stderr such as:
  - `Missing argument '...'`
  - `error sending config command`
  - `error receiving config reply`

**Notes:**
- **CRITICAL:** Must specify `--_name=<connection_name>` - this is a hidden
  mandatory field
  - Connection name can be: explicit name from config, peer hostname, or any
    unique identifier
  - Higher-level tooling often auto-generates this, but `drbdsetup` requires it
    explicitly
  - Omitting it causes: "Failure: (126) UnknownMandatoryTag" with "name missing"
- Peer must be created before adding paths or connecting
- Transport is immutable after peer creation
- Peer node ID must match configuration on peer node

---

### `del-peer`
**Purpose:** Remove a peer connection

**Synopsis:**
```bash
drbdsetup del-peer <resource_name> <peer_node_id> [options]
```

**Arguments:**
- `resource_name` - Resource name
- `peer_node_id` - Peer's node ID to remove

**Options:**
- `--force` (FLAG) - Force disconnect even if not cleanly possible

**Exit Codes and Errors:**
- `0` - Peer removed successfully
- `10` - Kernel error. Match stderr:
  - `Failure: (126) UnknownMandatoryTag`
  - `Failure: (129) Interrupted by Signal`
  - `Failure: (158) Unknown resource`
  - `Failure: (162) Invalid configuration request`
- `11` - State-change error. Match stderr:
  - `State change failed: (-10) State change was refused by peer node`
  - `State change failed: (-25) No quorum`
- `17` - State-change error. Match stderr:
  - `State change failed: (-2) Need access to UpToDate data`
- `20` - Local `drbdsetup` error, no kernel `(n)` code. Match stderr such as:
  - `error sending config command`
  - `error receiving config reply`

**Notes:**
- Connection must be disconnected first (unless --force)
- Removes peer configuration from kernel

---

### `forget-peer`
**Purpose:** Remove all references to a peer from meta-data

**Synopsis:**
```bash
drbdsetup forget-peer <resource_name> <peer_node_id>
```

**Arguments:**
- `resource_name` - Resource name
- `peer_node_id` - Peer node ID to forget

**Options:** None (other than the peer_node_id argument)

**Exit Codes and Errors:**
- `0` - Peer forgotten successfully
- `10` - Kernel error. Match stderr:
  - `Failure: (126) UnknownMandatoryTag`
  - `Failure: (129) Interrupted by Signal`
  - `Failure: (158) Unknown resource`
  - `Failure: (125) Device has a net-config (use disconnect first)`
  - `Failure: (171) Invalid peer-node-id`
- `20` - Local `drbdsetup` error, no kernel `(n)` code. Match stderr such as:
  - `Missing argument '...'`
  - `error sending config command`
  - `error receiving config reply`

**Notes:**
- **Destructive operation** - removes peer from meta-data
- Peer must be disconnected
- Used when permanently removing a node from cluster
- Cannot be undone without re-syncing

---

### `new-path`
**Purpose:** Add a network path (address pair) to a peer

**Synopsis:**
```bash
drbdsetup new-path <resource_name> <peer_node_id> <local_addr> <remote_addr>
```

**Arguments:**
- `resource_name` - Resource name
- `peer_node_id` - Peer's node ID
- `local_addr` - Local address:port (e.g., 192.168.1.1:7788,
  ipv4:192.168.1.1:7788, ipv6:[::1]:7788)
- `remote_addr` - Remote address:port

**Options:** None (path parameters only)

**Exit Codes and Errors:**
- `0` - Path added successfully
- `10` - Kernel error. Match stderr:
  - `Failure: (129) Interrupted by Signal`
  - `Failure: (126) UnknownMandatoryTag`
  - `Failure: (158) Unknown resource`
  - `Failure: (162) Invalid configuration request`
  - `Failure: (102) Local address(port) already in use.`
  - `Failure: (103) Remote address(port) already in use.`
  - `Failure: (173) Combination of local address(port) and remote address(port)
    already in use`
  - `Failure: (122) kmalloc() failed. Out of memory?`
  - optional `additional info from kernel:` lines for endpoint or transport
    problems
- `20` - Local `drbdsetup` error, no kernel `(n)` code. Match stderr such as:
  - `does not look like an endpoint address '<arg>'`
  - `error sending config command`
  - `error receiving config reply`

**Notes:**
- Can add multiple paths for multi-path support
- Addresses can be IPv4 or IPv6
- Must have at least one path before connect

---

### `del-path`
**Purpose:** Remove a network path from a peer

**Synopsis:**
```bash
drbdsetup del-path <resource_name> <peer_node_id> <local_addr> <remote_addr>
```

**Arguments:**
- `resource_name` - Resource name
- `peer_node_id` - Peer's node ID
- `local_addr` - Local address:port to remove
- `remote_addr` - Remote address:port to remove

**Options:** None

**Exit Codes and Errors:**
- `0` - Path removed successfully
- `10` - Kernel error. Match stderr:
  - `Failure: (129) Interrupted by Signal`
  - `Failure: (126) UnknownMandatoryTag`
  - `Failure: (158) Unknown resource`
  - `Failure: (162) Invalid configuration request`
  - optional `additional info from kernel:` lines such as `Can not delete last
    path, use disconnect first!` or `no such path`
- `20` - Local `drbdsetup` error, no kernel `(n)` code. Match stderr such as:
  - `does not look like an endpoint address '<arg>'`
  - `error sending config command`
  - `error receiving config reply`

**Notes:**
- Cannot remove last path while connected
- Path must match exactly (address and port)

---

### `connect`
**Purpose:** Establish connection to a peer

**Synopsis:**
```bash
drbdsetup connect <resource_name> <peer_node_id> [options]
```

**Arguments:**
- `resource_name` - Resource name
- `peer_node_id` - Peer's node ID to connect to

**Options:**
- `--tentative` (FLAG) - Tentative connection (for establishing initial
  handshake)
- `--dry-run` (FLAG) - Backward-compatibility alias for `--tentative`
- `--discard-my-data` (FLAG) - Discard local data in favor of peer's

**Exit Codes and Errors:**
- `0` - Connection initiated successfully (asynchronous - does not wait for
  `Connected`)
- `10` - Kernel error. Match stderr:
  - `Failure: (125) Device has a net-config (use disconnect first)`
  - `Failure: (123) --discard-my-data not allowed when primary.`
  - `Failure: (126) UnknownMandatoryTag`
  - `Failure: (158) Unknown resource`
  - `Failure: (162) Invalid configuration request`
  - optional `additional info from kernel:` lines such as `connection
    endpoint(s) missing`
- `11` - State-change error. Match stderr:
  - `State change failed: (<n>) <text>`
- `20` - Local `drbdsetup` error, no kernel `(n)` code. Match stderr such as:
  - `error sending config command`
  - `error receiving config reply`

**Notes:**
- **Asynchronous** - returns immediately, connection happens in background
- Use `drbdsetup status` or `events2` to monitor connection state
- Peer must also attempt connection (or already be listening)
- **--discard-my-data** is dangerous - only use when intentionally discarding
  local changes

---

### `disconnect`
**Purpose:** Disconnect from a peer

**Synopsis:**
```bash
drbdsetup disconnect <resource_name> <peer_node_id> [options]
```

**Arguments:**
- `resource_name` - Resource name
- `peer_node_id` - Peer's node ID to disconnect from

**Options:**
- `--force` (FLAG) - Force disconnect immediately

**Exit Codes and Errors:**
- `0` - Disconnection initiated successfully
  - initial module-load failure is also treated as success for `disconnect`
- `10` - Kernel error. Match stderr:
  - `Failure: (126) UnknownMandatoryTag`
  - `Failure: (129) Interrupted by Signal`
  - `Failure: (158) Unknown resource`
  - `Failure: (162) Invalid configuration request`
- `11` - State-change error. Match stderr:
  - `State change failed: (-10) State change was refused by peer node`
  - `State change failed: (-25) No quorum`
- `17` - State-change error. Match stderr:
  - `State change failed: (-2) Need access to UpToDate data`
- `20` - Local `drbdsetup` error, no kernel `(n)` code. Match stderr such as:
  - `error sending config command`
  - `error receiving config reply`

**Notes:**
- Without --force: graceful disconnect (waits for pending I/O)
- With --force: immediate disconnect, may lose data
- Connection state transitions to StandAlone

---

### `net-options`
**Purpose:** Change network options on an existing connection

**Synopsis:**
```bash
drbdsetup net-options <resource_name> <peer_node_id> [options]
```

**Arguments:**
- `resource_name` - Resource name
- `peer_node_id` - Peer's node ID

**Options:**
- All changeable net-options from `new-peer` (see above, excludes immutable
  options)
- `--set-defaults` (FLAG) - Explicitly reset unspecified changeable options to
  their defaults

**Exit Codes and Errors:**
- `0` - Options changed successfully
- `10` - Kernel error. Match stderr:
  - `Failure: (129) Interrupted by Signal`
  - `Failure: (122) kmalloc() failed. Out of memory?`
  - `Failure: (126) UnknownMandatoryTag`
  - `Failure: (158) Unknown resource`
  - `Failure: (162) Invalid configuration request`
  - `Failure: (148) Can not change csums-alg while resync is in progress`
  - `Failure: (149) Can not change verify-alg while online verify runs`
  - `Failure: (163) Prot version 100 required in order to change these network
    options while connected`
  - `Failure: (164) Can not clear allow_two_primaries as long as there a
    primaries on both sides`
  - `Failure: (139) Protocol C required`
  - `Failure: (154) Fencing policy resource-and-stonith only with prot B or C
    allowed`
  - `Failure: (155) on-congestion policy pull-ahead only with prot A allowed`
  - `Failure: (144) CSUMSAlgNotAvail`
  - `Failure: (146) VERIFYAlgNotAvail`
  - `Failure: (141) The 'data-integrity-alg' you specified is not known in the
    kernel. (Maybe you need to modprobe it, or modprobe hmac?)`
  - `Failure: (120) The 'cram-hmac-alg' you specified is not known in the
    kernel. (Maybe you need to modprobe it, or modprobe hmac?)`
- `20` - Local `drbdsetup` error, no kernel `(n)` code. Match stderr such as:
  - `error sending config command`
  - `error receiving config reply`

**Notes:**
- Most options can be changed while connected (requires protocol 100+)
- Some options require disconnection to change
- Changes apply immediately

---

### `peer-device-options`
**Purpose:** Change per-peer-device (volume) options

**Synopsis:**
```bash
drbdsetup peer-device-options <resource_name> <peer_node_id> <volume> [options]
```

**Arguments:**
- `resource_name` - Resource name
- `peer_node_id` - Peer's node ID
- `volume` - Volume number

**Options:**
- `--resync-rate=<bytes/second>` (NUMERIC) - Resync rate limit
- `--c-plan-ahead=<1/10s>` (NUMERIC) - Controller planning ahead time
- `--c-delay-target=<1/10s>` (NUMERIC) - Controller delay target
- `--c-fill-target=<bytes>` (NUMERIC) - Controller fill target
- `--c-max-rate=<bytes/second>` (NUMERIC) - Controller maximum rate
- `--c-min-rate=<bytes/second>` (NUMERIC) - Controller minimum rate
- `--bitmap={yes|no}` (BOOLEAN) - Enable/disable bitmap tracking
- `--set-defaults` (FLAG) - Explicitly reset unspecified changeable options to
  their defaults

**Exit Codes and Errors:**
- `0` - Options changed successfully
- `10` - Kernel error. Match stderr:
  - `Failure: (129) Interrupted by Signal`
  - `Failure: (122) kmalloc() failed. Out of memory?`
  - `Failure: (126) UnknownMandatoryTag`
  - `Failure: (158) Unknown resource`
  - `Failure: (162) Invalid configuration request`
  - optional `additional info from kernel:` lines such as `No bitmap slot
    available in meta-data` or `Can not drop the bitmap when both sides have a
    disk`
- `20` - Local `drbdsetup` error, no kernel `(n)` code. Match stderr such as:
  - `error sending config command`
  - `error receiving config reply`

**Notes:**
- These options control resync behavior
- Can be changed online
- Controller (c-*) options provide dynamic rate control

## INSPECTION AND MONITORING

### `show`
**Purpose:** Show the current effective DRBD configuration

**Synopsis:**
```bash
drbdsetup show [<resource_name>|all] [options]
```

**Arguments:**
- `resource_name` - Optional resource name to show
- `all` - Show all currently configured resources

**Options:**
- `--show-defaults` (FLAG) - Include options that are currently at their default
  values
- `--json` (FLAG) - Emit JSON instead of drbd.conf-style text

**Exit Codes and Errors:**
- `0` - Configuration shown successfully
  - `show <missing-resource>` currently prints nothing and still exits `0`
  - stderr may still contain dump-side lines such as `Failure: (158) Unknown
    resource` or `Failure: (129) Interrupted by Signal`
- `20` - Local `drbdsetup` error, no stable kernel `(n)` code. Match stderr such
  as:
  - `error sending config command`
  - `received netlink error reply: ...`
  - `reply did not validate - do you need to upgrade your userland tools?`
- `121` - Module disappeared while running. Match stderr:
  - `module unloaded`

**Notes:**
- **Read-only command** - does not modify kernel state
- **Lockless** - does not require the DRBD lock directory
- Shows the **current kernel configuration**, not the original `drbd.conf` input
- Default text output is close to `drbd.conf` syntax and is convenient for
  inspection
- `--json` is better for automation and structured parsing
- `--show-defaults` is useful when you want the fully expanded effective
  configuration
- Current implementation prints nothing and still exits `0` if a named resource
  does not exist

---

### `status`
**Purpose:** Show the current runtime state of DRBD resources

**Synopsis:**
```bash
drbdsetup status [<resource_name>|all] [options]
```

**Arguments:**
- `resource_name` - Optional resource name to show
- `all` - Show all currently configured resources

**Options:**
- `--verbose` (FLAG) - Include more detail in text output
- `--statistics` (FLAG) - Include counters, sizes, and progress information
- `--color[={auto|always|never}]` (OPTIONAL ARG) - Control ANSI color output for
  text mode
- `--json` (FLAG) - Emit JSON instead of human-oriented text

**Exit Codes and Errors:**
- `0` - Status shown successfully
  - stderr may still contain dump-side lines such as `Failure: (158) Unknown
    resource` or `Failure: (129) Interrupted by Signal`
- `10` - Named resource does not exist. Match stderr:
  - `<name>: No such resource`
- `20` - Local `drbdsetup` error, no stable kernel `(n)` code. Match stderr such
  as:
  - `unknown --color argument`
  - `error sending config command`
  - `received netlink error reply: ...`
  - `reply did not validate - do you need to upgrade your userland tools?`
- `121` - Module disappeared while running. Match stderr:
  - `module unloaded`

**Notes:**
- **Read-only command** - does not modify kernel state
- **Lockless** - does not require the DRBD lock directory
- Shows **runtime state**, such as role, disk state, connection state, quorum,
  and suspend/fail-io flags
- `--statistics` is the most useful mode for monitoring sync progress and
  traffic counters
- `--json` is the best choice for automation
- If no DRBD resources are currently configured, text mode prints `# No
  currently configured DRBD found.`
- Unlike `show`, a non-existent named resource causes exit code `10`

---

### `events2`
**Purpose:** Stream the current state and subsequent state changes

**Synopsis:**
```bash
drbdsetup events2 [<resource_name>|all] [options]
```

**Arguments:**
- `resource_name` - Optional resource name to monitor
- `all` - Monitor all currently configured resources

**Options:**
- `--timestamps` (FLAG) - Prefix events with timestamps
- `--statistics` (FLAG) - Include statistics in event output
- `--now` (FLAG) - Show only the current state snapshot and then exit
- `--poll` (FLAG) - Wait for input on stdin and emit the next update when
  requested
- `--diff` (FLAG) - Show changed values in old->new form for change events
- `--full` (FLAG) - Emit full change records; implies verbose output and
  statistics
- `--color[={auto|always|never}]` (OPTIONAL ARG) - Control ANSI color output

**Exit Codes and Errors:**
- `0` - Stream started successfully, or `--now` snapshot printed successfully
- `20` - Local `drbdsetup` / stream-handling error, no stable kernel `(n)` code.
  Match stderr such as:
  - `<objname>: unable to join drbd events multicast group`
  - `Unable to format timestamp`
  - `drbd_tla_parse() failed`
  - `received netlink error reply: ...`
  - `reply did not validate - do you need to upgrade your userland tools?`
- `121` - Module disappeared while running. Match stderr:
  - `module unloaded`

**Notes:**
- **Read-only command** - does not modify kernel state
- **Lockless** - does not require the DRBD lock directory
- Emits the initial state first, then continuous updates as objects change
- This is the best command for event-driven monitoring and automation
- `--now` is useful when you want a one-shot snapshot without subscribing to the
  live event stream
- `--poll` is specialized: it waits for stdin input and emits the next update
  when it receives `n`
- `--full` is helpful when consumers want complete change records instead of
  only changed fields
- Common event object types include `resource`, `device`, `connection`,
  `peer-device`, `path`, `helper`, and `rename`

## DRBDMETA OPERATIONS

### `drbdmeta create-md`
**Purpose:** Initialize DRBD metadata before first attach

**Synopsis:**
```bash
drbdmeta [--force] <minor_or_device> <format> [format_args...] create-md [options] <max_peers>
```

**Common v09 form:**
```bash
drbdmeta [--force] <minor> v09 <meta_dev> <internal|flex-external|flex-internal|index> create-md [options] <max_peers>
```

**Arguments:**
- `minor_or_device` - DRBD minor number or DRBD device identifier
- `format` - Metadata format such as `v09`
- `format_args...` - Format-specific arguments; for `v09`, this is `<meta_dev>
  <internal|flex-external|flex-internal|index>`
- `max_peers` - Required positional argument for `v09`; allowed range is `1 ..
  31`

**Options:**
- `--force` (FLAG) - Force confirmations and ignore some interactive safety
  prompts
- `--ignore-sanity-checks` (FLAG) - Ignore some metadata overlap sanity checks
- `--peer-max-bio-size=<val>` - Allowed only with `create-md`
- `--initialize-bitmap={automatic|zeroout|pwrite|skip}` - Bitmap initialization
  mode
- `--al-stripes=<val>` - Activity-log stripes
- `--al-stripe-size-kB=<val>` - Activity-log stripe size in KiB
- `--effective-size=<val>` - Effective size override (`-Z`)
- `--diskful-peers=<val>` - Diskful peer mask or node list (`-D`)

**Exit Codes and Errors:**
- `0` - Metadata initialized successfully. Match stdout/stderr such as:
  - `Writing meta data...`
  - `New drbd meta data block successfully created.`
- `1` - Command-level failure after command dispatch. Match stderr such as:
  - `operation failed`
  - `Operation cancelled.`
  - any of the interactive overwrite / convert prompts being declined
- `10` - Local validation or safety error. Match stderr such as:
  - `peer-max-bio-size out of range (0...1M)`
  - `invalid initialize-bitmap mode "<mode>", should be one of automatic |
    zeroout | pwrite | skip`
  - `invalid node list / mask value '<value>'`
  - `The --peer-max-bio-size option is only allowed with create-md`
  - `The --al-stripe* options are only allowed with create-md and restore-md`
  - `The -Z|--effective-size option is only allowed with create-md` - `The
  -D|--diskful-peers option is only allowed with create-md`
  - `invalid (too large) al-stripe* settings`
  - `invalid (too small) al-stripe* settings`
  - `MAX_PEERS argument not in allowed range 1 .. 31.`
  - `<meta_dev> is only <n> bytes. That's not enough.`
  - `Device too small: expecting meta data block at`
  - `Cannot move after offline resize and change AL-striping at the same time,
    yet.`
  - `fast zero-out failed, fallback disabled`
  - `<tag>: offset+count (...) not in meta data area range [...], aborted`
  - optional follow-up lines: `If you want to force this, tell me to
    --ignore-sanity-checks`
- `20` - Usage / invocation / environment error. Match stderr such as:
  - `USAGE: drbdmeta MINOR v09 ... create-md MAX_PEERS`
  - `MAX_PEERS argument missing`
  - `Format identifier missing`
  - `Unknown format '<fmt>'.`
  - `Too few arguments for format`
  - `'<index>' is not a valid index number.`
  - `command missing`
  - `Unknown command '<cmd>'.`
  - `Device '<dev>' is configured!`
  - `Cannot determine minor device number of drbd device '<dev>'`
  - lock/setup failures such as missing `/var/run/drbd/lock/`

**Notes:**
- This is required before `drbdsetup attach` can succeed on a fresh device
- For direct `drbdmeta` use on `v09`, `max_peers` is a **positional** argument,
  not `--max-peers=N`
- Existing metadata may trigger interactive prompts to overwrite or convert it;
  `--force` suppresses the confirmation step
- If existing internal metadata was found at a last-known offset after offline
  resize, `create-md` may offer to move it instead of reinitializing

---

### `drbdmeta dump-md`
**Purpose:** Dump DRBD metadata in text form, including bitmap and activity log

**Synopsis:**
```bash
drbdmeta [--force] <minor_or_device> <format> [format_args...] dump-md
```

**Common v09 form:**
```bash
drbdmeta [--force] <minor> v09 <meta_dev> <internal|flex-external|flex-internal|index> dump-md
```

**Arguments:**
- `minor_or_device` - DRBD minor number or DRBD device identifier
- `format` - Metadata format such as `v09`
- `format_args...` - Format-specific arguments; for `v09`, this is `<meta_dev>
  <internal|flex-external|flex-internal|index>`

**Options:**
- `--force` (FLAG) - Allow dumping unclean metadata

**Exit Codes and Errors:**
- `0` - Metadata dumped successfully
- `1` - Command-level failure after command dispatch. Match stderr such as:
  - `No valid meta data found`
  - `Found meta data is "unclean", please apply-al first`
  - `operation failed`
- `20` - Usage / invocation / environment error. Match stderr such as:
  - `Format identifier missing`
  - `Unknown format '<fmt>'.`
  - `Too few arguments for format`
  - `'<index>' is not a valid index number.`
  - `command missing`
  - `Unknown command '<cmd>'.`
  - `Cannot determine minor device number of drbd device '<dev>'`
  - lock/setup failures such as missing `/var/run/drbd/lock/`

**Notes:**
- Without `--force`, unclean metadata is refused with:
  - `Found meta data is "unclean", please apply-al first`
- With `--force`, dump continues and the output is intentionally marked unclean:
  - `This_is_an_unclean_meta_data_dump._Don't_trust_the_bitmap.`
- If metadata is found only at the last-known position after offline resize, the
  dump includes explanatory header comments about the old and expected offsets

---

## TYPICAL COMMAND SEQUENCES

**Full resource setup and promotion:**
```bash
drbdsetup new-resource myres 1
drbdsetup new-minor myres 0 0
drbdsetup attach 0 /dev/sda1 /dev/sda2 0
drbdsetup new-peer myres 2 --_name=peer-node --protocol=C
drbdsetup new-path myres 2 192.168.1.1:7788 192.168.1.2:7788
drbdsetup connect myres 2
# After sync completes:
drbdsetup primary myres
```

**Intentionally diskless resource (diskless client):**
```bash
drbdsetup new-resource myres 1
drbdsetup new-minor myres 0 0 --diskless    # No attach needed
drbdsetup new-peer myres 2 --_name=peer-node --protocol=C
drbdsetup new-path myres 2 192.168.1.1:7788 192.168.1.2:7788
drbdsetup connect myres 2
# Data is accessed via peers only; no local backing device.
# Verify with: drbdsetup status --json (look for "client": true)
```

**Inspect current config and state:**
```bash
# Effective kernel configuration
drbdsetup show myres

# Machine-readable runtime state
drbdsetup status myres --json

# One-shot event-style snapshot
drbdsetup events2 myres --now
```

**Full resource teardown:**
```bash
drbdsetup down myres  # Simple: everything in one command
```

**Manual teardown (equivalent to down):**
```bash
drbdsetup secondary myres      # Demote if primary
drbdsetup disconnect myres 2
drbdsetup detach 0
drbdsetup del-minor 0
drbdsetup del-resource myres
```

**Resize a device:**
```bash
# 1. Grow backing devices on all nodes
# 2. On one node (must have a primary):
drbdsetup resize 0
# New space will be resynced automatically
```

**Skip initial resync (both nodes):**
```bash
# On both nodes:
drbdmeta 0 v09 /dev/sda2 0 create-md --force 1
drbdsetup new-resource myres 1  # (node 2 uses node_id 2)
drbdsetup new-minor myres 0 0
drbdsetup attach 0 /dev/sda1 /dev/sda2 0
drbdsetup new-peer myres 2 --_name=peer-node --protocol=C
drbdsetup new-path myres 2 192.168.1.1:7788 192.168.1.2:7788
drbdsetup connect myres 2
# After handshake, on one node:
drbdsetup new-current-uuid 0 --clear-bitmap
```

**Direct `drbdmeta` usage (v09):**
```bash
# Initialize internal metadata with room for future peers
drbdmeta 0 v09 /dev/vg0/lv0 internal create-md --force 31

# Dump metadata in text form
drbdmeta 0 v09 /dev/vg0/lv0 internal dump-md
```

This guide covers the most commonly used administrative and inspection commands,
with arguments, options, exit codes, and behavior cross-checked against the
source code.