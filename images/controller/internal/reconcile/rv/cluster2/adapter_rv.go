package cluster2

import (
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
)

type rvAdapter struct {
	name         string
	sharedSecret string
}

type RVAdapter interface {
	RVName() string
	SharedSecret() string
}

var _ RVAdapter = &rvAdapter{}

func NewRVAdapter(rv *v1alpha2.ReplicatedVolume) (*rvAdapter, error) {
	if rv == nil {
		return nil, errArgNil("rv")
	}

	res := &rvAdapter{
		name:         rv.Name,
		sharedSecret: rv.Spec.SharedSecret,
	}

	return res, nil
}

func (rv *rvAdapter) RVName() string {
	return rv.name
}

func (rv *rvAdapter) SharedSecret() string {
	return rv.sharedSecret
}
