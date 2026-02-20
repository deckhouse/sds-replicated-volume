/*
Copyright 2025 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v9

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"
)

// <name> [address [<address-family>] <address>:<port-number>] [port <port-number>]
type HostAddress struct {
	Name            string
	AddressWithPort string
	AddressFamily   string
	Port            *uint
}

func (h *HostAddress) MarshalParameter() ([]string, error) {
	res := []string{h.Name}
	if h.AddressWithPort != "" {
		res = append(res, "address")
		if h.AddressFamily != "" {
			res = append(res, h.AddressFamily)
		}
		res = append(res, h.AddressWithPort)
	} else if h.Port != nil {
		res = append(res, "port")
		res = append(res, strconv.FormatUint(uint64(*h.Port), 10))
	}
	return res, nil
}

func (h *HostAddress) UnmarshalParameter(p []drbdconf.Word) error {
	if err := drbdconf.EnsureLen(p, 2); err != nil {
		return err
	}

	hostname := p[1].Value

	if len(p) == 2 {
		h.Name = hostname
		return nil
	}

	p = p[2:]

	addressWithPort, addressFamily, portStr, err := unmarshalHostAddress(p)
	if err != nil {
		return err
	}

	// write result
	var port *uint
	if portStr != "" {
		p, err := strconv.ParseUint(portStr, 10, 64)
		if err != nil {
			return err
		}
		port = ptr(uint(p))
	}
	h.Name = hostname
	h.AddressWithPort = addressWithPort
	h.AddressFamily = addressFamily
	h.Port = port

	return nil
}

func unmarshalHostAddress(p []drbdconf.Word) (
	addressWithPort, addressFamily, portStr string,
	err error,
) {
	if err = drbdconf.EnsureLen(p, 2); err != nil {
		return
	}

	switch p[0].Value {
	case "address":
		if len(p) == 2 {
			addressWithPort = p[1].Value
		} else { // >=3
			addressFamily = p[1].Value
			addressWithPort = p[2].Value
		}
	case "port":
		portStr = p[1].Value
	default:
		err = fmt.Errorf("unrecognized keyword: '%s'", p[0].Value)
	}

	return
}

var _ drbdconf.ParameterCodec = &HostAddress{}

//

// [<address-family>] <address>:<port>
type AddressWithPort struct {
	Address       string
	AddressFamily string
	Port          uint
}

var _ drbdconf.ParameterCodec = &AddressWithPort{}

func (a *AddressWithPort) UnmarshalParameter(p []drbdconf.Word) error {
	if err := drbdconf.EnsureLen(p, 2); err != nil {
		return err
	}

	addrIdx := 1
	if len(p) >= 3 {
		a.AddressFamily = p[1].Value
		addrIdx++
	}
	addrVal := p[addrIdx].Value

	portSepIdx := strings.LastIndexByte(addrVal, ':')
	if portSepIdx < 0 {
		return fmt.Errorf("invalid format: ':port' is required")
	}

	addrParts := []string{addrVal[0:portSepIdx], addrVal[portSepIdx+1:]}

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

type Unit struct {
	Value  int
	Suffix string
}

var _ drbdconf.ParameterCodec = new(Unit)

func (u *Unit) MarshalParameter() ([]string, error) {
	return []string{strconv.FormatUint(uint64(u.Value), 10) + u.Suffix}, nil
}

func (u *Unit) UnmarshalParameter(p []drbdconf.Word) error {
	if err := drbdconf.EnsureLen(p, 2); err != nil {
		return err
	}

	strVal := p[1].Value

	// treat non-digit suffix as units
	suffix := []byte{}
	for i := len(strVal) - 1; i >= 0; i-- {
		ch := strVal[i]
		if ch < '0' || ch > '9' {
			suffix = append(suffix, ch)
		} else {
			strVal = strVal[0 : i+1]
		}
	}

	val, err := strconv.Atoi(strVal)
	if err != nil {
		return err
	}

	u.Value = val
	u.Suffix = string(suffix)
	return nil
}
