package cluster2

import (
	"slices"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
)

type rvAdapter struct {
	name                    string
	replicas                byte
	size                    int
	sharedSecret            string
	publishRequested        []string
	quorum                  byte
	quorumMinimumRedundancy byte
}

type RVAdapter interface {
	RVName() string
	Replicas() byte
	Size() int
	SharedSecret() string
	AllowTwoPrimaries() bool
	PublishRequested() []string
	Quorum() byte
	QuorumMinimumRedundancy() byte
}

var _ RVAdapter = &rvAdapter{}

func NewRVAdapter(rv *v1alpha2.ReplicatedVolume) (*rvAdapter, error) {
	if rv == nil {
		return nil, errArgNil("rv")
	}

	var quorum byte = rv.Spec.Replicas/2 + 1
	var qmr byte
	if rv.Spec.Replicas > 2 {
		qmr = quorum
	}

	res := &rvAdapter{
		name:                    rv.Name,
		replicas:                rv.Spec.Replicas,
		size:                    int(rv.Spec.Size.Value()),
		sharedSecret:            rv.Spec.SharedSecret,
		publishRequested:        slices.Clone(rv.Spec.PublishRequested),
		quorum:                  quorum,
		quorumMinimumRedundancy: qmr,
	}

	return res, nil
}

func (rv *rvAdapter) RVName() string {
	return rv.name
}

func (rv *rvAdapter) Size() int {
	return rv.size
}

func (rv *rvAdapter) Replicas() byte {
	return rv.replicas
}

func (rv *rvAdapter) SharedSecret() string {
	return rv.sharedSecret
}

func (rv *rvAdapter) PublishRequested() []string {
	return slices.Clone(rv.publishRequested)
}

func (rv *rvAdapter) Quorum() byte {
	return rv.quorum
}

func (rv *rvAdapter) QuorumMinimumRedundancy() byte {
	return rv.quorumMinimumRedundancy
}

func (rv *rvAdapter) AllowTwoPrimaries() bool {
	return len(rv.publishRequested) > 1
}
