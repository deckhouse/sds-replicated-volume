package v9

import "github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"

// Define parameters for a connection. All parameters in this section are
// optional.
type Net struct {
	// Define how to react if a split-brain scenario is detected and none of the
	// two nodes is in primary role. (We detect split-brain scenarios when two
	// nodes connect; split-brain decisions are always between two nodes.)
	AfterSB0Pri AfterSB0PriPolicy

	// If AfterSB0Pri is [AfterSB0PriPolicyDiscardNode], this is the name of the
	// node
	AfterSB0PriPolicyDiscardNodeName string

	// Define how to react if a split-brain scenario is detected, with one node
	// in primary role and one node in secondary role. (We detect split-brain
	// scenarios when two nodes connect, so split-brain decisions are always
	// among two nodes.)
	AfterSB1Pri AfterSB1PriPolicy

	// Define how to react if a split-brain scenario is detected and both nodes
	// are in primary role. (We detect split-brain scenarios when two nodes
	// connect, so split-brain decisions are always among two nodes.)
	AfterSB2Pri AfterSB2PriPolicy

	// The most common way to configure DRBD devices is to allow only one node to be primary (and thus writable) at a time.
	//
	// In some scenarios it is preferable to allow two nodes to be primary at once; a mechanism outside of DRBD then must make sure that writes to the shared, replicated device happen in a coordinated way. This can be done with a shared-storage cluster file system like OCFS2 and GFS, or with virtual machine images and a virtual machine manager that can migrate virtual machines between physical machines.
	//
	// The allow-two-primaries parameter tells DRBD to allow two nodes to be primary at the same time. Never enable this option when using a non-distributed file system; otherwise, data corruption and node crashes will result!
	AllowTwoPrimaries bool

	// Normally the automatic after-split-brain policies are only used if current states of the UUIDs do not indicate the presence of a third node.
	//
	// With this option you request that the automatic after-split-brain policies are used as long as the data sets of the nodes are somehow related. This might cause a full sync, if the UUIDs indicate the presence of a third node. (Or double faults led to strange UUID sets.)
	AlwaysASBP bool

	// As soon as a connection between two nodes is configured with drbdsetup connect, DRBD immediately tries to establish the connection. If this fails, DRBD waits for connect-int seconds and then repeats. The default value of connect-int is 10 seconds.
	ConnectInt *int

	// Configure the hash-based message authentication code (HMAC) or secure hash algorithm to use for peer authentication. The kernel supports a number of different algorithms, some of which may be loadable as kernel modules. See the shash algorithms listed in /proc/crypto. By default, cram-hmac-alg is unset. Peer authentication also requires a shared-secret to be configured.
	CRAMHMACAlg string

	// Normally, when two nodes resynchronize, the sync target requests a piece of out-of-sync data from the sync source, and the sync source sends the data. With many usage patterns, a significant number of those blocks will actually be identical.
	//
	// When a csums-alg algorithm is specified, when requesting a piece of out-of-sync data, the sync target also sends along a hash of the data it currently has. The sync source compares this hash with its own version of the data. It sends the sync target the new data if the hashes differ, and tells it that the data are the same otherwise. This reduces the network bandwidth required, at the cost of higher cpu utilization and possibly increased I/O on the sync target.
	//
	// The csums-alg can be set to one of the secure hash algorithms supported by the kernel; see the shash algorithms listed in /proc/crypto. By default, csums-alg is unset.
	CSumsAlg string

	// Enabling this option (and csums-alg, above) makes it possible to use the checksum based resync only for the first resync after primary crash, but not for later "network hickups".
	//
	// In most cases, block that are marked as need-to-be-resynced are in fact changed, so calculating checksums, and both reading and writing the blocks on the resync target is all effective overhead.
	//
	// The advantage of checksum based resync is mostly after primary crash recovery, where the recovery marked larger areas (those covered by the activity log) as need-to-be-resynced, just in case. Introduced in 8.4.5.
	CSumsAfterCrashOnly bool

	// DRBD normally relies on the data integrity checks built into the TCP/IP protocol, but if a data integrity algorithm is configured, it will additionally use this algorithm to make sure that the data received over the network match what the sender has sent. If a data integrity error is detected, DRBD will close the network connection and reconnect, which will trigger a resync.
	//
	// The data-integrity-alg can be set to one of the secure hash algorithms supported by the kernel; see the shash algorithms listed in /proc/crypto. By default, this mechanism is turned off.
	//
	// Because of the CPU overhead involved, we recommend not to use this option in production environments. Also see the notes on data integrity below.
	DataIntegrityAlg string

	// Fencing is a preventive measure to avoid situations where both nodes are primary and disconnected. This is also known as a split-brain situation.
	Fencing FencingPolicy

	// If a secondary node fails to complete a write request in ko-count times the timeout parameter, it is excluded from the cluster. The primary node then sets the connection to this secondary node to Standalone. To disable this feature, you should explicitly set it to 0; defaults may change between versions.
	KOCount int

	// Limits the memory usage per DRBD minor device on the receiving side, or
	// for internal buffers during resync or online-verify. Unit is PAGE_SIZE,
	// which is 4 KiB on most systems. The minimum possible setting is hard
	// coded to 32 (=128 KiB). These buffers are used to hold data blocks while
	// they are written to/read from disk. To avoid possible distributed
	// deadlocks on congestion, this setting is used as a throttle threshold
	// rather than a hard limit. Once more than max-buffers pages are in use,
	// further allocation from this pool is throttled. You want to increase
	// max-buffers if you cannot saturate the IO backend on the receiving side.
	MaxBuffers *int `drbd:"max-buffers"`

	// Define the maximum number of write requests DRBD may issue before issuing a write barrier. The default value is 2048, with a minimum of 1 and a maximum of 20000. Setting this parameter to a value below 10 is likely to decrease performance.
	MaxEpochSize int

	// By default, DRBD blocks when the TCP send queue is full. This prevents applications from generating further write requests until more buffer space becomes available again.
	//
	// When DRBD is used together with DRBD-proxy, it can be better to use the pull-ahead on-congestion policy, which can switch DRBD into ahead/behind mode before the send queue is full. DRBD then records the differences between itself and the peer in its bitmap, but it no longer replicates them to the peer. When enough buffer space becomes available again, the node resynchronizes with the peer and switches back to normal replication.
	//
	// This has the advantage of not blocking application I/O even when the queues fill up, and the disadvantage that peer nodes can fall behind much further. Also, while resynchronizing, peer nodes will become inconsistent.
	OnCongestion OnCongestionPolicy

	// The congestion-fill parameter defines how much data is allowed to be "in flight" in this connection. The default value is 0, which disables this mechanism of congestion control, with a maximum of 10 GiBytes.
	//
	// Also see OnCongestion.
	CongestionFill int

	// The congestion-extents parameter defines how many bitmap extents may be active before switching into ahead/behind mode, with the same default and limits as the al-extents parameter. The congestion-extents parameter is effective only when set to a value smaller than al-extents.
	//
	// Also see OnCongestion.
	CongestionExtents int

	// When the TCP/IP connection to a peer is idle for more than ping-int
	// seconds, DRBD will send a keep-alive packet to make sure that a failed
	// peer or network connection is detected reasonably soon. The default value
	//  is 10 seconds, with a minimum of 1 and a maximum of 120 seconds. The
	// unit is seconds.
	PingInt int

	// Define the timeout for replies to keep-alive packets. If the peer does
	// not reply within ping-timeout, DRBD will close and try to reestablish the
	// connection. The default value is 0.5 seconds, with a minimum of 0.1
	// seconds and a maximum of 30 seconds. The unit is tenths of a second.
	PingTimeout int

	// In setups involving a DRBD-proxy and connections that experience a lot of buffer-bloat it might be necessary to set ping-timeout to an unusual high value. By default DRBD uses the same value to wait if a newly established TCP-connection is stable. Since the DRBD-proxy is usually located in the same data center such a long wait time may hinder DRBD's connect process.
	//
	// In such setups socket-check-timeout should be set to at least to the round trip time between DRBD and DRBD-proxy. I.e. in most cases to 1.
	//
	// The default unit is tenths of a second, the default value is 0 (which causes DRBD to use the value of ping-timeout instead). Introduced in 8.4.5.
	SocketCheckTimeout int

	// Use the specified protocol on this connection.
	Protocol Protocol

	// Configure the size of the TCP/IP receive buffer. A value of 0 (the default) causes the buffer size to adjust dynamically. This parameter usually does not need to be set, but it can be set to a value up to 10 MiB. The default unit is bytes.
	RcvbufSize int

	// This option helps to solve the cases when the outcome of the resync decision is incompatible with the current role assignment in the cluster. The defined policies are:
	RRConflict RRConflictPolicy

	// Configure the shared secret used for peer authentication. The secret is a string of up to 64 characters. Peer authentication also requires the cram-hmac-alg parameter to be set.
	SharedSecret string

	// Configure the size of the TCP/IP send buffer. Since DRBD 8.0.13 / 8.2.7, a value of 0 (the default) causes the buffer size to adjust dynamically. Values below 32 KiB are harmful to the throughput on this connection. Large buffer sizes can be useful especially when protocol A is used over high-latency networks; the maximum value supported is 10 MiB.
	SndbufSize int

	// By default, DRBD uses the TCP_CORK socket option to prevent the kernel from sending partial messages; this results in fewer and bigger packets on the network. Some network stacks can perform worse with this optimization. On these, the tcp-cork parameter can be used to turn this optimization off.
	TCPCork bool

	// Define the timeout for replies over the network: if a peer node does not send an expected reply within the specified timeout, it is considered dead and the TCP/IP connection is closed. The timeout value must be lower than connect-int and lower than ping-int. The default is 6 seconds; the value is specified in tenths of a second.
	Timeout int

	// Each replicated device on a cluster node has a separate bitmap for each of its peer devices. The bitmaps are used for tracking the differences between the local and peer device: depending on the cluster state, a disk range can be marked as different from the peer in the device's bitmap, in the peer device's bitmap, or in both bitmaps. When two cluster nodes connect, they exchange each other's bitmaps, and they each compute the union of the local and peer bitmap to determine the overall differences.
	//
	// Bitmaps of very large devices are also relatively large, but they usually compress very well using run-length encoding. This can save time and bandwidth for the bitmap transfers.
	//
	// The use-rle parameter determines if run-length encoding should be used. It is on by default since DRBD 8.4.0.
	UseRLE bool

	// Online verification (drbdadm verify) computes and compares checksums of disk blocks (i.e., hash values) in order to detect if they differ. The verify-alg parameter determines which algorithm to use for these checksums. It must be set to one of the secure hash algorithms supported by the kernel before online verify can be used; see the shash algorithms listed in /proc/crypto.
	//
	// We recommend to schedule online verifications regularly during low-load periods, for example once a month. Also see the notes on data integrity below.
	VerifyAlg string

	// Allows or disallows DRBD to read from a peer node.
	//
	// When the disk of a primary node is detached, DRBD will try to continue
	// reading and writing from another node in the cluster. For this purpose,
	// it searches for nodes with up-to-date data, and uses any found node to
	// resume operations. In some cases it may not be desirable to read back
	// data from a peer node, because the node should only be used as a
	// replication target. In this case, the allow-remote-read parameter can be
	// set to no, which would prohibit this node from reading data from the peer
	// node.
	//
	// The allow-remote-read parameter is available since DRBD 9.0.19, and
	// defaults to yes.
	AllowRemoteRead *bool
}

var _ drbdconf.SectionKeyworder = &Net{}

func (*Net) SectionKeyword() string {
	return "net"
}

type AfterSB0PriPolicy string

const (
	// No automatic resynchronization; simply disconnect.
	AfterSB0PriPolicyDisconnect AfterSB0PriPolicy = "disconnect"
	// Resynchronize from the node which became primary first. If both nodes
	// became primary independently, the discard-least-changes policy is used.
	AfterSB0PriPolicyDiscardYoungerPrimary AfterSB0PriPolicy = "discard-younger-primary"
	// Resynchronize from the node which became primary last. If both nodes
	// became primary independently, the discard-least-changes policy is used.
	AfterSB0PriPolicyDiscardOlderPrimary AfterSB0PriPolicy = "discard-older-primary"
	// If only one of the nodes wrote data since the split brain situation was
	// detected, resynchronize from this node to the other. If both nodes wrote
	// data, disconnect.
	AfterSB0PriPolicyDiscardZeroChanges AfterSB0PriPolicy = "discard-zero-changes"
	// Resynchronize from the node with more modified blocks.
	AfterSB0PriPolicyDiscardLeastChanges AfterSB0PriPolicy = "discard-least-changes"
	// Always resynchronize to the named node.
	// See [Net.AfterSB0PriPolicyDiscardNodeName] field for the node name.
	AfterSB0PriPolicyDiscardNode AfterSB0PriPolicy = "discard-node-"
)

type AfterSB1PriPolicy string

const (
	// No automatic resynchronization, simply disconnect.
	AfterSB1PriPolicyDisconnect AfterSB1PriPolicy = "disconnect"
	// Discard the data on the secondary node if the after-sb-0pri algorithm
	// would also discard the data on the secondary node. Otherwise, disconnect.
	AfterSB1PriPolicyConsensus AfterSB1PriPolicy = "consensus"
	// Always take the decision of the after-sb-0pri algorithm, even if it
	// causes an erratic change of the primary's view of the data. This is only
	// useful if a single-node file system (i.e., not OCFS2 or GFS) with the
	// allow-two-primaries flag is used. This option can cause the primary node
	// to crash, and should not be used.
	AfterSB1PriPolicyViolentlyAS0P AfterSB1PriPolicy = "violently-as0p"
	// Discard the data on the secondary node.
	AfterSB1PriPolicyDiscardSecondary AfterSB1PriPolicy = "discard-secondary"
	// Always take the decision of the after-sb-0pri algorithm. If the decision
	// is to discard the data on the primary node, call the pri-lost-after-sb
	// handler on the primary node.
	AfterSB1PriPolicyCallPriLostAfterSB AfterSB1PriPolicy = "call-pri-lost-after-sb"
)

type AfterSB2PriPolicy string

const (
	// No automatic resynchronization, simply disconnect.
	AfterSB2PriPolicyDisconnect AfterSB2PriPolicy = "disconnect"
	// See the violently-as0p policy for after-sb-1pri.
	AfterSB2PriPolicyViolentlyAS0P AfterSB2PriPolicy = "violently-as0p"
	// Call the pri-lost-after-sb helper program on one of the machines unless
	// that machine can demote to secondary. The helper program is expected to
	// reboot the machine, which brings the node into a secondary role. Which
	// machine runs the helper program is determined by the after-sb-0pri
	// strategy.
	AfterSB2PriPolicyCallPriLostAfterSB AfterSB2PriPolicy = "call-pri-lost-after-sb"
)

type FencingPolicy string

const (
	// No fencing actions are taken. This is the default policy.
	FencingPolicyDontCare FencingPolicy = "dont-care"
	// If a node becomes a disconnected primary, it tries to fence the peer. This is done by calling the fence-peer handler. The handler is supposed to reach the peer over an alternative communication path and call 'drbdadm outdate minor' there.
	FencingPolicyResourceOnly FencingPolicy = "resource-only"
	// If a node becomes a disconnected primary, it freezes all its IO operations and calls its fence-peer handler. The fence-peer handler is supposed to reach the peer over an alternative communication path and call 'drbdadm outdate minor' there. In case it cannot do that, it should stonith the peer. IO is resumed as soon as the situation is resolved. In case the fence-peer handler fails, I/O can be resumed manually with 'drbdadm resume-io'.
	FencingPolicyResourceAndSTONITH FencingPolicy = "resource-and-stonith"
)

type OnCongestionPolicy string

const (
	OnCongestionPolicyBlock     OnCongestionPolicy = "block"
	OnCongestionPolicyPullAhead OnCongestionPolicy = "pull-ahead"
)

type Protocol string

const (
	// Writes to the DRBD device complete as soon as they have reached the local
	// disk and the TCP/IP send buffer.
	ProtocolA Protocol = "A"
	// Writes to the DRBD device complete as soon as they have reached the local
	// disk, and all peers have acknowledged the receipt of the write requests.
	ProtocolB Protocol = "B"
	// Writes to the DRBD device complete as soon as they have reached the local
	// and all remote disks.
	ProtocolC Protocol = "C"
)

type RRConflictPolicy string

const (
	// No automatic resynchronization, simply disconnect.
	RRConflictPolicyDisconnect RRConflictPolicy = "disconnect"
	// Disconnect now, and retry to connect immediatly afterwards.
	RRConflictPolicyRetryConnect RRConflictPolicy = "retry-connect"
	// Resync to the primary node is allowed, violating the assumption that data
	// on a block device are stable for one of the nodes. Do not use this
	// option, it is dangerous.
	RRConflictPolicyViolently RRConflictPolicy = "violently"
	// Call the pri-lost handler on one of the machines. The handler is expected
	// to reboot the machine, which puts it into secondary role.
	RRConflictPolicyCallPriLost RRConflictPolicy = "call-pri-lost"
	// Auto-discard reverses the resync direction, so that DRBD resyncs the
	// current primary to the current secondary. Auto-discard only applies when
	// protocol A is in use and the resync decision is based on the principle
	// that a crashed primary should be the source of a resync. When a primary
	// node crashes, it might have written some last updates to its disk, which
	// were not received by a protocol A secondary. By promoting the secondary
	// in the meantime the user accepted that those last updates have been lost.
	// By using auto-discard you consent that the last updates (before the crash
	// of the primary) should be rolled back automatically.
	RRConflictPolicyAutoDiscard RRConflictPolicy = "auto-discard"
)
