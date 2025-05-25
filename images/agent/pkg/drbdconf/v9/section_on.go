package v9

import "github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"

// Define the properties of a resource on a particular host or set of hosts.
// Specifying more than one host name can make sense in a setup with IP address
// failover, for example. The host-name argument must match the Linux host name
// (uname -n).
//
// Usually contains or inherits at least one [Volume] section. The node-id and
// address parameters must be defined in this section. The device, disk, and
// meta-disk parameters must be defined in, or inherited by, this section.
//
// A normal configuration file contains two or more [On] sections for each
// resource. Also see the [Floating] section.
type On struct {
	HostNames []string `drbd:""`

	// Defines the address family, address, and port of a connection endpoint.
	//
	// The address families ipv4, ipv6, ssocks (Dolphin Interconnect Solutions' "super sockets"), sdp (Infiniband Sockets Direct Protocol), and sci are supported (sci is an alias for ssocks). If no address family is specified, ipv4 is assumed. For all address families except ipv6, the address is specified in IPV4 address notation (for example, 1.2.3.4). For ipv6, the address is enclosed in brackets and uses IPv6 address notation (for example, [fd01:2345:6789:abcd::1]). The port is always specified as a decimal number from 1 to 65535.
	//
	// On each host, the port numbers must be unique for each address; ports cannot be shared.
	Address *AddressWithPort `drbd:"address"`

	// Defines the unique node identifier for a node in the cluster. Node identifiers are used to identify individual nodes in the network protocol, and to assign bitmap slots to nodes in the metadata.
	//
	// Node identifiers can only be reasssigned in a cluster when the cluster is down. It is essential that the node identifiers in the configuration and in the device metadata are changed consistently on all hosts. To change the metadata, dump the current state with drbdmeta dump-md, adjust the bitmap slot assignment, and update the metadata with drbdmeta restore-md.
	//
	// The node-id parameter exists since DRBD 9. Its value ranges from 0 to 16; there is no default.
	NodeId *uint `drbd:"node-id"`

	Volume *Volume
}

func (o *On) SectionKeyword() string {
	return "on"
}

var _ drbdconf.SectionKeyworder = &On{}

// Like the [On] section, except that instead of the host name a network address
// is used to determine if it matches a floating section.
//
// The node-id parameter in this section is required. If the address parameter
// is not provided, no connections to peers will be created by default. The
// device, disk, and meta-disk parameters must be defined in, or inherited by,
// this section.
type Floating struct {
	// Defines the address family, address, and port of a connection endpoint.
	//
	// The address families ipv4, ipv6, ssocks (Dolphin Interconnect Solutions' "super sockets"), sdp (Infiniband Sockets Direct Protocol), and sci are supported (sci is an alias for ssocks). If no address family is specified, ipv4 is assumed. For all address families except ipv6, the address is specified in IPV4 address notation (for example, 1.2.3.4). For ipv6, the address is enclosed in brackets and uses IPv6 address notation (for example, [fd01:2345:6789:abcd::1]). The port is always specified as a decimal number from 1 to 65535.
	//
	// On each host, the port numbers must be unique for each address; ports cannot be shared.
	Address *AddressWithPort `drbd:"address"`

	// Defines the unique node identifier for a node in the cluster. Node identifiers are used to identify individual nodes in the network protocol, and to assign bitmap slots to nodes in the metadata.
	//
	// Node identifiers can only be reasssigned in a cluster when the cluster is down. It is essential that the node identifiers in the configuration and in the device metadata are changed consistently on all hosts. To change the metadata, dump the current state with drbdmeta dump-md, adjust the bitmap slot assignment, and update the metadata with drbdmeta restore-md.
	//
	// The node-id parameter exists since DRBD 9. Its value ranges from 0 to 16; there is no default.
	NodeId *int `drbd:"node-id"`
}

func (o *Floating) SectionKeyword() string {
	return "floating"
}
