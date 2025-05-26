package v9

// Define handlers to be invoked when certain events occur. The kernel passes
// the resource name in the first command-line argument and sets the following
// environment variables depending on the event's context:
//   - For events related to a particular device: the device's minor number in
//     DRBD_MINOR, the device's volume number in DRBD_VOLUME.
//   - For events related to a particular device on a particular peer: the
//     connection endpoints in DRBD_MY_ADDRESS, DRBD_MY_AF, DRBD_PEER_ADDRESS,
//     and DRBD_PEER_AF; the device's local minor number in DRBD_MINOR, and the
//     device's volume number in DRBD_VOLUME.
//   - For events related to a particular connection: the connection endpoints
//     in DRBD_MY_ADDRESS, DRBD_MY_AF, DRBD_PEER_ADDRESS, and DRBD_PEER_AF; and,
//     for each device defined for that connection: the device's minor number in
//     DRBD_MINOR_volume-number.
//   - For events that identify a device, if a lower-level device is attached,
//     the lower-level device's device name is passed in DRBD_BACKING_DEV (or
//     DRBD_BACKING_DEV_volume-number).
//
// All parameters in this section are optional. Only a single handler can be
// defined for each event; if no handler is defined, nothing will happen.
type Handlers struct {
	// Called on a resync target when a node state changes from Inconsistent to
	// Consistent when a resync finishes. This handler can be used for removing
	// the snapshot created in the before-resync-target handler.
	AfterResyncTarget string

	// Called on a resync target before a resync begins. This handler can be
	// used for creating a snapshot of the lower-level device for the duration
	// of the resync: if the resync source becomes unavailable during a resync,
	// reverting to the snapshot can restore a consistent state.
	BeforeResyncTarget string

	// Called on a resync source before a resync begins.
	BeforeResyncSource string

	// Called on all nodes after a verify finishes and out-of-sync blocks were
	// found. This handler is mainly used for monitoring purposes. An example
	// would be to call a script that sends an alert SMS.
	OutOfSync string

	// Called on a Primary that lost quorum. This handler is usually used to
	// reboot the node if it is not possible to restart the application that
	// uses the storage on top of DRBD.
	QuorumLost string

	// Called when a node should fence a resource on a particular peer. The
	// handler should not use the same communication path that DRBD uses for
	// talking to the peer.
	FencePeer string

	// Called when a node should remove fencing constraints from other nodes.
	UnfencePeer string

	// Called when DRBD connects to a peer and detects that the peer is in a
	// split-brain state with the local node. This handler is also called for
	// split-brain scenarios which will be resolved automatically.
	InitialSplitBrain string

	// Called when an I/O error occurs on a lower-level device.
	LocalIOError string

	// The local node is currently primary, but DRBD believes that it should
	// become a sync target. The node should give up its primary role.
	PriLost string

	// The local node is currently primary, but it has lost the
	// after-split-brain auto recovery procedure. The node should be abandoned.
	PriLostAfterSB string

	// The local node is primary, and neither the local lower-level device nor a
	// lower-level device on a peer is up to date. (The primary has no device to
	// read from or to write to.)
	PriOnInconDegr string

	// DRBD has detected a split-brain situation which could not be resolved
	// automatically. Manual recovery is necessary. This handler can be used to
	// call for administrator attention.
	SplitBrain string

	// A connection to a peer went down. The handler can learn about the reason
	// for the disconnect from the DRBD_CSTATE environment variable.
	Disconnected string
}
