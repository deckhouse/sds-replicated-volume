package v9

// The parameters in this section determine the behavior of a resource at
// startup time. They have no effect once the system is up and running.
type Startup struct {
	// Define how long to wait until all peers are connected in case the cluster
	// consisted of a single node only when the system went down. This parameter
	// is usually set to a value smaller than wfc-timeout. The assumption here
	// is that peers which were unreachable before a reboot are less likely to
	// be reachable after the reboot, so waiting is less likely to help.
	//
	// The timeout is specified in seconds. The default value is 0, which stands
	// for an infinite timeout. Also see the wfc-timeout parameter.
	DegrWFCTimeout *int `drbd:"degr-wfc-timeout"`

	// Define how long to wait until all peers are connected if all peers were
	// outdated when the system went down. This parameter is usually set to a
	// value smaller than wfc-timeout. The assumption here is that an outdated
	// peer cannot have become primary in the meantime, so we don't need to wait
	// for it as long as for a node which was alive before.
	//
	// The timeout is specified in seconds. The default value is 0, which stands
	// for an infinite timeout. Also see the wfc-timeout parameter.
	OutdatedWFCTimeout *int `drbd:"outdated-wfc-timeout"`

	// On stacked devices, the wfc-timeout and degr-wfc-timeout parameters in
	// the configuration are usually ignored, and both timeouts are set to twice
	// the connect-int timeout. The stacked-timeouts parameter tells DRBD to use
	// the wfc-timeout and degr-wfc-timeout parameters as defined in the
	// configuration, even on stacked devices. Only use this parameter if the
	// peer of the stacked resource is usually not available, or will not become
	// primary. Incorrect use of this parameter can lead to unexpected
	// split-brain scenarios.
	StackedTimeouts bool `drbd:"stacked-timeouts"`

	// This parameter causes DRBD to continue waiting in the init script even
	// when a split-brain situation has been detected, and the nodes therefore
	// refuse to connect to each other.
	WaitAfterSB bool `drbd:"wait-after-sb"`

	// Define how long the init script waits until all peers are connected. This
	// can be useful in combination with a cluster manager which cannot manage
	// DRBD resources: when the cluster manager starts, the DRBD resources will
	// already be up and running. With a more capable cluster manager such as
	// Pacemaker, it makes more sense to let the cluster manager control DRBD
	// resources. The timeout is specified in seconds. The default value is 0,
	// which stands for an infinite timeout. Also see the degr-wfc-timeout
	// parameter.
	WFCTimeout *int `drbd:"wfc-timeout"`
}
