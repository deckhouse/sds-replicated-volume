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

package v1alpha1

// MaintenanceMode pauses reconciliation of the object carrying it. Reconciliation is paused
// exactly while the field is non-empty; the value names the scope that is paused and, where
// more than one writer may set the field, who paused it.
//
// The type is shared by ReplicatedVolume and DRBDResource, but the scope each field names is
// not the same: on a DRBDResource the mode stops DRBD operations only (the resource keeps
// acquiring its finalizer, allocating addresses, releasing backing volume finalizers and
// reporting status), whereas on a ReplicatedVolume it stops every action on the volume,
// deletion included. Each field admits its own subset of the values below, declared by its own
// enum marker, so the scope of a value is always read together with the field it sits on.
type MaintenanceMode string

const (
	// MaintenanceModeNoResourceReconciliation pauses reconciliation but status is still updated.
	// Set by an operator, and never by a controller; a controller must leave it untouched.
	MaintenanceModeNoResourceReconciliation MaintenanceMode = "NoResourceReconciliation"

	// MaintenanceModeNoResourceReconciliationByVolume pauses reconciliation exactly as
	// MaintenanceModeNoResourceReconciliation does, and additionally names the replica controller
	// as the writer: it is the value that controller propagates down from a paused
	// ReplicatedVolume. Encoding the writer in the value, rather than adding a second field, is
	// what lets the two pauses coexist on one field: the controller writes this value only into an
	// empty field and clears only this value back out, so an operator's hand-set
	// MaintenanceModeNoResourceReconciliation is never overwritten and never lifted by the volume
	// resuming. An operator MUST NOT set this value by hand.
	//
	// Not admitted on ReplicatedVolume: the volume is where a pause originates, never where one
	// arrives.
	MaintenanceModeNoResourceReconciliationByVolume MaintenanceMode = "NoResourceReconciliationByVolume"
)

func (m MaintenanceMode) String() string { return string(m) }
