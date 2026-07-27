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
	"sort"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/match"
	tkmatch "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

// newMigrationRSC creates a dedicated (non-shared, per-spec) ReplicatedStorageClass
// with an explicit spec.replication and Ignored topology, waits until it is Ready,
// and returns the handle. A dedicated RSC is required because the layout-migration
// scenarios mutate spec.replication — the shared RSC cache must not be touched.
func newMigrationRSC(ctx SpecContext, replication v1alpha1.ReplicatedStorageClassReplication) *fw.TestRSC {
	GinkgoHelper()
	trsc := f.TestRSC().
		StorageType(v1alpha1.ReplicatedStoragePoolTypeLVMThin).
		StorageLVMVolumeGroups(f.Discovery.LVMVolumeGroups()...).
		ReclaimPolicy(v1alpha1.RSCReclaimPolicyDelete).
		Topology(v1alpha1.TopologyIgnored).
		Replication(replication)
	trsc.Create(ctx)
	trsc.Await(ctx, tkmatch.ConditionStatus(
		v1alpha1.ReplicatedStorageClassCondReadyType, "True"))
	return trsc
}

// layoutOf returns the RV's reported actual layout string (e.g. "3D", "2D+1TB").
// status.layout is optional: an unset layout (formation not finished, nothing reported yet)
// is returned as nil, never as an empty string — assert with Equal(ptr.To("...")) so an
// unreported layout can never satisfy an expectation.
func layoutOf(trv *fw.TestRV) *string {
	return trv.Object().Status.Layout
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
// LayoutConverged=True/Converged, status.layout=="2D+1TB", and exactly one
// tie-breaker member. Evaluated atomically on a single snapshot so it never
// matches a stale pre-migration snapshot (3D/Converged) nor a mid-migration one.
func migratedToR2() types.GomegaMatcher {
	return match.RV.Custom("migrated to 2D+1TB", func(rv *v1alpha1.ReplicatedVolume) bool {
		if rv.Status.Layout == nil || *rv.Status.Layout != "2D+1TB" {
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
			if c.Type == v1alpha1.ReplicatedVolumeCondLayoutConvergedType {
				return c.Status == metav1.ConditionTrue &&
					c.Reason == v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverged
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

// rvrOnNode returns the tracked RVR scheduled on the given node, failing the test
// if none is found.
func rvrOnNode(trv *fw.TestRV, nodeName string) *fw.TestRVR {
	GinkgoHelper()
	for _, r := range trv.TestRVRs() {
		obj := r.Object()
		if obj != nil && obj.Spec.NodeName == nodeName {
			return r
		}
	}
	Fail("no tracked RVR on node " + nodeName)
	return nil
}
