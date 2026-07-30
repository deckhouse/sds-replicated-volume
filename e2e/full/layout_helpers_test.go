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

package full

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/match"
	tkmatch "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

// The observables of the layout alerting pipeline, named once so a spec and the
// shipped artefacts cannot drift apart:
//   - the metric is emitted by images/controller/internal/metrics/current_metrics_collector.go,
//   - the alert is defined in monitoring/prometheus-rules/replicated-volume-layout.yaml
//     and selects exactly that metric.
const (
	layoutConvergedMetricName = "sds_rv_membership_layout_converged"
	layoutDegradedAlertName   = "D8ReplicatedVolumeLayoutDegraded"
)

// newMigrationRSC creates a dedicated (non-shared, per-spec) ReplicatedStorageClass
// with an explicit spec.replication and Ignored topology, waits until it is Ready,
// and returns the handle. A dedicated RSC is required because the layout-migration
// scenarios mutate spec.replication — the shared RSC cache must not be touched.
//
// tune adjusts the builder before creation, for the scenarios that need a
// different volume access mode or topology.
func newMigrationRSC(
	ctx SpecContext,
	replication v1alpha1.ReplicatedStorageClassReplication,
	tune ...func(*fw.TestRSC),
) *fw.TestRSC {
	GinkgoHelper()
	trsc := f.TestRSC().
		StorageType(v1alpha1.ReplicatedStoragePoolTypeLVMThin).
		StorageLVMVolumeGroups(f.Discovery.LVMVolumeGroups()...).
		ReclaimPolicy(v1alpha1.RSCReclaimPolicyDelete).
		Topology(v1alpha1.TopologyIgnored).
		Replication(replication)
	for _, t := range tune {
		t(trsc)
	}
	trsc.Create(ctx)
	trsc.Await(ctx, tkmatch.ConditionStatus(
		v1alpha1.ReplicatedStorageClassCondReadyType, "True"))
	return trsc
}

// placementLVGs turns discovery placements into the storage list of an RSC.
func placementLVGs(placements []fw.DiskfulPlacement) []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups {
	lvgs := make([]v1alpha1.ReplicatedStoragePoolLVMVolumeGroups, 0, len(placements))
	for _, p := range placements {
		lvgs = append(lvgs, v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
			Name:         p.LVGName,
			ThinPoolName: p.ThinPoolName,
		})
	}
	return lvgs
}

// placementNodes returns the node names of the placements, in their order.
func placementNodes(placements []fw.DiskfulPlacement) []string {
	nodes := make([]string, 0, len(placements))
	for _, p := range placements {
		nodes = append(nodes, p.NodeName)
	}
	return nodes
}

// rscPool returns a tracked handle to the storage pool the RSC computed for
// itself. The pool is where eligible nodes are published, so a spec that claims
// "exactly these nodes may host a replica" asserts on it.
func rscPool(ctx SpecContext, trsc *fw.TestRSC) *fw.TestRSP {
	GinkgoHelper()
	trsc.Await(ctx, match.RSC.Custom("storage pool computed",
		func(rsc *v1alpha1.ReplicatedStorageClass) bool {
			return rsc.Status.StoragePoolName != ""
		}))
	trsp := f.TestRSPExact(trsc.Object().Status.StoragePoolName)
	trsp.Get(ctx)
	trsp.Await(ctx, tkmatch.Present())
	return trsp
}

// usableEligibleNodes returns the sorted names of the pool's eligible nodes
// that can actually take a replica right now.
func usableEligibleNodes(trsp *fw.TestRSP) []string {
	var nodes []string
	for _, n := range trsp.Object().Status.EligibleNodes {
		if n.NodeReady && n.AgentReady && !n.Unschedulable {
			nodes = append(nodes, n.NodeName)
		}
	}
	sort.Strings(nodes)
	return nodes
}

// membershipLayoutOf returns the RV's reported actual layout string (e.g. "3D", "2D+1TB").
// status.membershipLayout is optional: an unset layout (formation not finished, nothing reported yet)
// is returned as nil, never as an empty string — assert with Equal(ptr.To("...")) so an
// unreported layout can never satisfy an expectation.
func membershipLayoutOf(trv *fw.TestRV) *string {
	return trv.Object().Status.MembershipLayout
}

// memberTypeCount counts current datamesh members of the given type.
func memberTypeCount(trv *fw.TestRV, t v1alpha1.DatameshMemberType) int {
	n := 0
	for _, m := range trv.Object().Status.Datamesh.Members {
		if m.Type == t {
			n++
		}
	}
	return n
}

// memberNodesOfType returns the node names of current datamesh members of the given type.
func memberNodesOfType(trv *fw.TestRV, t v1alpha1.DatameshMemberType) []string {
	var nodes []string
	for _, m := range trv.Object().Status.Datamesh.Members {
		if m.Type == t {
			nodes = append(nodes, m.NodeName)
		}
	}
	return nodes
}

// memberOnNodeIsDiskful matches an RV whose datamesh member on nodeName is a
// plain Diskful member. Registered as a continuous invariant it fails the moment
// that node is demoted — including the LiminalDiskful step of a demotion, where
// DRBD is already diskless and a volumeAccess=Local workload would lose its data
// path. A node that is not a member at all does not match either.
func memberOnNodeIsDiskful(nodeName string) types.GomegaMatcher {
	return match.RV.Custom("diskful member on "+nodeName, func(rv *v1alpha1.ReplicatedVolume) bool {
		for i := range rv.Status.Datamesh.Members {
			if rv.Status.Datamesh.Members[i].NodeName == nodeName {
				return rv.Status.Datamesh.Members[i].Type == v1alpha1.DatameshMemberTypeDiskful
			}
		}
		return false
	})
}

// memberZones maps node name to the zone the datamesh reports for the member on
// that node, restricted to members of the given type.
func memberZones(trv *fw.TestRV, t v1alpha1.DatameshMemberType) map[string]string {
	zones := map[string]string{}
	for _, m := range trv.Object().Status.Datamesh.Members {
		if m.Type == t {
			zones[m.NodeName] = m.Zone
		}
	}
	return zones
}

// rvrNames returns the sorted names of the RVRs currently tracked and present
// (not deleted) for trv. It proves that the r3->r2 retype keeps the SAME replicas
// (their spec.type flips in place) and never creates a new RVR — i.e. there is no
// add-replica / full resync. Unlike reading Status.DatameshTransitions, this does
// not depend on transition history, which the datamesh engine deletes as soon as a
// transition completes.
func rvrNames(trv *fw.TestRV) []string {
	var names []string
	for _, r := range trv.TestRVRs() {
		if r.IsPresent() {
			names = append(names, r.Name())
		}
	}
	sort.Strings(names)
	return names
}

// migratedToR2 matches an RV that has fully converged to the 2D+1TB layout:
// MembershipLayoutConverged=True/Converged, status.membershipLayout=="2D+1TB", and exactly one
// tie-breaker member. Evaluated atomically on a single snapshot so it never
// matches a stale pre-migration snapshot (3D/Converged) nor a mid-migration one.
func migratedToR2() types.GomegaMatcher {
	return match.RV.Custom("migrated to 2D+1TB", func(rv *v1alpha1.ReplicatedVolume) bool {
		if rv.Status.MembershipLayout == nil || *rv.Status.MembershipLayout != "2D+1TB" {
			return false
		}
		tb := 0
		for i := range rv.Status.Datamesh.Members {
			if rv.Status.Datamesh.Members[i].Type == v1alpha1.DatameshMemberTypeTieBreaker {
				tb++
			}
		}
		if tb != 1 {
			return false
		}
		for i := range rv.Status.Conditions {
			c := &rv.Status.Conditions[i]
			if c.Type == v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType {
				return c.Status == metav1.ConditionTrue &&
					c.Reason == v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged
			}
		}
		return false
	})
}

// noActiveAddReplica matches when the RV has NO active AddReplica transition.
// Registered as a continuous invariant, it fails the test if a resync-bearing
// AddReplica ever appears during a retype migration or after formation.
func noActiveAddReplica() types.GomegaMatcher {
	return Not(match.RV.HasActiveTransition(
		string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica)))
}

// healedTieBreakerOtherThan matches an RV that reports MembershipLayoutConverged=True/Converged
// while its single tie-breaker member is NOT the named one. The name is what makes the
// match provably fresh: the pre-deletion snapshot carries the replaced tie-breaker and
// can never satisfy it. That is why the spec does not wait for the transient Converging
// report — convergence can publish it before an Await on the RV subscribes (the P2 heal
// starts as soon as the replica is marked terminating), and a spec that demands the
// transient hangs until its own timeout even though the heal succeeded.
func healedTieBreakerOtherThan(replacedName string) types.GomegaMatcher {
	return match.RV.Custom("converged with a tie-breaker other than "+replacedName,
		func(rv *v1alpha1.ReplicatedVolume) bool {
			cond := meta.FindStatusCondition(rv.Status.Conditions,
				v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType)
			if cond == nil ||
				cond.Status != metav1.ConditionTrue ||
				cond.Reason != v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged {
				return false
			}

			tieBreakers := 0
			for _, m := range rv.Status.Datamesh.Members {
				if m.Type != v1alpha1.DatameshMemberTypeTieBreaker {
					continue
				}
				if m.Name == replacedName {
					return false
				}
				tieBreakers++
			}
			return tieBreakers == 1
		})
}

// noActiveChangeReplicaType matches when the RV has NO active ChangeReplicaType
// transition. Used during formation to prove the tie-breaker is part of the
// formation membership, not a post-formation retype.
func noActiveChangeReplicaType() types.GomegaMatcher {
	return Not(match.RV.HasActiveTransition(
		string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType)))
}

// noBackingVolume matches an RVR whose backing volume has been released
// (status.backingVolume == nil) — the observable of a deleted LLV.
func noBackingVolume() types.GomegaMatcher {
	return match.RVR.Custom("no backing volume", func(r *v1alpha1.ReplicatedVolumeReplica) bool {
		return r.Status.BackingVolume == nil
	})
}

// tieBreakerRVR returns the single present (non-deleted) TieBreaker RVR of trv,
// failing the test if there is not exactly one. Deleted snapshots are skipped so
// a freshly healed tie-breaker is not confused with the one it replaced.
func tieBreakerRVR(trv *fw.TestRV) *fw.TestRVR {
	GinkgoHelper()
	var found *fw.TestRVR
	count := 0
	for _, r := range trv.TestRVRs() {
		if !r.IsPresent() {
			continue
		}
		if r.Object().Spec.Type == v1alpha1.ReplicaTypeTieBreaker {
			found = r
			count++
		}
	}
	Expect(count).To(Equal(1), "expected exactly one present tie-breaker RVR")
	return found
}

// drbdResourceOn returns the kernel-side DRBD resource name of trv's replica
// living on nodeName.
func drbdResourceOn(trv *fw.TestRV, nodeName string) string {
	GinkgoHelper()
	return rvrOnNode(trv, nodeName).DRBDResourceName()
}

// drbdPeerNameOn returns the DRBD connection name under which trv's replica on
// nodeName shows up in its peers' configuration.
func drbdPeerNameOn(trv *fw.TestRV, nodeName string) string {
	GinkgoHelper()
	return fw.DRBDPeerName(rvrOnNode(trv, nodeName).ID())
}

// expectDRBDQuorum asserts on every diskful node that the kernel has quorum
// right now and enforces exactly the threshold the datamesh published.
//
// rv.status.datamesh.quorum is what the control plane WANTS; drbdsetup is what
// the data path actually obeys. A tie-breaker that is only a member on paper
// would leave the two apart.
func expectDRBDQuorum(ctx SpecContext, trv *fw.TestRV) {
	GinkgoHelper()
	want := strconv.Itoa(int(trv.Object().Status.Datamesh.Quorum))
	for _, node := range memberNodesOfType(trv, v1alpha1.DatameshMemberTypeDiskful) {
		res := drbdResourceOn(trv, node)
		Expect(f.DRBDStatus(ctx, node, res).Quorum).To(BeTrue(),
			"node %s has no DRBD quorum for %s", node, res)
		Expect(f.DRBDConfig(ctx, node, res).Quorum).To(Equal(want),
			"node %s enforces a quorum threshold other than the published %s", node, want)
	}
}

// memberNames returns the sorted names of the RV's current datamesh members.
func memberNames(trv *fw.TestRV) []string {
	var names []string
	for _, m := range trv.Object().Status.Datamesh.Members {
		names = append(names, m.Name)
	}
	sort.Strings(names)
	return names
}

// membersAre matches an RV whose datamesh membership is exactly this set of
// member names. Registered as a continuous invariant it fails the moment
// convergence adds or drops a member on its own — which is the whole point in
// windows where the spec claims that nothing but its own edit moves the
// composition.
func membersAre(want []string) types.GomegaMatcher {
	sorted := slices.Clone(want)
	sort.Strings(sorted)
	return match.RV.Custom("datamesh members are ["+strings.Join(sorted, " ")+"]",
		func(rv *v1alpha1.ReplicatedVolume) bool {
			var got []string
			for i := range rv.Status.Datamesh.Members {
				got = append(got, rv.Status.Datamesh.Members[i].Name)
			}
			sort.Strings(got)
			return slices.Equal(got, sorted)
		})
}

// noActiveRemoveReplica matches when the RV has NO active RemoveReplica
// transition. Registered as a continuous invariant it proves that convergence
// never starts removing a replica by itself.
func noActiveRemoveReplica() types.GomegaMatcher {
	return Not(match.RV.HasActiveTransition(
		string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica)))
}

// layoutDegraded matches an RV reporting MembershipLayoutConverged=False/TransitionUnsupported
// with exactly this arithmetic ("have 1D+1TB, want 2D+1TB").
//
// The arithmetic is not decoration: simulateDiskfulLoss passes through a window
// where the very same status/reason pair is reported for an EXCESS (the volume
// is temporarily configured for fewer replicas than it has), so a matcher
// looking only at the reason would be satisfied before anything was removed.
// Everything is read off ONE snapshot, so the reason and the arithmetic can
// never come from two different observations.
func layoutDegraded(have, want string) types.GomegaMatcher {
	arithmetic := fmt.Sprintf("have %s, want %s", have, want)
	return match.RV.Custom("MembershipLayoutConverged=False/TransitionUnsupported with "+arithmetic,
		func(rv *v1alpha1.ReplicatedVolume) bool {
			cond := meta.FindStatusCondition(rv.Status.Conditions,
				v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType)
			return cond != nil &&
				cond.Status == metav1.ConditionFalse &&
				cond.Reason == v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonTransitionUnsupported &&
				strings.Contains(cond.Message, arithmetic)
		})
}

// resolvedFTTGMDR matches an RV whose RESOLVED configuration (status, not spec)
// carries these FTT/GMDR values. The datamesh guards read the resolved
// configuration, so this is what says "the downgrade has taken effect and a
// voter may now leave".
func resolvedFTTGMDR(ftt, gmdr byte) types.GomegaMatcher {
	return match.RV.Custom(fmt.Sprintf("resolved configuration is FTT=%d/GMDR=%d", ftt, gmdr),
		func(rv *v1alpha1.ReplicatedVolume) bool {
			return rv.Status.Configuration.FailuresToTolerate == ftt &&
				rv.Status.Configuration.GuaranteedMinimumDataRedundancy == gmdr
		})
}

// addReplicaPlanIDIn matches an RV running an AddReplica transition whose plan
// is one of planIDs.
//
// The plan id is what distinguishes the two join paths the recovery specs care
// about: `diskful-q-up/v1` (odd→even voters) routes the joining replica through
// the Access vestibule and raises the quorum, `diskful/v1` (even→odd) does
// neither. It is set when the transition is created and survives all of its
// steps, so it is observable for the whole join rather than for one step.
func addReplicaPlanIDIn(planIDs ...string) types.GomegaMatcher {
	return match.RV.Custom("AddReplica runs one of the plans "+strings.Join(planIDs, ", "),
		func(rv *v1alpha1.ReplicatedVolume) bool {
			for i := range rv.Status.DatameshTransitions {
				t := &rv.Status.DatameshTransitions[i]
				if t.Type != v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica {
					continue
				}
				if slices.Contains(planIDs, t.PlanID) {
					return true
				}
			}
			return false
		})
}

// simulateDiskfulLoss removes one diskful replica of trv the way an operator
// could, and NEVER by stripping a finalizer.
//
// A volume standing exactly at its redundancy boundary (r2 = 2D+1TB with 2
// voters, r3 = 3D with 3) refuses to let a voter go: guardFTTPreserved requires
// more voters than dMin = FTT+GMDR+1. So the volume is temporarily switched to a
// Manual configuration with FTT=0/GMDR=0 (topology Ignored — the API rejects
// FTT=0+GMDR=0 under any other topology), the victim replica is deleted through
// the ordinary API path, and the original Auto configuration is restored. The
// member leaves through the graceful RemoveReplica plan and the controller
// releases its own finalizer; awaiting Deleted() is what proves it did.
//
// The same trick is already used by rv_resize_test.go ("resize completes after
// replica deletion") for the same reason.
//
// Contract:
//   - trv MUST be in Auto mode with a storage class — that configuration is what
//     gets restored.
//   - victimNode MUST NOT hold an attachment: the datamesh refuses to demote an
//     attached voter.
//   - On return the volume carries its ORIGINAL configuration and one diskful
//     member less. The caller asserts the resulting mismatch by its arithmetic
//     (layoutDegraded), never by the reason alone — inside the downgrade window
//     the very same reason is reported for the excess.
//
// While the configuration is downgraded, an Always invariant pins the member
// composition: the convergence whitelist holds only the P1 retype and the P2
// tie-breaker heal, so nothing is supposed to be removed automatically. If that
// ever changes, the spec has to notice instead of crediting its own Delete with
// someone else's removal.
func simulateDiskfulLoss(ctx SpecContext, trv *fw.TestRV, victimNode string) {
	GinkgoHelper()

	rv := trv.Object()
	Expect(rv.Spec.ConfigurationMode).To(Equal(v1alpha1.ReplicatedVolumeConfigurationModeAuto),
		"simulateDiskfulLoss restores an Auto configuration and must not be used on a Manual volume")
	rscName := rv.Spec.ReplicatedStorageClassName
	Expect(rscName).NotTo(BeEmpty(), "volume %s has no storage class to restore", trv.Name())
	poolName := rv.Status.Configuration.ReplicatedStoragePoolName
	Expect(poolName).NotTo(BeEmpty(), "volume %s has no resolved storage pool yet", trv.Name())
	volumeAccess := rv.Status.Configuration.VolumeAccess

	victim := rvrOnNode(trv, victimNode)
	Expect(trv.RVANodes()).NotTo(HaveKey(victimNode),
		"the victim replica on %s is attached; the datamesh refuses to demote an attached voter", victimNode)

	By("pinning the member composition for the downgrade window")
	frozen := tkmatch.NewSwitch(membersAre(memberNames(trv)))
	trv.Always(frozen)

	By("downgrading the configuration to Manual FTT=0/GMDR=0 so the guards let a voter leave")
	trv.Update(ctx, func(rv *v1alpha1.ReplicatedVolume) {
		rv.Spec.ConfigurationMode = v1alpha1.ReplicatedVolumeConfigurationModeManual
		rv.Spec.ReplicatedStorageClassName = ""
		rv.Spec.ManualConfiguration = &v1alpha1.ReplicatedVolumeConfiguration{
			ReplicatedStoragePoolName: poolName,
			Topology:                  v1alpha1.TopologyIgnored,
			VolumeAccess:              volumeAccess,
		}
	})
	// Wait for the RESOLVED configuration, not just for the accepted spec: the
	// guards read the resolved one, and this is also what gives the invariant
	// above a window with real reconcile passes in it.
	trv.Await(ctx, resolvedFTTGMDR(0, 0))

	By("deleting the diskful replica on " + victimNode + " through the ordinary API path")
	victim.Delete(ctx)
	// From here on the composition is meant to change — by our own request.
	frozen.Disable()
	victim.Await(ctx, tkmatch.Deleted())

	By("restoring the original Auto configuration")
	trv.Update(ctx, func(rv *v1alpha1.ReplicatedVolume) {
		rv.Spec.ConfigurationMode = v1alpha1.ReplicatedVolumeConfigurationModeAuto
		rv.Spec.ManualConfiguration = nil
		rv.Spec.ReplicatedStorageClassName = rscName
	})
}

// awaitLayoutMetric waits until every controller pod exports the layout metric
// of this volume with the given reason and value — the very series the
// D8ReplicatedVolumeLayoutDegraded rule selects.
func awaitLayoutMetric(ctx SpecContext, trv *fw.TestRV, reason string, value float64) {
	GinkgoHelper()
	f.AwaitControllerMetric(ctx, layoutConvergedMetricName,
		map[string]string{"name": trv.Name(), "reason": reason}, value)
}

// rvrOnNode returns the tracked RVR scheduled on the given node, failing the test
// if none is found. Replicas that are already gone are skipped: their handle
// still exists (the group keeps the history), but reading their object would
// fail the spec.
func rvrOnNode(trv *fw.TestRV, nodeName string) *fw.TestRVR {
	GinkgoHelper()
	for _, r := range trv.TestRVRs() {
		if !r.IsPresent() {
			continue
		}
		if r.Object().Spec.NodeName == nodeName {
			return r
		}
	}
	Fail("no tracked RVR on node " + nodeName)
	return nil
}
