package v9

import "github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"

// Define a connection mesh between multiple hosts. This section must contain a
// hosts parameter, which has the host names as arguments. This section is a
// shortcut to define many connections which share the same network options.
type ConnectionMesh struct {
	// Defines all nodes of a mesh. Each name refers to an [On] section in a
	// resource. The port that is defined in the [On] section will be used.
	Hosts []string `drbd:"hosts"`

	Net *Net
}

// SectionKeyword implements drbdconf.SectionKeyworder.
func (c *ConnectionMesh) SectionKeyword() string {
	return "connection-mesh"
}

var _ drbdconf.SectionKeyworder = &ConnectionMesh{}
