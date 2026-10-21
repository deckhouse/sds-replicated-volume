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
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
)

// updateBaselineGMDR recomputes gctx.baselineGMDR from committed qmr.
// Returns false if baseline is already correct (no-op).
//
// Formula: GMDR = min(qmr − 1, config.GMDR).
// Cap at Configuration ensures baseline never exceeds the target level.
//
// Used as both apply callback (via composeGlobalApply) and OnComplete
// callback (via updateBaselineGMDROnComplete wrapper).
func updateBaselineGMDR(gctx *globalContext) bool {
	qmr := gctx.datamesh.quorumMinimumRedundancy
	var gmdr byte
	if qmr > 0 {
		gmdr = qmr - 1
	}
	return dmte.SetChanged(&gctx.baselineGMDR, min(gmdr, gctx.configuration.GuaranteedMinimumDataRedundancy))
}

// computeCorrectQuorum returns the correct q and qmr for the current datamesh
// state. Used by ChangeQuorum plans and dispatcher as the single source of truth.
//
// q is always voters/2+1 (majority). qmr targets config.GMDR+1 but is constrained:
//   - Lowering is always safe (relaxes quorum requirement).
//   - Raising is limited by UpToDate D count (can't require more copies than exist).
func computeCorrectQuorum(gctx *globalContext) (q, qmr byte) {
	voters := voterCount(gctx)
	q = voters/2 + 1

	targetQMR := gctx.configuration.QuorumMinimumRedundancy()
	currentQMR := gctx.datamesh.quorumMinimumRedundancy

	if targetQMR <= currentQMR {
		// Lowering or no change — always safe.
		qmr = targetQMR
	} else {
		// R2 -> R3 intent is visible before layout convergence reserves the TB
		// or the replica controller submits ChangeRole. Keep the current data
		// requirement throughout that window. Once promotion starts its voting
		// transition owns the final raise. Never lower an already committed qmr.
		if holdR3QMRRaise(gctx) {
			return q, currentQMR
		}
		// Raising — raise as far as safely possible.
		safeQMR := min(targetQMR, upToDateDiskfulCount(gctx))
		qmr = max(currentQMR, safeQMR)
	}
	return
}

// Shared by standalone quorum reconciliation and the qmr step embedded in
// membership plans, including a repair after an interrupted TB promotion.
func holdR3QMRRaise(gctx *globalContext) bool {
	return gctx.configuration.FailuresToTolerate == 1 &&
		gctx.configuration.GuaranteedMinimumDataRedundancy == 1 &&
		gctx.baselineGMDR == 0 && freshUpToDateVoters(gctx) < 3
}
