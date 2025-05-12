package v9

// Define a resource. Usually contains at least two [On] sections and at least
// one [Connection] section.
type Resource struct {
	Name string

	Connection *Connection

	ConnectionMesh *ConnectionMesh

	Disk *DiskOptions

	Floating *Floating

	Handlers *Handlers

	Net *Net

	On *On

	Options *Options

	Startup *Startup
}
