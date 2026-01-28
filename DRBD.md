I'll create a comprehensive caller's guide for these drbdsetup commands by examining the source code and documentation.

[14 tools called]

Based on my analysis of the DRBD source code, here's a comprehensive **Caller's Guide** for the requested `drbdsetup` commands:

---

# DRBDSETUP COMMAND CALLER'S GUIDE

## Exit Code Mappings

**drbdsetup** commands return exit codes based on the operation result:

- **`0`** - Success (NO_ERROR or SS_SUCCESS)
- **`5`** - State change error: Lower than outdated
- **`10`** - Kernel error (any ERR_CODE_BASE error from kernel, 100-170 range)
- **`11`** - State change error (generic SS_ error)
- **`16`** - State change error: No local disk
- **`17`** - State change error: No up-to-date disk
- **`20`** - Userspace error (OTHER_ERROR - invalid arguments, parsing errors, netlink failures, etc.)
- **`121`** - DRBD kernel module not loaded

Each command documents its specific exit codes below. Exit code `10` indicates a kernel error - check kernel logs or error message for the specific error code (102-170 range).

---

## OPTION SYNTAX RULES

**CRITICAL:** Different option types require different syntax!

### Boolean Options (use `=` or prefix):
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

**All boolean options:** `--auto-promote`, `--allow-two-primaries`, `--disk-barrier`, `--disk-flushes`, `--disk-drain`, `--md-flushes`, `--al-updates`, `--discard-zeroes-if-aligned`, `--disable-write-same`, `--always-asbp`, `--tcp-cork`, `--use-rle`, `--csums-after-crash-only`, `--allow-remote-read`, `--tls`, `--load-balance-paths`, `--bitmap`

### Flag Options (use `=` or prefix):
```bash
# Flags work the same as booleans
--force
--tentative
--diskless
--discard-my-data
```

### Enum/Numeric/String Options (either syntax works):
```bash
# Both syntaxes work:
--protocol=C              # With equals
--protocol C              # With space

--timeout=50              # With equals
--timeout 50              # With space

--on-no-data-accessible=suspend-io    # With equals
--on-no-data-accessible suspend-io    # With space
```

**Best Practice:** Use `=` for ALL options to avoid confusion and ensure consistency.

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
- `--cpu-mask=<mask>` (STRING) - CPU affinity mask
- `--on-no-data-accessible={io-error|suspend-io}` (ENUM) - Action when no data accessible
- `--auto-promote={yes|no}` (BOOLEAN) - Enable automatic promotion (default: yes)
- `--peer-ack-window=<bytes>` (NUMERIC) - Peer acknowledgement window
- `--peer-ack-delay=<ms>` (NUMERIC) - Peer acknowledgement delay (milliseconds)
- `--twopc-timeout=<1/10s>` (NUMERIC) - Two-phase commit timeout
- `--twopc-retry-timeout=<1/10s>` (NUMERIC) - Two-phase commit retry timeout
- `--auto-promote-timeout=<1/10s>` (NUMERIC) - Auto-promote timeout
- `--max-io-depth=<num>` (NUMERIC) - Maximum I/O queue depth (nr_requests)
- `--quorum={off|majority|all|<1-31>}` (ENUM_NUM) - Quorum setting
- `--on-no-quorum={io-error|suspend-io}` (ENUM) - Action when no quorum
- `--quorum-minimum-redundancy={off|majority|all|<1-31>}` (ENUM_NUM) - Minimum redundancy
- `--on-suspended-primary-outdated={disconnect|force-secondary}` (ENUM) - Action on suspended primary outdated

**Exit Codes:**
- `0` - Resource created successfully
- `10` - Kernel error (e.g., resource already exists, permission denied)
- `20` - Invalid arguments or netlink communication failure
- `121` - DRBD module not loaded

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

**Exit Codes:**
- `0` - Options updated successfully
- `10` - Kernel error (e.g., resource not found, invalid option value)
- `20` - Invalid arguments (incorrect option syntax, excess arguments)
- `121` - DRBD module not loaded

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

**Exit Codes:**
- `0` - Resource torn down successfully (or didn't exist - command is very forgiving)
- `10` - Kernel error during teardown
- `20` - Netlink communication failure

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
- `--max-bio-size=<bytes>` (NUMERIC) - Maximum BIO size
- `--diskless` (FLAG) - Mark as intentionally diskless
- `--block-size=<bytes>` (NUMERIC) - Block size for the device

**Exit Codes:**
- `0` - Minor created successfully
- `10` - Kernel error (e.g., minor/volume already exists, resource not found)
- `20` - Invalid arguments or netlink failure
- `121` - DRBD module not loaded

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
- `--size=<bytes>` (NUMERIC) - DRBD device size
- `--on-io-error={pass_on|call-local-io-error|detach}` (ENUM) - I/O error handling
- `--disk-barrier={yes|no}` (BOOLEAN) - Use disk barriers
- `--disk-flushes={yes|no}` (BOOLEAN) - Use disk flushes
- `--disk-drain={yes|no}` (BOOLEAN) - Drain before barrier
- `--md-flushes={yes|no}` (BOOLEAN) - Flush meta-data
- `--resync-after=<minor>` (NUMERIC) - Resync after this other minor
- `--al-extents=<num>` (NUMERIC) - Activity log extents (default: 1237)
- `--al-updates={yes|no}` (BOOLEAN) - Enable activity log updates
- `--discard-zeroes-if-aligned={yes|no}` (BOOLEAN) - Discard optimization
- `--disable-write-same={yes|no}` (BOOLEAN) - Disable WRITE_SAME
- `--disk-timeout=<1/10s>` (NUMERIC) - Disk timeout
- `--read-balancing={prefer-local|prefer-remote|round-robin|least-pending|when-congested-remote|*K-striping}` (ENUM) - Read balancing policy
- `--rs-discard-granularity=<bytes>` (NUMERIC) - Resync discard granularity

**Exit Codes:**
- `0` - Device attached successfully
- `10` - Kernel error (check message for specific error: cannot open device, device too small, already claimed, meta-data invalid, etc.)
- `20` - Invalid arguments or netlink failure
- `121` - DRBD module not loaded

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

**Exit Codes:**
- `0` - Options changed successfully
- `10` - Kernel error (e.g., no disk attached, cannot change during verify/resync)
- `20` - Invalid arguments
- `121` - DRBD module not loaded

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
- `--transport=<name>` (STRING) - Transport type (tcp, rdma, etc.)
- `--load-balance-paths={yes|no}` (BOOLEAN) - Load balance across multiple paths

**Changeable:**
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
- `--after-sb-0pri={disconnect|discard-younger-primary|discard-older-primary|discard-zero-changes|discard-least-changes|discard-local|discard-remote}` (ENUM) - Split-brain 0-primary recovery
- `--after-sb-1pri={disconnect|consensus|violently-as0p|discard-secondary|call-pri-lost-after-sb}` (ENUM) - Split-brain 1-primary recovery
- `--after-sb-2pri={disconnect|violently-as0p|call-pri-lost-after-sb}` (ENUM) - Split-brain 2-primary recovery
- `--always-asbp={yes|no}` (BOOLEAN) - Always apply after-split-brain policies
- `--rr-conflict={disconnect|violently|call-pri-lost|retry-connect|auto-discard}` (ENUM) - Concurrent writes resolution
- `--ping-timeout=<1/10s>` (NUMERIC) - Ping timeout
- `--data-integrity-alg=<algorithm>` (STRING) - Data integrity algorithm
- `--tcp-cork={yes|no}` (BOOLEAN) - TCP_CORK optimization
- `--on-congestion={block|pull-ahead|disconnect}` (ENUM) - Congestion handling
- `--congestion-fill=<bytes>` (NUMERIC) - Congestion fill threshold
- `--congestion-extents=<num>` (NUMERIC) - Congestion extents threshold
- `--csums-alg=<algorithm>` (STRING) - Checksum algorithm
- `--csums-after-crash-only={yes|no}` (BOOLEAN) - Only checksum after crash
- `--verify-alg=<algorithm>` (STRING) - Online verify algorithm
- `--use-rle={yes|no}` (BOOLEAN) - Use run-length encoding
- `--socket-check-timeout=<1/10s>` (NUMERIC) - Socket check timeout
- `--fencing={dont-care|resource-only|resource-and-stonith}` (ENUM) - Fencing policy
- `--max-buffers=<num>` (NUMERIC) - Maximum buffers
- `--allow-remote-read={yes|no}` (BOOLEAN) - Allow reading from secondary
- `--tls={yes|no}` (BOOLEAN) - Enable TLS
- `--tls-keyring=<keyring>` (KEY_SERIAL) - TLS keyring ID
- `--tls-privkey=<key>` (KEY_SERIAL) - TLS private key ID
- `--tls-certificate=<cert>` (KEY_SERIAL) - TLS certificate ID
- `--rdma-ctrl-rcvbuf-size=<bytes>` (NUMERIC) - RDMA control receive buffer
- `--rdma-ctrl-sndbuf-size=<bytes>` (NUMERIC) - RDMA control send buffer

**Exit Codes:**
- `0` - Peer created successfully
- `10` - Kernel error (e.g., resource not found, invalid peer node ID, failed to create transport)
- `20` - Invalid arguments or netlink failure
- `121` - DRBD module not loaded

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
- `--force` (FLAG) - Force disconnect even if not cleanly possible

**Exit Codes:**
- `0` - Peer removed successfully
- `10` - Kernel error (e.g., resource not found, peer still connected)
- `20` - Invalid arguments or netlink failure
- `121` - DRBD module not loaded

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

**Exit Codes:**
- `0` - Peer forgotten successfully
- `10` - Kernel error (e.g., resource not found, peer still connected)
- `20` - Invalid arguments or netlink failure
- `121` - DRBD module not loaded

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

**Exit Codes:**
- `0` - Path added successfully
- `10` - Kernel error (e.g., address already in use, path already exists)
- `20` - Invalid address format or netlink failure
- `121` - DRBD module not loaded

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

**Exit Codes:**
- `0` - Path removed successfully
- `10` - Kernel error (e.g., path not found)
- `20` - Invalid arguments or netlink failure
- `121` - DRBD module not loaded

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
- `--tentative` (FLAG) - Tentative connection (for establishing initial handshake)
- `--discard-my-data` (FLAG) - Discard local data in favor of peer's

**Exit Codes:**
- `0` - Connection initiated successfully (asynchronous - doesn't wait for established)
- `10` - Kernel error (e.g., --discard-my-data not allowed when primary, resource not found, need to be standalone)
- `11` - State change error (connection state not allowing connect)
- `20` - Invalid arguments or netlink failure
- `121` - DRBD module not loaded

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
- `--force` (FLAG) - Force disconnect immediately

**Exit Codes:**
- `0` - Disconnection initiated successfully
- `10` - Kernel error (e.g., resource not found)
- `11` - State change error
- `20` - Invalid arguments or netlink failure
- `121` - DRBD module not loaded

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

**Exit Codes:**
- `0` - Options changed successfully
- `10` - Kernel error (e.g., protocol version too low, cannot change during verify/resync, cannot clear allow-two-primaries)
- `20` - Invalid arguments
- `121` - DRBD module not loaded

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

**Flags:**
- `.set_defaults = true` - Can set defaults explicitly

**Exit Codes:**
- `0` - Options changed successfully
- `10` - Kernel error (e.g., resource not found, invalid option value)
- `20` - Invalid arguments
- `121` - DRBD module not loaded

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
drbdsetup new-peer myres 2 --protocol=C
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