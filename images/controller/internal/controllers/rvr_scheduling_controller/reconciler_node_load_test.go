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

package rvrschedulingcontroller

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
	schext "github.com/deckhouse/sds-replicated-volume/images/controller/internal/scheduler_extender"
)

// maxImbalance is how far apart the busiest and the quietest node may end up.
//
// Placement here is deterministic, so this is not a flake margin: it is the slack
// the greedy choice is allowed. Zero would demand perfect division, which only
// holds when the replica count happens to divide by the node count; one would
// hold for the cases below but would break on any future scoring step that
// legitimately prefers one node over another.
const maxImbalance = 2

// ──────────────────────────────────────────────────────────────────────────────
// Test helpers: multi-volume fixtures
//
// The helpers in reconciler_test.go all build the single volume testRVName. Load
// balancing is only observable across volumes, so these build named ones.

func newRVNamed(rvName string, cfg *v1alpha1.ReplicatedVolumeConfiguration) *v1alpha1.ReplicatedVolume {
	return &v1alpha1.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{Name: rvName},
		Spec: v1alpha1.ReplicatedVolumeSpec{
			Size:                       resource.MustParse("10Gi"),
			ReplicatedStorageClassName: "rsc-test",
		},
		Status: v1alpha1.ReplicatedVolumeStatus{
			Configuration: cfg,
		},
	}
}

func newRVRFor(rvName string, id uint8, replicaType v1alpha1.ReplicaType) *v1alpha1.ReplicatedVolumeReplica {
	return &v1alpha1.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{Name: v1alpha1.FormatReplicatedVolumeReplicaName(rvName, id)},
		Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: rvName,
			Type:                 replicaType,
		},
	}
}

// newPlacedRVR builds a replica that is already sitting on a node. It stands for
// the load another volume put there, which is exactly what NodeLoad counts
// and what nothing in the scheduling context knows about.
func newPlacedRVR(rvName string, id uint8, replicaType v1alpha1.ReplicaType, nodeName, lvgName string) *v1alpha1.ReplicatedVolumeReplica {
	rvr := newRVRFor(rvName, id, replicaType)
	rvr.Spec.NodeName = nodeName
	rvr.Spec.LVMVolumeGroupName = lvgName
	return rvr
}

func reconcileRVNamed(ctx context.Context, rec *Reconciler, rvName string) (reconcile.Result, error) {
	return rec.Reconcile(ctx, reconcile.Request{
		NamespacedName: client.ObjectKey{Name: rvName},
	})
}

// nodesWithOneLVG builds count eligible nodes named node-00..node-NN, each carrying
// a single ready LVG. Equal nodes on purpose: with nothing to tell them apart the
// extender scores them the same, which is the tie NodeLoad breaks.
func nodesWithOneLVG(count int) []v1alpha1.ReplicatedStoragePoolEligibleNode {
	nodes := make([]v1alpha1.ReplicatedStoragePoolEligibleNode, 0, count)
	for i := range count {
		name := nodeName(i)
		nodes = append(nodes, makeNode(name, "", makeLVG("vg-"+name, true)))
	}
	return nodes
}

func nodeName(i int) string { return fmt.Sprintf("node-%02d", i) }

// volumeLayout is the set of replicas one volume asks to have placed.
type volumeLayout []v1alpha1.ReplicaType

var (
	// layoutAvailability is what replication: Availability produces — two Diskful
	// voters plus the tie-breaker that keeps quorum on an even count.
	layoutAvailability = volumeLayout{
		v1alpha1.ReplicaTypeDiskful,
		v1alpha1.ReplicaTypeDiskful,
		v1alpha1.ReplicaTypeTieBreaker,
	}
	// layoutConsistency is what replication: Consistency produces — two Diskful and
	// no tie-breaker. Mixed in so the number of replicas per volume is not constant.
	layoutConsistency = volumeLayout{
		v1alpha1.ReplicaTypeDiskful,
		v1alpha1.ReplicaTypeDiskful,
	}
)

// addVolume appends an RV and its replicas to objs, and the replicas to statusObjs.
func addVolume(
	objs *[]client.Object,
	statusObjs *[]client.Object,
	rvName string,
	layout volumeLayout,
) {
	*objs = append(*objs, newRVNamed(rvName, defaultConfig()))
	for i, replicaType := range layout {
		rvr := newRVRFor(rvName, uint8(i), replicaType)
		*objs = append(*objs, rvr)
		*statusObjs = append(*statusObjs, rvr)
	}
}

// replicasPerNode counts placed replicas per node, keeping a zero entry for every
// node that is eligible but empty — a node nothing ever lands on is the failure
// this whole change is about, and it has to be visible in the counts.
func replicasPerNode(ctx context.Context, cl client.Client, nodeCount int) map[string]int {
	return countReplicasPerNode(ctx, cl, nodeCount, func(*v1alpha1.ReplicatedVolumeReplica) bool { return true })
}

// replicasPerNodeOfType counts one replica type only. The tie-breakers have to be looked
// at apart from the total: the skew this whole change is about was a diskless one, and a
// total that stays level can still hide it if the Diskful placements move the other way.
func replicasPerNodeOfType(
	ctx context.Context,
	cl client.Client,
	nodeCount int,
	replicaType v1alpha1.ReplicaType,
) map[string]int {
	return countReplicasPerNode(ctx, cl, nodeCount, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return rvr.Spec.Type == replicaType
	})
}

func countReplicasPerNode(
	ctx context.Context,
	cl client.Client,
	nodeCount int,
	include func(*v1alpha1.ReplicatedVolumeReplica) bool,
) map[string]int {
	list := &v1alpha1.ReplicatedVolumeReplicaList{}
	// Offset 2: the caller of interest is the spec, not the one-line wrapper above.
	ExpectWithOffset(2, cl.List(ctx, list)).To(Succeed())

	counts := make(map[string]int, nodeCount)
	for i := range nodeCount {
		counts[nodeName(i)] = 0
	}
	for i := range list.Items {
		if !include(&list.Items[i]) {
			continue
		}
		counts[list.Items[i].Spec.NodeName]++
	}
	return counts
}

// ──────────────────────────────────────────────────────────────────────────────
// Test helpers: watching the by-node reads
//

// listsByNode reports whether opts select replicas through the by-node index — the read
// reconcileNodeLoad makes, as opposed to every other List the reconciliation performs.
func listsByNode(opts []client.ListOption) bool {
	for _, o := range opts {
		if m, ok := o.(client.MatchingFields); ok {
			if _, byNodeName := m[indexes.IndexFieldRVRByNodeName]; byNodeName {
				return true
			}
		}
	}
	return false
}

// countingNodeLoadReads tallies the by-node reads into count and lets them through.
func countingNodeLoadReads(count *int) interceptor.Funcs {
	return interceptor.Funcs{
		List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if listsByNode(opts) {
				*count++
			}
			return cl.List(ctx, list, opts...)
		},
	}
}

// failingNodeLoadReads fails every by-node read with err, leaving the others alone.
func failingNodeLoadReads(err error) interceptor.Funcs {
	return interceptor.Funcs{
		List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if listsByNode(opts) {
				return err
			}
			return cl.List(ctx, list, opts...)
		},
	}
}

// imbalance reports the gap between the busiest and the quietest node, ignoring the
// bucket of replicas that were not placed at all.
func imbalance(counts map[string]int) int {
	lo, hi := 0, 0
	first := true
	for node, c := range counts {
		if node == "" {
			continue
		}
		if first {
			lo, hi, first = c, c, false
			continue
		}
		lo, hi = min(lo, c), max(hi, c)
	}
	return hi - lo
}

// formatCounts renders the distribution in node order, for failure messages and for
// the -v log: the shape of the spread says far more than the single gap number.
func formatCounts(counts map[string]int) string {
	nodes := make([]string, 0, len(counts))
	for node := range counts {
		if node != "" {
			nodes = append(nodes, node)
		}
	}
	slices.Sort(nodes)

	out := ""
	for _, node := range nodes {
		out += fmt.Sprintf("%s=%d ", node, counts[node])
	}
	if unplaced := counts[""]; unplaced > 0 {
		out += fmt.Sprintf("(unplaced=%d)", unplaced)
	}
	return out
}

// ──────────────────────────────────────────────────────────────────────────────
// Tests: reconcileNodeLoad
//

var _ = Describe("reconcileNodeLoad", func() {
	var (
		scheme *runtime.Scheme
		ctx    context.Context
	)

	BeforeEach(func() {
		scheme = newScheme()
		ctx = context.Background()
	})

	// countOn runs the counter over count nodes with the replicas given.
	countOn := func(nodeCount int, rvrs ...*v1alpha1.ReplicatedVolumeReplica) map[string]int {
		objs := make([]client.Object, 0, len(rvrs))
		for _, rvr := range rvrs {
			objs = append(objs, rvr)
		}
		cl := newClientBuilder(scheme).WithObjects(objs...).Build()
		rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

		load, outcome := rec.reconcileNodeLoad(ctx, nodesWithOneLVG(nodeCount))
		ExpectWithOffset(1, outcome.ShouldReturn()).To(BeFalse())
		return load
	}

	It("counts replicas per node", func() {
		Expect(countOn(2,
			newPlacedRVR("vol-a", 0, v1alpha1.ReplicaTypeDiskful, nodeName(0), "vg-0"),
			newPlacedRVR("vol-a", 1, v1alpha1.ReplicaTypeDiskful, nodeName(1), "vg-1"),
			newPlacedRVR("vol-b", 0, v1alpha1.ReplicaTypeDiskful, nodeName(0), "vg-0"),
		)).To(Equal(map[string]int{nodeName(0): 2, nodeName(1): 1}))
	})

	It("ignores unplaced replicas", func() {
		Expect(countOn(2,
			newPlacedRVR("vol-a", 0, v1alpha1.ReplicaTypeDiskful, nodeName(0), "vg-0"),
			newRVRFor("vol-a", 1, v1alpha1.ReplicaTypeDiskful),
		)).To(Equal(map[string]int{nodeName(0): 1}))
	})

	It("counts diskless replicas too", func() {
		// A TieBreaker and an Access replica hold no data, but each is a full DRBD
		// resource on its node — the load this map exists to spread.
		Expect(countOn(1,
			newPlacedRVR("vol-a", 0, v1alpha1.ReplicaTypeDiskful, nodeName(0), "vg-0"),
			newPlacedRVR("vol-a", 1, v1alpha1.ReplicaTypeTieBreaker, nodeName(0), ""),
			newPlacedRVR("vol-a", 2, v1alpha1.ReplicaTypeAccess, nodeName(0), ""),
		)).To(Equal(map[string]int{nodeName(0): 3}))
	})

	It("counts replicas that are being deleted", func() {
		// Their DRBD resource is still up until teardown completes.
		deleting := newPlacedRVR("vol-a", 0, v1alpha1.ReplicaTypeDiskful, nodeName(0), "vg-0")
		now := metav1.Now()
		deleting.DeletionTimestamp = &now
		deleting.Finalizers = []string{"test.deckhouse.io/keep"}

		Expect(countOn(1, deleting)).To(Equal(map[string]int{nodeName(0): 1}))
	})

	It("counts only the eligible nodes", func() {
		// A replica outside the pool can never be a candidate, so counting it would
		// only cost the read.
		Expect(countOn(1,
			newPlacedRVR("vol-a", 0, v1alpha1.ReplicaTypeDiskful, nodeName(0), "vg-0"),
			newPlacedRVR("vol-b", 0, v1alpha1.ReplicaTypeDiskful, "node-outside-the-pool", "vg-x"),
		)).To(Equal(map[string]int{nodeName(0): 1}))
	})

	It("returns empty when nothing is placed", func() {
		Expect(countOn(3)).To(BeEmpty())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Tests: placement spread
//
// The fake client answers reads immediately, while a real informer lags behind the
// writes the scheduler just made, and these volumes are reconciled one after another
// while the controller runs ten at a time. So an even spread here does not promise
// an even spread in a cluster — it shows that the tie-break itself is sound.
//

var _ = Describe("Placement balance", func() {
	var (
		scheme *runtime.Scheme
		ctx    context.Context
	)

	BeforeEach(func() {
		scheme = newScheme()
		ctx = context.Background()
	})

	It("spreads 25 volumes over 20 equal nodes", func() {
		// The regression this guards: with every node scored the same, selection falls
		// through to the node name, the first few nodes by name take everything and the
		// rest stay empty.
		//
		// The volume count is deliberately not a multiple of the node count, and the
		// layouts differ in size, so a perfect division cannot hide an off-by-one.
		const (
			nodeCount = 20
			// Deliberately not a multiple of nodeCount, so that a perfect division
			// cannot hide an off-by-one, and deliberately no larger: the property
			// under test is already exercised at 25, while the cost grows faster
			// than the volume count does. The fake client answers a field selector
			// by walking every object it holds, so each added volume lengthens all
			// of the per-node reads at once — a real informer indexes them instead.
			volumeCount = 25
		)

		rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM, nodesWithOneLVG(nodeCount))

		objs := []client.Object{rsp}
		statusObjs := []client.Object{}
		rvNames := make([]string, 0, volumeCount)
		for v := range volumeCount {
			rvName := fmt.Sprintf("vol-%02d", v)
			rvNames = append(rvNames, rvName)

			layout := layoutAvailability
			if v%3 == 0 {
				layout = layoutConsistency
			}
			addVolume(&objs, &statusObjs, rvName, layout)
		}

		cl := newClientBuilder(scheme).
			WithObjects(objs...).
			WithStatusSubresource(statusObjs...).
			Build()
		// The default mock scores every LVG at 5, so capacity never separates the nodes.
		rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

		// Sampled along the way rather than only at the end, so a failure points at a
		// short window of volumes instead of at the final tally.
		//
		// Every volume would be needless: placing k replicas on the k least loaded
		// nodes cannot open a gap and close it again, so a skew between checkpoints
		// would still be there at the next one.
		const checkEvery = 10
		for i, rvName := range rvNames {
			result, err := reconcileRVNamed(ctx, rec, rvName)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			if (i+1)%checkEvery != 0 {
				continue
			}
			// Volumes further down the list are still unplaced at this point, so only
			// the spread over the nodes is meaningful here; full placement is checked
			// once the loop is done.
			counts := replicasPerNode(ctx, cl, nodeCount)
			Expect(imbalance(counts)).To(BeNumerically("<=", maxImbalance),
				"after volume %d (%s): %s", i, rvName, formatCounts(counts))
		}

		final := replicasPerNode(ctx, cl, nodeCount)
		GinkgoWriter.Printf("\n25 volumes over 20 nodes: %s\n", formatCounts(final))
		Expect(final).NotTo(HaveKey(""), "every replica should have been placed")
		Expect(final).To(HaveLen(nodeCount), "no replica should have landed outside the pool")
		for node, c := range final {
			Expect(c).To(BeNumerically(">", 0), "node %s carries nothing: %s", node, formatCounts(final))
		}

		// A level total is not on its own proof that the diskless skew is gone: it could
		// be offset by the Diskful placements moving the other way. So the tie-breakers
		// are checked apart from the rest.
		//
		// Not for an even spread, though — nothing promises one per type. Placement
		// balances the total, because a node carries a DRBD resource per replica
		// whatever its type, and the types are free to sit unevenly inside a level
		// total. What the incident was is a pile: 77% of a cluster's tie-breakers on
		// three nodes. Without the load key all sixteen here land on one node; with it
		// no node takes more than two.
		diskful := replicasPerNodeOfType(ctx, cl, nodeCount, v1alpha1.ReplicaTypeDiskful)
		tieBreakers := replicasPerNodeOfType(ctx, cl, nodeCount, v1alpha1.ReplicaTypeTieBreaker)
		GinkgoWriter.Printf("  Diskful:    %s\n  TieBreaker: %s\n",
			formatCounts(diskful), formatCounts(tieBreakers))

		total := 0
		mostOnOneNode := 0
		for _, c := range tieBreakers {
			total += c
			mostOnOneNode = max(mostOnOneNode, c)
		}
		Expect(mostOnOneNode*4).To(BeNumerically("<=", total),
			"a node holds a quarter of the %d tie-breakers or more: %s",
			total, formatCounts(tieBreakers))
	})

	It("evens out a cluster that starts skewed", func() {
		// The state the cluster was actually found in: a few nodes carrying almost
		// everything. New volumes have to go to the empty ones until the rest catch up.
		const (
			nodeCount   = 10
			loadedNodes = 3
			loadPerNode = 8
			// Enough volumes that the quiet nodes fill up and the loaded ones start
			// taking replicas again — which is what makes the comparison below mean
			// "prefers the quiet nodes" rather than "never touches a loaded one".
			// Bringing 7 quiet nodes to 8 replicas each takes 56, so anything under
			// 19 volumes stops before the first replica can land on a loaded node.
			addedVolumes  = 30
			replicasAdded = addedVolumes * 3
		)

		rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM, nodesWithOneLVG(nodeCount))
		objs := []client.Object{rsp}
		statusObjs := []client.Object{}

		// Pre-existing load, owned by volumes this run never reconciles.
		for n := range loadedNodes {
			for i := range loadPerNode {
				objs = append(objs, newPlacedRVR(
					fmt.Sprintf("old-%02d-%02d", n, i), 0,
					v1alpha1.ReplicaTypeDiskful, nodeName(n), "vg-"+nodeName(n)))
			}
		}

		rvNames := make([]string, 0, addedVolumes)
		for v := range addedVolumes {
			rvName := fmt.Sprintf("new-%02d", v)
			rvNames = append(rvNames, rvName)
			addVolume(&objs, &statusObjs, rvName, layoutAvailability)
		}

		cl := newClientBuilder(scheme).
			WithObjects(objs...).
			WithStatusSubresource(statusObjs...).
			Build()
		rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

		before := replicasPerNode(ctx, cl, nodeCount)
		GinkgoWriter.Printf("\nbefore: %s (imbalance %d)\n", formatCounts(before), imbalance(before))

		for _, rvName := range rvNames {
			result, err := reconcileRVNamed(ctx, rec, rvName)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
		}

		after := replicasPerNode(ctx, cl, nodeCount)
		GinkgoWriter.Printf("after:  %s (imbalance %d)\n", formatCounts(after), imbalance(after))

		Expect(after).NotTo(HaveKey(""), "every replica should have been placed")
		Expect(imbalance(after)).To(BeNumerically("<=", maxImbalance),
			"the skew should be gone: %s", formatCounts(after))

		// The new replicas had to land mostly on the nodes that started empty.
		addedToLoaded, addedToQuiet := 0, 0
		for n := range nodeCount {
			added := after[nodeName(n)] - before[nodeName(n)]
			if n < loadedNodes {
				addedToLoaded += added
			} else {
				addedToQuiet += added
			}
		}
		GinkgoWriter.Printf("added to loaded nodes: %d, to quiet nodes: %d (of %d)\n",
			addedToLoaded, addedToQuiet, replicasAdded)
		Expect(addedToQuiet).To(BeNumerically(">", addedToLoaded),
			"new replicas should prefer the nodes that started empty")
	})

	It("balances inside a score tier, never across tiers", func() {
		// Capacity outranks load, so the low-scored nodes stay empty however idle they
		// are. Inside the high-scored group, where the score no longer separates the
		// nodes, the load has to.
		const (
			nodeCount   = 8
			highScored  = 4
			volumeCount = 25
		)

		rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM, nodesWithOneLVG(nodeCount))
		objs := []client.Object{rsp}
		statusObjs := []client.Object{}
		rvNames := make([]string, 0, volumeCount)
		for v := range volumeCount {
			rvName := fmt.Sprintf("vol-%02d", v)
			rvNames = append(rvNames, rvName)
			// Diskful only: the tie-breaker is placed without asking the extender and
			// would land on a low-scored node, which is correct but not what this checks.
			addVolume(&objs, &statusObjs, rvName, layoutConsistency)
		}

		highTier := make(map[string]struct{}, highScored)
		for n := range highScored {
			highTier["vg-"+nodeName(n)] = struct{}{}
		}

		cl := newClientBuilder(scheme).
			WithObjects(objs...).
			WithStatusSubresource(statusObjs...).
			Build()
		extender := &reconcilerMockExtender{
			filterAndScoreFn: func(lvgs []schext.LVMVolumeGroup) ([]schext.ScoredLVMVolumeGroup, error) {
				scored := make([]schext.ScoredLVMVolumeGroup, 0, len(lvgs))
				for _, l := range lvgs {
					score := 2
					if _, ok := highTier[l.LVGName]; ok {
						score = 8
					}
					scored = append(scored, schext.ScoredLVMVolumeGroup{LVMVolumeGroup: l, Score: score})
				}
				return scored, nil
			},
		}
		rec := NewReconciler(cl, logr.Discard(), scheme, extender)

		for _, rvName := range rvNames {
			result, err := reconcileRVNamed(ctx, rec, rvName)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
		}

		counts := replicasPerNode(ctx, cl, nodeCount)
		GinkgoWriter.Printf("\nscore tiers: %s\n", formatCounts(counts))

		high := map[string]int{}
		for n := range nodeCount {
			if n < highScored {
				high[nodeName(n)] = counts[nodeName(n)]
				continue
			}
			Expect(counts[nodeName(n)]).To(BeZero(),
				"low-scored %s should stay empty: %s", nodeName(n), formatCounts(counts))
		}
		Expect(imbalance(high)).To(BeNumerically("<=", maxImbalance),
			"the high-scored tier should be balanced: %s", formatCounts(counts))
	})

	It("follows score order against both name and load", func() {
		// Every other key is stacked against the winners: the best-scored nodes sort
		// last by name and already carry the most replicas. Only capacity can explain
		// the result, which is what makes this the ordering check — the existing score
		// tests all happen to score the alphabetically first nodes highest.
		const nodeCount = 4

		rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM, nodesWithOneLVG(nodeCount))

		rv := newRVNamed("vol-new", defaultConfig())
		d0 := newRVRFor("vol-new", 0, v1alpha1.ReplicaTypeDiskful)
		d1 := newRVRFor("vol-new", 1, v1alpha1.ReplicaTypeDiskful)

		objs := []client.Object{rsp, rv, d0, d1}
		// node-03 gets three replicas of another volume, node-02 gets two: the two
		// nodes that must win on score are the two that lose on load.
		for n, load := range map[int]int{2: 2, 3: 3} {
			for i := range load {
				objs = append(objs, newPlacedRVR(
					fmt.Sprintf("old-%02d-%02d", n, i), 0,
					v1alpha1.ReplicaTypeDiskful, nodeName(n), "vg-"+nodeName(n)))
			}
		}

		cl := newClientBuilder(scheme).
			WithObjects(objs...).
			WithStatusSubresource(d0, d1).
			Build()

		// Score descends as the node name ascends.
		scores := map[string]int{
			"vg-" + nodeName(0): 2,
			"vg-" + nodeName(1): 4,
			"vg-" + nodeName(2): 6,
			"vg-" + nodeName(3): 8,
		}
		extender := &reconcilerMockExtender{
			filterAndScoreFn: func(lvgs []schext.LVMVolumeGroup) ([]schext.ScoredLVMVolumeGroup, error) {
				scored := make([]schext.ScoredLVMVolumeGroup, 0, len(lvgs))
				for _, l := range lvgs {
					scored = append(scored, schext.ScoredLVMVolumeGroup{LVMVolumeGroup: l, Score: scores[l.LVGName]})
				}
				return scored, nil
			},
		}
		rec := NewReconciler(cl, logr.Discard(), scheme, extender)

		result, err := reconcileRVNamed(ctx, rec, "vol-new")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(reconcile.Result{}))

		// Best score first, then the next best once its node is taken by the first replica.
		Expect(getUpdatedRVR(ctx, cl, d0).Spec.NodeName).To(Equal(nodeName(3)))
		Expect(getUpdatedRVR(ctx, cl, d1).Spec.NodeName).To(Equal(nodeName(2)))
	})

	It("does not count the load when no replica needs a node", func() {
		// A replica back only for its LVG is narrowed to the node it already sits on,
		// so no two nodes are compared and the count cannot change the outcome. The
		// reads it would take are skipped, and this is the only thing that tells the
		// skip apart from counting anyway.
		rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM, nodesWithOneLVG(3))
		rv := newRVNamed("vol-new", defaultConfig())

		// Node already assigned, LVG still missing.
		rvr := newRVRFor("vol-new", 0, v1alpha1.ReplicaTypeDiskful)
		rvr.Spec.NodeName = nodeName(1)

		byNode := 0
		cl := newClientBuilder(scheme).
			WithObjects(rsp, rv, rvr).
			WithStatusSubresource(rvr).
			WithInterceptorFuncs(countingNodeLoadReads(&byNode)).
			Build()
		rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

		result, err := reconcileRVNamed(ctx, rec, "vol-new")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(reconcile.Result{}))

		// It still gets its LVG on the node it was already on.
		placed := getUpdatedRVR(ctx, cl, rvr)
		Expect(placed.Spec.NodeName).To(Equal(nodeName(1)))
		Expect(placed.Spec.LVMVolumeGroupName).NotTo(BeEmpty())

		Expect(byNode).To(BeZero(), "the node load should not have been counted")
	})

	It("counts the load when a replica does need a node", func() {
		// The other direction, so that the check above cannot pass by the count having
		// been dropped altogether.
		rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM, nodesWithOneLVG(3))
		rv := newRVNamed("vol-new", defaultConfig())
		rvr := newRVRFor("vol-new", 0, v1alpha1.ReplicaTypeDiskful)

		byNode := 0
		cl := newClientBuilder(scheme).
			WithObjects(rsp, rv, rvr).
			WithStatusSubresource(rvr).
			WithInterceptorFuncs(countingNodeLoadReads(&byNode)).
			Build()
		rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

		_, err := reconcileRVNamed(ctx, rec, "vol-new")
		Expect(err).NotTo(HaveOccurred())

		Expect(byNode).To(Equal(3), "one read per eligible node")
	})

	It("fails the reconciliation when a node cannot be read", func() {
		// A failure here must stop the pass. Were it swallowed, NodeLoad would stay nil,
		// every candidate would read as load zero, and the tie-break would quietly fall
		// back to the node name — the very skew this exists to prevent, with nothing in
		// the logs to say so.
		rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM, nodesWithOneLVG(3))
		rv := newRVNamed("vol-new", defaultConfig())
		rvr := newRVRFor("vol-new", 0, v1alpha1.ReplicaTypeDiskful)

		readErr := errors.New("index read failed")
		cl := newClientBuilder(scheme).
			WithObjects(rsp, rv, rvr).
			WithStatusSubresource(rvr).
			WithInterceptorFuncs(failingNodeLoadReads(readErr)).
			Build()
		rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

		_, err := reconcileRVNamed(ctx, rec, "vol-new")
		Expect(err).To(MatchError(readErr))
		Expect(err.Error()).To(ContainSubstring("counting node load"))

		// And nothing was placed on the way out.
		Expect(getUpdatedRVR(ctx, cl, rvr).Spec.NodeName).To(BeEmpty())
	})

	It("breaks a score tie by node load", func() {
		// node-00 sorts first by name and would win on a tie, but another volume
		// already put two replicas there.
		rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM, nodesWithOneLVG(3))

		rv := newRVNamed("vol-new", defaultConfig())
		rvr := newRVRFor("vol-new", 0, v1alpha1.ReplicaTypeDiskful)

		cl := newClientBuilder(scheme).
			WithObjects(
				rsp, rv, rvr,
				newPlacedRVR("vol-old", 0, v1alpha1.ReplicaTypeDiskful, nodeName(0), "vg-"+nodeName(0)),
				newPlacedRVR("vol-old", 1, v1alpha1.ReplicaTypeDiskful, nodeName(1), "vg-"+nodeName(1)),
				newPlacedRVR("vol-old", 2, v1alpha1.ReplicaTypeTieBreaker, nodeName(0), ""),
			).
			WithStatusSubresource(rvr).
			Build()
		rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

		result, err := reconcileRVNamed(ctx, rec, "vol-new")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(reconcile.Result{}))

		// node-00 carries 2, node-01 carries 1, node-02 carries none.
		Expect(getUpdatedRVR(ctx, cl, rvr).Spec.NodeName).To(Equal(nodeName(2)))
	})

	It("is repelled by a pile of Access replicas when capacity ties", func() {
		// Access replicas are diskless, so they leave the node's capacity score
		// untouched — the extender cannot see them at all. They are still DRBD
		// resources on it, and on a tie that is what has to decide.
		//
		// node-00 sorts first by name and is just as empty on disk as the others.
		const accessOnFirstNode = 4

		rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM, nodesWithOneLVG(3))

		rv := newRVNamed("vol-new", defaultConfig())
		rvr := newRVRFor("vol-new", 0, v1alpha1.ReplicaTypeDiskful)

		objs := []client.Object{rsp, rv, rvr}
		for i := range accessOnFirstNode {
			objs = append(objs, newPlacedRVR(
				fmt.Sprintf("attached-%02d", i), 0,
				v1alpha1.ReplicaTypeAccess, nodeName(0), ""))
		}
		objs = append(objs, newPlacedRVR(
			"attached-lonely", 0, v1alpha1.ReplicaTypeAccess, nodeName(1), ""))

		cl := newClientBuilder(scheme).
			WithObjects(objs...).
			WithStatusSubresource(rvr).
			Build()
		rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

		result, err := reconcileRVNamed(ctx, rec, "vol-new")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(reconcile.Result{}))

		// node-00 carries 4 Access, node-01 one, node-02 none.
		Expect(getUpdatedRVR(ctx, cl, rvr).Spec.NodeName).To(Equal(nodeName(2)))
	})

	It("still takes the better-scored node under a pile of Access replicas", func() {
		// The other half of the rule above: Access replicas only ever decide a tie.
		// Where the capacity score differs they must not, or a node with room would
		// be passed over because workload happens to be attached there.
		const accessOnFirstNode = 5

		rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM, nodesWithOneLVG(2))

		rv := newRVNamed("vol-new", defaultConfig())
		rvr := newRVRFor("vol-new", 0, v1alpha1.ReplicaTypeDiskful)

		objs := []client.Object{rsp, rv, rvr}
		for i := range accessOnFirstNode {
			objs = append(objs, newPlacedRVR(
				fmt.Sprintf("attached-%02d", i), 0,
				v1alpha1.ReplicaTypeAccess, nodeName(0), ""))
		}

		cl := newClientBuilder(scheme).
			WithObjects(objs...).
			WithStatusSubresource(rvr).
			Build()
		extender := &reconcilerMockExtender{
			filterAndScoreFn: func(lvgs []schext.LVMVolumeGroup) ([]schext.ScoredLVMVolumeGroup, error) {
				scored := make([]schext.ScoredLVMVolumeGroup, 0, len(lvgs))
				for _, l := range lvgs {
					score := 1
					if l.LVGName == "vg-"+nodeName(0) {
						score = 9
					}
					scored = append(scored, schext.ScoredLVMVolumeGroup{LVMVolumeGroup: l, Score: score})
				}
				return scored, nil
			},
		}
		rec := NewReconciler(cl, logr.Discard(), scheme, extender)

		result, err := reconcileRVNamed(ctx, rec, "vol-new")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(reconcile.Result{}))

		Expect(getUpdatedRVR(ctx, cl, rvr).Spec.NodeName).To(Equal(nodeName(0)))
	})

	It("prefers score over load", func() {
		// The loaded node is also the one with room. Capacity has to win, or a
		// volume would be sent to a node that cannot hold it just to even out counts.
		rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM, nodesWithOneLVG(2))

		rv := newRVNamed("vol-new", defaultConfig())
		rvr := newRVRFor("vol-new", 0, v1alpha1.ReplicaTypeDiskful)

		cl := newClientBuilder(scheme).
			WithObjects(
				rsp, rv, rvr,
				newPlacedRVR("vol-old", 0, v1alpha1.ReplicaTypeDiskful, nodeName(0), "vg-"+nodeName(0)),
				newPlacedRVR("vol-old", 1, v1alpha1.ReplicaTypeDiskful, nodeName(0), "vg-"+nodeName(0)),
			).
			WithStatusSubresource(rvr).
			Build()

		extender := &reconcilerMockExtender{
			filterAndScoreFn: func(lvgs []schext.LVMVolumeGroup) ([]schext.ScoredLVMVolumeGroup, error) {
				scored := make([]schext.ScoredLVMVolumeGroup, 0, len(lvgs))
				for _, l := range lvgs {
					score := 1
					if l.LVGName == "vg-"+nodeName(0) {
						score = 9
					}
					scored = append(scored, schext.ScoredLVMVolumeGroup{LVMVolumeGroup: l, Score: score})
				}
				return scored, nil
			},
		}
		rec := NewReconciler(cl, logr.Discard(), scheme, extender)

		result, err := reconcileRVNamed(ctx, rec, "vol-new")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(reconcile.Result{}))

		Expect(getUpdatedRVR(ctx, cl, rvr).Spec.NodeName).To(Equal(nodeName(0)))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Tests: schedulingContext.Update
//

var _ = Describe("schedulingContext.Update", func() {
	entry := &CandidateEntry{
		Node: &v1alpha1.ReplicatedStoragePoolEligibleNode{NodeName: nodeName(0)},
	}

	It("records the placement", func() {
		sctx := &schedulingContext{
			OccupiedNodes:  map[string]struct{}{},
			ReplicasByZone: map[string]idset.IDSet{},
		}
		rvr := newRVRFor("vol-a", 1, v1alpha1.ReplicaTypeDiskful)

		sctx.Update(rvr, entry)

		Expect(sctx.OccupiedNodes).To(HaveKey(nodeName(0)))
		Expect(sctx.Scheduled.Contains(rvr.ID())).To(BeTrue())
	})

	It("leaves the node load alone", func() {
		// Occupying the node is what keeps the volume's other replicas off it, so the
		// count is never consulted for it again. Charging it would only put the map
		// above the real replica count — and skew the placement log, which reads it.
		sctx := &schedulingContext{
			OccupiedNodes:  map[string]struct{}{},
			ReplicasByZone: map[string]idset.IDSet{},
			NodeLoad:       map[string]int{nodeName(0): 3},
		}

		sctx.Update(newRVRFor("vol-a", 0, v1alpha1.ReplicaTypeDiskful), entry)

		Expect(sctx.NodeLoad[nodeName(0)]).To(Equal(3))
	})
})
