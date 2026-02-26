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

package rvrcontroller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/controlleroptions"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
)

const RVRControllerName = "rvr-controller"

func BuildController(mgr manager.Manager, agentPodNamespace string) error {
	cl := mgr.GetClient()
	scheme := mgr.GetScheme()

	rec := NewReconciler(cl, scheme, mgr.GetLogger().WithName(RVRControllerName), agentPodNamespace)

	return builder.ControllerManagedBy(mgr).
		Named(RVRControllerName).
		For(&v1alpha1.ReplicatedVolumeReplica{}, builder.WithPredicates(rvrPredicates()...)).
		Owns(&snc.LVMLogicalVolume{}, builder.WithPredicates(llvPredicates()...)).
		Owns(&v1alpha1.DRBDResource{}, builder.WithPredicates(drbdrPredicates()...)).
		Watches(
			&v1alpha1.ReplicatedVolume{},
			newRVEventHandler(cl),
			builder.WithPredicates(rvPredicates()...),
		).
		Watches(
			&corev1.Pod{},
			handler.EnqueueRequestsFromMapFunc(mapAgentPodToRVRs(cl, agentPodNamespace)),
			builder.WithPredicates(agentPodPredicates(agentPodNamespace)...),
		).
		Watches(
			&v1alpha1.ReplicatedStoragePool{},
			newRSPEventHandler(cl),
			builder.WithPredicates(rspPredicates()...),
		).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 10,
			RateLimiter:             controlleroptions.DefaultRateLimiter(),
		}).
		Complete(rec)
}

// rvEventHandler handles ReplicatedVolume events and enqueues RVRs with targeted logic:
// - DatameshRevision changed: enqueue members from old/new datamesh (or all if initial)
// - ReplicatedStorageClassName changed: enqueue all RVRs
// - DatameshPendingReplicaTransitions message changed: enqueue only affected RVRs
type rvEventHandler struct {
	cl client.Client
}

func newRVEventHandler(cl client.Client) handler.EventHandler {
	return &rvEventHandler{cl: cl}
}

func (h *rvEventHandler) Create(ctx context.Context, e event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	rv, ok := e.Object.(*v1alpha1.ReplicatedVolume)
	if !ok || rv == nil {
		return
	}
	h.enqueueAllRVRs(ctx, rv.Name, q)
}

func (h *rvEventHandler) Delete(ctx context.Context, e event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	rv, ok := e.Object.(*v1alpha1.ReplicatedVolume)
	if !ok || rv == nil {
		return
	}
	h.enqueueAllRVRs(ctx, rv.Name, q)
}

func (h *rvEventHandler) Generic(_ context.Context, _ event.GenericEvent, _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	// Not used.
}

func (h *rvEventHandler) Update(ctx context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	oldRV, okOld := e.ObjectOld.(*v1alpha1.ReplicatedVolume)
	newRV, okNew := e.ObjectNew.(*v1alpha1.ReplicatedVolume)
	if !okOld || !okNew || oldRV == nil || newRV == nil {
		return
	}

	// Cases that require enqueuing ALL RVRs (early return).
	if oldRV.Spec.ReplicatedStorageClassName != newRV.Spec.ReplicatedStorageClassName {
		// ReplicatedStorageClassName changed (for labels).
		h.enqueueAllRVRs(ctx, newRV.Name, q)
		return
	}
	if oldRV.Status.DatameshRevision != newRV.Status.DatameshRevision && oldRV.Status.DatameshRevision == 0 {
		// Initial setup — enqueue ALL RVRs.
		h.enqueueAllRVRs(ctx, newRV.Name, q)
		return
	}

	// Collect affected replicas from multiple independent changes.
	var replicas idset.IDSet

	// DatameshRevision changed (non-initial): enqueue members from old OR new datamesh.
	if oldRV.Status.DatameshRevision != newRV.Status.DatameshRevision {
		replicas = idset.FromAll(oldRV.Status.Datamesh.Members).
			Union(idset.FromAll(newRV.Status.Datamesh.Members))
	}

	// DatameshPendingReplicaTransitions messages changed: enqueue affected replicas.
	oldTx := oldRV.Status.DatameshPendingReplicaTransitions
	newTx := newRV.Status.DatameshPendingReplicaTransitions
	i, j := 0, 0
	for i < len(oldTx) || j < len(newTx) {
		switch {
		case i >= len(oldTx):
			replicas.Add(newTx[j].ID())
			j++
		case j >= len(newTx):
			replicas.Add(oldTx[i].ID())
			i++
		case oldTx[i].Name < newTx[j].Name:
			replicas.Add(oldTx[i].ID())
			i++
		case oldTx[i].Name > newTx[j].Name:
			replicas.Add(newTx[j].ID())
			j++
		default:
			if oldTx[i].Message != newTx[j].Message {
				replicas.Add(oldTx[i].ID())
			}
			i++
			j++
		}
	}

	if !replicas.IsEmpty() {
		h.enqueueRVRsByIDs(newRV.Name, replicas, q)
	}
}

// enqueueAllRVRs enqueues all RVRs for the given RV.
func (h *rvEventHandler) enqueueAllRVRs(ctx context.Context, rvName string, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	var rvrList v1alpha1.ReplicatedVolumeReplicaList
	if err := h.cl.List(ctx, &rvrList,
		client.MatchingFields{indexes.IndexFieldRVRByReplicatedVolumeName: rvName},
		client.UnsafeDisableDeepCopy,
	); err != nil {
		log.FromContext(ctx).Error(err, "rvEventHandler: failed to list RVRs", "rv", rvName)
		return
	}
	for _, rvr := range rvrList.Items {
		q.Add(reconcile.Request{NamespacedName: client.ObjectKey{Name: rvr.Name}})
	}
}

// enqueueRVRsByIDs enqueues RVRs by constructing names from RV name and IDs.
func (h *rvEventHandler) enqueueRVRsByIDs(rvName string, ids idset.IDSet, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	for id := range ids.All() {
		q.Add(reconcile.Request{NamespacedName: client.ObjectKey{Name: v1alpha1.FormatReplicatedVolumeReplicaName(rvName, id)}})
	}
}

// mapAgentPodToRVRs maps an agent pod to ReplicatedVolumeReplica resources on the same node.
func mapAgentPodToRVRs(cl client.Client, podNamespace string) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		pod, ok := obj.(*corev1.Pod)
		if !ok || pod == nil {
			return nil
		}

		// Only handle pods in the agent namespace with the agent label.
		if pod.Namespace != podNamespace {
			return nil
		}
		if pod.Labels["app"] != "agent" {
			return nil
		}

		nodeName := pod.Spec.NodeName
		if nodeName == "" {
			return nil // Pod not yet scheduled.
		}

		// Find all RVRs on this node.
		var rvrList v1alpha1.ReplicatedVolumeReplicaList
		if err := cl.List(ctx, &rvrList,
			client.MatchingFields{indexes.IndexFieldRVRByNodeName: nodeName},
			client.UnsafeDisableDeepCopy,
		); err != nil {
			log.FromContext(ctx).Error(err, "mapAgentPodToRVRs: failed to list RVRs", "node", nodeName)
			return nil
		}

		requests := make([]reconcile.Request, 0, len(rvrList.Items))
		for _, rvr := range rvrList.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKey{Name: rvr.Name},
			})
		}
		return requests
	}
}

// rspEventHandler handles ReplicatedStoragePool events and enqueues RVRs
// on nodes where eligibleNodes changed.
type rspEventHandler struct {
	cl client.Client
}

func newRSPEventHandler(cl client.Client) handler.EventHandler {
	return &rspEventHandler{cl: cl}
}

func (h *rspEventHandler) Create(ctx context.Context, e event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	rsp, ok := e.Object.(*v1alpha1.ReplicatedStoragePool)
	if !ok || rsp == nil {
		return
	}
	h.enqueueAllRVRsForRSP(ctx, rsp.Name, q)
}

func (h *rspEventHandler) Update(ctx context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	oldRSP, okOld := e.ObjectOld.(*v1alpha1.ReplicatedStoragePool)
	newRSP, okNew := e.ObjectNew.(*v1alpha1.ReplicatedStoragePool)
	if !okOld || !okNew || oldRSP == nil || newRSP == nil {
		return
	}

	// Find nodes where eligibleNodes changed.
	changedNodes := computeChangedEligibleNodes(oldRSP.Status.EligibleNodes, newRSP.Status.EligibleNodes)
	if len(changedNodes) == 0 {
		return
	}

	h.enqueueRVRsForRSPAndNodes(ctx, newRSP.Name, changedNodes, q)
}

func (h *rspEventHandler) Delete(ctx context.Context, e event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	rsp, ok := e.Object.(*v1alpha1.ReplicatedStoragePool)
	if !ok || rsp == nil {
		return
	}
	h.enqueueAllRVRsForRSP(ctx, rsp.Name, q)
}

func (h *rspEventHandler) Generic(_ context.Context, _ event.GenericEvent, _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	// Not used.
}

// enqueueAllRVRsForRSP enqueues all RVRs for RVs that use the given RSP.
func (h *rspEventHandler) enqueueAllRVRsForRSP(
	ctx context.Context,
	rspName string,
	q workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	// Find all RVs that use this RSP.
	var rvList v1alpha1.ReplicatedVolumeList
	if err := h.cl.List(ctx, &rvList,
		client.MatchingFields{indexes.IndexFieldRVByStoragePoolName: rspName},
		client.UnsafeDisableDeepCopy,
	); err != nil {
		log.FromContext(ctx).Error(err, "rspEventHandler: failed to list RVs", "rsp", rspName)
		return
	}

	// For each RV, enqueue all its RVRs.
	for _, rv := range rvList.Items {
		var rvrList v1alpha1.ReplicatedVolumeReplicaList
		if err := h.cl.List(ctx, &rvrList,
			client.MatchingFields{indexes.IndexFieldRVRByReplicatedVolumeName: rv.Name},
			client.UnsafeDisableDeepCopy,
		); err != nil {
			log.FromContext(ctx).Error(err, "rspEventHandler: failed to list RVRs", "rv", rv.Name)
			continue
		}
		for _, rvr := range rvrList.Items {
			q.Add(reconcile.Request{NamespacedName: client.ObjectKey{Name: rvr.Name}})
		}
	}
}

// enqueueRVRsForRSPAndNodes finds RVRs that use the given RSP and are on one of the given nodes.
func (h *rspEventHandler) enqueueRVRsForRSPAndNodes(
	ctx context.Context,
	rspName string,
	nodes []string,
	q workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	if len(nodes) == 0 {
		return
	}

	// Find all RVs that use this RSP.
	var rvList v1alpha1.ReplicatedVolumeList
	if err := h.cl.List(ctx, &rvList,
		client.MatchingFields{indexes.IndexFieldRVByStoragePoolName: rspName},
		client.UnsafeDisableDeepCopy,
	); err != nil {
		log.FromContext(ctx).Error(err, "rspEventHandler: failed to list RVs", "rsp", rspName)
		return
	}

	// For each RV and each changed node, find RVR using composite index.
	for _, rv := range rvList.Items {
		for _, nodeName := range nodes {
			var rvrList v1alpha1.ReplicatedVolumeReplicaList
			if err := h.cl.List(ctx, &rvrList,
				client.MatchingFields{indexes.IndexFieldRVRByRVAndNode: indexes.RVRByRVAndNodeKey(rv.Name, nodeName)},
				client.UnsafeDisableDeepCopy,
			); err != nil {
				log.FromContext(ctx).Error(err, "rspEventHandler: failed to list RVRs", "rv", rv.Name, "node", nodeName)
				continue
			}
			for _, rvr := range rvrList.Items {
				q.Add(reconcile.Request{NamespacedName: client.ObjectKey{Name: rvr.Name}})
			}
		}
	}
}

// computeChangedEligibleNodes returns node names where eligibleNodes entry changed
// (added, removed, or modified).
//
// Uses EligibleNodesSortedIndex to handle potentially unsorted input safely.
// In the common case (already sorted), this adds only O(n) overhead with zero allocations.
func computeChangedEligibleNodes(oldNodes, newNodes []v1alpha1.ReplicatedStoragePoolEligibleNode) []string {
	// Build sorted indices — O(n) if already sorted, O(n log n) otherwise.
	oldIdx := v1alpha1.NewEligibleNodesSortedIndex(oldNodes)
	newIdx := v1alpha1.NewEligibleNodesSortedIndex(newNodes)

	var changed []string

	// Merge-style traversal of two sorted lists.
	i, j := 0, 0
	for i < oldIdx.Len() || j < newIdx.Len() {
		switch {
		case i >= oldIdx.Len():
			// Remaining newNodes are added.
			changed = append(changed, newIdx.NodeName(j))
			j++
		case j >= newIdx.Len():
			// Remaining oldNodes are removed.
			changed = append(changed, oldIdx.NodeName(i))
			i++
		case oldIdx.NodeName(i) < newIdx.NodeName(j):
			// Node removed.
			changed = append(changed, oldIdx.NodeName(i))
			i++
		case oldIdx.NodeName(i) > newIdx.NodeName(j):
			// Node added.
			changed = append(changed, newIdx.NodeName(j))
			j++
		default:
			// Same node — check if modified.
			if !eligibleNodeEqual(*oldIdx.At(i), *newIdx.At(j)) {
				changed = append(changed, oldIdx.NodeName(i))
			}
			i++
			j++
		}
	}

	return changed
}

// eligibleNodeEqual compares two EligibleNode entries for equality.
func eligibleNodeEqual(a, b v1alpha1.ReplicatedStoragePoolEligibleNode) bool {
	if a.NodeName != b.NodeName {
		return false
	}
	if a.Unschedulable != b.Unschedulable || a.NodeReady != b.NodeReady || a.AgentReady != b.AgentReady {
		return false
	}
	if len(a.LVMVolumeGroups) != len(b.LVMVolumeGroups) {
		return false
	}
	for i := range a.LVMVolumeGroups {
		if !eligibleNodeLVGEqual(a.LVMVolumeGroups[i], b.LVMVolumeGroups[i]) {
			return false
		}
	}
	return true
}

// eligibleNodeLVGEqual compares two EligibleNodeLVMVolumeGroup entries for equality.
func eligibleNodeLVGEqual(a, b v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup) bool {
	return a.Name == b.Name &&
		a.ThinPoolName == b.ThinPoolName &&
		a.Unschedulable == b.Unschedulable &&
		a.Ready == b.Ready
}
