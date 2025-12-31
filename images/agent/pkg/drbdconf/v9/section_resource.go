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

// Define a resource. Usually contains at least two [On] sections and at least
// one [Connection] section.
type Resource struct {
	Name           string `drbd:""`
	Connections    []*Connection
	ConnectionMesh *ConnectionMesh
	Disk           *DiskOptions
	Floating       []*Floating
	Handlers       *Handlers
	Net            *Net
	On             []*On
	Options        *Options
	Startup        *Startup
}

var _ drbdconf.SectionKeyworder = &Resource{}

func (r *Resource) SectionKeyword() string {
	return "resource"
}
