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

package drbdr

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/indexes"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// getCurrentNodeDRBDR gets the DRBDResource if it belongs to the current node.
func (r *Reconciler) getCurrentNodeDRBDR(
	ctx context.Context,
	req reconcile.Request,
) (*v1alpha1.DRBDResource, bool, error) {
	sf := flow.BeginStep(ctx, "get-drbdr")
	log := sf.Log()

	dr := &v1alpha1.DRBDResource{}
	if err := r.cl.Get(ctx, req.NamespacedName, dr); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return nil, false, flow.Wrapf(err, "getting DRBDResource")
		}
		log.V(1).Info("DRBDResource not found, skipping")
		return nil, false, nil
	}

	if dr.Spec.NodeName != r.nodeName {
		log.V(1).Info("DRBDResource belongs to different node, skipping", "nodeName", dr.Spec.NodeName)
		return nil, false, nil
	}

	return dr, true, nil
}

// getRequiredCurrentNode gets the Node object by name.
// Returns error if the Node is not found (required resource).
func (r *Reconciler) getRequiredCurrentNode(ctx context.Context) (*corev1.Node, error) {
	node := &corev1.Node{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: r.nodeName}, node); err != nil {
		return nil, flow.Wrapf(err, "getting Node %q", r.nodeName)
	}
	return node, nil
}

// getLVMLogicalVolume gets the LVMLogicalVolume by name.
// Returns (nil, nil) if not found.
func (r *Reconciler) getLVMLogicalVolume(ctx context.Context, name string) (*snc.LVMLogicalVolume, error) {
	if name == "" {
		return nil, nil
	}
	llv := &snc.LVMLogicalVolume{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, llv); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil, nil
		}
		return nil, flow.Wrapf(err, "getting LVMLogicalVolume %q", name)
	}
	return llv, nil
}

// getLVMVolumeGroup gets the LVMVolumeGroup by name.
// Returns (nil, nil) if not found.
func (r *Reconciler) getLVMVolumeGroup(ctx context.Context, name string) (*snc.LVMVolumeGroup, error) {
	if name == "" {
		return nil, nil
	}
	lvg := &snc.LVMVolumeGroup{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, lvg); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil, nil
		}
		return nil, flow.Wrapf(err, "getting LVMVolumeGroup %q", name)
	}
	return lvg, nil
}

// getLVGsOnNode lists LVMVolumeGroup objects on the current node using the index.
// Returns empty slice (not error) if none found.
func (r *Reconciler) getLVGsOnNode(ctx context.Context) ([]snc.LVMVolumeGroup, error) {
	list := &snc.LVMVolumeGroupList{}
	if err := r.cl.List(ctx, list, client.MatchingFields{
		indexes.IndexFieldLVGByNodeName: r.nodeName,
	}); err != nil {
		return nil, flow.Wrapf(err, "listing LVMVolumeGroups on node")
	}
	return list.Items, nil
}

// getLLVsForLVG lists LVMLogicalVolume objects for a specific LVG using the index.
// Returns empty slice (not error) if none found.
func (r *Reconciler) getLLVsForLVG(ctx context.Context, lvgName string) ([]snc.LVMLogicalVolume, error) {
	list := &snc.LVMLogicalVolumeList{}
	if err := r.cl.List(ctx, list, client.MatchingFields{
		indexes.IndexFieldLLVByLVGName: lvgName,
	}); err != nil {
		return nil, flow.Wrapf(err, "listing LVMLogicalVolumes for LVG %q", lvgName)
	}
	return list.Items, nil
}

// getDRBDRsOnNode lists all DRBDResource objects on this node using the field index.
// Returns empty slice if none found. Order is unspecified.
func (r *Reconciler) getDRBDRsOnNode(ctx context.Context) ([]v1alpha1.DRBDResource, error) {
	list := &v1alpha1.DRBDResourceList{}
	if err := r.cl.List(ctx, list, client.MatchingFields{
		indexes.IndexFieldDRBDRByNodeName: r.nodeName,
	}); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// getDRBDRByName fetches a DRBDResource by K8S name.
// Returns (nil, false, nil) if not found.
func (r *Reconciler) getDRBDRByName(ctx context.Context, name string) (*v1alpha1.DRBDResource, bool, error) {
	var drbdr v1alpha1.DRBDResource
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, &drbdr); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &drbdr, true, nil
}
