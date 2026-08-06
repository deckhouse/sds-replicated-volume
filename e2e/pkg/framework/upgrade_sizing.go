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

package framework

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

const (
	// EnvUpgradeVolumes names the variable that overrides the computed number of
	// volumes.
	//
	// The framework NEVER reads it. The variable is parsed exactly once per
	// process — by a suite's gate, before RunSpecs, through ParseVolumesOverride —
	// and travels through the code as an already validated number
	// (VolumeCountOptions.Override). The constant lives next to that parser so the
	// literal is spelled once, and so that a message about the value can name the
	// variable it came from.
	EnvUpgradeVolumes = "E2E_UPGRADE_VOLUMES"

	// VolumeCountFloor is the smallest number of volumes a computed count may end
	// up at. A scenario running on fewer volumes tests something else, so a pool
	// that cannot host this many is a failure and not a quieter run.
	VolumeCountFloor = 5

	// VolumeCountCeiling is the largest number of volumes a computed count may end
	// up at, no matter how much space the pool has: every volume costs a pod, an
	// attachment and a share of the suite's budget. It is also the number a suite
	// has to budget its nodes for when no override was given — the count computed
	// later can only be lower.
	VolumeCountCeiling = 20

	// volumeSpaceHeadroomPercent is how much of a node's reported free space the
	// computation is allowed to fill. The rest absorbs everything the arithmetic
	// cannot see: DRBD metadata, LVM rounding to extents, thin-pool metadata
	// growth, and the placement decisions of the module — which balances replicas
	// across nodes on its own terms, not on this helper's.
	volumeSpaceHeadroomPercent = 70

	// defaultDiskfulReplicas is the replica count VolumeCountOptions defaults to:
	// an r3 volume, three diskful replicas each holding a full copy. A tie-breaker
	// replica is diskless and costs no space in the pool, so it is never counted.
	defaultDiskfulReplicas = 3
)

// ---------------------------------------------------------------------------
// Override parsing
// ---------------------------------------------------------------------------

// ParseVolumesOverride reads the value of E2E_UPGRADE_VOLUMES: a decimal integer
// >= 1, or an empty string standing for "not set", which it reports as 0.
//
// This is the ONLY parser of that variable, and its only caller is meant to be a
// suite's entry point (func TestUpgrade), before RunSpecs. Hence the deviation
// from the framework's rule that a helper reports problems as Ginkgo failures:
// outside a running Ginkgo node Fail and Skip panic, so an invalid value has to
// come back as an error the gate turns into t.Fatalf. The error text carries both
// the offending string and the rule, so the gate can print it as it is.
//
// Nothing downstream re-reads the variable or re-validates the number: budget
// decorators computed on tree construction and PlanVolumeCount take it as an
// argument. That is what keeps one string from being read two ways — " 20",
// "+20" and "1e2" are accepted or refused by this single call.
//
// A value that cannot be read is refused rather than defaulted: a suite that
// silently created 0 volumes would report a green run for a scenario that never
// ran.
func ParseVolumesOverride(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	return parseVolumeCountEnv(EnvUpgradeVolumes, raw)
}

// ---------------------------------------------------------------------------
// Options
// ---------------------------------------------------------------------------

// VolumeCountOptions configures PlanVolumeCount.
type VolumeCountOptions struct {
	// Pool is the storage pool the volumes are going to be created in: the free
	// space of ITS volume groups, on ITS usable diskful nodes, is what the count
	// is computed from. Required — obtain it from f.Discovery.From(poolType).
	Pool *PoolScope

	// VolumeSize is the size of one volume as a Kubernetes quantity ("1Gi").
	// Defaults to DefaultVolumeSize.
	VolumeSize string

	// DiskfulReplicas is how many diskful replicas each volume gets; each of them
	// occupies VolumeSize on a node of its own. Defaults to
	// defaultDiskfulReplicas (an r3 volume). A tie-breaker replica holds no data
	// and is not counted.
	DiskfulReplicas int

	// Override is the number of volumes to use regardless of the computation: the
	// value of E2E_UPGRADE_VOLUMES as ParseVolumesOverride returned it, with 0
	// meaning "not set, compute it". It is NOT read from the environment here and
	// NOT clamped — an explicit request is obeyed above the ceiling and below the
	// floor alike — but it is still checked against the pool's free space.
	Override int
}

// ---------------------------------------------------------------------------
// Exported helpers
// ---------------------------------------------------------------------------

// PlanVolumeCount decides how many volumes of opts.VolumeSize the caller should
// create in opts.Pool and returns that number.
//
// Read-only and idempotent: it reads the pool's usable diskful placements from
// the RSP snapshot discovery already tracks, plus one LVMVolumeGroup per
// placement. Nothing is created or modified, and no DeferCleanup is registered.
//
// How the number is decided:
//
//   - Every usable diskful node of the pool contributes the free space behind its
//     volume group: status.vgFree of the LVMVolumeGroup for a thick (LVM) pool,
//     status.thinPools[].availableSpace of the placement's thin pool for a thin
//     (LVMThin) one — the value that already accounts for the thin pool's
//     allocation limit.
//   - Only volumeSpaceHeadroomPercent of that space is offered to the volumes,
//     and a node's share is floored to whole volumes.
//   - The answer is the largest N whose opts.DiskfulReplicas replicas can be
//     spread over those per-node numbers, with no two replicas of a volume on one
//     node. Per node and not from the pool's total on purpose: a total divided by
//     the replica count assumes the space is spread evenly and answers "77" for a
//     pool of three nodes with 10Gi and one with 300Gi, where 10 is the truth. On
//     nodes of equal free space the two agree.
//   - That N is clamped to [VolumeCountFloor, VolumeCountCeiling].
//   - opts.Override replaces the whole computation, both clamps included, but not
//     the space check (see VolumeCountOptions.Override).
//
// It fails the spec — never skips it — when the pool cannot host
// VolumeCountFloor volumes, when an override does not fit, when the pool offers
// fewer usable diskful nodes than a volume has replicas, or when a volume group
// cannot be read. A stand too small for the scenario is a degradation to report,
// and the message carries the whole derivation: free, usable and fitting volumes
// per node.
//
// The derivation is written to the Ginkgo log on success too, because the count
// is the scale of everything the spec does afterwards.
func (f *Framework) PlanVolumeCount(ctx context.Context, opts VolumeCountOptions) int {
	GinkgoHelper()
	plan, err := planVolumeCount(ctx, f.Client, opts)
	if err != nil {
		Fail(fmt.Sprintf("volume count: %v", err))
	}
	fmt.Fprintf(GinkgoWriter, "[%s] [sizing] %s\n", time.Now().Format("15:04:05.000"), plan)
	return plan.Count
}

// ---------------------------------------------------------------------------
// Seams
// ---------------------------------------------------------------------------

// volumeSizingClient is the seam the LVMVolumeGroup reads go through.
// client.Client implements it against the cluster; helper unit tests substitute
// the controller-runtime fake client, so a missing volume group is answered with
// a real NotFound — without a cluster.
type volumeSizingClient interface {
	Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error
}

// ---------------------------------------------------------------------------
// Core: everything below returns errors, so the whole computation is
// unit-testable with a fake client and an injected RSP snapshot.
// ---------------------------------------------------------------------------

// volumeCountRequest is a validated sizing request: the pool it is about, the
// size of one volume in bytes and as written, the replica count and the override.
type volumeCountRequest struct {
	poolName  string
	poolType  v1alpha1.ReplicatedStoragePoolType
	size      string
	sizeBytes int64
	replicas  int
	override  int
}

// newVolumeCountRequest applies the defaults of VolumeCountOptions and validates
// what the Go types cannot. Pure: the pool's name and type are passed in, because
// reading them from the RSP snapshot is the caller's business.
func newVolumeCountRequest(
	poolName string,
	poolType v1alpha1.ReplicatedStoragePoolType,
	opts VolumeCountOptions,
) (volumeCountRequest, error) {
	req := volumeCountRequest{
		poolName: poolName,
		poolType: poolType,
		size:     opts.VolumeSize,
		replicas: opts.DiskfulReplicas,
		override: opts.Override,
	}
	if req.size == "" {
		req.size = DefaultVolumeSize
	}
	if req.replicas == 0 {
		req.replicas = defaultDiskfulReplicas
	}

	switch {
	case req.poolName == "":
		return volumeCountRequest{}, errors.New("require: the pool has no name, so its snapshot was never read")
	case req.poolType != v1alpha1.ReplicatedStoragePoolTypeLVM &&
		req.poolType != v1alpha1.ReplicatedStoragePoolTypeLVMThin:
		// Most likely an RSP snapshot that was never delivered: the field is
		// required and immutable on a live object.
		return volumeCountRequest{}, fmt.Errorf("pool %q reports spec.type %q, which is neither %s nor %s",
			req.poolName, req.poolType,
			v1alpha1.ReplicatedStoragePoolTypeLVM, v1alpha1.ReplicatedStoragePoolTypeLVMThin)
	case req.replicas < 1:
		return volumeCountRequest{}, fmt.Errorf("require: DiskfulReplicas must be at least 1, got %d", req.replicas)
	case req.override < 0:
		return volumeCountRequest{}, fmt.Errorf(
			"require: Override must not be negative, got %d (0 means \"compute it\"; the value comes from %s"+
				" through ParseVolumesOverride)", req.override, EnvUpgradeVolumes)
	}

	size, err := resource.ParseQuantity(req.size)
	if err != nil {
		return volumeCountRequest{}, fmt.Errorf("require: VolumeSize %q is not a Kubernetes quantity: %w", req.size, err)
	}
	req.sizeBytes = size.Value()
	if req.sizeBytes <= 0 {
		return volumeCountRequest{}, fmt.Errorf("require: VolumeSize %q must be positive", req.size)
	}
	return req, nil
}

// nodeVolumeCapacity is one node's contribution to the pool: the free space
// behind its volume group, the part of it this computation may fill, and how many
// whole volumes fit in that part.
type nodeVolumeCapacity struct {
	NodeName     string
	LVGName      string
	ThinPoolName string

	FreeBytes   int64
	UsableBytes int64
	Volumes     int64
}

// newNodeVolumeCapacity turns free space into whole volumes: the headroom is
// taken off first, then the remainder is floored to volumes of sizeBytes.
//
// A negative free space is read as zero — the field is a quantity a node
// publishes, and a node that reports nonsense must not inflate the count.
func newNodeVolumeCapacity(placement DiskfulPlacement, freeBytes, sizeBytes int64) nodeVolumeCapacity {
	if freeBytes < 0 {
		freeBytes = 0
	}
	// A volume group's free space is orders of magnitude away from overflowing an
	// int64 multiplied by 70.
	usable := freeBytes * volumeSpaceHeadroomPercent / 100
	return nodeVolumeCapacity{
		NodeName:     placement.NodeName,
		LVGName:      placement.LVGName,
		ThinPoolName: placement.ThinPoolName,
		FreeBytes:    freeBytes,
		UsableBytes:  usable,
		Volumes:      usable / sizeBytes,
	}
}

// String renders one node's capacity for the log line and for failure messages.
func (c nodeVolumeCapacity) String() string {
	target := c.LVGName
	if c.ThinPoolName != "" {
		target += "/" + c.ThinPoolName
	}
	return fmt.Sprintf("%s %s free %s, usable %s, fits %d",
		c.NodeName, target, humanBytes(c.FreeBytes), humanBytes(c.UsableBytes), c.Volumes)
}

// humanBytes renders a byte count the way the quantity it came from spells it
// (7Gi).
//
// A value that is no round binary multiple — which is what taking a percentage
// off a volume group's free space produces — has no such form and would print as
// ten digits of bytes, so it is approximated down to whole mebibytes and marked
// with a tilde. Only messages read these numbers; the arithmetic uses the exact
// bytes.
func humanBytes(v int64) string {
	const mebibyte = 1 << 20
	canonical := resource.NewQuantity(v, resource.BinarySI).String()
	if v >= mebibyte && !strings.ContainsAny(canonical, "KMGTPE") {
		return "~" + resource.NewQuantity(v/mebibyte*mebibyte, resource.BinarySI).String()
	}
	return canonical
}

// readPoolCapacity reads the free space behind every placement of the pool and
// projects it onto whole volumes.
//
// One placement per usable diskful node is what UsableDiskfulPlacements provides,
// and an LVMVolumeGroup is local to its node (spec.local.nodeName), so no node's
// space is counted twice. A node whose volume group holds several LVGs of the
// pool contributes only the first usable one — the same one the placement names,
// which under-counts rather than over-counts.
func readPoolCapacity(
	ctx context.Context,
	cl volumeSizingClient,
	placements []DiskfulPlacement,
	req volumeCountRequest,
) ([]nodeVolumeCapacity, error) {
	out := make([]nodeVolumeCapacity, 0, len(placements))
	for _, placement := range placements {
		var lvg snc.LVMVolumeGroup
		if err := cl.Get(ctx, client.ObjectKey{Name: placement.LVGName}, &lvg); err != nil {
			return nil, fmt.Errorf("reading LVMVolumeGroup %q of node %q: %w",
				placement.LVGName, placement.NodeName, err)
		}
		free, err := placementFreeSpace(&lvg, placement, req)
		if err != nil {
			return nil, err
		}
		out = append(out, newNodeVolumeCapacity(placement, free, req.sizeBytes))
	}
	return out, nil
}

// placementFreeSpace returns the free space the pool may allocate from on one
// placement, read from the field that describes THIS pool type:
//
//   - LVM (thick): status.vgFree of the volume group. A thin pool living in the
//     same group is already subtracted from it, so a group serving both types
//     needs no correction here.
//   - LVMThin: status.thinPools[].availableSpace of the thin pool the placement
//     names — the value snc computes from the pool's allocation limit, i.e. the
//     space a thin volume may actually claim.
func placementFreeSpace(
	lvg *snc.LVMVolumeGroup,
	placement DiskfulPlacement,
	req volumeCountRequest,
) (int64, error) {
	if req.poolType != v1alpha1.ReplicatedStoragePoolTypeLVMThin {
		return lvg.Status.VGFree.Value(), nil
	}
	if placement.ThinPoolName == "" {
		return 0, fmt.Errorf(
			"pool %q is %s, but its node %q offers LVMVolumeGroup %q without a thin pool name",
			req.poolName, req.poolType, placement.NodeName, placement.LVGName)
	}
	for i := range lvg.Status.ThinPools {
		if lvg.Status.ThinPools[i].Name == placement.ThinPoolName {
			return lvg.Status.ThinPools[i].AvailableSpace.Value(), nil
		}
	}
	return 0, fmt.Errorf("LVMVolumeGroup %q of node %q reports no thin pool %q in status.thinPools (it has %v)",
		placement.LVGName, placement.NodeName, placement.ThinPoolName, thinPoolNames(lvg))
}

// thinPoolNames lists the thin pools a volume group publishes, so a message about
// the missing one shows what was there instead.
func thinPoolNames(lvg *snc.LVMVolumeGroup) []string {
	out := make([]string, 0, len(lvg.Status.ThinPools))
	for i := range lvg.Status.ThinPools {
		out = append(out, lvg.Status.ThinPools[i].Name)
	}
	return out
}

// volumeCountPlan is the outcome of the computation: the number to use, whether
// it came from the override, the largest number that fits, and the per-node
// numbers both were derived from.
type volumeCountPlan struct {
	Count      int
	Overridden bool
	MaxFitting int64
	Capacities []nodeVolumeCapacity

	req volumeCountRequest
}

// String renders the whole derivation, which is what the log line prints and what
// makes a failure diagnosable without a second run.
func (p volumeCountPlan) String() string {
	source := fmt.Sprintf("computed, clamped to [%d, %d]", VolumeCountFloor, VolumeCountCeiling)
	if p.Overridden {
		source = EnvUpgradeVolumes
	}
	return fmt.Sprintf("pool %q (%s): %d volumes of %s with %d diskful replicas each (%s; at most %d fit; %s)",
		p.req.poolName, p.req.poolType, p.Count, p.req.size, p.req.replicas, source, p.MaxFitting, p.spaceReport())
}

// spaceReport renders the per-node numbers a count was decided from.
func (p volumeCountPlan) spaceReport() string {
	var b strings.Builder
	fmt.Fprintf(&b, "usable space is %d%% of free; nodes: ", volumeSpaceHeadroomPercent)
	if len(p.Capacities) == 0 {
		b.WriteString("none")
	}
	for i := range p.Capacities {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(p.Capacities[i].String())
	}
	return b.String()
}

// computeVolumeCount turns per-node capacities into the number of volumes to
// create. Pure — this is the function the sizing table tests.
func computeVolumeCount(capacities []nodeVolumeCapacity, req volumeCountRequest) (volumeCountPlan, error) {
	plan := volumeCountPlan{Capacities: capacities, req: req}
	if len(capacities) < req.replicas {
		return volumeCountPlan{}, fmt.Errorf(
			"pool %q offers %d usable diskful node(s), fewer than the %d diskful replicas of one volume (%s)",
			req.poolName, len(capacities), req.replicas, plan.spaceReport())
	}
	plan.MaxFitting = maxFittingVolumeCount(capacities, int64(req.replicas))

	if req.override > 0 {
		plan.Count, plan.Overridden = req.override, true
		if int64(req.override) > plan.MaxFitting {
			return volumeCountPlan{}, fmt.Errorf(
				"%s asks for %d volumes of %s with %d diskful replicas each, but pool %q fits at most %d"+
					" (%s); reclaim space on the pool's nodes, ask for fewer volumes or for smaller ones",
				EnvUpgradeVolumes, req.override, req.size, req.replicas, req.poolName, plan.MaxFitting,
				plan.spaceReport())
		}
		return plan, nil
	}

	if plan.MaxFitting < VolumeCountFloor {
		return volumeCountPlan{}, fmt.Errorf(
			"pool %q fits at most %d volumes of %s with %d diskful replicas each, fewer than the %d the"+
				" scenario needs (%s); reclaim space on the pool's nodes or ask for smaller volumes",
			req.poolName, plan.MaxFitting, req.size, req.replicas, VolumeCountFloor, plan.spaceReport())
	}
	plan.Count = int(min(plan.MaxFitting, int64(VolumeCountCeiling)))
	return plan, nil
}

// maxFittingVolumeCount returns the largest number of volumes whose replicas fit
// into the per-node capacities — no clamping, no override.
//
// It binary-searches volumesFit, which it may do because "n volumes fit" is
// downward closed: dropping a volume from a valid placement leaves a valid one.
// The upper bound is the total capacity divided by the replica count, which
// cannot fit by definition, so the search always straddles the answer.
func maxFittingVolumeCount(capacities []nodeVolumeCapacity, replicas int64) int64 {
	var total int64
	for i := range capacities {
		total += capacities[i].Volumes
	}

	// lo always fits (nothing to place), hi never does.
	lo, hi := int64(0), total/replicas+1
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		if volumesFit(capacities, replicas, mid) {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo
}

// volumesFit reports whether n volumes, each with replicas replicas on distinct
// nodes, fit into the per-node capacities.
//
// A node can serve at most min(capacity, n) of the n×replicas replica slots — its
// space bounds it from one side, and "no two replicas of one volume on one node"
// from the other. Summing that bound over the nodes gives the number of slots the
// pool can serve at all, and a placement exists exactly when it covers
// n×replicas: this is the flow bound for spreading n volumes over the nodes, and
// it is tight.
//
// The comparison is written as a division so that a huge override cannot overflow
// n×replicas.
func volumesFit(capacities []nodeVolumeCapacity, replicas, n int64) bool {
	if n <= 0 {
		return true
	}
	var slots int64
	for i := range capacities {
		slots += min(capacities[i].Volumes, n)
	}
	return slots/replicas >= n
}

// planVolumeCount is the failing logic of PlanVolumeCount: project the pool's
// snapshot, read the free space behind it, compute the count.
func planVolumeCount(
	ctx context.Context,
	cl volumeSizingClient,
	opts VolumeCountOptions,
) (volumeCountPlan, error) {
	if opts.Pool == nil {
		return volumeCountPlan{}, errors.New("require: Pool must not be nil, pass f.Discovery.From(poolType)")
	}
	req, err := newVolumeCountRequest(opts.Pool.PoolName(), opts.Pool.PoolType(), opts)
	if err != nil {
		return volumeCountPlan{}, err
	}
	capacities, err := readPoolCapacity(ctx, cl, opts.Pool.UsableDiskfulPlacements(), req)
	if err != nil {
		return volumeCountPlan{}, err
	}
	return computeVolumeCount(capacities, req)
}
