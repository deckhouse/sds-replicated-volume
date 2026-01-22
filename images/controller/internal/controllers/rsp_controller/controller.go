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

package rspcontroller

import (
	"context"
	"slices"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
)

const RSPControllerName = "rsp-controller"

// BuildController registers the RSP controller with the manager.
// It sets up watches on ReplicatedStoragePool, Node, LVMVolumeGroup, and agent Pod resources.
func BuildController(mgr manager.Manager, podNamespace string) error {
	cl := mgr.GetClient()

	rec := NewReconciler(cl, mgr.GetLogger().WithName(RSPControllerName), podNamespace)

	return builder.ControllerManagedBy(mgr).
		Named(RSPControllerName).
		For(&v1alpha1.ReplicatedStoragePool{}, builder.WithPredicates(RSPPredicates()...)).
		Watches(
			&corev1.Node{},
			handler.EnqueueRequestsFromMapFunc(mapNodeToRSP(cl)),
			builder.WithPredicates(NodePredicates()...),
		).
		Watches(
			&snc.LVMVolumeGroup{},
			handler.EnqueueRequestsFromMapFunc(mapLVGToRSP(cl)),
			builder.WithPredicates(LVGPredicates()...),
		).
		Watches(
			&corev1.Pod{},
			handler.EnqueueRequestsFromMapFunc(mapAgentPodToRSP(cl, podNamespace)),
			builder.WithPredicates(AgentPodPredicates(podNamespace)...),
		).
		WithOptions(controller.Options{MaxConcurrentReconciles: 10}).
		Complete(rec)
}

// mapNodeToRSP maps a Node to ReplicatedStoragePool resources that are affected.
// This includes RSPs where:
// 1. Node is already in EligibleNodes (for updates/removals)
// 2. Node matches RSP's NodeLabelSelector and Zones (for potential additions)
func mapNodeToRSP(cl client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		node, ok := obj.(*corev1.Node)
		if !ok || node == nil {
			return nil
		}

		// 1. Find RSPs where this node is already in EligibleNodes (for update/removal).
		var byIndex v1alpha1.ReplicatedStoragePoolList
		if err := cl.List(ctx, &byIndex, client.MatchingFields{
			indexes.IndexFieldRSPByEligibleNodeName: node.Name,
		}); err != nil {
			return nil
		}

		// 2. Find all RSPs to check if node could be added.
		var all v1alpha1.ReplicatedStoragePoolList
		if err := cl.List(ctx, &all); err != nil {
			return nil
		}

		// Collect unique RSP names that need reconciliation.
		seen := make(map[string]struct{}, len(byIndex.Items)+len(all.Items))

		// Add RSPs where node is already tracked.
		for i := range byIndex.Items {
			name := byIndex.Items[i].Name
			seen[name] = struct{}{}
		}

		// Add RSPs where node matches selector/zones (potential addition).
		nodeLabels := labels.Set(node.Labels)
		nodeZone := node.Labels[corev1.LabelTopologyZone]
		for i := range all.Items {
			rsp := &all.Items[i]
			if _, exists := seen[rsp.Name]; exists {
				continue // Already included.
			}
			if nodeMatchesRSP(rsp, nodeLabels, nodeZone) {
				seen[rsp.Name] = struct{}{}
			}
		}

		// Build requests.
		requests := make([]reconcile.Request, 0, len(seen))
		for name := range seen {
			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKey{Name: name},
			})
		}
		return requests
	}
}

// nodeMatchesRSP checks if a node could potentially be added to RSP's EligibleNodes.
// This is a quick check based on NodeLabelSelector and Zones.
func nodeMatchesRSP(rsp *v1alpha1.ReplicatedStoragePool, nodeLabels labels.Set, nodeZone string) bool {
	// Check zones filter.
	if len(rsp.Spec.Zones) > 0 && !slices.Contains(rsp.Spec.Zones, nodeZone) {
		return false
	}

	// Check NodeLabelSelector.
	if rsp.Spec.NodeLabelSelector == nil {
		return true
	}

	selector, err := metav1.LabelSelectorAsSelector(rsp.Spec.NodeLabelSelector)
	if err != nil {
		return true // Be conservative: if we can't parse, trigger reconciliation.
	}

	return selector.Matches(nodeLabels)
}

// mapLVGToRSP maps an LVMVolumeGroup to all ReplicatedStoragePool resources that reference it.
func mapLVGToRSP(cl client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		lvg, ok := obj.(*snc.LVMVolumeGroup)
		if !ok || lvg == nil {
			return nil
		}

		// Find all RSPs that reference this LVG (using index).
		var rspList v1alpha1.ReplicatedStoragePoolList
		if err := cl.List(ctx, &rspList, client.MatchingFields{
			indexes.IndexFieldRSPByLVMVolumeGroupName: lvg.Name,
		}); err != nil {
			return nil
		}

		requests := make([]reconcile.Request, 0, len(rspList.Items))
		for i := range rspList.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(&rspList.Items[i]),
			})
		}
		return requests
	}
}

// mapAgentPodToRSP maps an agent pod to ReplicatedStoragePool resources
// where the pod's node is in EligibleNodes.
func mapAgentPodToRSP(cl client.Client, podNamespace string) handler.MapFunc {
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

		// Only reconcile RSPs where this node is in EligibleNodes.
		var rspList v1alpha1.ReplicatedStoragePoolList
		if err := cl.List(ctx, &rspList, client.MatchingFields{
			indexes.IndexFieldRSPByEligibleNodeName: nodeName,
		}); err != nil {
			return nil
		}

		requests := make([]reconcile.Request, 0, len(rspList.Items))
		for i := range rspList.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(&rspList.Items[i]),
			})
		}
		return requests
	}
}
