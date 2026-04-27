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

package snapmesh

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
)

var registry *dmte.Registry[*globalContext, *replicaContext]

func BuildRegistry() {
	if registry != nil {
		panic("snapmesh.BuildRegistry: already initialized")
	}
	registry = dmte.NewRegistry[*globalContext, *replicaContext]()
	registerSlots(registry)
	registerPreparePlans(registry)
	registerSyncPlans(registry)
}

func ProcessPrepare(
	ctx context.Context,
	rvs *v1alpha1.ReplicatedVolumeSnapshot,
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	childRVRSs []*v1alpha1.ReplicatedVolumeReplicaSnapshot,
	cl client.Client,
	scheme *runtime.Scheme,
) bool {
	cp := buildPrepareContexts(ctx, rvs, rv, rvrs, childRVRSs, cl, scheme)

	dispatchers := []dmte.DispatchFunc[provider]{
		prepareDispatcher(),
	}
	tracker := &concurrencyTracker{}
	engine := dmte.NewEngine(ctx, registry, tracker, dispatchers,
		&rvs.Status.PrepareRevision, rvs.Status.PrepareTransitions, cp)

	changed := engine.Process(ctx)

	rvs.Status.PrepareTransitions = engine.Finalize()

	return changed
}

func ProcessSync(
	ctx context.Context,
	rvs *v1alpha1.ReplicatedVolumeSnapshot,
	rv *v1alpha1.ReplicatedVolume,
	childRVRSs []*v1alpha1.ReplicatedVolumeReplicaSnapshot,
	syncDRBDRs []*v1alpha1.DRBDResource,
	originalNodeIDs map[string]uint8,
	cl client.Client,
	scheme *runtime.Scheme,
) bool {
	cp := buildContexts(ctx, rvs, rv, childRVRSs, syncDRBDRs, originalNodeIDs, cl, scheme)

	dispatchers := []dmte.DispatchFunc[provider]{
		syncDispatcher(),
	}
	tracker := &concurrencyTracker{}
	engine := dmte.NewEngine(ctx, registry, tracker, dispatchers,
		&rvs.Status.SyncRevision, rvs.Status.SyncTransitions, cp)

	changed := engine.Process(ctx)

	rvs.Status.SyncTransitions = engine.Finalize()

	return changed
}
