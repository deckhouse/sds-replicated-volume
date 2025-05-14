package v9

import "github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"

// Define a resource. Usually contains at least two [On] sections and at least
// one [Connection] section.
type Resource struct {
	Name           string `drbd:""`
	Connection     *Connection
	ConnectionMesh *ConnectionMesh
	Disk           *DiskOptions
	Floating       *Floating
	Handlers       *Handlers
	Net            *Net
	On             *On
	Options        *Options
	Startup        *Startup
}

var _ Section = &Resource{}

func (r *Resource) Keyword() string {
	dname := "resource"
	if r != nil && r.Name != "" {
		dname += " " + r.Name
	}
	return dname
}

// UnmarshalFromSection implements Section.
func (r *Resource) UnmarshalFromSection(sec *drbdconf.Section) error {
	return nil
}

func (r *Resource) MarshalToSection() *drbdconf.Section {
	panic("unimplemented")
}
