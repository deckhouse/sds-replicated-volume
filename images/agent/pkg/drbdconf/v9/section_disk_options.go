package v9

import "github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"

// Define parameters for a volume. All parameters in this section are optional.
type DiskOptions struct {
	// DRBD automatically maintains a "hot" or "active" disk area likely to be
	// written to again soon based on the recent write activity. The "active"
	// disk area can be written to immediately, while "inactive" disk areas must
	// be "activated" first, which requires a meta-data write. We also refer to
	// this active disk area as the "activity log".
	//
	// The activity log saves meta-data writes, but the whole log must be
	// resynced upon recovery of a failed node. The size of the activity log is
	// a major factor of how long a resync will take and how fast a replicated
	// disk will become consistent after a crash.
	//
	// The activity log consists of a number of 4-Megabyte segments; the
	// al-extents parameter determines how many of those segments can be active
	// at the same time. The default value for al-extents is 1237, with a
	// minimum of 7 and a maximum of 65536.
	//
	// Note that the effective maximum may be smaller, depending on how you
	// created the device meta data, see also drbdmeta(8) The effective maximum
	// is 919 * (available on-disk activity-log ring-buffer area/4kB -1), the
	// default 32kB ring-buffer effects a maximum of 6433 (covers more than
	// 25 GiB of data).
	//
	// We recommend to keep this well within the amount your backend storage and
	// replication link are able to resync inside of about 5 minutes.
	ALExtents *uint `drbd:"al-extents,no-al-extents"`

	// With this parameter, the activity log can be turned off entirely (see the
	// al-extents parameter). This will speed up writes because fewer meta-data
	// writes will be necessary, but the entire device needs to be
	// resynchronized opon recovery of a failed primary node. The default value
	// for al-updates is yes.
	ALUpdates *bool `drbd:"al-updates,no-al-updates"`

	// Use disk barriers to make sure that requests are written to disk in the
	// right order. Barriers ensure that all requests submitted before a barrier
	// make it to the disk before any requests submitted after the barrier. This
	// is implemented using 'tagged command queuing' on SCSI devices and 'native
	// command queuing' on SATA devices. Only some devices and device stacks
	// support this method. The device mapper (LVM) only supports barriers in
	// some configurations.
	//
	// Note that on systems which do not support disk barriers, enabling this
	// option can lead to data loss or corruption. Until DRBD 8.4.1,
	// disk-barrier was turned on if the I/O stack below DRBD did support
	// barriers. Kernels since linux-2.6.36 (or 2.6.32 RHEL6) no longer allow to
	// detect if barriers are supported. Since drbd-8.4.2, this option is off by
	// default and needs to be enabled explicitly.
	DiskBarrier *bool `drbd:"disk-barrier,no-disk-barrier"`

	// Use disk flushes between dependent write requests, also referred to as
	// 'force unit access' by drive vendors. This forces all data to disk. This
	// option is enabled by default.
	DiskFlushes *bool `drbd:"disk-flushes,no-disk-flushes"`

	// Wait for the request queue to "drain" (that is, wait for the requests to
	// finish) before submitting a dependent write request. This method requires
	// that requests are stable on disk when they finish. Before DRBD 8.0.9,
	// this was the only method implemented. This option is enabled by default.
	// Do not disable in production environments.
	//
	// From these three methods, drbd will use the first that is enabled and
	// supported by the backing storage device. If all three of these options
	// are turned off, DRBD will submit write requests without bothering about
	// dependencies. Depending on the I/O stack, write requests can be
	// reordered, and they can be submitted in a different order on different
	// cluster nodes. This can result in data loss or corruption. Therefore,
	// turning off all three methods of controlling write ordering is strongly
	// discouraged.
	//
	// A general guideline for configuring write ordering is to use disk
	// barriers or disk flushes when using ordinary disks (or an ordinary disk
	// array) with a volatile write cache. On storage without cache or with a
	// battery backed write cache, disk draining can be a reasonable choice.
	DiskDrain *bool `drbd:"disk-drain,no-disk-drain"`

	// If the lower-level device on which a DRBD device stores its data does not
	// finish an I/O request within the defined disk-timeout, DRBD treats this
	// as a failure. The lower-level device is detached, and the device's disk
	// state advances to Diskless. If DRBD is connected to one or more peers,
	// the failed request is passed on to one of them.
	//
	// This option is dangerous and may lead to kernel panic!
	//
	// "Aborting" requests, or force-detaching the disk, is intended for
	// completely blocked/hung local backing devices which do no longer complete
	// requests at all, not even do error completions. In this situation,
	// usually a hard-reset and failover is the only way out.
	//
	// By "aborting", basically faking a local error-completion, we allow for a
	// more graceful swichover by cleanly migrating services. Still the affected
	// node has to be rebooted "soon".
	//
	// By completing these requests, we allow the upper layers to re-use the
	// associated data pages.
	//
	// If later the local backing device "recovers", and now DMAs some data from
	// disk into the original request pages, in the best case it will just put
	// random data into unused pages; but typically it will corrupt meanwhile
	// completely unrelated data, causing all sorts of damage.
	//
	// Which means delayed successful completion, especially for READ requests,
	// is a reason to panic(). We assume that a delayed *error* completion is
	// OK, though we still will complain noisily about it.
	//
	// The default value of disk-timeout is 0, which stands for an infinite
	// timeout. Timeouts are specified in units of 0.1 seconds. This option is
	// available since DRBD 8.3.12.
	DiskTimeout *uint `drbd:"disk-timeout"`

	// Enable disk flushes and disk barriers on the meta-data device. This
	// option is enabled by default. See the disk-flushes parameter.
	MDFlushes *bool `drbd:"md-flushes,no-md-flushes"`

	// Configure how DRBD reacts to I/O errors on a lower-level device.
	OnIOError IOErrorPolicy `drbd:"on-io-error"`

	// Distribute read requests among cluster nodes as defined by policy. The
	// supported policies are prefer-local (the default), prefer-remote,
	// round-robin, least-pending, when-congested-remote, 32K-striping,
	// 64K-striping, 128K-striping, 256K-striping, 512K-striping and
	// 1M-striping.
	//
	// This option is available since DRBD 8.4.1.
	ReadBalancing ReadBalancingPolicy `drbd:"read-balancing"`

	// Define that a device should only resynchronize after the specified other
	// device. By default, no order between devices is defined, and all devices
	// will resynchronize in parallel. Depending on the configuration of the
	// lower-level devices, and the available network and disk bandwidth, this
	// can slow down the overall resync process. This option can be used to form
	// a chain or tree of dependencies among devices.
	ResyncAfter string `drbd:"resync-after"`

	// When rs-discard-granularity is set to a non zero, positive value then
	// DRBD tries to do a resync operation in requests of this size. In case
	// such a block contains only zero bytes on the sync source node, the sync
	// target node will issue a discard/trim/unmap command for the area.
	//
	// The value is constrained by the discard granularity of the backing block
	// device. In case rs-discard-granularity is not a multiplier of the discard
	// granularity of the backing block device DRBD rounds it up. The feature
	// only gets active if the backing block device reads back zeroes after a
	// discard command.
	//
	// The usage of rs-discard-granularity may cause c-max-rate to be exceeded.
	// In particular, the resync rate may reach 10x the value of
	// rs-discard-granularity per second.
	//
	// The default value of rs-discard-granularity is 0. This option is
	// available since 8.4.7.
	RsDiscardGranularity *uint `drbd:"rs-discard-granularity"`

	// There are several aspects to discard/trim/unmap support on linux block
	// devices. Even if discard is supported in general, it may fail silently,
	// or may partially ignore discard requests. Devices also announce whether
	// reading from unmapped blocks returns defined data (usually zeroes), or
	// undefined data (possibly old data, possibly garbage).
	//
	// If on different nodes, DRBD is backed by devices with differing discard
	// characteristics, discards may lead to data divergence (old data or
	// garbage left over on one backend, zeroes due to unmapped areas on the
	// other backend). Online verify would now potentially report tons of
	// spurious differences. While probably harmless for most use cases (fstrim
	// on a file system), DRBD cannot have that.
	//
	// To play safe, we have to disable discard support, if our local backend
	// (on a Primary) does not support "discard_zeroes_data=true". We also have
	// to translate discards to explicit zero-out on the receiving side, unless
	// the receiving side (Secondary) supports "discard_zeroes_data=true",
	// thereby allocating areas what were supposed to be unmapped.
	//
	// There are some devices (notably the LVM/DM thin provisioning) that are
	// capable of discard, but announce discard_zeroes_data=false. In the case
	// of DM-thin, discards aligned to the chunk size will be unmapped, and
	// reading from unmapped sectors will return zeroes. However, unaligned
	// partial head or tail areas of discard requests will be silently ignored.
	//
	// If we now add a helper to explicitly zero-out these unaligned partial
	// areas, while passing on the discard of the aligned full chunks, we
	// effectively achieve discard_zeroes_data=true on such devices.
	//
	// Setting discard-zeroes-if-aligned to yes will allow DRBD to use discards,
	// and to announce discard_zeroes_data=true, even on backends that announce
	// discard_zeroes_data=false.
	//
	// Setting discard-zeroes-if-aligned to no will cause DRBD to always
	// fall-back to zero-out on the receiving side, and to not even announce
	// discard capabilities on the Primary, if the respective backend announces
	// discard_zeroes_data=false.
	//
	// We used to ignore the discard_zeroes_data setting completely. To not
	// break established and expected behaviour, and suddenly cause fstrim on
	// thin-provisioned LVs to run out-of-space instead of freeing up space, the
	// default value is yes.
	//
	// This option is available since 8.4.7.
	DiscardZeroesIfAligned *bool `drbd:"discard-zeroes-if-aligned,no-discard-zeroes-if-aligned"`

	// Some disks announce WRITE_SAME support to the kernel but fail with an I/O
	// error upon actually receiving such a request. This mostly happens when
	// using virtualized disks -- notably, this behavior has been observed with
	// VMware's virtual disks.
	//
	// When disable-write-same is set to yes, WRITE_SAME detection is manually
	// overriden and support is disabled.
	//
	// The default value of disable-write-same is no. This option is available
	// since 8.4.7.
	DisableWriteSame *bool
}

var _ drbdconf.SectionKeyworder = &DiskOptions{}

func (d *DiskOptions) SectionKeyword() string {
	return "disk"
}

type IOErrorPolicy string

var _ drbdconf.ParameterCodec = ptr(IOErrorPolicy(""))

func (i *IOErrorPolicy) MarshalParameter() ([]string, error) {
	return []string{string(*i)}, nil
}

func (i *IOErrorPolicy) UnmarshalParameter(p []drbdconf.Word) error {
	panic("unimplemented")
}

const (
	// Change the disk status to Inconsistent, mark the failed block as
	// inconsistent in the bitmap, and retry the I/O operation on a remote
	// cluster node.
	IOErrorPolicyPassOn IOErrorPolicy = "pass_on"
	// Call the local-io-error handler (see the [Handlers] section).
	IOErrorPolicyCallLocalIOError IOErrorPolicy = "call-local-io-error"
	// Detach the lower-level device and continue in diskless mode.
	IOErrorPolicyDetach IOErrorPolicy = "detach"
)

type ReadBalancingPolicy string

var _ drbdconf.ParameterCodec = ptr(ReadBalancingPolicy(""))

// MarshalParameter implements drbdconf.ParameterCodec.
func (r *ReadBalancingPolicy) MarshalParameter() ([]string, error) {
	return []string{string(*r)}, nil
}

// UnmarshalParameter implements drbdconf.ParameterCodec.
func (r *ReadBalancingPolicy) UnmarshalParameter(p []drbdconf.Word) error {
	panic("unimplemented")
}

const (
	ReadBalancingPolicyPreferLocal         ReadBalancingPolicy = "prefer-local"
	ReadBalancingPolicyPreferRemote        ReadBalancingPolicy = "prefer-remote"
	ReadBalancingPolicyRoundRobin          ReadBalancingPolicy = "round-robin"
	ReadBalancingPolicyLeastPending        ReadBalancingPolicy = "least-pending"
	ReadBalancingPolicyWhenCongestedRemote ReadBalancingPolicy = "when-congested-remote"
	ReadBalancingPolicy32KStriping         ReadBalancingPolicy = "32K-striping"
	ReadBalancingPolicy64KStriping         ReadBalancingPolicy = "64K-striping"
	ReadBalancingPolicy128KStriping        ReadBalancingPolicy = "128K-striping"
	ReadBalancingPolicy256KStriping        ReadBalancingPolicy = "256K-striping"
	ReadBalancingPolicy512KStriping        ReadBalancingPolicy = "512K-striping"
	ReadBalancingPolicy1MStriping          ReadBalancingPolicy = "1M-striping"
)
