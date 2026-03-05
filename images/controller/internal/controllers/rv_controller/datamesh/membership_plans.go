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

package datamesh

import (
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
)

// registerMembershipPlans registers AddReplica, RemoveReplica (and future membership)
// transition plans in the registry.
//
// Transition handles are created once here and passed to plan registration functions.
// Each plan file registers its plans on the shared handles.
func registerMembershipPlans(reg *dmte.Registry[*globalContext, *ReplicaContext]) {
	addReplica := reg.ReplicaTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica, membershipSlot)
	removeReplica := reg.ReplicaTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica, membershipSlot)

	registerAccessPlans(addReplica, removeReplica)
	registerTieBreakerPlans(addReplica, removeReplica)
	registerShadowDiskfulPlans(addReplica, removeReplica)
	// TODO: register Diskful plans (future)

	changeReplicaType := reg.ReplicaTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType, membershipSlot)
	registerChangeTypePlans(changeReplicaType)
}
