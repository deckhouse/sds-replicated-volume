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
	"strings"

	. "github.com/onsi/ginkgo/v2"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	tk "github.com/deckhouse/sds-replicated-volume/lib/go/testkit"
	tkmatch "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

// rscCacheKey uniquely identifies an RSC configuration within a test suite.
type rscCacheKey struct {
	FTT, GMDR    byte
	Topology     v1alpha1.ReplicatedStorageClassTopology
	VolumeAccess v1alpha1.ReplicatedStorageClassVolumeAccess
	PoolType     v1alpha1.ReplicatedStoragePoolType
	Suffix       string
}

// rscName builds a deterministic RSC name from the cache key.
// Format: "e2e-{runID}-f{FTT}g{GMDR}-{topology}-{access}-{pool}"
func (f *Framework) rscName(key rscCacheKey) string {
	topo := strings.ToLower(string(key.Topology))
	if len(topo) > 3 {
		topo = topo[:3]
	}
	access := strings.ToLower(string(key.VolumeAccess))
	if len(access) > 3 {
		access = access[:3]
	}
	pool := poolAbbrev(key.PoolType)
	return fmt.Sprintf("%s-f%dg%d-%s-%s-%s",
		f.prefix, key.FTT, key.GMDR, topo, access, pool)
}

func poolAbbrev(pt v1alpha1.ReplicatedStoragePoolType) string {
	switch pt {
	case v1alpha1.ReplicatedStoragePoolTypeLVMThin:
		return "thn" //nolint:misspell // intentional 3-char abbreviation
	case v1alpha1.ReplicatedStoragePoolTypeLVM:
		return "thk"
	default:
		s := strings.ToLower(string(pt))
		if len(s) > 3 {
			return s[:3]
		}
		return s
	}
}

// SharedRSC returns a *TestRSC pre-configured for shared (run-scoped) usage.
// The TrackedObject is not initialized yet — it is created lazily inside
// CreateShared after the cache key is resolved from builder fields.
//
// Chain builder methods (FTT, GMDR, Topology, VolumeAccess, StorageType),
// then call CreateShared(ctx) to ensure the RSC exists.
func (f *Framework) SharedRSC() *TestRSC {
	topology := v1alpha1.TopologyIgnored
	access := v1alpha1.VolumeAccessAny
	poolType := v1alpha1.ReplicatedStoragePoolTypeLVMThin
	return &TestRSC{
		f:                 f,
		shared:            true,
		buildTopology:     &topology,
		buildVolumeAccess: &access,
		buildStorageType:  &poolType,
	}
}

// CreateShared is the terminal method for shared RSC lifecycle.
// It computes a cache key from builder fields, deduplicates against
// previously created shared RSCs, and ensures the RSC exists on the
// cluster. On cache hit the receiver is replaced with the cached
// instance (*t = *cached). On cache miss the RSC is created via
// TrackedObject.CreateShared (IgnoreAlreadyExists, no DeferCleanup)
// and cached for subsequent callers.
//
// An optional suffix is appended to the generated name and included
// in the cache key (e.g. CreateShared(ctx, "custom") produces
// "e2e-{runID}-f0g0-ign-any-thn-custom").
//
// The TrackedObject uses NewLiteTrackedObject (keepOnlyLast) to avoid
// history accumulation for long-lived run-scoped objects.
func (t *TestRSC) CreateShared(ctx context.Context, suffix ...string) {
	GinkgoHelper()

	key := t.rscCacheKey()
	if len(suffix) > 0 && suffix[0] != "" {
		key.Suffix = suffix[0]
	}

	if cached, ok := t.f.rscCache[key]; ok {
		*t = *cached
		return
	}

	name := t.f.rscName(key)
	if key.Suffix != "" {
		name = name + "-" + key.Suffix
	}
	t.initSharedTrackedObject(name)

	ps := t.f.Discovery.From(key.PoolType)
	t.StorageLVMVolumeGroups(ps.LVMVolumeGroups()...).
		Zones(ps.Zones()...).
		NodeLabelSelector(ps.NodeLabelSelector()).
		StorageType(key.PoolType).
		ReclaimPolicy(v1alpha1.RSCReclaimPolicyDelete)

	t.TrackedObject.CreateShared(ctx)

	t.Await(ctx, tkmatch.ConditionStatus(
		v1alpha1.ReplicatedStorageClassCondReadyType, "True"))

	t.f.rscCache[key] = t
}

// rscCacheKey builds the cache key from TestRSC builder fields.
// Topology, VolumeAccess, PoolType must be pre-filled by SharedRSC().
// FTT and GMDR default to 0 (Go zero value) when unset.
func (t *TestRSC) rscCacheKey() rscCacheKey {
	key := rscCacheKey{
		Topology:     *t.buildTopology,
		VolumeAccess: *t.buildVolumeAccess,
		PoolType:     *t.buildStorageType,
	}
	if t.buildFTT != nil {
		key.FTT = *t.buildFTT
	}
	if t.buildGMDR != nil {
		key.GMDR = *t.buildGMDR
	}
	return key
}

func (t *TestRSC) initSharedTrackedObject(name string) {
	t.TrackedObject = tk.NewLiteTrackedObject(t.f.Cache, t.f.Client, gvkRSC, name,
		tk.Lifecycle[*v1alpha1.ReplicatedStorageClass]{
			Debugger:   t.f.Debugger,
			Standalone: true,
			OnBuild:    func(_ context.Context) *v1alpha1.ReplicatedStorageClass { return t.buildObject() },
			OnNewEmpty: func() *v1alpha1.ReplicatedStorageClass { return &v1alpha1.ReplicatedStorageClass{} },
		})
}
