package v9

type Endpoint struct {
	Source *Host
	Target *Host
}

type Host struct {
	Name    string
	Address *Address
	Port    *Port
}

type Address struct {
	Address       string
	AddressFamily string
}

type AddressWithPort struct {
	AddressFamily string
	Address       string
	Port          uint16
}

type Port struct {
	PortNumber uint16
}

type Sectors uint
