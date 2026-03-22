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
	"fmt"
	"math/rand"
	"os"

	. "github.com/onsi/ginkgo/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	tkmatch "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

const (
	defaultRSPThin  = "e2e-thin"
	defaultRSPThick = "e2e-thick"
)

// PoolScope provides topology queries scoped to a single RSP.
// It holds a live-tracked TestRSP whose Object() always returns
// the latest informer snapshot.
//
// All query methods on Discovery are promoted from the embedded
// PoolScope (which points at the thin RSP by default).
type PoolScope struct {
	trsp *TestRSP
}

// Discovery holds two live-tracked RSP objects (thin and thick).
// All cluster topology queries are answered from these RSPs.
//
// Methods called directly on Discovery operate on the thin RSP
// (promoted from the embedded PoolScope). Use From(pt) to get
// a PoolScope for a different pool type.
type Discovery struct {
	PoolScope // embedded, defaults to thin RSP
	thickPool PoolScope
}

// From returns a PoolScope for the given pool type.
func (d *Discovery) From(pt v1alpha1.ReplicatedStoragePoolType) *PoolScope {
	if pt == v1alpha1.ReplicatedStoragePoolTypeLVMThin {
		return &d.PoolScope
	}
	return &d.thickPool
}

// newDiscovery creates live-tracked TestRSP objects for thin and thick
// RSPs and waits for each informer to deliver the first snapshot.
// RSP names are read from E2E_RSP_THIN / E2E_RSP_THICK env vars,
// defaulting to "e2e-thin" / "e2e-thick".
func newDiscovery(ctx context.Context, f *Framework) *Discovery {
	thinName := envOrDefault("E2E_RSP_THIN", defaultRSPThin)
	thickName := envOrDefault("E2E_RSP_THICK", defaultRSPThick)

	fmt.Fprintf(GinkgoWriter, "[Discovery] thin RSP %q (override: E2E_RSP_THIN), thick RSP %q (override: E2E_RSP_THICK)\n",
		thinName, thickName)

	thinRSP := f.TestRSPExact(thinName)
	thinRSP.Get(ctx)
	thinRSP.Await(ctx, tkmatch.Present())

	thickRSP := f.TestRSPExact(thickName)
	thickRSP.Get(ctx)
	thickRSP.Await(ctx, tkmatch.Present())

	return &Discovery{
		PoolScope: PoolScope{trsp: thinRSP},
		thickPool: PoolScope{trsp: thickRSP},
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ThinRSPName returns the name of the thin RSP.
func (d *Discovery) ThinRSPName() string { return d.trsp.Name() }

// ThickRSPName returns the name of the thick RSP.
func (d *Discovery) ThickRSPName() string { return d.thickPool.trsp.Name() }

// ──────────────────────────────────────────────────────────────────────────────
// PoolScope query methods
//
// Called directly on Discovery they use the thin RSP.
// Called via From(pt) they use the requested pool.
//

// LVMVolumeGroups returns spec.lvmVolumeGroups from the scoped RSP.
func (ps *PoolScope) LVMVolumeGroups() []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups {
	return ps.trsp.Object().Spec.LVMVolumeGroups
}

// NodeLabelSelector returns spec.nodeLabelSelector from the scoped RSP.
func (ps *PoolScope) NodeLabelSelector() *metav1.LabelSelector {
	return ps.trsp.Object().Spec.NodeLabelSelector
}

// Zones returns spec.zones from the scoped RSP.
func (ps *PoolScope) Zones() []string {
	return ps.trsp.Object().Spec.Zones
}

// EligibleNodes returns status.eligibleNodes from the scoped RSP (unfiltered).
func (ps *PoolScope) EligibleNodes() []v1alpha1.ReplicatedStoragePoolEligibleNode {
	return ps.trsp.Object().Status.EligibleNodes
}

// DiskfulPlacement describes a placement target: a node with a specific
// LVM volume group (and optional thin pool) where a diskful replica can live.
type DiskfulPlacement struct {
	NodeName     string
	LVGName      string
	ThinPoolName string // empty for thick pools
}

// AnyNode returns a random eligible ready node name from the scoped RSP,
// excluding names listed in except. Fails the test if no node is available.
func (ps *PoolScope) AnyNode(except ...string) string {
	GinkgoHelper()
	return ps.pickNode(false, except)
}

// AnyDiskfulNode returns a random eligible ready node name that has at
// least one ready LVMVolumeGroup in the scoped RSP, excluding names
// listed in except. Fails the test if no suitable node is available.
func (ps *PoolScope) AnyDiskfulNode(except ...string) string {
	GinkgoHelper()
	return ps.pickNode(true, except)
}

// AnyDiskfulPlacement returns a random DiskfulPlacement from the scoped RSP,
// excluding node names listed in except. Only considers ready nodes with
// ready LVGs. Fails the test if no suitable placement is available.
func (ps *PoolScope) AnyDiskfulPlacement(except ...string) DiskfulPlacement {
	GinkgoHelper()
	rsp := ps.trsp.Object()
	excSet := makeExcludeSet(except)
	type candidate struct {
		nodeName string
		lvg      v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup
	}
	var candidates []candidate
	for i := range rsp.Status.EligibleNodes {
		n := &rsp.Status.EligibleNodes[i]
		if _, skip := excSet[n.NodeName]; skip {
			continue
		}
		if !nodeUsable(n) {
			continue
		}
		for j := range n.LVMVolumeGroups {
			if !lvgUsable(&n.LVMVolumeGroups[j]) {
				continue
			}
			candidates = append(candidates, candidate{nodeName: n.NodeName, lvg: n.LVMVolumeGroups[j]})
		}
	}
	if len(candidates) == 0 {
		Fail(fmt.Sprintf("no diskful placement found in pool %q after excluding %v", rsp.Name, except))
	}
	c := candidates[rand.Intn(len(candidates))]
	return DiskfulPlacement{
		NodeName:     c.nodeName,
		LVGName:      c.lvg.Name,
		ThinPoolName: c.lvg.ThinPoolName,
	}
}

func (ps *PoolScope) pickNode(needLVG bool, except []string) string {
	GinkgoHelper()
	rsp := ps.trsp.Object()
	excSet := makeExcludeSet(except)
	var candidates []string
	for i := range rsp.Status.EligibleNodes {
		n := &rsp.Status.EligibleNodes[i]
		if _, skip := excSet[n.NodeName]; skip {
			continue
		}
		if !nodeUsable(n) {
			continue
		}
		if needLVG && len(n.LVMVolumeGroups) == 0 {
			continue
		}
		candidates = append(candidates, n.NodeName)
	}
	if len(candidates) == 0 {
		Fail(fmt.Sprintf("no eligible node found in pool %q after excluding %v", rsp.Name, except))
	}
	return candidates[rand.Intn(len(candidates))]
}

func nodeUsable(n *v1alpha1.ReplicatedStoragePoolEligibleNode) bool {
	return n.NodeReady && n.AgentReady && !n.Unschedulable
}

func lvgUsable(g *v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup) bool {
	return g.Ready && !g.Unschedulable
}

func makeExcludeSet(except []string) map[string]struct{} {
	excSet := make(map[string]struct{}, len(except))
	for _, e := range except {
		excSet[e] = struct{}{}
	}
	return excSet
}
