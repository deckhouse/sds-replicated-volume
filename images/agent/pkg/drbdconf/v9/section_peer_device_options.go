package v9

type PeerDeviceOptions struct {
	// The c-delay-target parameter defines the delay in the resync path that
	// DRBD should aim for. This should be set to five times the network
	// round-trip time or more. The default value of c-delay-target is 10, in
	// units of 0.1 seconds.
	// Also see CPlanAhead.
	CDelayTarget int

	// The c-fill-target parameter defines the how much resync data DRBD should
	// aim to have in-flight at all times. Common values for "normal" data paths
	// range from 4K to 100K. The default value of c-fill-target is 100, in
	// units of sectors
	// Also see CPlanAhead.
	CFillTarget Sectors

	// The c-max-rate parameter limits the maximum bandwidth used by dynamically
	// controlled resyncs. Setting this to zero removes the limitation
	// (since DRBD 9.0.28). It should be set to either the bandwidth available
	// between the DRBD hosts and the machines hosting DRBD-proxy, or to the
	// available disk bandwidth. The default value of c-max-rate is 102400, in
	// units of KiB/s.
	// Also see CPlanAhead.
	CMaxRate int

	// The c-plan-ahead parameter defines how fast DRBD adapts to changes in the
	// resync speed. It should be set to five times the network round-trip time
	// or more. The default value of c-plan-ahead is 20, in units of
	// 0.1 seconds.
	//
	// # Dynamically control the resync speed
	//
	// The following modes are available:
	//  - Dynamic control with fill target (default). Enabled when c-plan-ahead is non-zero and c-fill-target is non-zero. The goal is to fill the buffers along the data path with a defined amount of data. This mode is recommended when DRBD-proxy is used. Configured with c-plan-ahead, c-fill-target and c-max-rate.
	//  - Dynamic control with delay target. Enabled when c-plan-ahead is non-zero (default) and c-fill-target is zero. The goal is to have a defined delay along the path. Configured with c-plan-ahead, c-delay-target and c-max-rate.
	//  - Fixed resync rate. Enabled when c-plan-ahead is zero. DRBD will try to perform resync I/O at a fixed rate. Configured with resync-rate.
	CPlanAhead int

	// A node which is primary and sync-source has to schedule application I/O
	// requests and resync I/O requests. The c-min-rate parameter limits how
	// much bandwidth is available for resync I/O; the remaining bandwidth is
	// used for application I/O.
	//
	// A c-min-rate value of 0 means that there is no limit on the resync I/O
	// bandwidth. This can slow down application I/O significantly. Use a value
	// of 1 (1 KiB/s) for the lowest possible resync rate.
	//
	// The default value of c-min-rate is 250, in units of KiB/s.
	CMinRate int

	// Define how much bandwidth DRBD may use for resynchronizing. DRBD allows
	// "normal" application I/O even during a resync. If the resync takes up too
	// much bandwidth, application I/O can become very slow. This parameter
	// allows to avoid that. Please note this is option only works when the
	// dynamic resync controller is disabled.
	ResyncRate int
}
