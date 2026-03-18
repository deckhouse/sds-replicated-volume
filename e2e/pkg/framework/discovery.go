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
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

const (
	defaultRSPThin  = "e2e-thin"
	defaultRSPThick = "e2e-thick"
)

// PoolScope provides topology queries scoped to a single RSP.
// All query methods on Discovery are promoted from the embedded PoolScope
// (which points at the thin RSP by default).
type PoolScope struct {
	rsp *v1alpha1.ReplicatedStoragePool
}

// Discovery holds two pre-existing RSP objects (thin and thick) fetched
// during framework init. All cluster topology queries are answered from
// these RSPs instead of scanning individual LVGs.
//
// Methods called directly on Discovery operate on the thin RSP (promoted
// from the embedded PoolScope). Use From(pt) to get a PoolScope for a
// different pool type.
type Discovery struct {
	PoolScope // embedded, defaults to thin RSP
	thick     *v1alpha1.ReplicatedStoragePool
}

// From returns a PoolScope for the given pool type.
func (d *Discovery) From(pt v1alpha1.ReplicatedStoragePoolType) *PoolScope {
	switch pt {
	case v1alpha1.ReplicatedStoragePoolTypeLVMThin:
		return &d.PoolScope
	default:
		return &PoolScope{rsp: d.thick}
	}
}

// newDiscovery fetches the thin and thick RSP objects from the cluster.
// RSP names are read from E2E_RSP_THIN / E2E_RSP_THICK env vars,
// defaulting to "e2e-thin" / "e2e-thick".
func newDiscovery(ctx context.Context, cl client.Client) *Discovery {
	thinName := envOrDefault("E2E_RSP_THIN", defaultRSPThin)
	thickName := envOrDefault("E2E_RSP_THICK", defaultRSPThick)

	thin := &v1alpha1.ReplicatedStoragePool{}
	if err := cl.Get(ctx, client.ObjectKey{Name: thinName}, thin); err != nil {
		Fail(fmt.Sprintf("thin RSP %q not found; set E2E_RSP_THIN to override the name", thinName))
	}

	thick := &v1alpha1.ReplicatedStoragePool{}
	if err := cl.Get(ctx, client.ObjectKey{Name: thickName}, thick); err != nil {
		Fail(fmt.Sprintf("thick RSP %q not found; set E2E_RSP_THICK to override the name", thickName))
	}

	return &Discovery{
		PoolScope: PoolScope{rsp: thin},
		thick:     thick,
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ThinRSPName returns the name of the thin RSP.
func (d *Discovery) ThinRSPName() string { return d.rsp.Name }

// ThickRSPName returns the name of the thick RSP.
func (d *Discovery) ThickRSPName() string { return d.thick.Name }

// ──────────────────────────────────────────────────────────────────────────────
// PoolScope query methods
//
// Called directly on Discovery they use the thin RSP.
// Called via From(pt) they use the requested pool.
//

// LVMVolumeGroups returns spec.lvmVolumeGroups from the scoped RSP.
func (ps *PoolScope) LVMVolumeGroups() []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups {
	return ps.rsp.Spec.LVMVolumeGroups
}

// NodeLabelSelector returns spec.nodeLabelSelector from the scoped RSP.
func (ps *PoolScope) NodeLabelSelector() *metav1.LabelSelector {
	return ps.rsp.Spec.NodeLabelSelector
}

// Zones returns spec.zones from the scoped RSP.
func (ps *PoolScope) Zones() []string {
	return ps.rsp.Spec.Zones
}

// EligibleNodes returns status.eligibleNodes from the scoped RSP.
func (ps *PoolScope) EligibleNodes() []v1alpha1.ReplicatedStoragePoolEligibleNode {
	return ps.rsp.Status.EligibleNodes
}

// DiskfulPlacement describes a placement target: a node with a specific
// LVM volume group (and optional thin pool) where a diskful replica can live.
type DiskfulPlacement struct {
	NodeName     string
	LVGName      string
	ThinPoolName string // empty for thick pools
}

// AnyNode returns a random eligible node name from the scoped RSP,
// excluding names listed in except. Fails the test if no node is available.
func (ps *PoolScope) AnyNode(except ...string) string {
	return ps.pickNode(false, except)
}

// AnyDiskfulNode returns a random eligible node name that has at least one
// LVMVolumeGroup in the scoped RSP, excluding names listed in except.
// Fails the test if no suitable node is available.
func (ps *PoolScope) AnyDiskfulNode(except ...string) string {
	return ps.pickNode(true, except)
}

// AnyDiskfulPlacement returns a random DiskfulPlacement from the scoped RSP,
// excluding node names listed in except. It picks a random eligible node
// that has LVMVolumeGroups, then a random LVG on that node.
// Fails the test if no suitable placement is available.
func (ps *PoolScope) AnyDiskfulPlacement(except ...string) DiskfulPlacement {
	excSet := makeExcludeSet(except)
	type candidate struct {
		nodeName string
		lvg      v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup
	}
	var candidates []candidate
	for _, n := range ps.rsp.Status.EligibleNodes {
		if _, skip := excSet[n.NodeName]; skip {
			continue
		}
		for _, lvg := range n.LVMVolumeGroups {
			candidates = append(candidates, candidate{nodeName: n.NodeName, lvg: lvg})
		}
	}
	if len(candidates) == 0 {
		Fail(fmt.Sprintf("no diskful placement found in pool %q after excluding %v", ps.rsp.Name, except))
	}
	c := candidates[rand.Intn(len(candidates))]
	return DiskfulPlacement{
		NodeName:     c.nodeName,
		LVGName:      c.lvg.Name,
		ThinPoolName: c.lvg.ThinPoolName,
	}
}

func (ps *PoolScope) pickNode(needLVG bool, except []string) string {
	excSet := makeExcludeSet(except)
	var candidates []string
	for _, n := range ps.rsp.Status.EligibleNodes {
		if _, skip := excSet[n.NodeName]; skip {
			continue
		}
		if needLVG && len(n.LVMVolumeGroups) == 0 {
			continue
		}
		candidates = append(candidates, n.NodeName)
	}
	if len(candidates) == 0 {
		Fail(fmt.Sprintf("no eligible node found in pool %q after excluding %v", ps.rsp.Name, except))
	}
	return candidates[rand.Intn(len(candidates))]
}

func makeExcludeSet(except []string) map[string]struct{} {
	excSet := make(map[string]struct{}, len(except))
	for _, e := range except {
		excSet[e] = struct{}{}
	}
	return excSet
}
