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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	tk "github.com/deckhouse/sds-replicated-volume/lib/go/testkit"
)

// TestRSC creates a new TestRSC with an auto-generated or named name.
func (f *Framework) TestRSC(name ...string) *TestRSC {
	var zero *v1alpha1.ReplicatedStorageClass
	return newTestRSC(f, f.autoName(zero, name...))
}

// TestRSCExact creates a new TestRSC with an exact literal name.
func (f *Framework) TestRSCExact(fullName string) *TestRSC {
	return newTestRSC(f, fullName)
}

func newTestRSC(f *Framework, name string) *TestRSC {
	t := &TestRSC{f: f}
	t.TrackedObject = tk.NewTrackedObject(f.Cache, f.Client, gvkRSC, name,
		tk.Lifecycle[*v1alpha1.ReplicatedStorageClass]{
			Debugger:   f.Debugger,
			Standalone: true,
			OnBuild:    func(_ context.Context) *v1alpha1.ReplicatedStorageClass { return t.buildObject() },
			OnNewEmpty: func() *v1alpha1.ReplicatedStorageClass { return &v1alpha1.ReplicatedStorageClass{} },
		})
	return t
}

// TestRSC is the domain wrapper for ReplicatedStorageClass test objects.
type TestRSC struct {
	*tk.TrackedObject[*v1alpha1.ReplicatedStorageClass]
	f *Framework

	shared bool // when true, buildObject stamps run-level metadata

	buildStoragePool      string // deprecated spec.storagePool
	buildStorageType      *v1alpha1.ReplicatedStoragePoolType
	buildStorageLVGs      []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups
	buildFTT              *byte
	buildGMDR             *byte
	buildTopology         *v1alpha1.ReplicatedStorageClassTopology
	buildVolumeAccess     *v1alpha1.ReplicatedStorageClassVolumeAccess
	buildReclaimPolicy    *v1alpha1.ReplicatedStorageClassReclaimPolicy
	buildZones            []string
	buildNodeSelector     *metav1.LabelSelector
	buildCRSType          *v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategyType
	buildCRSMaxParallel   *int32
	buildENCRSType        *v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategyType
	buildENCRSMaxParallel *int32
}

// ---------------------------------------------------------------------------
// Builder chain
// ---------------------------------------------------------------------------

func (t *TestRSC) StoragePool(name string) *TestRSC {
	t.buildStoragePool = name
	return t
}

func (t *TestRSC) StorageType(st v1alpha1.ReplicatedStoragePoolType) *TestRSC {
	t.buildStorageType = &st
	return t
}

func (t *TestRSC) StorageLVMVolumeGroups(lvgs ...v1alpha1.ReplicatedStoragePoolLVMVolumeGroups) *TestRSC {
	t.buildStorageLVGs = lvgs
	return t
}

func (t *TestRSC) FTT(n byte) *TestRSC {
	t.buildFTT = &n
	return t
}

func (t *TestRSC) GMDR(n byte) *TestRSC {
	t.buildGMDR = &n
	return t
}

func (t *TestRSC) Topology(topo v1alpha1.ReplicatedStorageClassTopology) *TestRSC {
	t.buildTopology = &topo
	return t
}

func (t *TestRSC) VolumeAccess(va v1alpha1.ReplicatedStorageClassVolumeAccess) *TestRSC {
	t.buildVolumeAccess = &va
	return t
}

func (t *TestRSC) ReclaimPolicy(rp v1alpha1.ReplicatedStorageClassReclaimPolicy) *TestRSC {
	t.buildReclaimPolicy = &rp
	return t
}

func (t *TestRSC) Zones(zones ...string) *TestRSC {
	t.buildZones = zones
	return t
}

func (t *TestRSC) NodeLabelSelector(sel *metav1.LabelSelector) *TestRSC {
	t.buildNodeSelector = sel
	return t
}

func (t *TestRSC) ConfigurationRolloutStrategyType(st v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategyType) *TestRSC {
	t.buildCRSType = &st
	return t
}

func (t *TestRSC) ConfigurationRolloutStrategyMaxParallel(n int32) *TestRSC {
	t.buildCRSMaxParallel = &n
	return t
}

func (t *TestRSC) EligibleNodesConflictResolutionStrategyType(st v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategyType) *TestRSC {
	t.buildENCRSType = &st
	return t
}

func (t *TestRSC) EligibleNodesConflictResolutionStrategyMaxParallel(n int32) *TestRSC {
	t.buildENCRSMaxParallel = &n
	return t
}

// ---------------------------------------------------------------------------
// buildObject
// ---------------------------------------------------------------------------

// buildObject returns the API object from builder fields.
// When shared=true, stamps run-level metadata; otherwise worker-level.
func (t *TestRSC) buildObject() *v1alpha1.ReplicatedStorageClass {
	rsc := &v1alpha1.ReplicatedStorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: t.Name(),
		},
	}
	if t.buildStoragePool != "" {
		rsc.Spec.StoragePool = t.buildStoragePool //nolint:staticcheck // testing deprecated field
	}
	if t.buildStorageType != nil || len(t.buildStorageLVGs) > 0 {
		rsc.Spec.Storage = &v1alpha1.ReplicatedStorageClassStorage{}
		if t.buildStorageType != nil {
			rsc.Spec.Storage.Type = *t.buildStorageType
		}
		if len(t.buildStorageLVGs) > 0 {
			rsc.Spec.Storage.LVMVolumeGroups = t.buildStorageLVGs
		}
	}
	if t.buildFTT != nil {
		rsc.Spec.FailuresToTolerate = t.buildFTT
	}
	if t.buildGMDR != nil {
		rsc.Spec.GuaranteedMinimumDataRedundancy = t.buildGMDR
	}
	if t.buildTopology != nil {
		rsc.Spec.Topology = *t.buildTopology
	}
	if t.buildVolumeAccess != nil {
		rsc.Spec.VolumeAccess = *t.buildVolumeAccess
	}
	if t.buildReclaimPolicy != nil {
		rsc.Spec.ReclaimPolicy = *t.buildReclaimPolicy
	}
	if len(t.buildZones) > 0 {
		rsc.Spec.Zones = t.buildZones
	}
	if t.buildNodeSelector != nil {
		rsc.Spec.NodeLabelSelector = t.buildNodeSelector
	}
	if t.buildCRSType != nil {
		rsc.Spec.ConfigurationRolloutStrategy = &v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategy{
			Type: *t.buildCRSType,
		}
		if t.buildCRSMaxParallel != nil {
			rsc.Spec.ConfigurationRolloutStrategy.RollingUpdate = &v1alpha1.ReplicatedStorageClassConfigurationRollingUpdateStrategy{
				MaxParallel: *t.buildCRSMaxParallel,
			}
		}
	}
	if t.buildENCRSType != nil {
		rsc.Spec.EligibleNodesConflictResolutionStrategy = &v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategy{
			Type: *t.buildENCRSType,
		}
		if t.buildENCRSMaxParallel != nil {
			rsc.Spec.EligibleNodesConflictResolutionStrategy.RollingRepair = &v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionRollingRepair{
				MaxParallel: *t.buildENCRSMaxParallel,
			}
		}
	}
	if t.f != nil {
		if t.shared {
			t.f.stampRunMetadata(rsc)
		} else {
			t.f.stampMetadata(rsc)
		}
	}
	return rsc
}
