package v9

// Define a path between two hosts. This section must contain two host
// parameters.
type Path struct {
	// Defines an endpoint for a connection. Each [Host] statement refers to an
	// [On] section in a resource. If a port number is defined, this endpoint
	// will use the specified port instead of the port defined in the [On]
	// section. Each [Path] section must contain exactly two [Host] parameters.
	Hosts *Endpoint
}
