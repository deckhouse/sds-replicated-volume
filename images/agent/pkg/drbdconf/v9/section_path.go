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
