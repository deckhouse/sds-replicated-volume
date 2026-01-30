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
			handler.EnqueueRequestsFromMapFunc(mapRVToRVRs(cl)),
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
		WithOptions(controller.Options{MaxConcurrentReconciles: 10}).
		Complete(rec)
}

// mapRVToRVRs maps a ReplicatedVolume to all ReplicatedVolumeReplica resources that belong to it.
func mapRVToRVRs(cl client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		rv, ok := obj.(*v1alpha1.ReplicatedVolume)
		if !ok || rv == nil {
			return nil
		}

		var rvrList v1alpha1.ReplicatedVolumeReplicaList
		if err := cl.List(ctx, &rvrList,
			client.MatchingFields{indexes.IndexFieldRVRByReplicatedVolumeName: rv.Name},
			client.UnsafeDisableDeepCopy,
		); err != nil {
			log.FromContext(ctx).Error(err, "mapRVToRVRs: failed to list RVRs", "rv", rv.Name)
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
// Precondition: both oldNodes and newNodes are sorted by NodeName.
func computeChangedEligibleNodes(oldNodes, newNodes []v1alpha1.ReplicatedStoragePoolEligibleNode) []string {
	var changed []string

	// Merge-style traversal of two sorted lists.
	i, j := 0, 0
	for i < len(oldNodes) || j < len(newNodes) {
		switch {
		case i >= len(oldNodes):
			// Remaining newNodes are added.
			changed = append(changed, newNodes[j].NodeName)
			j++
		case j >= len(newNodes):
			// Remaining oldNodes are removed.
			changed = append(changed, oldNodes[i].NodeName)
			i++
		case oldNodes[i].NodeName < newNodes[j].NodeName:
			// Node removed.
			changed = append(changed, oldNodes[i].NodeName)
			i++
		case oldNodes[i].NodeName > newNodes[j].NodeName:
			// Node added.
			changed = append(changed, newNodes[j].NodeName)
			j++
		default:
			// Same node — check if modified.
			if !eligibleNodeEqual(oldNodes[i], newNodes[j]) {
				changed = append(changed, oldNodes[i].NodeName)
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
