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

package nodecontroller

import (
	"context"
	"slices"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// --- Wiring / construction ---

type Reconciler struct {
	cl client.Client
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

func NewReconciler(cl client.Client) *Reconciler {
	return &Reconciler{cl: cl}
}

// --- Reconcile ---

// Reconcile pattern: Pure orchestration
func (r *Reconciler) Reconcile(ctx context.Context, _ reconcile.Request) (reconcile.Result, error) {
	rf := flow.BeginRootReconcile(ctx)

	// Get all RSCs.
	rscs, err := r.getRSCs(rf.Ctx())
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}

	// Get all DRBDResources.
	drbdResources, err := r.getDRBDResources(rf.Ctx())
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}

	// Get all nodes.
	nodes, err := r.getNodes(rf.Ctx())
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}

	// Compute target: which nodes should have the agent label.
	targetNodes := computeTargetNodes(rscs, drbdResources, nodes)

	// Reconcile each node.
	var outcomes []flow.ReconcileOutcome
	for i := range nodes {
		node := &nodes[i]
		shouldHaveLabel := targetNodes[node.Name]
		outcome := r.reconcileNode(rf.Ctx(), node, shouldHaveLabel)
		outcomes = append(outcomes, outcome)
	}

	return flow.MergeReconciles(outcomes...).ToCtrl()
}

// reconcileNode reconciles a single node's agent label.
func (r *Reconciler) reconcileNode(ctx context.Context, node *corev1.Node, shouldHaveLabel bool) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "node", "node", node.Name)
	defer rf.OnEnd(&outcome)

	// Check if node is already in sync.
	hasLabel := obju.HasLabel(node, v1alpha1.AgentNodeLabelKey)
	if hasLabel == shouldHaveLabel {
		return rf.Done()
	}

	// Take patch base.
	base := node.DeepCopy()

	// Ensure label state.
	if shouldHaveLabel {
		obju.SetLabel(node, v1alpha1.AgentNodeLabelKey, node.Name)
	} else {
		obju.RemoveLabel(node, v1alpha1.AgentNodeLabelKey)
	}

	// Patch node.
	if err := r.cl.Patch(rf.Ctx(), node, client.MergeFrom(base)); err != nil {
		return rf.Fail(err)
	}

	return rf.Done()
}

// --- Helpers: compute ---

// computeTargetNodes returns a map of node names that should have the AgentNodeLabelKey.
// A node should have the label if:
//   - it matches at least one RSC, OR
//   - it has at least one DRBDResource (to prevent orphaning DRBD resources)
func computeTargetNodes(
	rscs []v1alpha1.ReplicatedStorageClass,
	drbdResources []v1alpha1.DRBDResource,
	nodes []corev1.Node,
) map[string]bool {
	// Compute nodes that have DRBDResources.
	nodesWithDRBDResources := computeNodesWithDRBDResources(drbdResources)

	target := make(map[string]bool, len(nodes))
	for i := range nodes {
		node := &nodes[i]
		// Node should have label if it matches any RSC OR has any DRBDResource.
		target[node.Name] = nodesWithDRBDResources[node.Name] || nodeMatchesAnyRSC(node, rscs)
	}

	return target
}

// computeNodesWithDRBDResources returns a set of node names that have at least one DRBDResource.
func computeNodesWithDRBDResources(drbdResources []v1alpha1.DRBDResource) map[string]bool {
	nodes := make(map[string]bool)
	for i := range drbdResources {
		nodeName := drbdResources[i].Spec.NodeName
		if nodeName != "" {
			nodes[nodeName] = true
		}
	}
	return nodes
}

// nodeMatchesAnyRSC returns true if the node matches at least one RSC.
func nodeMatchesAnyRSC(node *corev1.Node, rscs []v1alpha1.ReplicatedStorageClass) bool {
	for i := range rscs {
		if nodeMatchesRSC(node, &rscs[i]) {
			return true
		}
	}
	return false
}

// nodeMatchesRSC returns true if the node matches the RSC's configuration zones AND nodeLabelSelector.
// Returns false if RSC has no configuration yet.
func nodeMatchesRSC(node *corev1.Node, rsc *v1alpha1.ReplicatedStorageClass) bool {
	cfg := rsc.Status.Configuration
	if cfg == nil {
		// RSC has no configuration yet — skip.
		return false
	}

	// Zones check: if RSC has zones, node must be in one of them.
	if len(cfg.Zones) > 0 {
		nodeZone := node.Labels[corev1.LabelTopologyZone]
		if !slices.Contains(cfg.Zones, nodeZone) {
			return false
		}
	}

	// NodeLabelSelector check: if RSC has nodeLabelSelector, node must match it.
	if cfg.NodeLabelSelector != nil {
		selector, err := metav1.LabelSelectorAsSelector(cfg.NodeLabelSelector)
		if err != nil {
			// Configuration is validated before being written to status.configuration,
			// so an invalid selector here indicates a bug.
			panic(err)
		}
		if !selector.Matches(labels.Set(node.Labels)) {
			return false
		}
	}

	return true
}

// --- Single-call I/O helper categories ---

// getDRBDResources returns all DRBDResource objects.
func (r *Reconciler) getDRBDResources(ctx context.Context) ([]v1alpha1.DRBDResource, error) {
	var list v1alpha1.DRBDResourceList
	if err := r.cl.List(ctx, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// getNodes returns all Node objects.
func (r *Reconciler) getNodes(ctx context.Context) ([]corev1.Node, error) {
	var list corev1.NodeList
	if err := r.cl.List(ctx, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// getRSCs returns all ReplicatedStorageClass objects.
func (r *Reconciler) getRSCs(ctx context.Context) ([]v1alpha1.ReplicatedStorageClass, error) {
	var list v1alpha1.ReplicatedStorageClassList
	if err := r.cl.List(ctx, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}
