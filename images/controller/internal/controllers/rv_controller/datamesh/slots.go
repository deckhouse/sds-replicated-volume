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
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
)

// Slot constants identify replica slots. Each slot allows at most one active
// transition per replica. Domain code uses these constants for readable access;
// the engine uses them as indices into a fixed-size array.
const (
	membershipSlot dmte.ReplicaSlotID = iota
	attachmentSlot
)

// registerSlots registers all slot accessors in the registry.
func registerSlots(reg *dmte.Registry[*globalContext, *ReplicaContext]) {
	reg.RegisterReplicaSlot(membershipSlot, membershipSlotAccessor{})
	reg.RegisterReplicaSlot(attachmentSlot, attachmentSlotAccessor{})
}

// ──────────────────────────────────────────────────────────────────────────────
// Membership slot
//

// membershipSlotAccessor binds the membership slot to ReplicaContext fields.
type membershipSlotAccessor struct{}

func (membershipSlotAccessor) GetActiveTransition(rctx *ReplicaContext) *dmte.Transition {
	return rctx.membershipTransition
}

func (membershipSlotAccessor) SetActiveTransition(rctx *ReplicaContext, t *dmte.Transition) {
	rctx.membershipTransition = t
}

func (membershipSlotAccessor) SetStatus(rctx *ReplicaContext, msg string, _ any) {
	rctx.membershipMessage = msg
}

// ──────────────────────────────────────────────────────────────────────────────
// Attachment slot
//

// attachmentSlotAccessor binds the attachment slot to ReplicaContext fields.
type attachmentSlotAccessor struct{}

func (attachmentSlotAccessor) GetActiveTransition(rctx *ReplicaContext) *dmte.Transition {
	return rctx.attachmentTransition
}

func (attachmentSlotAccessor) SetActiveTransition(rctx *ReplicaContext, t *dmte.Transition) {
	rctx.attachmentTransition = t
}

func (attachmentSlotAccessor) SetStatus(rctx *ReplicaContext, msg string, details any) {
	rctx.attachmentConditionMessage = msg
	if reason, ok := details.(string); ok {
		rctx.attachmentConditionReason = reason
	}
}
