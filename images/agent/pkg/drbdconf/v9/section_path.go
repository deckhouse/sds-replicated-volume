package v9

import "github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"

// Define a path between two hosts. This section must contain two host
// parameters.
type Path struct {
	// Defines an endpoint for a connection. Each [Host] statement refers to an
	// [On] section in a resource. If a port number is defined, this endpoint
	// will use the specified port instead of the port defined in the [On]
	// section. Each [Path] section must contain exactly two [Host] parameters.
	Hosts []HostAddress `drbd:"host"`
}

var _ drbdconf.SectionKeyworder = &Path{}

func (*Path) SectionKeyword() string { return "path" }
