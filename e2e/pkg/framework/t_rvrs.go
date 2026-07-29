/*
Copyright 2026 Flant JSC

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

package framework

import (
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	tk "github.com/deckhouse/sds-replicated-volume/lib/go/testkit"
)

// TestRVRS is the domain wrapper for ReplicatedVolumeReplicaSnapshot test
// objects. Unlike TestRV/TestRVR, tests never create RVRSs directly — they
// are created by the rvs_controller as children of an RVS.
type TestRVRS struct {
	*tk.TrackedObject[*v1alpha1.ReplicatedVolumeReplicaSnapshot]
	f *Framework
}
