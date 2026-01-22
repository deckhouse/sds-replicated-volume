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
var _ PatchStatusAction = ConfigureIPAddressAction{}
var _ PatchStatusAction = AllocatePortsAction{}

//

func (f AddAgentFinalizerAction) _action()    {}
func (f RemoveAgentFinalizerAction) _action() {}
func (f ConfigureIPAddressAction) _action()   {}
func (f AllocatePortsAction) _action()        {}

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

type ConfigureIPAddressAction struct {
	IPv4BySystemNetworkNames map[string]string
}

// ApplyStatusPatch implements PatchStatusAction.
func (a ConfigureIPAddressAction) ApplyStatusPatch(drbdr *v1alpha1.DRBDResource) bool {
	// allocate missing
	for _, nw := range a.SystemNetworks {

		for _, existingAddr := range drbdr.Status.Addresses {
			if existingAddr.SystemNetworkName == nw {
				// existingAddr.Address.IPv4
				// existingAddr.Address.Port
			}
		}

	}
}

type AllocatePortsAction struct {
}

func (f AllocatePortsAction) ApplyStatusPatch(drbdr *v1alpha1.DRBDResource) bool {
	panic("unimplemented")
}
