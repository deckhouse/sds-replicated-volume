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

package rvcontroller

import (
	"slices"
	"time"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// ──────────────────────────────────────────────────────────────────────────────
// Configuration rollout budget (RollingUpdate.maxParallel)
//
// A storage class edit rewrites the intended layout of every volume of that class at once.
// spec.configurationRolloutStrategy.rollingUpdate.maxParallel caps how many of them may carry
// the new configuration before it has actually converged, so a configuration that turns out to
// be unsatisfiable damages a bounded number of volumes instead of the whole class.
//
// The budget is enforced without any shared ledger: every volume decides for itself from one
// indexed list of the class (getRVsByRSC), by counting how many volumes are already rolling out
// (active) and where it stands in the name-sorted queue of volumes that still need the new
// configuration (pending). Two properties make that decision safe under ten concurrent workers
// reading one informer cache:
//
//   - The order is total and stable (cluster-scoped names), so every worker computes the same
//     frontier from the same snapshot and admits the same prefix of it.
//   - A stale snapshot can only under-admit. A volume that has just been admitted is seen by a
//     lagging snapshot either as active (it consumes a slot) or as a pending volume that still
//     sorts ahead of the later names, so it keeps occupying the same place in the queue. A
//     volume leaves the cohort only once it has published a fresh Converged verdict for the
//     configuration of the current epoch, never earlier.
//
// The accepted cost is the mirror image: a volume that converged but whose verdict has not
// reached the cache yet keeps holding its slot, so the real parallelism dips below the limit
// for that window. The periodic requeue below is what closes it.

// configurationRolloutRequeueInterval is how often a volume waiting for a rollout slot re-checks
// the class-wide budget.
//
// Nothing wakes such a volume when a sibling converges: the controller deliberately watches no
// sibling volumes (that would mean a class-wide fan-out on every volume status write), so the
// periodic re-check is the progress mechanism.
const configurationRolloutRequeueInterval = 5 * time.Second

// defaultConfigurationRolloutMaxParallel is the budget assumed for a storage class that does not
// state one. It mirrors the default the class controller writes into
// spec.configurationRolloutStrategy.rollingUpdate.maxParallel (rsc_controller applySpecDefaults),
// so a class whose defaults have not been persisted yet already behaves like a defaulted one.
const defaultConfigurationRolloutMaxParallel = 5

// rolloutRole is the part a volume plays in the configuration rollout of its storage class.
type rolloutRole int

const (
	// rolloutRoleExcluded means the volume is outside the rollout: it neither consumes a slot
	// nor waits for one.
	rolloutRoleExcluded rolloutRole = iota
	// rolloutRoleActive means the volume already stores the intended configuration but has not
	// been observed to finish with it: either it has not reported the layout that configuration
	// asks for as converged, or the content it stores was adopted in an earlier configuration
	// epoch. It consumes a slot.
	rolloutRoleActive
	// rolloutRoleConverged means the volume adopted the intended configuration in the current
	// epoch and reports MembershipLayoutConverged for it. The rollout is over for this volume,
	// its slot is free.
	rolloutRoleConverged
	// rolloutRolePending means the volume still stores an older configuration and needs a slot
	// to adopt the intended one.
	rolloutRolePending
)

// rolloutCohort is the class-wide observation the rollout budget is decided against.
type rolloutCohort struct {
	// activeCount is the number of volumes of the class that are mid-rollout.
	activeCount int
	// pendingNames holds the names of the volumes that still need the intended configuration,
	// sorted ascending. The order picks the winners: the leading names take the free slots, all
	// of them in the same pass.
	pendingNames []string
}

// computeIntendedRolloutMaxParallel returns how many volumes of the class may be mid-rollout at
// the same time.
//
// A strategy the class controller has not defaulted yet (nil, or RollingUpdate without
// parameters) reads as the default it is about to write. A non-positive value is impossible
// through the API (the schema enforces a minimum of 1) and is treated the same way, so a hand
// crafted object cannot stall the rollout of a whole class.
func computeIntendedRolloutMaxParallel(rsc *v1alpha1.ReplicatedStorageClass) int {
	strategy := rsc.Spec.ConfigurationRolloutStrategy
	if strategy == nil || strategy.RollingUpdate == nil || strategy.RollingUpdate.MaxParallel <= 0 {
		return defaultConfigurationRolloutMaxParallel
	}
	return int(strategy.RollingUpdate.MaxParallel)
}

// computeActualRolloutCohort classifies the volumes of the storage class rv belongs to
// (rv.Spec.ReplicatedStorageClassName) against the configuration that class intends and the
// generation it published that configuration under.
//
// classRVs is the (possibly stale) listing of that class; rv is the volume being reconciled and
// always wins over its own listed copy, even when the listing does not contain it yet. Both are
// read-only: the listing may hold informer cache objects.
func computeActualRolloutCohort(
	rv *v1alpha1.ReplicatedVolume,
	classRVs []*v1alpha1.ReplicatedVolume,
	intended *v1alpha1.ReplicatedVolumeConfiguration,
	intendedGeneration int64,
) rolloutCohort {
	members := make([]*v1alpha1.ReplicatedVolume, 0, len(classRVs)+1)
	selfIncluded := false
	for _, listed := range classRVs {
		if listed == nil {
			continue
		}
		if listed.Name != rv.Name {
			members = append(members, listed)
			continue
		}
		if !selfIncluded {
			members = append(members, rv)
			selfIncluded = true
		}
	}
	if !selfIncluded {
		members = append(members, rv)
	}

	var cohort rolloutCohort
	for _, member := range members {
		switch computeActualRolloutRole(member, rv.Spec.ReplicatedStorageClassName, intended, intendedGeneration) {
		case rolloutRoleActive:
			cohort.activeCount++
		case rolloutRolePending:
			cohort.pendingNames = append(cohort.pendingNames, member.Name)
		case rolloutRoleExcluded, rolloutRoleConverged:
		}
	}
	slices.Sort(cohort.pendingNames)

	return cohort
}

// computeActualRolloutRole classifies one volume of the class named by rscName against the
// configuration that class intends and the generation it published that configuration under.
func computeActualRolloutRole(
	rv *v1alpha1.ReplicatedVolume,
	rscName string,
	intended *v1alpha1.ReplicatedVolumeConfiguration,
	intendedGeneration int64,
) rolloutRole {
	// A volume on its way out is not worth a slot: its layout convergence is suspended anyway
	// (MembershipLayoutConverged reports VolumeDeleting).
	if rv.DeletionTimestamp != nil {
		return rolloutRoleExcluded
	}
	// Manual volumes and volumes of another class are not driven by this class at all. The index
	// already answers the second one; it is re-checked because rv is inserted unconditionally.
	if rv.Spec.ConfigurationMode == v1alpha1.ReplicatedVolumeConfigurationModeManual ||
		rv.Spec.ReplicatedStorageClassName != rscName {
		return rolloutRoleExcluded
	}
	// A volume that never received a configuration is not part of the rollout — it is a new
	// volume and gets the current configuration straight away, throttled or not.
	if rv.Status.Configuration == nil {
		return rolloutRoleExcluded
	}
	// A forming volume builds its layout from scratch instead of migrating one, and the
	// configuration it forms against is frozen for the duration.
	if forming, _ := isFormationInProgress(rv); forming {
		return rolloutRoleExcluded
	}

	if *rv.Status.Configuration != *intended {
		return rolloutRolePending
	}

	// Equal content is not proof that the volume belongs to the rollout going on now. A class
	// edited A → B → A leaves volumes that converged on the first A byte-identical to volumes
	// that have just adopted the second one, and a lagging listing serves exactly that: an
	// object from the first epoch, with the Converged verdict it earned back then.
	//
	// The generation the content came from separates the two epochs. It is stamped by the very
	// write that admits a volume (status.configurationGeneration is set next to
	// status.configuration below in reconcileRVConfiguration), so it is never late: any volume
	// admitted in this epoch already carries it, and no verdict can be mistaken for one earned
	// in this epoch.
	//
	// A volume of an older epoch is counted as active rather than dropped from the cohort. It
	// needs no slot of its own — its next pass takes the content-equal fast path, restamps the
	// generation and, from then on, reports through the branch below — but until then it must
	// stay in the accounting: a stale reader cannot tell it apart from a volume that really is
	// mid-rollout, and dropping either one is what breaks the budget.
	if rv.Status.ConfigurationGeneration != intendedGeneration {
		return rolloutRoleActive
	}

	// The volume stores the intended configuration, so it is part of this rollout until its
	// datamesh actually reaches the layout that configuration asks for — and that verdict
	// belongs to MembershipLayoutConverged, not to a count of members. Member counts equal to
	// the intended ones are not convergence: a retype already flipped on a replica spec, or a
	// tie-breaker replacement that cannot be placed, both leave the counts matching while the
	// volume is demonstrably still moving (Converging) or stuck (CannotConverge). Those signals
	// are derived from the replicas of that volume, which this classifier does not read: one
	// indexed listing of the class is its entire I/O budget.
	//
	// Reading a peer's condition is sound because the condition and status.configuration are
	// written in the same object version: every normal-operation pass runs
	// reconcileRVConfiguration (the only writer of status.configuration) and
	// reconcileLayoutStatus (the only writer of the condition) together, and the condition is
	// recomputed unconditionally on each of them. So a peer that carries the intended
	// configuration carries a verdict computed against that same configuration.
	return computeActualRolloutConvergence(rv)
}

// computeActualRolloutConvergence reports whether rv has published a convergence verdict that
// frees its rollout slot.
//
// Only a fresh Converged frees the slot; every other state — a False verdict, a verdict computed
// for an older spec generation, or no verdict at all (the volume has only just left formation,
// which does not run reconcileLayoutStatus) — keeps the volume active. That is the safe
// direction: an extra pass of holding a slot merely under-admits and resolves itself on the next
// reconcile, which always republishes the condition, whereas freeing a slot too early lets the
// class exceed maxParallel.
func computeActualRolloutConvergence(rv *v1alpha1.ReplicatedVolume) rolloutRole {
	converged := obju.StatusCondition(rv, v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType).
		IsTrue().
		ReasonEqual(v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged).
		ObservedGenerationCurrent().
		Eval()
	if converged {
		return rolloutRoleConverged
	}
	return rolloutRoleActive
}

// computeTargetRolloutAdmission decides whether rv may store the intended configuration in this
// pass: the free slots (maxParallel minus the volumes already mid-rollout) go to the first
// pending names.
//
// A volume outside the cohort is admitted unconditionally — the budget has nothing to say about
// volumes it does not account for.
func computeTargetRolloutAdmission(
	rv *v1alpha1.ReplicatedVolume,
	cohort rolloutCohort,
	maxParallel int,
) bool {
	position := slices.Index(cohort.pendingNames, rv.Name)
	if position < 0 {
		return true
	}
	// The budget goes negative when maxParallel is lowered below the number of volumes already
	// mid-rollout. Those keep going (stopping them would leave layouts half-migrated), but no
	// pending volume joins them until the count drops back under the new limit.
	return position < maxParallel-cohort.activeCount
}
