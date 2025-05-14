package v9

// Define parameters for a resource. All parameters in this section are
// optional.
type Options struct {
	// A resource must be promoted to primary role before any of its devices can be mounted or opened for writing.
	// Before DRBD 9, this could only be done explicitly ("drbdadm primary"). Since DRBD 9, the auto-promote parameter allows to automatically promote a resource to primary role when one of its devices is mounted or opened for writing. As soon as all devices are unmounted or closed with no more remaining users, the role of the resource changes back to secondary.
	//
	// Automatic promotion only succeeds if the cluster state allows it (that is, if an explicit drbdadm primary command would succeed). Otherwise, mounting or opening the device fails as it already did before DRBD 9: the mount(2) system call fails with errno set to EROFS (Read-only file system); the open(2) system call fails with errno set to EMEDIUMTYPE (wrong medium type).
	//
	// Irrespective of the auto-promote parameter, if a device is promoted explicitly (drbdadm primary), it also needs to be demoted explicitly (drbdadm secondary).
	//
	// The auto-promote parameter is available since DRBD 9.0.0, and defaults to yes.
	AutoPromote *bool

	// Set the cpu affinity mask for DRBD kernel threads. The cpu mask is specified as a hexadecimal number. The default value is 0, which lets the scheduler decide which kernel threads run on which CPUs. CPU numbers in cpu-mask which do not exist in the system are ignored.
	CPUMask *string

	// Determine how to deal with I/O requests when the requested data is not available locally or remotely (for example, when all disks have failed). When quorum is enabled, on-no-data-accessible should be set to the same value as on-no-quorum. The defined policies are:
	OnNoDataAccessible *OnNoDataAccessiblePolicy

	// On each node and for each device, DRBD maintains a bitmap of the differences between the local and remote data for each peer device. For example, in a three-node setup (nodes A, B, C) each with a single device, every node maintains one bitmap for each of its peers.
	//
	// When nodes receive write requests, they know how to update the bitmaps for the writing node, but not how to update the bitmaps between themselves. In this example, when a write request propagates from node A to B and C, nodes B and C know that they have the same data as node A, but not whether or not they both have the same data.
	//
	// As a remedy, the writing node occasionally sends peer-ack packets to its peers which tell them which state they are in relative to each other.
	//
	// The peer-ack-window parameter specifies how much data a primary node may send before sending a peer-ack packet. A low value causes increased network traffic; a high value causes less network traffic but higher memory consumption on secondary nodes and higher resync times between the secondary nodes after primary node failures. (Note: peer-ack packets may be sent due to other reasons as well, e.g. membership changes or expiry of the peer-ack-delay timer.)
	//
	// The default value for peer-ack-window is 2 MiB, the default unit is sectors. This option is available since 9.0.0.
	PeerAckWindow *Sectors

	// If after the last finished write request no new write request gets issued for expiry-time, then a peer-ack packet is sent. If a new write request is issued before the timer expires, the timer gets reset to expiry-time. (Note: peer-ack packets may be sent due to other reasons as well, e.g. membership changes or the peer-ack-window option.)
	//
	// This parameter may influence resync behavior on remote nodes. Peer nodes need to wait until they receive an peer-ack for releasing a lock on an AL-extent. Resync operations between peers may need to wait for for these locks.
	//
	// The default value for peer-ack-delay is 100 milliseconds, the default unit is milliseconds. This option is available since 9.0.0.
	PeerAckDelay *int

	// When activated, a cluster partition requires quorum in order to modify the replicated data set. That means a node in the cluster partition can only be promoted to primary if the cluster partition has quorum. Every node with a disk directly connected to the node that should be promoted counts. If a primary node should execute a write request, but the cluster partition has lost quorum, it will freeze IO or reject the write request with an error (depending on the on-no-quorum setting). Upon loosing quorum a primary always invokes the quorum-lost handler. The handler is intended for notification purposes, its return code is ignored.
	//
	// The option's value might be set to off, majority, all or a numeric value. If you set it to a numeric value, make sure that the value is greater than half of your number of nodes. Quorum is a mechanism to avoid data divergence, it might be used instead of fencing when there are more than two repicas. It defaults to off
	//
	// If all missing nodes are marked as outdated, a partition always has quorum, no matter how small it is. I.e. If you disconnect all secondary nodes gracefully a single primary continues to operate. In the moment a single secondary is lost, it has to be assumed that it forms a partition with all the missing outdated nodes. In case my partition might be smaller than the other, quorum is lost in this moment.
	//
	// In case you want to allow permanently diskless nodes to gain quorum it is recommended to not use majority or all. It is recommended to specify an absolute number, since DBRD's heuristic to determine the complete number of diskfull nodes in the cluster is unreliable.
	//
	// The quorum implementation is available starting with the DRBD kernel driver version 9.0.7.
	Quorum *Quorum

	// This option sets the minimal required number of nodes with an UpToDate
	// disk to allow the partition to gain quorum. This is a different
	// requirement than the plain quorum option expresses.
	//
	// The option's value might be set to off, majority, all or a numeric value.
	// If you set it to a numeric value, make sure that the value is greater
	// than half of your number of nodes.
	//
	// In case you want to allow permanently diskless nodes to gain quorum it is
	// recommended to not use majority or all. It is recommended to specify an
	// absolute number, since DBRD's heuristic to determine the complete number
	// of diskfull nodes in the cluster is unreliable.
	//
	// This option is available starting with the DRBD kernel driver version
	// 9.0.10.
	// See QuorumMinimumRedundancyNumber for a numeric value
	QuorumMinimumRedundancy *QuorumMinimumRedundancyValue

	QuorumMinimumRedundancyNumber *int

	// By default DRBD freezes IO on a device, that lost quorum. By setting the on-no-quorum to io-error it completes all IO operations with an error if quorum is lost.
	//
	// Usually, the on-no-data-accessible should be set to the same value as on-no-quorum, as it has precedence.
	//
	// The on-no-quorum options is available starting with the DRBD kernel driver version 9.0.8.
	OnNoQuorum *OnNoQuorumPolicy

	// This setting is only relevant when on-no-quorum is set to suspend-io. It is relevant in the following scenario. A primary node loses quorum hence has all IO requests frozen. This primary node then connects to another, quorate partition. It detects that a node in this quorate partition was promoted to primary, and started a newer data-generation there. As a result, the first primary learns that it has to consider itself outdated.
	//
	// When it is set to force-secondary then it will demote to secondary immediately, and fail all pending (and new) IO requests with IO errors. It will refuse to allow any process to open the DRBD devices until all openers closed the device. This state is visible in status and events2 under the name force-io-failures.
	//
	// The disconnect setting simply causes that node to reject connect attempts and stay isolated.
	//
	// The on-suspended-primary-outdated option is available starting with the DRBD kernel driver version 9.1.7. It has a default value of disconnect.
	OnSuspendedPrimaryOutdated *OnSuspendedPrimaryOutdatedPolicy
}

type OnNoDataAccessiblePolicy string

const (
	OnNoDataAccessiblePolicyIOError   OnNoDataAccessiblePolicy = "io-error"
	OnNoDataAccessiblePolicySuspendIO OnNoDataAccessiblePolicy = "suspend-io"
)

type Quorum string

const (
	QuorumOff      Quorum = "off"
	QuorumMajority Quorum = "majority"
	QuorumAll      Quorum = "all"
)

type QuorumMinimumRedundancyValue string

const (
	QuorumMinimumRedundancyValueOff      QuorumMinimumRedundancyValue = "off"
	QuorumMinimumRedundancyValueMajority QuorumMinimumRedundancyValue = "majority"
	QuorumMinimumRedundancyValueAll      QuorumMinimumRedundancyValue = "all"
)

type OnNoQuorumPolicy string

const (
	OnNoQuorumPolicyIOError   OnNoQuorumPolicy = "io-error"
	OnNoQuorumPolicySuspendIO OnNoQuorumPolicy = "suspend-io"
)

type OnSuspendedPrimaryOutdatedPolicy string

const (
	OnSuspendedPrimaryOutdatedPolicyDisconnect     OnSuspendedPrimaryOutdatedPolicy = "disconnect"
	OnSuspendedPrimaryOutdatedPolicyForceSecondary OnSuspendedPrimaryOutdatedPolicy = "force-secondary"
)
