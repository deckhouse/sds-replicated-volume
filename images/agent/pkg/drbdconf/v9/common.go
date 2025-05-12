package v9

// This section can contain each a disk, handlers, net, options, and startup
// section. All resources inherit the parameters in these sections as their
// default values.
type Common struct {
	Disk     *DiskOptions
	Handlers *Handlers
	Net      *Net
	Startup  *Startup
}
