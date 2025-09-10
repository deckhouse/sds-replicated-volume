package rv

import "github.com/deckhouse/sds-replicated-volume/api/v1alpha2"

type replicaQueryResult2 struct {
	ExtraReplicas    []any
	ExistingReplicas []any
	MissingReplicas  []any
}

type replicaQueryResult interface{ _isReplicaResult() }

type errorReplicaQueryResult struct {
	Node string
	Err  error
}

func (errorReplicaQueryResult) _isReplicaResult() {}

type replicaExists struct {
	Node string
	RVR  *v1alpha2.ReplicatedVolumeReplica
}

func (replicaExists) _isReplicaResult() {}

type replicaMissing struct {
	Node      string
	FreePort  uint
	FreeMinor uint
}

func (replicaMissing) _isReplicaResult() {}
