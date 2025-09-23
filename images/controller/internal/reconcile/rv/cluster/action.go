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
