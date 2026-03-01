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

package dme

import (
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// CreateGlobalTransition evaluates global guards, checks parallelism, builds a new
// datamesh-wide transition, writes all steps to status, calls step[0].Apply, and
// appends to rv.Status.DatameshTransitions.
//
// Returns (transition, "") on success, or (nil, reason) if blocked by a guard or parallelism.
//
// Exception: uses metav1.Now() for step StartedAt (controller-owned state timestamp).
func (e *Engine) CreateGlobalTransition(
	transitionType v1alpha1.ReplicatedVolumeDatameshTransitionType,
	planID PlanID,
) (*v1alpha1.ReplicatedVolumeDatameshTransition, string) {
	p := e.registry.get(transitionType, planID)
	if p == nil {
		panic("dme.CreateGlobalTransition: unknown plan (" + string(transitionType) + ", " + string(planID) + ")")
	}

	// Evaluate global guards.
	for _, guard := range p.globalGuards {
		blocked, msg := guard(&e.globalCtx)
		if blocked {
			return nil, msg
		}
	}

	// Check parallelism.
	allowed, reason := e.parallelism.check(e.globalCtx.RV.Status.DatameshTransitions, p.group, noReplicaID)
	if !allowed {
		return nil, reason
	}

	t := createTransitionCommon(e.globalCtx.RV, p, transitionType, planID)

	// Apply step 0, bump revision, and generate initial message via confirm.
	e.applyAndConfirmStep(p, t, nil, 0)

	// Update parallelism cache.
	e.parallelism.add(t)

	return t, ""
}

// CreateReplicaTransition looks up the ReplicaContext by replicaID and delegates
// to createReplicaTransition. Panics if replicaID is unknown.
//
// Returns (transition, "") on success, or (nil, reason) if blocked by a guard or parallelism.
//
// Exception: uses metav1.Now() for step StartedAt (controller-owned state timestamp).
func (e *Engine) CreateReplicaTransition(
	transitionType v1alpha1.ReplicatedVolumeDatameshTransitionType,
	planID PlanID,
	replicaID uint8,
) (*v1alpha1.ReplicatedVolumeDatameshTransition, string) {
	rctx := e.replicaCtxByID[replicaID]
	if rctx == nil {
		panic("dme.CreateReplicaTransition: unknown replica ID")
	}
	return e.createReplicaTransition(transitionType, planID, rctx)
}

// createReplicaTransition evaluates replica guards, checks parallelism, builds a new
// replica transition, writes all steps to status, calls step[0].Apply, and appends
// to rv.Status.DatameshTransitions.
//
// For Emergency transitions, existing transitions for the same replica are automatically
// cancelled after all checks pass.
func (e *Engine) createReplicaTransition(
	transitionType v1alpha1.ReplicatedVolumeDatameshTransitionType,
	planID PlanID,
	rctx *ReplicaContext,
) (*v1alpha1.ReplicatedVolumeDatameshTransition, string) {
	p := e.registry.get(transitionType, planID)
	if p == nil {
		panic("dme.CreateReplicaTransition: unknown plan (" + string(transitionType) + ", " + string(planID) + ")")
	}

	// Evaluate replica guards.
	for _, guard := range p.replicaGuards {
		blocked, msg := guard(&e.globalCtx, rctx)
		if blocked {
			return nil, msg
		}
	}

	id, hasID := rctx.ID()
	if !hasID {
		panic("dme.CreateReplicaTransition: replica context has no ID")
	}

	// Check parallelism.
	allowed, reason := e.parallelism.check(e.globalCtx.RV.Status.DatameshTransitions, p.group, id)
	if !allowed {
		return nil, reason
	}

	// Emergency: cancel existing transitions for this replica before creating.
	if p.group == v1alpha1.ReplicatedVolumeDatameshTransitionGroupEmergency {
		e.cancelTransitionsForReplica(e.globalCtx.RV, id)
	}

	t := createTransitionCommon(e.globalCtx.RV, p, transitionType, planID)
	switch {
	case rctx.MembershipRequest != nil:
		t.ReplicaName = rctx.MembershipRequest.Name
	case rctx.RVR != nil:
		t.ReplicaName = rctx.RVR.Name
	case rctx.Member != nil:
		t.ReplicaName = rctx.Member.Name
	}

	// Apply step 0, bump revision, and generate initial message via confirm.
	e.applyAndConfirmStep(p, t, rctx, 0)

	// Write membership message to request.
	if rctx.MembershipRequest != nil && rctx.membershipMessage != "" {
		rctx.MembershipRequest.Message = rctx.membershipMessage
	}

	// Update parallelism cache.
	e.parallelism.add(t)

	return t, ""
}

// createTransitionCommon is the shared logic for CreateGlobalTransition and createReplicaTransition.
func createTransitionCommon(
	rv *v1alpha1.ReplicatedVolume,
	p *plan,
	transitionType v1alpha1.ReplicatedVolumeDatameshTransitionType,
	planID PlanID,
) *v1alpha1.ReplicatedVolumeDatameshTransition {
	now := metav1.Now()

	// Build steps from plan.
	steps := make([]v1alpha1.ReplicatedVolumeDatameshTransitionStep, len(p.steps))
	for i := range p.steps {
		steps[i].Name = p.steps[i].name
		steps[i].Status = v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending
	}

	// Activate step 0.
	steps[0].Status = v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive
	steps[0].StartedAt = &now

	// Append the transition.
	rv.Status.DatameshTransitions = append(rv.Status.DatameshTransitions, v1alpha1.ReplicatedVolumeDatameshTransition{
		Type:            transitionType,
		Group:           p.group,
		PlanID:          string(planID),
		ReplicaType:     p.replicaType,
		FromReplicaType: p.fromReplicaType,
		ToReplicaType:   p.toReplicaType,
		Steps:           steps,
	})
	return &rv.Status.DatameshTransitions[len(rv.Status.DatameshTransitions)-1]
}

// applyAndConfirmStep applies the step, bumps DatameshRevision, runs a single confirm,
// and generates the initial progress message. Does NOT check completion or advance.
func (e *Engine) applyAndConfirmStep(
	p *plan,
	t *v1alpha1.ReplicatedVolumeDatameshTransition,
	rctx *ReplicaContext,
	stepIdx int,
) {
	s := &p.steps[stepIdx]
	s.apply(&e.globalCtx, rctx)
	e.globalCtx.RV.Status.DatameshRevision++
	t.Steps[stepIdx].DatameshRevision = e.globalCtx.RV.Status.DatameshRevision
	cr := s.confirm(&e.globalCtx, rctx, t.Steps[stepIdx].DatameshRevision)
	progress := generateProgressMessage(e.globalCtx.RVRs, t.Steps[stepIdx].DatameshRevision, cr, s, rctx)
	t.Steps[stepIdx].Message = progress
	e.setReplicaCtxMessage(t.Group, s, rctx, progress)
}

// setReplicaCtxMessage stores the composed message in the appropriate rctx output field
// based on the transition group.
func (e *Engine) setReplicaCtxMessage(
	group v1alpha1.ReplicatedVolumeDatameshTransitionGroup,
	s *step,
	rctx *ReplicaContext,
	progress string,
) {
	if rctx == nil {
		return
	}
	composed := composeMessage(s, progress)
	switch group {
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
		v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership,
		v1alpha1.ReplicatedVolumeDatameshTransitionGroupEmergency:
		rctx.membershipMessage = composed
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment:
		rctx.attachmentConditionMessage = composed
		rctx.attachmentConditionReason = s.attachmentConditionReason
	}
}

// advanceStep completes the step at fromIdx and activates the next step.
// Clears the message on the completed step (no leftover "waiting for..." text).
//
// Exception: uses metav1.Now() for timestamps (controller-owned state).
func advanceStep(t *v1alpha1.ReplicatedVolumeDatameshTransition, fromIdx int) {
	now := metav1.Now()
	t.Steps[fromIdx].Status = v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted
	t.Steps[fromIdx].CompletedAt = &now
	t.Steps[fromIdx].Message = ""
	t.Steps[fromIdx+1].Status = v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive
	t.Steps[fromIdx+1].StartedAt = &now
}

// completeTransition updates the parallelism cache and removes the transition at index i.
// Must be called when a transition has all steps completed.
func (e *Engine) completeTransition(rv *v1alpha1.ReplicatedVolume, idx int) {
	e.parallelism.remove(&rv.Status.DatameshTransitions[idx])
	rv.Status.DatameshTransitions = slices.Delete(rv.Status.DatameshTransitions, idx, idx+1)
}

// cancelTransitionsForReplica removes all active transitions for the given replica ID
// and updates the parallelism cache.
func (e *Engine) cancelTransitionsForReplica(rv *v1alpha1.ReplicatedVolume, replicaID uint8) bool {
	changed := false
	for i := len(rv.Status.DatameshTransitions) - 1; i >= 0; i-- {
		t := &rv.Status.DatameshTransitions[i]
		if t.ReplicaName != "" && t.ReplicaID() == replicaID {
			e.parallelism.remove(t)
			rv.Status.DatameshTransitions = slices.Delete(rv.Status.DatameshTransitions, i, i+1)
			changed = true
		}
	}
	return changed
}
