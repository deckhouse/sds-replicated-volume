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

// updateBaselineGMDR recomputes gctx.baselineGMDR from committed qmr.
// Called from step callbacks after replicas confirm qmr/membership changes.
//
// Formula: GMDR = min(qmr − 1, config.GMDR).
// Cap at Configuration ensures baseline never exceeds the target level.
func updateBaselineGMDR(gctx *globalContext) {
	qmr := gctx.datamesh.quorumMinimumRedundancy
	var gmdr byte
	if qmr > 0 {
		gmdr = qmr - 1
	}
	gctx.baselineGMDR = min(gmdr, gctx.configuration.GuaranteedMinimumDataRedundancy)
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

	targetQMR := gctx.configuration.GuaranteedMinimumDataRedundancy + 1
	currentQMR := gctx.datamesh.quorumMinimumRedundancy

	if targetQMR <= currentQMR {
		// Lowering or no change — always safe.
		qmr = targetQMR
	} else {
		// Raising — raise as far as safely possible.
		safeQMR := min(targetQMR, upToDateDiskfulCount(gctx))
		qmr = max(currentQMR, safeQMR)
	}
	return
}
