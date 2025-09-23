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

type Patch[T any] func(T) error

type RVRPatch = Patch[*v1alpha2.ReplicatedVolumeReplica]
type LLVPatch = Patch[*snc.LVMLogicalVolume]

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

func (Actions) _action()                       {}
func (ParallelActions) _action()               {}
func (Patch[T]) _action()                      {}
func (CreateReplicatedVolumeReplica) _action() {}
func (WaitReplicatedVolumeReplica) _action()   {}
func (DeleteReplicatedVolumeReplica) _action() {}
func (CreateLVMLogicalVolume) _action()        {}
func (WaitLVMLogicalVolume) _action()          {}
func (DeleteLVMLogicalVolume) _action()        {}

var _ Action = Actions{}
var _ Action = ParallelActions{}
var _ Action = Patch[any](nil)
var _ Action = CreateReplicatedVolumeReplica{}
var _ Action = WaitReplicatedVolumeReplica{}
var _ Action = DeleteReplicatedVolumeReplica{}
var _ Action = CreateLVMLogicalVolume{}
var _ Action = WaitLVMLogicalVolume{}
var _ Action = DeleteLVMLogicalVolume{}
