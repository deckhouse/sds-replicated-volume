package v9

import (
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"
)

// Define some global parameters. All parameters in this section are optional.
// Only one [Global] section is allowed in the configuration.
type Global struct {
	// The DRBD init script can be used to configure and start DRBD devices,
	// which can involve waiting for other cluster nodes. While waiting, the
	// init script shows the remaining waiting time. The dialog-refresh defines
	// the number of seconds between updates of that countdown. The default
	// value is 1; a value of 0 turns off the countdown.
	DialogRefresh *int `drbd:"dialog-refresh"`

	// Normally, DRBD verifies that the IP addresses in the configuration match
	// the host names. Use the disable-ip-verification parameter to disable
	// these checks.
	DisableIPVerification bool `drbd:"disable-ip-verification"`

	// A explained on DRBD's Online Usage Counter[2] web page, DRBD includes a
	// mechanism for anonymously counting how many installations are using which
	// versions of DRBD. The results are available on the web page for anyone to
	// see.
	//
	// This parameter defines if a cluster node participates in the usage
	// counter; the supported values are yes, no, and ask (ask the user, the
	// default).
	//
	// We would like to ask users to participate in the online usage counter as
	// this provides us valuable feedback for steering the development of DRBD.
	UsageCount UsageCountValue `drbd:"usage-count"`

	// When udev asks drbdadm for a list of device related symlinks, drbdadm
	// would suggest symlinks with differing naming conventions, depending on
	// whether the resource has explicit volume VNR { } definitions, or only one
	// single volume with the implicit volume number 0:
	//   # implicit single volume without "volume 0 {}" block
	//   DEVICE=drbd<minor>
	//   SYMLINK_BY_RES=drbd/by-res/<resource-name>
	//   SYMLINK_BY_DISK=drbd/by-disk/<backing-disk-name>
	//   # explicit volume definition: volume VNR { }
	//   DEVICE=drbd<minor>
	//   SYMLINK_BY_RES=drbd/by-res/<resource-name>/VNR
	//   SYMLINK_BY_DISK=drbd/by-disk/<backing-disk-name>
	// If you define this parameter in the global section, drbdadm will always
	// add the .../VNR part, and will not care for whether the volume definition
	// was implicit or explicit.
	// For legacy backward compatibility, this is off by default, but we do
	// recommend to enable it.
	UdevAlwaysUseVNR bool `drbd:"udev-always-use-vnr"`
}

var _ drbdconf.SectionKeyworder = &Global{}

func (g *Global) SectionKeyword() string { return "global" }

type UsageCountValue string

const (
	UsageCountValueYes UsageCountValue = "yes"
	UsageCountValueNo  UsageCountValue = "no"
	UsageCountValueAsk UsageCountValue = "ask"
)

var _ drbdconf.ParameterCodec = ptr(UsageCountValue(""))

var knownValuesUsageCountValue = map[UsageCountValue]struct{}{
	UsageCountValueYes: {},
	UsageCountValueNo:  {},
	UsageCountValueAsk: {},
}

func (u *UsageCountValue) MarshalParameter() ([]string, error) {
	return []string{string(*u)}, nil
}

func (u *UsageCountValue) UnmarshalParameter(p []drbdconf.Word) error {
	return drbdconf.ReadEnumAt(u, knownValuesUsageCountValue, p, 1)
}
