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
	tkmatch "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

// rscCacheKey uniquely identifies an RSC configuration within a test suite.
type rscCacheKey struct {
	FTT, GMDR    byte
	Topology     v1alpha1.ReplicatedStorageClassTopology
	VolumeAccess v1alpha1.ReplicatedStorageClassVolumeAccess
	PoolType     v1alpha1.ReplicatedStoragePoolType
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

// getOrCreateRSC returns the cached RSC name for the given key, creating the
// RSC on the cluster (and waiting for Ready) if it doesn't exist yet.
// The RSC is shared across workers (run-level metadata, IgnoreAlreadyExists).
func (f *Framework) getOrCreateRSC(ctx context.Context, key rscCacheKey) string {
	GinkgoHelper()
	if trsc, ok := f.rscCache[key]; ok {
		return trsc.Name()
	}

	trsc := f.TestRSCExact(f.rscName(key)).
		StorageType(key.PoolType).
		StorageLVMVolumeGroups(f.Discovery.From(key.PoolType).LVMVolumeGroups()...).
		Zones(f.Discovery.From(key.PoolType).Zones()...).
		NodeLabelSelector(f.Discovery.From(key.PoolType).NodeLabelSelector()).
		ReclaimPolicy(v1alpha1.RSCReclaimPolicyDelete).
		FTT(key.FTT).GMDR(key.GMDR).
		VolumeAccess(key.VolumeAccess).
		Topology(key.Topology)

	trsc.createShared(ctx)
	trsc.Await(ctx, tkmatch.ConditionStatus(
		v1alpha1.ReplicatedStorageClassCondReadyType, "True"))

	f.rscCache[key] = trsc
	return trsc.Name()
}
