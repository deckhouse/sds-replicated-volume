package cluster

import (
	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
)

type Action interface {
	_action()
}

type Actions []Action

type ParallelActions []Action

func cleanAction(a Action) Action {
	switch t := a.(type) {
	case Actions:
		t = cleanActions(t)
		if len(t) == 1 {
			return t[0]
		}
		return t
	case ParallelActions:
		t = cleanActions(t)
		if len(t) == 1 {
			return t[0]
		}
		return t
	default:
		return a
	}
}

func cleanActions[T ~[]Action](actions T) (result T) {
	for _, a := range actions {
		a = cleanAction(a)
		if a == nil {
			continue
		}
		// ungroup items of same type
		if t, ok := a.(T); ok {
			result = append(result, t...)
		} else {
			result = append(result, a)
		}
	}
	return
}

// RVRPatch represents a patch to be applied to a specific ReplicatedVolumeReplica
type RVRPatch struct {
	ReplicatedVolumeReplica *v1alpha2.ReplicatedVolumeReplica
	Apply                   func(*v1alpha2.ReplicatedVolumeReplica) error
}

// LLVPatch represents a patch to be applied to a specific LVMLogicalVolume
type LLVPatch struct {
	LVMLogicalVolume *snc.LVMLogicalVolume
	Apply            func(*snc.LVMLogicalVolume) error
}

type CreateReplicatedVolumeReplica struct {
	ReplicatedVolumeReplica *v1alpha2.ReplicatedVolumeReplica
}

type WaitReplicatedVolumeReplica struct {
	ReplicatedVolumeReplica *v1alpha2.ReplicatedVolumeReplica
}

type DeleteReplicatedVolumeReplica struct {
	ReplicatedVolumeReplica *v1alpha2.ReplicatedVolumeReplica
}

type CreateLVMLogicalVolume struct {
	LVMLogicalVolume *snc.LVMLogicalVolume
}

type WaitLVMLogicalVolume struct {
	LVMLogicalVolume *snc.LVMLogicalVolume
}

type DeleteLVMLogicalVolume struct {
	LVMLogicalVolume *snc.LVMLogicalVolume
}

type WaitAndTriggerInitialSync struct {
	ReplicatedVolumeReplicas []*v1alpha2.ReplicatedVolumeReplica
}

type TriggerRVRResize struct {
	ReplicatedVolumeReplica *v1alpha2.ReplicatedVolumeReplica
}

func (Actions) _action()                       {}
func (ParallelActions) _action()               {}
func (RVRPatch) _action()                      {}
func (LLVPatch) _action()                      {}
func (CreateReplicatedVolumeReplica) _action() {}
func (WaitReplicatedVolumeReplica) _action()   {}
func (DeleteReplicatedVolumeReplica) _action() {}
func (CreateLVMLogicalVolume) _action()        {}
func (WaitLVMLogicalVolume) _action()          {}
func (DeleteLVMLogicalVolume) _action()        {}
func (WaitAndTriggerInitialSync) _action()     {}
func (TriggerRVRResize) _action()              {}

var _ Action = Actions{}
var _ Action = ParallelActions{}

// ensure interface conformance
var _ Action = RVRPatch{}
var _ Action = LLVPatch{}
var _ Action = CreateReplicatedVolumeReplica{}
var _ Action = WaitReplicatedVolumeReplica{}
var _ Action = DeleteReplicatedVolumeReplica{}
var _ Action = CreateLVMLogicalVolume{}
var _ Action = WaitLVMLogicalVolume{}
var _ Action = DeleteLVMLogicalVolume{}
var _ Action = WaitAndTriggerInitialSync{}
var _ Action = TriggerRVRResize{}
