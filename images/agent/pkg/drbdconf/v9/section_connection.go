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

import "github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"

// Define a connection between two hosts. This section must contain two [HostAddress]
// parameters or multiple [Path] sections. The optional name is used to refer to
// the connection in the system log and in other messages. If no name is
// specified, the peer's host name is used instead.
type Connection struct {
	Name string `drbd:""`

	// Defines an endpoint for a connection. Each [Host] statement refers to an
	// [On] section in a [Resource]. If a port number is defined, this endpoint
	// will use the specified port instead of the port defined in the on
	// section. Each [Connection] section must contain exactly two [Host]
	// parameters. Instead of two [Host] parameters the connection may contain
	// multiple [Path] sections.
	Hosts []HostAddress `drbd:"host"`

	Paths []*Path

	Net *Net

	Volume *ConnectionVolume

	PeerDeviceOptions *PeerDeviceOptions
}

func (c *Connection) SectionKeyword() string {
	return "connection"
}

var _ drbdconf.SectionKeyworder = &Connection{}
