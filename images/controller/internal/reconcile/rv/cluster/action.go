package cluster

import "github.com/deckhouse/sds-replicated-volume/api/v1alpha2"

type Action interface {
	_action()
}

type ParallelActionGroup []Action

type DeleteReplica struct {
	ReplicatedVolumeReplica *v1alpha2.ReplicatedVolumeReplica
}

type AddReplica struct {
	ReplicatedVolumeReplica *v1alpha2.ReplicatedVolumeReplica
}

type FixReplicaIPOp struct {
	NewIPv4 string
}

type WaitForVolumeOp struct {
	VolumeId int
}

type DeleteVolumeOp struct {
	VolumeId int
}

func (*ParallelActionGroup) _action() {}
func (*DeleteReplica) _action()       {}
func (*AddReplica) _action()          {}
func (*FixReplicaIPOp) _action()      {}
func (*WaitForVolumeOp) _action()     {}

var _ Action = &ParallelActionGroup{}
var _ Action = &DeleteReplica{}
var _ Action = &AddReplica{}
var _ Action = &FixReplicaIPOp{}
var _ Action = &WaitForVolumeOp{}
