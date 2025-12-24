package rvrtiebreakercount

import (
	"slices"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

type baseReplica *v1alpha1.ReplicatedVolumeReplica

type tb *v1alpha1.ReplicatedVolumeReplica

type failureDomain struct {
	nodeNames    []string // for Any/Zonal topology it is always single node
	baseReplicas []baseReplica
	tbs          []tb
}

func (fd *failureDomain) baseReplicaCount() int {
	return len(fd.baseReplicas)
}

func (fd *failureDomain) tbReplicaCount() int {
	return len(fd.tbs)
}

func (fd *failureDomain) addTBReplica(rvr tb) bool {
	if !slices.Contains(fd.nodeNames, rvr.Spec.NodeName) {
		return false
	}
	fd.tbs = append(fd.tbs, rvr)

	return true
}

func (fd *failureDomain) addBaseReplica(rvr baseReplica) bool {
	if !slices.Contains(fd.nodeNames, rvr.Spec.NodeName) {
		return false
	}

	fd.baseReplicas = append(fd.baseReplicas, rvr)

	return true
}

func (fd *failureDomain) popTBReplica() *v1alpha1.ReplicatedVolumeReplica {
	if len(fd.tbs) == 0 {
		return nil
	}
	tb := fd.tbs[len(fd.tbs)-1]
	fd.tbs = fd.tbs[0 : len(fd.tbs)-1]
	return tb
}
