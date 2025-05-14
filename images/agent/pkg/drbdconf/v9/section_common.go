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

var _ Section = &Common{}

func (*Common) Keyword() string {
	return "common"
}

func (c *Common) UnmarshalFromSection(sec *drbdconf.Section) error {
	return nil
}

func (c *Common) MarshalToSection() *drbdconf.Section {
	sec := &drbdconf.Section{
		Key: []drbdconf.Word{drbdconf.Word{}},
	}
	_ = sec
	panic("unimplemented")
}
