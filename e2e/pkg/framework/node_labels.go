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
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ZoneLabelKey is the well-known topology label a storage pool reads a node's
// zone from. A spec that writes it edits system state and MUST carry
// LabelDisruptive.
const ZoneLabelKey = corev1.LabelTopologyZone

// NodeScopeLabelKey is the key a spec uses to carve a named subset of nodes out
// of the cluster — with a value unique to that spec — and then point the node
// selector of its own RSC at it. This is how a scenario that needs an exact
// eligible set ("these three nodes and nothing else") is built on a cluster of
// any size instead of being skipped.
//
// The key belongs to the suite, so writing it cannot disturb anything except
// specs that opted in; it still edits node objects, so the spec MUST carry
// LabelDisruptive and MUST set it through SetNodeLabel.
const NodeScopeLabelKey = "e2e.deckhouse.io/node-scope"

// nodeLabelSnapshot records what one label looked like on one node before the
// suite touched it — including the case where it did not exist at all, which a
// restore by "set it back to the old value" would silently turn into an empty
// label.
type nodeLabelSnapshot struct {
	NodeName string
	Key      string
	Value    string
	Existed  bool
}

// String renders the snapshot for logs and failure messages.
func (s nodeLabelSnapshot) String() string {
	if !s.Existed {
		return fmt.Sprintf("%s: %s absent", s.NodeName, s.Key)
	}
	return fmt.Sprintf("%s: %s=%s", s.NodeName, s.Key, s.Value)
}

// SetNodeLabel sets key to the given value on every node of valueByNode and
// arranges for the exact previous state of that label to be restored at the end
// of the spec.
//
// The restore is registered BEFORE the first node is written and works per
// node: a label that existed goes back to its old value, a label that did not
// exist is removed. Cleanup is therefore correct even when the spec fails half
// way through the labelling.
//
// Editing a system label such as ZoneLabelKey is globally visible: the calling
// spec MUST carry LabelDisruptive (which also makes it Serial).
func (f *Framework) SetNodeLabel(ctx context.Context, key string, valueByNode map[string]string) {
	GinkgoHelper()

	plan, err := planNodeLabel(ctx, f, key, valueByNode)
	if err != nil {
		Fail(fmt.Sprintf("SetNodeLabel: %v", err))
	}

	DeferCleanup(func(cleanupCtx SpecContext) {
		if err := plan.restore(cleanupCtx, f); err != nil {
			Fail(fmt.Sprintf("SetNodeLabel cleanup: %v", err))
		}
	})

	if err := plan.apply(ctx, f); err != nil {
		Fail(fmt.Sprintf("SetNodeLabel: %v", err))
	}
}

// NodeLabel returns the current value of key on nodeName and whether the label
// is present at all.
func (f *Framework) NodeLabel(ctx context.Context, nodeName, key string) (string, bool) {
	GinkgoHelper()
	snap, err := readNodeLabel(ctx, f, nodeName, key)
	if err != nil {
		Fail(err.Error())
	}
	return snap.Value, snap.Existed
}

// ---------------------------------------------------------------------------
// Core
// ---------------------------------------------------------------------------

// nodeLabelAPI is the seam the label cores reach the cluster through: one live
// Node read and one label write. *Framework implements it against the API
// server; unit tests substitute a stub, which is what makes the whole
// snapshot/restore lifecycle testable without a cluster.
type nodeLabelAPI interface {
	getNodeLive(ctx context.Context, nodeName string) (*corev1.Node, error)
	patchNodeLabel(ctx context.Context, nodeName, key, value string, set bool) error
}

// nodeLabelPlan is the read phase of SetNodeLabel: the nodes in a deterministic
// order, the value each of them gets, and the state to put back afterwards.
//
// Writing goes exclusively through a plan, so a label can never be changed
// before the snapshot that undoes it was taken.
type nodeLabelPlan struct {
	key       string
	nodes     []string
	values    map[string]string
	snapshots []nodeLabelSnapshot
}

// planNodeLabel validates the request and snapshots the label on EVERY node
// before anything is written.
func planNodeLabel(
	ctx context.Context,
	api nodeLabelAPI,
	key string,
	valueByNode map[string]string,
) (nodeLabelPlan, error) {
	if key == "" {
		return nodeLabelPlan{}, errors.New("label key must not be empty")
	}

	nodes := slices.Sorted(maps.Keys(valueByNode))
	snapshots := make([]nodeLabelSnapshot, 0, len(nodes))
	for _, nodeName := range nodes {
		snap, err := readNodeLabel(ctx, api, nodeName, key)
		if err != nil {
			return nodeLabelPlan{}, err
		}
		snapshots = append(snapshots, snap)
	}

	return nodeLabelPlan{key: key, nodes: nodes, values: maps.Clone(valueByNode), snapshots: snapshots}, nil
}

// apply writes the planned value to every node, in the planned order.
func (p nodeLabelPlan) apply(ctx context.Context, api nodeLabelAPI) error {
	for _, nodeName := range p.nodes {
		value := p.values[nodeName]
		if err := api.patchNodeLabel(ctx, nodeName, p.key, value, true); err != nil {
			return fmt.Errorf("labelling node %q with %s=%s: %w", nodeName, p.key, value, err)
		}
		fmt.Fprintf(GinkgoWriter, "[%s] [node-label] %s: %s=%s\n",
			time.Now().Format("15:04:05.000"), nodeName, p.key, value)
	}
	return nil
}

// restore puts the label back exactly as the plan found it — a label that
// existed goes back to its old value, a label that did not exist is removed.
//
// Every node of the plan is restored, including the ones a partially failed
// apply never reached: their snapshot is simply written back unchanged, which
// is cheaper than tracking who was written and correct either way.
func (p nodeLabelPlan) restore(ctx context.Context, api nodeLabelAPI) error {
	for _, snap := range p.snapshots {
		if err := api.patchNodeLabel(ctx, snap.NodeName, snap.Key, snap.Value, snap.Existed); err != nil {
			return fmt.Errorf("restoring %s: %w", snap, err)
		}
		fmt.Fprintf(GinkgoWriter, "[%s] [node-label] restored %s\n",
			time.Now().Format("15:04:05.000"), snap)
	}
	return nil
}

// readNodeLabel reads one label off a live Node.
func readNodeLabel(ctx context.Context, api nodeLabelAPI, nodeName, key string) (nodeLabelSnapshot, error) {
	node, err := api.getNodeLive(ctx, nodeName)
	if err != nil {
		return nodeLabelSnapshot{}, fmt.Errorf("reading node %q: %w", nodeName, err)
	}
	return snapshotNodeLabel(node, key), nil
}

// snapshotNodeLabel captures the current state of one label on one node.
func snapshotNodeLabel(node *corev1.Node, key string) nodeLabelSnapshot {
	value, existed := node.GetLabels()[key]
	return nodeLabelSnapshot{NodeName: node.GetName(), Key: key, Value: value, Existed: existed}
}

// patchNodeLabel sets (set=true) or removes (set=false) one label on a node.
//
// The patch is built from the arguments alone rather than from a read-modify-
// write cycle: the framework client reads through an informer cache, and a base
// object that has not caught up with our own previous write would produce an
// EMPTY diff — the restore would then silently do nothing.
func (f *Framework) patchNodeLabel(ctx context.Context, nodeName, key, value string, set bool) error {
	payload, err := nodeLabelMergePatch(key, value, set)
	if err != nil {
		return err
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: nodeName}}
	return f.Client.Patch(ctx, node, client.RawPatch(types.MergePatchType, payload))
}

// nodeLabelMergePatch renders the merge patch that sets or removes one label.
// Removal is a JSON null, which is how a merge patch deletes a key.
func nodeLabelMergePatch(key, value string, set bool) ([]byte, error) {
	var label any
	if set {
		label = value
	}
	patch := map[string]any{
		"metadata": map[string]any{
			"labels": map[string]any{key: label},
		},
	}
	payload, err := json.Marshal(patch)
	if err != nil {
		return nil, fmt.Errorf("rendering the label patch for %q: %w", key, err)
	}
	return payload, nil
}

// getNodeLive reads a Node straight from the API server, bypassing the
// framework's informer cache.
func (f *Framework) getNodeLive(ctx context.Context, nodeName string) (*corev1.Node, error) {
	return f.clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
}
