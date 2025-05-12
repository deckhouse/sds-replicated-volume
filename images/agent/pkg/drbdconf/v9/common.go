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

func (c *Common) Read(sec *drbdconf.Section) (bool, error) {
	if sec.Key[0].Value != "common" {
		return false, nil
	}

	return true, nil
}

func (c *Common) validate() error {
	return nil
}

func (c *Common) Write() error {
	return nil
}
