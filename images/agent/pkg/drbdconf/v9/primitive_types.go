package v9

import (
	"strconv"
	"strings"

	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"
)

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
	Port          uint
}

var _ drbdconf.ParameterCodec = &AddressWithPort{}

func (a *AddressWithPort) UnmarshalParameter(p []drbdconf.Word) error {
	addrIdx := 1
	if len(p) >= 3 {
		a.AddressFamily = p[1].Value
		addrIdx++
	}
	addrVal := p[addrIdx].Value
	addrParts := strings.Split(addrVal, ":")

	a.Address = addrParts[0]
	port, err := strconv.ParseUint(addrParts[1], 10, 64)
	if err != nil {
		return err
	}
	a.Port = uint(port)
	return nil
}

func (a *AddressWithPort) MarshalParameter() ([]string, error) {
	res := []string{}

	if a.AddressFamily != "" {
		res = append(res, a.AddressFamily)
	}
	res = append(res, a.Address+":"+strconv.FormatUint(uint64(a.Port), 10))

	return res, nil
}

type Port struct {
	PortNumber uint16
}

type Sectors uint
