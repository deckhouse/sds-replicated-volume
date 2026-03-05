/*
Copyright 2026 Flant JSC

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

package datamesh

import (
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// effectiveLayoutEqual returns true if both pointers are nil, or both are
// non-nil with equal FTT and GMDR values.
func effectiveLayoutEqual(a, b *v1alpha1.ReplicatedVolumeLayout) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// updateBaselineLayout recomputes gctx.baselineLayout from committed datamesh
// state (members, q, qmr). Called from step OnComplete callbacks after replicas
// confirm q/qmr/membership changes.
//
// Formulas:
//
//	GMDR = qmr − 1
//	FTT  = min(D − q + TB_bonus, D − qmr)
//	TB_bonus = 1 if D is even and at least one TieBreaker member exists, else 0
func updateBaselineLayout(gctx *globalContext) {
	var voters, tiebreakers byte
	for i := range gctx.allReplicas {
		m := gctx.allReplicas[i].member
		if m == nil {
			continue
		}
		if m.Type.IsVoter() {
			voters++
		}
		if m.Type == v1alpha1.DatameshMemberTypeTieBreaker {
			tiebreakers++
		}
	}

	q := gctx.datamesh.Quorum
	qmr := gctx.datamesh.QuorumMinimumRedundancy

	// GMDR = qmr − 1
	var gmdr byte
	if qmr > 0 {
		gmdr = qmr - 1
	}

	// FTT = min(D − q + TB_bonus, D − qmr)
	var tbBonus byte
	if voters%2 == 0 && tiebreakers > 0 {
		tbBonus = 1
	}
	var ftt byte
	if voters >= q && voters >= qmr {
		fttQuorum := voters - q + tbBonus
		fttRedundancy := voters - qmr
		ftt = min(fttQuorum, fttRedundancy)
	}

	// Cap at Configuration: baseline rises toward Configuration but never exceeds it.
	// During downgrade (Configuration lowered), this ensures baseline decreases immediately.
	gctx.baselineLayout.FailuresToTolerate = min(ftt, gctx.configuration.FailuresToTolerate)
	gctx.baselineLayout.GuaranteedMinimumDataRedundancy = min(gmdr, gctx.configuration.GuaranteedMinimumDataRedundancy)
}

// computeEffectiveLayout computes the FTT/GMDR level that the datamesh
// actually provides right now, based on observable cluster state.
//
// The result reflects reality including degradation (dead voters reduce FTT)
// and transient upgrades (new voter added but baseline not yet raised).
// Returns nil when the effective layout cannot be determined (e.g., no voter
// replicas with ready agents to observe).
//
// Called unconditionally each reconciliation cycle (not gated by engine changed).
//
// TODO: implement — compute effective FTT/GMDR from actual voter health,
// quorum status, and applied qmr across replicas.
func computeEffectiveLayout(_ *globalContext) *v1alpha1.ReplicatedVolumeLayout {
	// TODO: compute from actual cluster state. For now, return nil.
	return nil
}
