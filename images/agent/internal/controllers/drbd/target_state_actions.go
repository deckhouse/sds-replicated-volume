package drbd

import (
	"context"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

type Action interface {
	_action()
}

type PatchAction interface {
	Action
	ApplyPatch(drbdr *v1alpha1.DRBDResource) bool
}

type PatchStatusAction interface {
	Action
	ApplyStatusPatch(drbdr *v1alpha1.DRBDResource) bool
}

type ExecuteDRBDAction interface {
	Action
	Execute(ctx context.Context) error
}

//

var _ PatchAction = AddAgentFinalizerAction{}
var _ PatchAction = RemoveAgentFinalizerAction{}
var _ PatchStatusAction = BumpObservedGenerationForConfiguredAction{}

//

func (f AddAgentFinalizerAction) _action()                   {}
func (f RemoveAgentFinalizerAction) _action()                {}
func (a BumpObservedGenerationForConfiguredAction) _action() {}

//

type AddAgentFinalizerAction struct{}

func (AddAgentFinalizerAction) ApplyPatch(drbdr *v1alpha1.DRBDResource) bool {
	return obju.AddFinalizer(drbdr, v1alpha1.AgentFinalizer)
}

//

type RemoveAgentFinalizerAction struct{}

func (RemoveAgentFinalizerAction) ApplyPatch(drbdr *v1alpha1.DRBDResource) bool {
	return obju.RemoveFinalizer(drbdr, v1alpha1.AgentFinalizer)
}

//

type BumpObservedGenerationForConfiguredAction struct{}

func (BumpObservedGenerationForConfiguredAction) ApplyStatusPatch(drbdr *v1alpha1.DRBDResource) (changed bool) {
	// only bump ObservedGeneration for type=Configured
	cond := obju.GetStatusCondition(drbdr, v1alpha1.DRBDResourceCondConfiguredType)
	return obju.SetStatusCondition(drbdr, *cond)
}
