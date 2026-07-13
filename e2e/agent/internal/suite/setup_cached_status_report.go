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

package suite

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/kubetesting"
)

// SetupCachedStatusReport validates that the agent reports DRBD runtime state
// sourced from the cached drbdsetup status/show output, whose freshness is
// driven by the events2 invalidation signal.
//
// Maintenance mode is used as a deterministic probe. While a resource is in
// maintenance the reconciler observes actual state from the status/show caches
// and then skips every convergence action and the post-convergence status
// refresh — so the status it reports is derived entirely from the cached
// drbdsetup output that events2 kept fresh. If cache invalidation were broken
// (stale or dropped entries), the maintenance report would carry stale or
// missing fields.
//
// The single maintenance report asserted here exercises the full cached view:
//   - drbdsetup status -> local diskState and size,
//   - drbdsetup status connections/peer-devices -> per-peer connectionState
//     and diskState.
//
// Requires drbdrs to be a peered, synced pair: DiskState=UpToDate on both and
// each seeing the other Connected/UpToDate (as arranged by SetupInitialSync).
// The maintenance patch is reverted on cleanup; the agent then resumes
// reconciliation and returns the resource to Configured=True.
func SetupCachedStatusReport(
	e envtesting.E,
	cl client.WithWatch,
	drbdrs []*v1alpha1.DRBDResource,
) {
	if len(drbdrs) < 2 {
		e.Fatalf("require: need at least 2 drbdrs, got %d", len(drbdrs))
	}

	var drbdrConfiguredTimeout DRBDRConfiguredTimeout
	e.Options(&drbdrConfiguredTimeout)

	self := drbdrs[0]
	peer := drbdrs[1]

	// Arrange: enter maintenance mode, forcing the next report to be sourced
	// from the cached drbdsetup status/show output. Wait for the maintenance
	// report to land (Configured=False/InMaintenance) — this is the
	// cache-sourced report.
	self = kubetesting.SetupResourcePatch(
		e.ScopeWithTimeout(drbdrConfiguredTimeout.Duration),
		cl,
		client.ObjectKey{Name: self.Name},
		func(d *v1alpha1.DRBDResource) {
			d.Spec.Maintenance = v1alpha1.MaintenanceModeNoResourceReconciliation
		},
		isDRBDRCondition(
			v1alpha1.DRBDResourceCondConfiguredType,
			metav1.ConditionFalse,
			v1alpha1.DRBDResourceCondConfiguredReasonInMaintenance,
		),
	)

	// Assert: the maintenance report confirms we are on the cache-sourced path.
	assertDRBDRCondition(e, self,
		v1alpha1.DRBDResourceCondConfiguredType,
		metav1.ConditionFalse,
		v1alpha1.DRBDResourceCondConfiguredReasonInMaintenance)

	// Local device state and size, sourced from the cached drbdsetup status.
	assertDRBDRDiskState(e, self, v1alpha1.DiskStateUpToDate)
	assertDRBDRSizePopulated(e, self)

	// Per-peer connection and disk state, sourced from the cached drbdsetup
	// status connections/peer-devices.
	assertPeerConnectedUpToDate(e, self, peer.Spec.NodeName, peer.Spec.NodeID)
}
