package drbd

import (
	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

type IntendedState interface {
	IsZero() bool

	IsUpAndNotInCleanup() bool

	IPv4BySystemNetworkNames() map[string]string

	Peers() []IntendedPeer
}

type IntendedPeer interface {
	NodeId() uint
	Paths() []IntendedPath
}

type IntendedPath interface {
	IPv4() string
	Port() uint
}

type intendedState struct {
	drbdr *v1alpha1.DRBDResource
}

// IPv4BySystemNetworkNames implements IntendedState.
func (iState *intendedState) IPv4BySystemNetworkNames() map[string]string {
	panic("unimplemented")
}

// Peers implements IntendedState.
func (iState *intendedState) Peers() []IntendedPeer {
	panic("unimplemented")
}

func (iState *intendedState) IsZero() bool {
	return iState == nil
}

func (iState *intendedState) IsUpAndNotInCleanup() bool {
	if iState.drbdr.DeletionTimestamp != nil &&
		!obju.HasFinalizersOtherThan(iState.drbdr, v1alpha1.AgentFinalizer) {
		// it's time to cleanup, so ignoring an spec.state
		return false
	}

	return iState.drbdr.Spec.State != v1alpha1.DRBDResourceStateDown
}

var _ IntendedState = (*intendedState)(nil)

func getIntendedState(drbdr *v1alpha1.DRBDResource) (*intendedState, error) {
	// TODO: add fields needed for [Intended] getters and materialize them here

	return &intendedState{drbdr}, nil
}
