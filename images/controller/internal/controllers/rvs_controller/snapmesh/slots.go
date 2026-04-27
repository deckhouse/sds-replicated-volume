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

package snapmesh

import (
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
)

const (
	prepareSlot dmte.ReplicaSlotID = 0
	syncSlot    dmte.ReplicaSlotID = 1
)

func registerSlots(reg *dmte.Registry[*globalContext, *replicaContext]) {
	reg.RegisterReplicaSlot(prepareSlot, prepareSlotAccessor{})
	reg.RegisterReplicaSlot(syncSlot, syncSlotAccessor{})
}

type prepareSlotAccessor struct{}

func (prepareSlotAccessor) GetActiveTransition(rctx *replicaContext) *dmte.Transition {
	return rctx.prepareTransition
}

func (prepareSlotAccessor) SetActiveTransition(rctx *replicaContext, t *dmte.Transition) {
	rctx.prepareTransition = t
}

func (prepareSlotAccessor) SetStatus(rctx *replicaContext, msg string, _ any) {
	rctx.statusMessage = msg
}

type syncSlotAccessor struct{}

func (syncSlotAccessor) GetActiveTransition(rctx *replicaContext) *dmte.Transition {
	return rctx.syncTransition
}

func (syncSlotAccessor) SetActiveTransition(rctx *replicaContext, t *dmte.Transition) {
	rctx.syncTransition = t
}

func (syncSlotAccessor) SetStatus(rctx *replicaContext, msg string, _ any) {
	rctx.statusMessage = msg
}
