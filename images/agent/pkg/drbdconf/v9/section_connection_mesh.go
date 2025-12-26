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
