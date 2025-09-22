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

type DeleteReplica struct {
	ReplicatedVolumeReplica *v1alpha2.ReplicatedVolumeReplica
}

type DeleteLVMLogicalVolume struct {
	LVMLogicalVolume *snc.LVMLogicalVolume
}

type CreateLVMLogicalVolume struct {
	LVMLogicalVolume *snc.LVMLogicalVolume
}

type WaitLVMLogicalVolume struct {
	LVMLogicalVolume *snc.LVMLogicalVolume
}

type CreateReplicatedVolumeReplica struct {
	ReplicatedVolumeReplica *v1alpha2.ReplicatedVolumeReplica
}

type WaitReplicatedVolumeReplica struct {
	ReplicatedVolumeReplica *v1alpha2.ReplicatedVolumeReplica
}

type Patch[T any] func(T) error

type WaitForVolumeOp struct {
	VolumeId int
}

type DeleteVolumeOp struct {
	VolumeId int
}

type RetryReconcile struct {
}

func (Actions) _action()                       {}
func (ParallelActions) _action()               {}
func (DeleteReplica) _action()                 {}
func (DeleteLVMLogicalVolume) _action()        {}
func (CreateLVMLogicalVolume) _action()        {}
func (WaitLVMLogicalVolume) _action()          {}
func (CreateReplicatedVolumeReplica) _action() {}
func (WaitReplicatedVolumeReplica) _action()   {}
func (Patch[T]) _action()                      {}
func (WaitForVolumeOp) _action()               {}
func (RetryReconcile) _action()                {}

var _ Action = Actions{}
var _ Action = ParallelActions{}
var _ Action = DeleteReplica{}
var _ Action = DeleteLVMLogicalVolume{}
var _ Action = CreateLVMLogicalVolume{}
var _ Action = WaitLVMLogicalVolume{}
var _ Action = CreateReplicatedVolumeReplica{}
var _ Action = WaitReplicatedVolumeReplica{}
var _ Action = Patch[any](nil)
var _ Action = WaitForVolumeOp{}
var _ Action = RetryReconcile{}
