I'll create a comprehensive caller's guide for these drbdsetup commands by examining the source code and documentation.

[14 tools called]

Based on my analysis of the DRBD source code, here's a comprehensive **Caller's Guide** for the requested `drbdsetup` commands:

---

# DRBDSETUP COMMAND CALLER'S GUIDE

## Exit Codes (Common to All Commands)

```
0   (SS_SUCCESS / NO_ERROR) - Command succeeded
101 (NO_ERROR)              - No error / Success
102-180                     - Specific error codes (see below)
900 (OTHER_ERROR)          - General error
901 (ERR_MODULE_UNLOADED)  - DRBD kernel module not loaded
```

### Common Error Codes:
- `119` - Meta-data not valid (need to initialize)
- `124` - Disk already configured (detach first)
- `125` - Network already configured (disconnect first)
- `127` - Minor not allocated
- `152` - Permission denied (need CAP_SYS_ADMIN)
- `158` - Resource not known
- `159` - Resource in use (delete minors first)
- `160` - Minor still configured (down it first)
- `161` - Minor or volume already exists

---

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
- `--cpu-mask <mask>` - CPU affinity mask
- `--on-no-data-accessible {io-error|suspend-io}` - Action when no data accessible
- `--auto-promote {yes|no}` - Enable automatic promotion (default from kernel)
- `--peer-ack-window <bytes>` - Peer acknowledgement window
- `--peer-ack-delay <ms>` - Peer acknowledgement delay (milliseconds)
- `--twopc-timeout <1/10s>` - Two-phase commit timeout
- `--twopc-retry-timeout <1/10s>` - Two-phase commit retry timeout
- `--auto-promote-timeout <1/10s>` - Auto-promote timeout
- `--max-io-depth <num>` - Maximum I/O queue depth (nr_requests)
- `--quorum {off|majority|all|<1-31>}` - Quorum setting
- `--on-no-quorum {io-error|suspend-io}` - Action when no quorum
- `--quorum-minimum-redundancy {off|majority|all|<1-31>}` - Minimum redundancy
- `--on-suspended-primary-outdated {disconnect|force-secondary}` - Action on suspended primary outdated

**Exit Behavior:**
- Success: `0` - Resource created
- Failure: `161` - Resource already exists
- Failure: `152` - Permission denied

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

**Options:** Same as `new-resource` (see above)

**Flags:**
- `.set_defaults = true` - Can set default values explicitly

**Exit Behavior:**
- Success: `0` - Options updated
- Failure: `158` - Resource not found

**Notes:**
- Can change options on a running resource (most options)
- Some options may require disconnection/detachment

---

### `down`
**Purpose:** Completely tear down a resource (all-in-one shutdown)

**Synopsis:**
```bash
drbdsetup down {<resource_name>|all}
```

**Arguments:**
- `resource_name` - Name of resource to tear down
- `all` - Tear down all resources

**Options:** None (NO_PAYLOAD)

**Flags:**
- `.missing_ok = true` - Succeeds even if resource doesn't exist
- `.warn_on_missing = true` - Warns if resource not found

**Exit Behavior:**
- Success: `0` - Resource torn down (or didn't exist)
- Failure: rare (command is very forgiving)

**What it does (internally):**
1. Lists all devices in the resource
2. Sends `DRBD_ADM_DOWN` netlink command
3. Unregisters all minors
4. Unregisters the resource
5. Removes all volumes, connections, and the resource itself

**Notes:**
- **Convenience command** - does everything in one shot
- Succeeds even if DRBD module not loaded
- Preferred over manual teardown sequence
- Equivalent to: disconnect + detach + del-minor (for all) + del-resource

---

## VOLUME/DEVICE MANAGEMENT

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

**Context:** `CTX_RESOURCE | CTX_MINOR | CTX_VOLUME | CTX_MULTIPLE_ARGUMENTS`

**Options** (device-options):
- `--max-bio-size <bytes>` - Maximum BIO size
- `--diskless` - Mark as intentionally diskless
- `--block-size <bytes>` - Block size for the device

**Exit Behavior:**
- Success: `0` - Minor created
- Failure: `161` - Minor or volume already exists
- Failure: `158` - Resource not found

**Notes:**
- Minor number must be unique system-wide
- Checks for existing sysfs nodes to avoid kernel issues
- Resource must exist before creating minors

---

## DISK ATTACHMENT

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
- `--size <bytes>` - DRBD device size
- `--on-io-error {pass_on|call-local-io-error|detach}` - I/O error handling
- `--disk-barrier {yes|no}` - Use disk barriers
- `--disk-flushes {yes|no}` - Use disk flushes
- `--disk-drain {yes|no}` - Drain before barrier
- `--md-flushes {yes|no}` - Flush meta-data
- `--resync-after <minor>` - Resync after this other minor
- `--al-extents <num>` - Activity log extents (default: 1237)
- `--al-updates {yes|no}` - Enable activity log updates
- `--discard-zeroes-if-aligned {yes|no}` - Discard optimization
- `--disable-write-same {yes|no}` - Disable WRITE_SAME
- `--disk-timeout <1/10s>` - Disk timeout
- `--read-balancing {prefer-local|prefer-remote|round-robin|least-pending|when-congested-remote|*K-striping}` - Read balancing policy
- `--rs-discard-granularity <bytes>` - Resync discard granularity

**Exit Behavior:**
- Success: `0` - Device attached
- Failure: `104` - Cannot open backing device
- Failure: `105` - Cannot open meta device
- Failure: `107/108` - Device not a block device
- Failure: `111/112` - Device too small
- Failure: `114/115` - Device already claimed (mounted?)
- Failure: `116` - Invalid meta-data index
- Failure: `118` - I/O error on meta-data
- Failure: `119` - Meta-data invalid/uninitialized
- Failure: `124` - Already attached (detach first)
- Failure: `127` - Minor not allocated
- Failure: `165` - Meta-data unclean (run apply-al)

**Notes:**
- Backing device must not be in use
- Meta-data must be initialized (drbdadm create-md)
- Device sizes validated against meta-data

---

### `disk-options`
**Purpose:** Change disk options on an attached device

**Synopsis:**
```bash
drbdsetup disk-options <minor> [options]
```

**Arguments:**
- `minor` - Device minor number

**Options:** Same changeable disk options as `attach` (see above)

**Flags:**
- `.set_defaults = true` - Can explicitly set defaults

**Exit Behavior:**
- Success: `0` - Options changed
- Failure: `138` - No disk attached
- Failure: `149` - Cannot change during verify
- Failure: `148` - Cannot change csums during resync

**Notes:**
- Can change most options online
- Some changes may require specific states

---

## CONNECTION/PEER MANAGEMENT

### `new-peer`
**Purpose:** Make a peer node known to the resource

**Synopsis:**
```bash
drbdsetup new-peer <resource_name> <peer_node_id> [options]
```

**Arguments:**
- `resource_name` - Resource name
- `peer_node_id` - Peer's node ID

**Options** (net-options, both immutable and changeable):
**Immutable:**
- `--transport <name>` - Transport type (tcp, rdma, etc.)
- `--load-balance-paths {yes|no}` - Load balance across multiple paths

**Changeable:**
- `--protocol {A|B|C}` - Replication protocol
- `--timeout <1/10s>` - Network timeout
- `--max-epoch-size <num>` - Maximum epoch size
- `--connect-int <seconds>` - Connection retry interval
- `--ping-int <seconds>` - Keepalive ping interval
- `--sndbuf-size <bytes>` - Send buffer size
- `--rcvbuf-size <bytes>` - Receive buffer size
- `--ko-count <num>` - Keepalive timeout count
- `--allow-two-primaries {yes|no}` - Allow dual-primary
- `--cram-hmac-alg <algorithm>` - HMAC algorithm for authentication
- `--shared-secret <secret>` - Shared secret for authentication
- `--after-sb-0pri {disconnect|discard-younger-primary|discard-older-primary|discard-zero-changes|discard-least-changes|discard-local|discard-remote}` - Split-brain 0-primary recovery
- `--after-sb-1pri {disconnect|consensus|violently-as0p|discard-secondary|call-pri-lost-after-sb}` - Split-brain 1-primary recovery
- `--after-sb-2pri {disconnect|violently-as0p|call-pri-lost-after-sb}` - Split-brain 2-primary recovery
- `--always-asbp {yes|no}` - Always apply after-split-brain policies
- `--rr-conflict {disconnect|violently|call-pri-lost|retry-connect|auto-discard}` - Concurrent writes resolution
- `--ping-timeout <1/10s>` - Ping timeout
- `--data-integrity-alg <algorithm>` - Data integrity algorithm
- `--tcp-cork {yes|no}` - TCP_CORK optimization
- `--on-congestion {block|pull-ahead|disconnect}` - Congestion handling
- `--congestion-fill <bytes>` - Congestion fill threshold
- `--congestion-extents <num>` - Congestion extents threshold
- `--csums-alg <algorithm>` - Checksum algorithm
- `--csums-after-crash-only {yes|no}` - Only checksum after crash
- `--verify-alg <algorithm>` - Online verify algorithm
- `--use-rle {yes|no}` - Use run-length encoding
- `--socket-check-timeout <1/10s>` - Socket check timeout
- `--fencing {dont-care|resource-only|resource-and-stonith}` - Fencing policy
- `--max-buffers <num>` - Maximum buffers
- `--allow-remote-read {yes|no}` - Allow reading from secondary
- `--tls {yes|no}` - Enable TLS
- `--tls-keyring <keyring>` - TLS keyring ID
- `--tls-privkey <key>` - TLS private key ID
- `--tls-certificate <cert>` - TLS certificate ID
- `--rdma-ctrl-rcvbuf-size <bytes>` - RDMA control receive buffer
- `--rdma-ctrl-sndbuf-size <bytes>` - RDMA control send buffer

**Exit Behavior:**
- Success: `0` - Peer created
- Failure: `158` - Resource not found
- Failure: `561` - Invalid peer node ID
- Failure: `562` - Failed to create transport

**Notes:**
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
- `--force` - Force disconnect even if not cleanly possible

**Exit Behavior:**
- Success: `0` - Peer removed
- Failure: `158` - Resource not found

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

**Exit Behavior:**
- Success: `0` - Peer forgotten
- Failure: `158` - Resource not found

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
- `local_addr` - Local address:port (e.g., 192.168.1.1:7788, ipv4:192.168.1.1:7788, ipv6:[::1]:7788)
- `remote_addr` - Remote address:port

**Options:** None (path parameters only)

**Exit Behavior:**
- Success: `0` - Path added
- Failure: `102` - Local address already in use
- Failure: `103` - Remote address already in use
- Failure: `563` - Address pair combination already in use
- Failure: `564` - Already exists

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

**Exit Behavior:**
- Success: `0` - Path removed
- Failure: `158` - Resource/path not found

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
- `--tentative` - Tentative connection (for establishing initial handshake)
- `--discard-my-data` - Discard local data in favor of peer's

**Exit Behavior:**
- Success: `0` - Connection initiated (async - doesn't wait for established)
- Failure: `123` - --discard-my-data not allowed when primary
- Failure: `158` - Resource not found
- Failure: `151` - Need to be standalone

**Notes:**
- **Asynchronous** - returns immediately, connection happens in background
- Use `drbdsetup status` or `events2` to monitor connection state
- Peer must also attempt connection (or already be listening)
- **--discard-my-data** is dangerous - only use when intentionally discarding local changes

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
- `--force` - Force disconnect immediately

**Exit Behavior:**
- Success: `0` - Disconnection initiated
- Failure: `158` - Resource not found

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

**Options:** All changeable net-options from `new-peer` (see above, excludes immutable options)

**Flags:**
- `.set_defaults = true` - Can set defaults explicitly

**Exit Behavior:**
- Success: `0` - Options changed
- Failure: `163` - Protocol version 100+ required for online changes
- Failure: `164` - Cannot clear allow-two-primaries with both primaries
- Failure: `149` - Cannot change verify-alg during verify
- Failure: `148` - Cannot change csums-alg during resync

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
- `--resync-rate <bytes/second>` - Resync rate limit
- `--c-plan-ahead <1/10s>` - Controller planning ahead time
- `--c-delay-target <1/10s>` - Controller delay target
- `--c-fill-target <bytes>` - Controller fill target
- `--c-max-rate <bytes/second>` - Controller maximum rate
- `--c-min-rate <bytes/second>` - Controller minimum rate
- `--bitmap {yes|no}` - Enable/disable bitmap tracking

**Flags:**
- `.set_defaults = true` - Can set defaults explicitly

**Exit Behavior:**
- Success: `0` - Options changed
- Failure: `158` - Resource not found

**Notes:**
- These options control resync behavior
- Can be changed online
- Controller (c-*) options provide dynamic rate control

---

## SUMMARY TABLE

| Command | Context | Arguments | Key Options | Notes |
|---------|---------|-----------|-------------|-------|
| `new-resource` | Resource | name, node_id | resource options | Creates resource object |
| `resource-options` | Resource | name | resource options | Modifies existing resource |
| `down` | Resource | name\|all | none | All-in-one teardown |
| `new-minor` | Minor | name, minor, vol | device options | Creates /dev/drbd<N> |
| `attach` | Minor | minor, lower, meta, idx | disk options | Attaches backing storage |
| `disk-options` | Minor | minor | disk options | Changes disk settings |
| `new-peer` | Peer | name, node_id | net options | Defines peer node |
| `del-peer` | Peer | name, node_id | --force | Removes peer config |
| `forget-peer` | Resource | name, node_id | none | Clears from metadata |
| `new-path` | Peer | name, node_id, local, remote | none | Adds network path |
| `del-path` | Peer | name, node_id, local, remote | none | Removes network path |
| `connect` | Peer | name, node_id | --tentative, --discard-my-data | Initiates connection |
| `disconnect` | Peer | name, node_id | --force | Tears down connection |
| `net-options` | Peer | name, node_id | net options | Changes net settings |
| `peer-device-options` | Peer+Vol | name, node_id, vol | resync options | Per-volume-per-peer |

---

## TYPICAL COMMAND SEQUENCES

**Full resource setup:**
```bash
drbdsetup new-resource myres 1
drbdsetup new-minor myres 0 0
drbdsetup attach 0 /dev/sda1 /dev/sda2 0
drbdsetup new-peer myres 2
drbdsetup new-path myres 2 192.168.1.1:7788 192.168.1.2:7788
drbdsetup connect myres 2
```

**Full resource teardown:**
```bash
drbdsetup down myres  # Simple: everything in one command
```

**Manual teardown (equivalent to down):**
```bash
drbdsetup disconnect myres 2
drbdsetup detach 0
drbdsetup del-minor 0
drbdsetup del-resource myres
```

This guide covers all the commands you requested with complete argument, option, exit code, and behavioral information drawn directly from the source code.