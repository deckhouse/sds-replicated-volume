package cluster2

import (
	"slices"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
)

type rvAdapter struct {
	name         string
	sharedSecret string
	ownedRVRs    []RVRAdapter
	ownedLLVs    []LLVAdapter
}

type RVAdapter interface {
	RVName() string
	SharedSecret() string
}

type RVAdapterWithOwned interface {
	RVAdapter
	OwnedRVRs() []RVRAdapter
	OwnedLLVs() []LLVAdapter
}

var _ RVAdapterWithOwned = &rvAdapter{}

func NewRVAdapter(
	rv *v1alpha2.ReplicatedVolume,
	ownedRVRs []v1alpha2.ReplicatedVolumeReplica,
	ownedLLVs []snc.LVMLogicalVolume,
) (*rvAdapter, error) {
	if rv == nil {
		return nil, errArgNil("rv")
	}

	res := &rvAdapter{
		name:         rv.Name,
		sharedSecret: rv.Spec.SharedSecret,
	}

	for i := range ownedRVRs {
		rvrA := NewRVRAdapter(&ownedRVRs[i])
		res.ownedRVRs = append(res.ownedRVRs, rvrA)
	}

	for i := range ownedLLVs {
		llvA := NewLLVAdapter(&ownedLLVs[i])
		res.ownedLLVs = append(res.ownedLLVs, llvA)
	}

	return res, nil
}

func (rv *rvAdapter) RVName() string {
	return rv.name
}

func (rv *rvAdapter) SharedSecret() string {
	return rv.sharedSecret
}

func (rv *rvAdapter) OwnedRVRs() []RVRAdapter {
	return slices.Clone(rv.ownedRVRs)
}

func (rv *rvAdapter) OwnedLLVs() []LLVAdapter {
	return slices.Clone(rv.ownedLLVs)
}
