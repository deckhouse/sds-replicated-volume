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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
)

type globalContext struct {
	ctx    context.Context
	rvs    *v1alpha1.ReplicatedVolumeSnapshot
	rv     *v1alpha1.ReplicatedVolume
	cl     client.Client
	scheme *runtime.Scheme

	sharedSecret  string
	syncCompleted bool

	replicas    [32]*replicaContext
	allReplicas []replicaContext
}

type replicaContext struct {
	id         uint8
	drbdNodeID uint8
	rvrName    string
	rvrsName   string
	nodeName   string
	rvrs       *v1alpha1.ReplicatedVolumeReplicaSnapshot
	drbdr      *v1alpha1.DRBDResource

	prepareTransition *dmte.Transition
	syncTransition    *dmte.Transition
	statusMessage     string
}

func (rc *replicaContext) ID() uint8                      { return rc.id }
func (rc *replicaContext) Name() string                   { return rc.rvrName }
func (rc *replicaContext) Exists() bool                   { return rc.drbdr != nil }
func (rc *replicaContext) Generation() int64              { return rc.drbdr.Generation }
func (rc *replicaContext) Conditions() []metav1.Condition { return rc.drbdr.Status.Conditions }

type provider struct {
	global *globalContext
}

func (p provider) Global() *globalContext           { return p.global }
func (p provider) Replica(id uint8) *replicaContext { return p.global.replicas[id] }

func buildContexts(
	ctx context.Context,
	rvs *v1alpha1.ReplicatedVolumeSnapshot,
	rv *v1alpha1.ReplicatedVolume,
	childRVRSs []*v1alpha1.ReplicatedVolumeReplicaSnapshot,
	syncDRBDRs []*v1alpha1.DRBDResource,
	originalNodeIDs map[string]uint8,
	cl client.Client,
	scheme *runtime.Scheme,
) provider {
	gctx := &globalContext{
		ctx:    ctx,
		rvs:    rvs,
		rv:     rv,
		cl:     cl,
		scheme: scheme,
	}

	drbdrByName := make(map[string]*v1alpha1.DRBDResource, len(syncDRBDRs))
	for _, d := range syncDRBDRs {
		drbdrByName[d.Name] = d
	}

	gctx.allReplicas = make([]replicaContext, 0, len(childRVRSs))
	for _, rvrs := range childRVRSs {
		if rvrs.Status.Phase != v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseReady ||
			rvrs.Status.SnapshotHandle == "" {
			continue
		}

		rvrName := rvrs.Spec.ReplicatedVolumeReplicaName
		id := v1alpha1.IDFromName(rvrName)
		drbdNodeID := id
		if nid, ok := originalNodeIDs[rvrName]; ok {
			drbdNodeID = nid
		}
		rc := replicaContext{
			id:         id,
			drbdNodeID: drbdNodeID,
			rvrName:    rvrName,
			rvrsName:   rvrs.Name,
			nodeName:   rvrs.Spec.NodeName,
			rvrs:       rvrs,
			drbdr:      drbdrByName[rvrs.Name],
		}
		gctx.allReplicas = append(gctx.allReplicas, rc)
	}

	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		gctx.replicas[rc.id] = rc
	}

	return provider{global: gctx}
}

func buildPrepareContexts(
	ctx context.Context,
	rvs *v1alpha1.ReplicatedVolumeSnapshot,
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	childRVRSs []*v1alpha1.ReplicatedVolumeReplicaSnapshot,
	cl client.Client,
	scheme *runtime.Scheme,
) provider {
	gctx := &globalContext{
		ctx:    ctx,
		rvs:    rvs,
		rv:     rv,
		cl:     cl,
		scheme: scheme,
	}

	childByRVRName := make(map[string]*v1alpha1.ReplicatedVolumeReplicaSnapshot, len(childRVRSs))
	for _, rvrs := range childRVRSs {
		childByRVRName[rvrs.Spec.ReplicatedVolumeReplicaName] = rvrs
	}

	gctx.allReplicas = make([]replicaContext, 0, len(rvrs))
	for _, rvr := range rvrs {
		if rvr.Spec.Type != v1alpha1.ReplicaTypeDiskful || rvr.Spec.NodeName == "" {
			continue
		}

		id := v1alpha1.IDFromName(rvr.Name)
		rc := replicaContext{
			id:         id,
			drbdNodeID: id,
			rvrName:    rvr.Name,
			nodeName:   rvr.Spec.NodeName,
			rvrs:       childByRVRName[rvr.Name],
		}
		if rc.rvrs != nil {
			rc.rvrsName = rc.rvrs.Name
		}

		gctx.allReplicas = append(gctx.allReplicas, rc)
	}

	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		gctx.replicas[rc.id] = rc
	}

	return provider{global: gctx}
}
