package rv

import "github.com/deckhouse/sds-replicated-volume/images/controller/internal/reconcile/rv/cluster/topology"

type replicaScoreBuilder struct {
	disklessPurpose  bool
	withDisk         bool
	publishRequested bool
	alreadyExists    bool
}

func (b *replicaScoreBuilder) ClusterHasDiskless() {
	b.disklessPurpose = true
}

func (b *replicaScoreBuilder) NodeWithDisk() {
	b.withDisk = true
}

func (b *replicaScoreBuilder) AlreadyExists() {
	b.alreadyExists = true
}

func (b *replicaScoreBuilder) PublishRequested() {
	b.publishRequested = true
}

func (b *replicaScoreBuilder) Build() []topology.Score {
	baseScore := topology.Score(100)
	maxScore := topology.Score(1000000)
	alreadyExistsScore := topology.Score(1000)
	var scores []topology.Score
	if b.withDisk {
		if b.publishRequested {
			scores = append(scores, maxScore)
		} else if b.alreadyExists {
			scores = append(scores, alreadyExistsScore)
		} else {
			scores = append(scores, baseScore)
		}
	} else {
		scores = append(scores, topology.NeverSelect)
	}

	if b.disklessPurpose {
		if b.withDisk {
			scores = append(scores, baseScore)
		} else {
			// prefer nodes without disk for diskless purposes
			scores = append(scores, baseScore*2)
		}
	}
	return scores
}
