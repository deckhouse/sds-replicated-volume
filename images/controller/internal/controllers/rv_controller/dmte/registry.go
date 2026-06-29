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

package dmte

import "strconv"

// ──────────────────────────────────────────────────────────────────────────────
// Registry
//

// registryKey is the composite key for the registry: (transition type, plan ID).
type registryKey struct {
	transitionType TransitionType
	planID         PlanID
}

// Registry is the static plan store built at startup. Owns slots, transition
// types (via RegisteredTransition), and plans (registered under transitions).
type Registry[G any, R ReplicaCtx] struct {
	replicaSlotAccessors [MaxReplicaSlots]ReplicaSlotAccessor[R]
	plans                map[registryKey]*plan[G, R]
}

// NewRegistry creates an empty Registry.
func NewRegistry[G any, R ReplicaCtx]() *Registry[G, R] {
	return &Registry[G, R]{
		plans: make(map[registryKey]*plan[G, R]),
	}
}

// RegisterReplicaSlot registers a slot accessor at the given index.
// Panics if accessor is nil, id >= MaxReplicaSlots, or the slot is already registered.
func (reg *Registry[G, R]) RegisterReplicaSlot(id ReplicaSlotID, accessor ReplicaSlotAccessor[R]) {
	if accessor == nil {
		panic("dmte.Registry.RegisterReplicaSlot: accessor must not be nil")
	}
	if id >= MaxReplicaSlots {
		panic("dmte.Registry.RegisterReplicaSlot: slot ID " + strconv.Itoa(int(id)) + " >= MaxReplicaSlots")
	}
	if reg.replicaSlotAccessors[id] != nil {
		panic("dmte.Registry.RegisterReplicaSlot: slot " + strconv.Itoa(int(id)) + " already registered")
	}
	reg.replicaSlotAccessors[id] = accessor
}

// get returns the plan for the given composite key, or nil if not found.
func (reg *Registry[G, R]) get(tt TransitionType, id PlanID) *plan[G, R] {
	return reg.plans[registryKey{transitionType: tt, planID: id}]
}

// replicaSlotAccessor returns the slot accessor for the given ID, or nil if not registered.
func (reg *Registry[G, R]) replicaSlotAccessor(id ReplicaSlotID) ReplicaSlotAccessor[R] {
	if id >= MaxReplicaSlots {
		return nil
	}
	return reg.replicaSlotAccessors[id] // nil if not registered
}

// PlanStepCount returns the number of steps in the plan identified by
// (transitionType, planID). Returns 0 if the plan is not found.
func (reg *Registry[G, R]) PlanStepCount(tt TransitionType, planID PlanID) int {
	p := reg.get(tt, planID)
	if p == nil {
		return 0
	}
	return len(p.steps)
}

// MaxPlanStepCount returns the maximum step count across all plans
// registered for the given transition type. Returns 0 if no plans exist.
func (reg *Registry[G, R]) MaxPlanStepCount(tt TransitionType) int {
	m := 0
	for key, p := range reg.plans {
		if key.transitionType == tt && len(p.steps) > m {
			m = len(p.steps)
		}
	}
	return m
}

// ──────────────────────────────────────────────────────────────────────────────
// RegisteredTransition
//

// RegisteredTransition is a handle to a transition type registered in the Registry.
// Carries scope and slot. Plans registered under it inherit both.
type RegisteredTransition[G any, R ReplicaCtx] struct {
	registry       *Registry[G, R]
	transitionType TransitionType
	scope          Scope
	slot           ReplicaSlotID // meaningful only for ReplicaScope
}

// ReplicaTransition registers a replica-scoped transition type with a slot
// and returns a handle for registering plans under it.
func (reg *Registry[G, R]) ReplicaTransition(tt TransitionType, slot ReplicaSlotID) *RegisteredTransition[G, R] {
	return &RegisteredTransition[G, R]{
		registry:       reg,
		transitionType: tt,
		scope:          ReplicaScope,
		slot:           slot,
	}
}

// GlobalTransition registers a global-scoped (slot-less) transition type
// and returns a handle for registering plans under it.
func (reg *Registry[G, R]) GlobalTransition(tt TransitionType) *RegisteredTransition[G, R] {
	return &RegisteredTransition[G, R]{
		registry:       reg,
		transitionType: tt,
		scope:          GlobalScope,
	}
}

// Plan creates a PlanBuilder under this transition type.
// Inherits scope and slot from the RegisteredTransition.
func (rt *RegisteredTransition[G, R]) Plan(id PlanID) *PlanBuilder[G, R] {
	return &PlanBuilder[G, R]{
		registry: rt.registry,
		key:      registryKey{transitionType: rt.transitionType, planID: id},
		p: plan[G, R]{
			scope:          rt.scope,
			slot:           rt.slot,
			transitionType: rt.transitionType,
		},
	}
}
