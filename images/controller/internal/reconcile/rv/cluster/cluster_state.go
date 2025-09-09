package cluster

import (
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
)

type Cluster struct {
	rv    *v1alpha2.ReplicatedVolume
	nodes []Node
}

type Node struct {
	resources []Replica
}

type Replica struct {
	rvr *v1alpha2.ReplicatedVolumeReplica
}

type Step interface {
	_step()
}

type DeleteReplicaStep struct {
}

type AddReplicaStep struct {
}

type FixReplicaStep struct {
}

type WaitReplicaStep struct {
}

func (d *DeleteReplicaStep) _step() {}
func (a *AddReplicaStep) _step()    {}
func (f *FixReplicaStep) _step()    {}
func (f *WaitReplicaStep) _step()   {}

var _ Step = &DeleteReplicaStep{}
var _ Step = &AddReplicaStep{}
var _ Step = &FixReplicaStep{}
var _ Step = &WaitReplicaStep{}

func ProduceSteps(target *Cluster, current *Cluster) []Step {
	return nil
}
