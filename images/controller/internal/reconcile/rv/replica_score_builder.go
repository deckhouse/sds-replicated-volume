/*
Copyright 2025 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

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
	switch {
	case !b.withDisk:
		scores = append(scores, topology.NeverSelect)
	case b.publishRequested:
		scores = append(scores, maxScore)
	case b.alreadyExists:
		scores = append(scores, alreadyExistsScore)
	default:
		scores = append(scores, baseScore)
	}

	if b.disklessPurpose {
		switch {
		case b.publishRequested:
			scores = append(scores, maxScore)
		case b.alreadyExists:
			scores = append(scores, alreadyExistsScore)
		default:
			scores = append(scores, baseScore)
		}

		if !b.withDisk {
			// prefer nodes without disk for diskless purposes
			scores[len(scores)-1] = scores[len(scores)-1] * 2
		}
	}
	return scores
}
