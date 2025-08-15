package v9

import "github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"

// This section can contain each a disk, handlers, net, options, and startup
// section. All resources inherit the parameters in these sections as their
// default values.
type Common struct {
	Disk     *DiskOptions
	Handlers *Handlers
	Net      *Net
	Startup  *Startup
}

var _ drbdconf.SectionKeyworder = &Common{}

func (*Common) SectionKeyword() string {
	return "common"
}
