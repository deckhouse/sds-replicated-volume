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

	if key == "" {
		Fail("SetNodeLabel: label key must not be empty")
	}

	// Snapshot every node first, so a single DeferCleanup covers all of them.
	nodes := slices.Sorted(maps.Keys(valueByNode))
	snapshots := make([]nodeLabelSnapshot, 0, len(nodes))
	for _, nodeName := range nodes {
		node, err := f.getNodeLive(ctx, nodeName)
		if err != nil {
			Fail(fmt.Sprintf("SetNodeLabel: reading node %q: %v", nodeName, err))
		}
		snapshots = append(snapshots, snapshotNodeLabel(node, key))
	}

	DeferCleanup(func(cleanupCtx SpecContext) {
		for _, snap := range snapshots {
			if err := f.restoreNodeLabel(cleanupCtx, snap); err != nil {
				Fail(fmt.Sprintf("SetNodeLabel cleanup: restoring %s: %v", snap, err))
			}
			fmt.Fprintf(GinkgoWriter, "[%s] [node-label] restored %s\n",
				time.Now().Format("15:04:05.000"), snap)
		}
	})

	for _, nodeName := range nodes {
		if err := f.patchNodeLabel(ctx, nodeName, key, valueByNode[nodeName], true); err != nil {
			Fail(fmt.Sprintf("SetNodeLabel: labelling node %q with %s=%s: %v",
				nodeName, key, valueByNode[nodeName], err))
		}
		fmt.Fprintf(GinkgoWriter, "[%s] [node-label] %s: %s=%s\n",
			time.Now().Format("15:04:05.000"), nodeName, key, valueByNode[nodeName])
	}
}

// NodeLabel returns the current value of key on nodeName and whether the label
// is present at all.
func (f *Framework) NodeLabel(ctx context.Context, nodeName, key string) (string, bool) {
	GinkgoHelper()
	node, err := f.getNodeLive(ctx, nodeName)
	if err != nil {
		Fail(fmt.Sprintf("reading node %q: %v", nodeName, err))
	}
	snap := snapshotNodeLabel(node, key)
	return snap.Value, snap.Existed
}

// ---------------------------------------------------------------------------
// Core
// ---------------------------------------------------------------------------

// snapshotNodeLabel captures the current state of one label on one node.
func snapshotNodeLabel(node *corev1.Node, key string) nodeLabelSnapshot {
	value, existed := node.GetLabels()[key]
	return nodeLabelSnapshot{NodeName: node.GetName(), Key: key, Value: value, Existed: existed}
}

// restoreNodeLabel puts one label back exactly as the snapshot describes it.
func (f *Framework) restoreNodeLabel(ctx context.Context, snap nodeLabelSnapshot) error {
	return f.patchNodeLabel(ctx, snap.NodeName, snap.Key, snap.Value, snap.Existed)
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
