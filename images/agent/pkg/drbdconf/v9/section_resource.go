package v9

import "github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"

// Define a resource. Usually contains at least two [On] sections and at least
// one [Connection] section.
type Resource struct {
	Name           string `drbd:""`
	Connections    []*Connection
	ConnectionMesh *ConnectionMesh
	Disk           *DiskOptions
	Floating       []*Floating
	Handlers       *Handlers
	Net            *Net
	On             []*On
	Options        *Options
	Startup        *Startup
}

var _ drbdconf.SectionKeyworder = &Resource{}

func (r *Resource) SectionKeyword() string {
	return "resource"
}
