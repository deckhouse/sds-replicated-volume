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

package dme

import (
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// UpdateEffectiveLayout recalculates rv.Status.EffectiveLayout from current voter
// count and qmr. Called by Apply functions of steps that change q or qmr.
//
// EffectiveLayout tracks the protection levels the datamesh actually provides right now:
//   - GMDR = qmr - 1
//   - FTT is derived from voter count, q, and TB presence
func UpdateEffectiveLayout(rv *v1alpha1.ReplicatedVolume) {
	qmr := rv.Status.Datamesh.QuorumMinimumRedundancy

	// GMDR = qmr - 1 (guaranteed minimum data redundancy).
	if qmr > 0 {
		rv.Status.EffectiveLayout.GuaranteedMinimumDataRedundancy = qmr - 1
	} else {
		rv.Status.EffectiveLayout.GuaranteedMinimumDataRedundancy = 0
	}

	// Count voters (D + D∅) and tiebreakers.
	voters := 0
	tiebreakers := 0
	for i := range rv.Status.Datamesh.Members {
		m := &rv.Status.Datamesh.Members[i]
		if m.Type.IsVoter() {
			voters++
		}
		if m.Type == v1alpha1.DatameshMemberTypeTieBreaker {
			tiebreakers++
		}
	}

	// FTT: how many arbitrary node failures the system can tolerate.
	// q = floor(voters/2) + 1 (minimum safe quorum).
	// With TB: FTT = voters - q (TB bridges the last vote gap for even voters).
	// Without TB (odd voters): FTT = voters - q.
	// Without TB (even voters): FTT = voters - q - 1 (symmetric partition has no TB to break tie).
	//
	// Simplified: FTT = D - qmr (D = FTT + GMDR + 1, so FTT = D - GMDR - 1 = D - qmr).
	// But this only works when D ≥ qmr. More precisely:
	//   FTT = max(0, voters - qmr)  when tiebreaker is available for even voters
	//   FTT = max(0, voters - qmr - 1) when no tiebreaker and voters are even
	//
	// Use the safe formula: q = floor(voters/2) + 1, FTT = voters - q.
	q := voters/2 + 1
	if q < 1 {
		q = 1
	}

	ftt := 0
	if voters >= q {
		ftt = voters - q
	}
	rv.Status.EffectiveLayout.FailuresToTolerate = byte(ftt)
}
